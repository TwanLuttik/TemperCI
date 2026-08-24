#!/bin/bash
# TemperCI guest agent: wait for JIT on inject disk, run official actions/runner, write exit code.
# IMPORTANT: upstream run.sh exits 0 even when run-helper fails — we capture logs and
# treat missing "Connected"/"Listening" as failure when the process exits too quickly.
set -eu

INJECT_DEV="${TEMPERCI_INJECT_DEV:-/dev/vdb}"
MNT="${TEMPERCI_INJECT_MNT:-/mnt/temperci}"
RUNNER_DIR="${TEMPERCI_RUNNER_DIR:-/opt/actions-runner}"
RUNNER="${TEMPERCI_RUNNER:-$RUNNER_DIR/run.sh}"
POLL_SEC="${TEMPERCI_POLL_SEC:-0.05}"
WORKDIR="${TEMPERCI_WORKDIR:-/run/temperci}"
MAILBOX_PORT="${TEMPERCI_MAILBOX_PORT:-9876}"

mkdir -p "$MNT" "$WORKDIR"

log() { echo "temperci-agent: $*" | tee -a "$WORKDIR/agent.log" >&2; }

host_ip() {
  ip route 2>/dev/null | awk '/default/{print $3; exit}'
}

# Host UDP mailbox (no inject loop-mount). Bash /dev/udp is enough.
signal_host() {
  local ip
  ip="$(host_ip)"
  [ -n "$ip" ] || return 0
  echo "$1" >"/dev/udp/${ip}/${MAILBOX_PORT}" 2>/dev/null || true
}

# Ensure root filesystem is writable (runner copies run-helper.sh into place).
mount -o remount,rw / 2>/dev/null || true
# Ownership is set at image build time. Do not chmod -R the runner tree on every boot.

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
  signal_host "exit $code"
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

# 2 GiB swapfile so a Node/Docker spike does not SIGABRT Runner.Listener.
# Created on the overlay at boot (not baked into the base image — that would
# add 2G to every sparse copy). TEMPERCI_SWAP_MIB=0 disables.
ensure_swap() {
  local mib="${TEMPERCI_SWAP_MIB:-2048}"
  local file="${TEMPERCI_SWAPFILE:-/swapfile}"
  if ! [ "${mib}" -gt 0 ] 2>/dev/null; then
    log "swap disabled (TEMPERCI_SWAP_MIB=${mib})"
    return 0
  fi
  local kb=0
  if [ -r /proc/meminfo ]; then
    kb=$(awk '/^SwapTotal:/{print $2}' /proc/meminfo 2>/dev/null || echo 0)
  fi
  if [ "${kb:-0}" -gt 0 ]; then
    log "swap already on (${kb} kB)"
    return 0
  fi
  mkdir -p "$(dirname "$file")" 2>/dev/null || true
  if command -v fallocate >/dev/null 2>&1 && fallocate -l "${mib}M" "$file" 2>/dev/null; then
    :
  elif dd if=/dev/zero of="$file" bs=1M count="$mib" status=none 2>/dev/null; then
    :
  else
    log "swapfile create failed (${mib} MiB at $file)"
    return 0
  fi
  chmod 600 "$file" 2>/dev/null || true
  if ! mkswap "$file" >/dev/null 2>&1; then
    log "mkswap failed for $file"
    return 0
  fi
  if swapon "$file" 2>/dev/null; then
    log "swap enabled (${mib} MiB at $file)"
  else
    log "swapon failed for $file"
  fi
}

log "starting; waiting for JIT (ip=$(hostname -I 2>/dev/null | tr -d '\n' || true); gw=$(ip route 2>/dev/null | awk '/default/{print $3; exit}' || true))"
log "devices: $(ls /dev/vd* 2>/dev/null | tr '\n' ' ' || true)"
ensure_swap

publish_ready() {
  echo "ready $(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$WORKDIR/agent.ready" 2>/dev/null || true
  umount "$MNT" 2>/dev/null || true
  if mount "$INJECT_DEV" "$MNT" 2>/dev/null || mount -o rw "$INJECT_DEV" "$MNT" 2>/dev/null; then
    echo "ready $(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$MNT/agent.ready" 2>/dev/null || true
    umount "$MNT" 2>/dev/null || true
    log "guest ready signaled"
  fi
  signal_host "ready"
}

# Kick dockerd during JIT wait so bind does not pay a full docker start.
export DOCKER_INSECURE_NO_IPTABLES_RAW=1
if [ -x /usr/sbin/iptables-legacy ]; then
  update-alternatives --set iptables /usr/sbin/iptables-legacy >/dev/null 2>&1 || true
fi
if [ -x /usr/bin/dockerd ] && [ ! -S /var/run/docker.sock ]; then
  systemctl start docker.service >/dev/null 2>&1 &
fi

# Warm means dockerd is already listening so bind does not wait on Docker.
if [ -x /usr/bin/dockerd ]; then
  i=0
  while [ "$i" -lt 20 ]; do
    if [ -S /var/run/docker.sock ] && /usr/bin/docker info >/dev/null 2>&1; then
      break
    fi
    i=$((i + 1))
    sleep 1
  done
fi

