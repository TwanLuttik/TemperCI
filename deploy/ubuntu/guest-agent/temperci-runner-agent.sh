#!/bin/bash
# TemperCI guest agent: wait for JIT on inject disk, run official actions/runner, write exit code.
# IMPORTANT: upstream run.sh exits 0 even when run-helper fails — we capture logs and
# treat missing "Connected"/"Listening" as failure when the process exits too quickly.
set -eu

INJECT_DEV="${TEMPERCI_INJECT_DEV:-/dev/vdb}"
MNT="${TEMPERCI_INJECT_MNT:-/mnt/temperci}"
RUNNER_DIR="${TEMPERCI_RUNNER_DIR:-/opt/actions-runner}"
RUNNER="${TEMPERCI_RUNNER:-$RUNNER_DIR/run.sh}"
POLL_SEC="${TEMPERCI_POLL_SEC:-0.5}"
WORKDIR="${TEMPERCI_WORKDIR:-/run/temperci}"

mkdir -p "$MNT" "$WORKDIR"

log() { echo "temperci-agent: $*" | tee -a "$WORKDIR/agent.log" >&2; }

# Ensure root filesystem is writable (runner copies run-helper.sh into place).
mount -o remount,rw / 2>/dev/null || true
# Ensure runner dir is writable by the agent (often owned by uid 1001 from the tarball).
if [ -d "$RUNNER_DIR" ]; then
  chmod -R u+rwX "$RUNNER_DIR" 2>/dev/null || true
fi

write_exit() {
  local code="$1"
  # Best-effort publish exit code on inject disk for the host waiter.
  umount "$MNT" 2>/dev/null || true
  if mount "$INJECT_DEV" "$MNT" 2>/dev/null || mount -o rw "$INJECT_DEV" "$MNT" 2>/dev/null; then
    echo "$code" >"$MNT/runner.exit"
    cp -a "$WORKDIR/agent.log" "$MNT/agent.log" 2>/dev/null || true
    cp -a "$WORKDIR/runner.log" "$MNT/runner.log" 2>/dev/null || true
    sync
    umount "$MNT" 2>/dev/null || true
  fi
  # Always mirror on local workdir too.
  echo "$code" >"$WORKDIR/runner.exit"
  exit "$code"
}

# Prefer the real inject disk: first non-root virtio block with an ext4 fs.
resolve_inject_dev() {
  if [ -e "$INJECT_DEV" ]; then
    echo "$INJECT_DEV"
    return
  fi
  for d in /dev/vdb /dev/vdc /dev/vdd; do
    [ -e "$d" ] || continue
    echo "$d"
    return
  done
  echo "$INJECT_DEV"
}

# Best-effort static DNS when systemd-resolved/stub is missing at early boot.
if [ ! -e /etc/resolv.conf ] || [ ! -s /etc/resolv.conf ]; then
  rm -f /etc/resolv.conf 2>/dev/null || true
  printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\n' >/etc/resolv.conf 2>/dev/null || true
elif [ -L /etc/resolv.conf ]; then
  # Symlink to missing stub → replace with static resolvers for the runner.
  if [ ! -e /etc/resolv.conf ]; then
    rm -f /etc/resolv.conf
    printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\n' >/etc/resolv.conf 2>/dev/null || true
  fi
fi

# Azure SDK in actions/cache requires a *.blob.core.windows.net URL. Point
# our fake account at a routed dummy so SNI intercept can terminate it.
if ! grep -q 'tempercicache.blob.core.windows.net' /etc/hosts 2>/dev/null; then
  echo "10.231.255.254 tempercicache.blob.core.windows.net" >>/etc/hosts 2>/dev/null || true
fi

log "starting; waiting for JIT (ip=$(hostname -I 2>/dev/null | tr -d '\n' || true); gw=$(ip route 2>/dev/null | awk '/default/{print $3; exit}' || true))"
log "devices: $(ls /dev/vd* 2>/dev/null | tr '\n' ' ' || true)"