# Wait until inject disk has jitconfig (host syncs after bind).
# Signal agent.ready on first successful mount so the host can mark the VM warm.
polls=0
ready_written=0
while true; do
  INJECT_DEV=$(resolve_inject_dev)
  if [ -e "$INJECT_DEV" ]; then
    umount "$MNT" 2>/dev/null || true
    if mount -o ro "$INJECT_DEV" "$MNT" 2>/dev/null; then
      if [ "$ready_written" -eq 0 ]; then
        umount "$MNT" 2>/dev/null || true
        publish_ready
        ready_written=1
        continue
      fi
      if [ -f "$MNT/jitconfig" ]; then
        log "found jitconfig on $INJECT_DEV"
        break
      fi
      # Publish a heartbeat so the host can see the agent is alive (when inject free).
      polls=$((polls + 1))
      if [ $((polls % 20)) -eq 0 ]; then
        umount "$MNT" 2>/dev/null || true
        if mount "$INJECT_DEV" "$MNT" 2>/dev/null; then
          echo "waiting polls=$polls dev=$INJECT_DEV $(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$MNT/agent.heartbeat" 2>/dev/null || true
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
INJECT_CA=""
if [ -f "$MNT/cache-ca.crt" ]; then
  cp -a "$MNT/cache-ca.crt" "$WORKDIR/temperci-cache.crt"
  INJECT_CA="$WORKDIR/temperci-cache.crt"
fi
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
    log "docker already running"
    return 0
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

# Official Node (actions/cache, setup-node, upload-artifact) ignores the system
# store. Install the intercept CA for root + user runner and export it so every
# job process trusts the host SNI MITM.
apply_cache_ca() {
  local src="$1"
  local dest="/usr/local/share/ca-certificates/temperci-cache.crt"
  local work="$WORKDIR/temperci-cache.crt"
  if [ -n "$src" ] && [ -f "$src" ]; then
    cp -f "$src" "$work" 2>/dev/null || true
    mkdir -p /usr/local/share/ca-certificates 2>/dev/null || true
    if [ ! -f "$dest" ] || ! cmp -s "$src" "$dest" 2>/dev/null; then
      cp -f "$src" "$dest" 2>/dev/null || true
      chmod 0644 "$dest" 2>/dev/null || true
      if [ -x /usr/sbin/update-ca-certificates ]; then
        /usr/sbin/update-ca-certificates >/dev/null 2>&1 || true
      fi
    fi
  fi
  local ca=""
  for c in "$dest" /etc/ssl/certs/temperci-cache.pem "$work"; do
    if [ -f "$c" ]; then
      ca="$c"
      break
    fi
  done
  [ -n "$ca" ] || return 0
  export NODE_EXTRA_CA_CERTS="$ca"
  local bundle="/etc/ssl/certs/ca-certificates.crt"
  if [ -f "$bundle" ]; then
    export SSL_CERT_FILE="$bundle"
    export REQUESTS_CA_BUNDLE="$bundle"
    export CURL_CA_BUNDLE="$bundle"
    export GIT_SSL_CAINFO="$bundle"
  else
    bundle="$ca"
  fi
  if [ -d /opt/actions-runner ]; then
    printf 'NODE_EXTRA_CA_CERTS=%s\nSSL_CERT_FILE=%s\nREQUESTS_CA_BUNDLE=%s\nCURL_CA_BUNDLE=%s\nGIT_SSL_CAINFO=%s\n' \
      "$ca" "${SSL_CERT_FILE:-}" "${SSL_CERT_FILE:-}" "${SSL_CERT_FILE:-}" "${SSL_CERT_FILE:-}" \
      >/opt/actions-runner/.env 2>/dev/null || true
  fi
  # npm/pnpm cafile replaces the default CA list. Use the full bundle so
  # registry.npmjs.org still verifies (intercept CA alone breaks public TLS).
  printf 'cafile=%s\n' "$bundle" >/etc/npmrc 2>/dev/null || true
  for home in /root /home/runner; do
    [ -d "$home" ] || continue
    if [ ! -f "$home/.profile" ] || ! grep -q NODE_EXTRA_CA_CERTS "$home/.profile" 2>/dev/null; then
      printf '\nexport NODE_EXTRA_CA_CERTS=%s\nexport SSL_CERT_FILE=%s\n' "$ca" "${SSL_CERT_FILE:-}" >>"$home/.profile" 2>/dev/null || true
    fi
    printf 'cafile=%s\n' "$bundle" >"$home/.npmrc" 2>/dev/null || true
  done
  mkdir -p /etc/sudoers.d 2>/dev/null || true
  printf 'Defaults env_keep += "NODE_EXTRA_CA_CERTS SSL_CERT_FILE REQUESTS_CA_BUNDLE CURL_CA_BUNDLE GIT_SSL_CAINFO"\n' \
    >/etc/sudoers.d/temperci-cache-ca 2>/dev/null || true
  chmod 0440 /etc/sudoers.d/temperci-cache-ca 2>/dev/null || true
  log "cache CA trusted for Node/npm (NODE_EXTRA_CA_CERTS=$ca)"
}

apply_cache_ca "$INJECT_CA"

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
    umount "$MNT" 2>/dev/null || true
  fi
}

# Workstation GC + 1 GiB hard cap so Listener does not size its heap off
# guest RAM (8g boxes were aborting sooner than 6g). Scoped to the runner
# process so job steps do not inherit the limit.
log "starting $RUNNER --jitconfig <${#JIT_B64} bytes> (as root, RUNNER_ALLOW_RUNASROOT=1)"
set +e
env DOTNET_gcServer=0 DOTNET_GCHeapHardLimit=1073741824 \
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
elif ! grep -qiE "completed with result: succeeded" "$WORKDIR/runner.log" 2>/dev/null && \
     grep -qiE "out of memory|unknown error code: 134|Aborted.+Runner\.Listener" "$WORKDIR/runner.log" 2>/dev/null; then
  log "runner aborted (OOM/134); marking as 97"
  if [ -s "$WORKDIR/runner.log" ]; then
    log "runner.log tail: $(tail -c 500 "$WORKDIR/runner.log" | tr '\n' ' ')"
  fi
  code=97
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