# Wait until inject disk has jitconfig (host syncs after bind).
polls=0
while true; do
  INJECT_DEV=$(resolve_inject_dev)
  if [ -e "$INJECT_DEV" ]; then
    umount "$MNT" 2>/dev/null || true
    if mount -o ro "$INJECT_DEV" "$MNT" 2>/dev/null; then
      if [ -f "$MNT/jitconfig" ]; then
        log "found jitconfig on $INJECT_DEV"
        break
      fi
      # Publish a heartbeat so the host can see the agent is alive (when inject free).
      polls=$((polls + 1))
      if [ $((polls % 20)) -eq 0 ]; then
        umount "$MNT" 2>/dev/null || true
        if mount "$INJECT_DEV" "$MNT" 2>/dev/null; then
          echo "waiting polls=$polls dev=$INJECT_DEV $(date -Is)" >"$MNT/agent.heartbeat" 2>/dev/null || true
          sync
          umount "$MNT" 2>/dev/null || true
        fi
      else
        umount "$MNT" 2>/dev/null || true
      fi
    else
      polls=$((polls + 1))
      if [ $((polls % 10)) -eq 1 ]; then
        log "mount $INJECT_DEV failed (poll=$polls); ls=$(ls /dev/vd* 2>/dev/null | tr '\n' ' ' || true)"
      fi
    fi
  else
    polls=$((polls + 1))
    if [ $((polls % 10)) -eq 1 ]; then
      log "waiting for inject device (poll=$polls)"
    fi
  fi
  sleep "$POLL_SEC"
done

# Remount read-write to copy JIT off the disk.
umount "$MNT" 2>/dev/null || true
if ! mount "$INJECT_DEV" "$MNT" 2>/dev/null; then
  log "cannot remount inject rw"
  write_exit 92
fi

JIT="$MNT/jitconfig"
if [ ! -f "$JIT" ]; then
  log "jitconfig missing after mount"
  write_exit 90
fi

cp -a "$JIT" "$WORKDIR/jitconfig"
chmod 600 "$WORKDIR/jitconfig"
# Keep inject unmounted while runner runs so host can poll independently.
umount "$MNT" 2>/dev/null || true

if [ ! -x "$RUNNER" ]; then
  log "runner missing or not executable: $RUNNER"
  write_exit 91
fi

# Preflight: runner must be able to materialize run-helper.sh
if ! touch "$RUNNER_DIR/.temperci_write_test" 2>/dev/null; then
  log "runner dir not writable: $RUNNER_DIR — attempting chmod"
  chmod -R u+rwX "$RUNNER_DIR" 2>/dev/null || true
  if ! touch "$RUNNER_DIR/.temperci_write_test" 2>/dev/null; then
    log "FATAL: cannot write to $RUNNER_DIR (rootfs may be read-only)"
    write_exit 93
  fi
fi
rm -f "$RUNNER_DIR/.temperci_write_test"

docker_ready() {
  [ -S /var/run/docker.sock ] && /usr/bin/docker info >/dev/null 2>&1
}

prefer_iptables_legacy() {
  # Guest kernel has CONFIG_IP_NF_* but not CONFIG_NF_TABLES. nft iptables
  # then dies with "Failed to initialize nft: Protocol not supported".
  if [ -x /usr/sbin/iptables-legacy ]; then
    update-alternatives --set iptables /usr/sbin/iptables-legacy >/dev/null 2>&1 || true
  fi
  if [ -x /usr/sbin/ip6tables-legacy ]; then
    update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy >/dev/null 2>&1 || true
  fi
  # Kernel also lacks iptables `raw`; Docker 28+ uses it for bridge isolation.
  export DOCKER_INSECURE_NO_IPTABLES_RAW=1
}

ensure_docker() {
  if [ ! -x /usr/bin/dockerd ]; then
    log "dockerd not installed; skipping"
    return 0
  fi
  prefer_iptables_legacy
  if docker_ready; then
    if iptables -t raw -L >/dev/null 2>&1; then
      log "docker already running"
      return 0
    fi
    log "docker is up but iptables raw table is missing; restarting with DOCKER_INSECURE_NO_IPTABLES_RAW"
    systemctl stop docker.service 2>/dev/null || true
    pkill -x dockerd 2>/dev/null || true
    sleep 1
  fi
  log "starting docker.service"
  systemctl reset-failed docker.service 2>/dev/null || true
  systemctl start docker.service 2>/dev/null || true
  local i
  for i in 1 2 3 4 5 6 7 8 9 10 11 12; do
    if docker_ready; then
      log "docker is up"
      return 0
    fi
    sleep 1
  done
  log "docker.service failed; journal: $(journalctl -u docker -n 40 --no-pager 2>/dev/null | tr '\n' '|' | tail -c 1500)"
  if [ -f /etc/docker/daemon.json ] && grep -q overlay2 /etc/docker/daemon.json; then
    log "retrying dockerd with vfs storage-driver"
    mkdir -p /etc/docker
    cat >/etc/docker/daemon.json <<'EOF'
{
  "storage-driver": "vfs",
  "iptables": true,
  "ip6tables": false,
  "live-restore": false
}
EOF
    systemctl reset-failed docker.service 2>/dev/null || true
    systemctl restart docker.service 2>/dev/null || true
    for i in 1 2 3 4 5 6 7 8 9 10; do
      if docker_ready; then
        log "docker is up (vfs)"
        return 0
      fi
      sleep 1
    done
  fi
  if ! docker_ready && [ -x /usr/bin/dockerd ]; then
    log "starting dockerd directly"
    mkdir -p /var/run
    /usr/bin/dockerd --host=unix:///var/run/docker.sock >/var/log/dockerd.direct.log 2>&1 &
    for i in 1 2 3 4 5 6 7 8; do
      if docker_ready; then
        log "docker is up (direct)"
        return 0
      fi
      sleep 1
    done
    log "direct dockerd log: $(tail -c 800 /var/log/dockerd.direct.log 2>/dev/null | tr '\n' '|')"
  fi
  log "WARNING: docker still not ready; compose jobs will fail"
  return 1
}

# Official actions/runner refuses root unless this is set (microVM runs as root).
export RUNNER_ALLOW_RUNASROOT=1
# Avoid the "Must not run interactively with sudo" guard when no TTY is present.
export RUNNER_MANUALLY_TRAP_SIG=1
# Node (actions/cache, upload-artifact) does not use the system trust store.
# Point it at the TemperCI intercept CA so results-receiver MITM succeeds.
for ca in /usr/local/share/ca-certificates/temperci-cache.crt /etc/ssl/certs/temperci-cache.pem; do
  if [ -f "$ca" ]; then
    export NODE_EXTRA_CA_CERTS="$ca"
    break
  fi
done
if [ -f /etc/ssl/certs/ca-certificates.crt ]; then
  export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
fi

# --jitconfig takes the encoded base64 string itself, NOT a filesystem path.
# Passing a path makes Runner.Listener try to Base64-decode the path text and exit.
JIT_B64=$(tr -d '\n\r' <"$WORKDIR/jitconfig")
if [ -z "$JIT_B64" ]; then
  log "jitconfig file is empty"
  write_exit 90
fi
ensure_docker || true
publish_live_logs() {
  umount "$MNT" 2>/dev/null || true
  if mount "$INJECT_DEV" "$MNT" 2>/dev/null || mount -o rw "$INJECT_DEV" "$MNT" 2>/dev/null; then
    cp -a "$WORKDIR/agent.log" "$MNT/agent.log" 2>/dev/null || true
    cp -a "$WORKDIR/runner.log" "$MNT/runner.log" 2>/dev/null || true
    sync
    umount "$MNT" 2>/dev/null || true
  fi
}

log "starting $RUNNER --jitconfig <${#JIT_B64} bytes> (as root, RUNNER_ALLOW_RUNASROOT=1)"
set +e
"$RUNNER" --jitconfig "$JIT_B64" >"$WORKDIR/runner.log" 2>&1 &
rpid=$!
while kill -0 "$rpid" 2>/dev/null; do
  publish_live_logs
  sleep 2
done
wait "$rpid"
code=$?
set -e
publish_live_logs
log "runner exited code=$code"

# Upstream run.sh exits 0 for almost every helper failure (only code 2 restarts).
# Treat exit 0 without actually running a job as failure so TemperCI does not
# report success while GitHub still shows "Waiting for a runner…".
# Connecting + "Listening for Jobs" is not enough: a deprecated runner does
# that and then exits without receiving the JIT job.
if grep -qiE "is deprecated and cannot receive messages|Runner version .+ is deprecated" "$WORKDIR/runner.log" 2>/dev/null; then
  log "runner rejected as deprecated; marking as 96"
  if [ -s "$WORKDIR/runner.log" ]; then
    log "runner.log tail: $(tail -c 500 "$WORKDIR/runner.log" | tr '\n' ' ')"
  fi
  code=96
elif [ "$code" -eq 0 ]; then
  if ! grep -qiE "Running job:|Job .+ completed" "$WORKDIR/runner.log" 2>/dev/null; then
    log "runner exit 0 without running a job; marking as 95"
    if [ -s "$WORKDIR/runner.log" ]; then
      log "runner.log tail: $(tail -c 500 "$WORKDIR/runner.log" | tr '\n' ' ')"
    fi
    code=95
  fi
fi

write_exit "$code"
