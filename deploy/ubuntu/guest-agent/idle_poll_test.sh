#!/usr/bin/env bash
# After agent.ready, the guest must not mount inject at 20Hz (that burns ~70%
# of a host core per warm VM). Idle poll should be seconds, not 50ms.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT="${SCRIPT_DIR}/temperci-runner-agent.sh"
WORKDIR="$(mktemp -d /tmp/temperci-idle-poll.XXXXXX)"
cleanup() {
  if [[ -n "${AGENT_PID:-}" ]] && kill -0 "${AGENT_PID}" 2>/dev/null; then
    kill "${AGENT_PID}" 2>/dev/null || true
    wait "${AGENT_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORKDIR}"
}
trap cleanup EXIT

INJECT="${WORKDIR}/inject"
MNT="${WORKDIR}/mnt"
RUNNER_DIR="${WORKDIR}/runner"
STUBS="${WORKDIR}/stubs"
STATE="${WORKDIR}/mount-state"
mkdir -p "${INJECT}" "${MNT}" "${RUNNER_DIR}" "${STUBS}" "${STATE}"
: >"${WORKDIR}/mount.count"

cat >"${RUNNER_DIR}/run.sh" <<'EOF'
#!/usr/bin/env bash
echo "Connected to GitHub"
echo "Listening for Jobs"
echo "Job idle-poll completed"
exit 0
EOF
chmod +x "${RUNNER_DIR}/run.sh"

cat >"${STUBS}/mount" <<'EOF'
#!/usr/bin/env bash
set -eu
STATE_DIR="${TEMPERCI_MOUNT_STATE:?}"
echo 1 >>"${TEMPERCI_MOUNT_COUNT}"
args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) shift; [[ $# -gt 0 ]] && shift ;;
    -*) shift ;;
    *) args+=("$1"); shift ;;
  esac
done
n=${#args[@]}
[[ "${n}" -lt 2 ]] && exit 0
src="${args[$((n-2))]}"
dst="${args[$((n-1))]}"
[[ -d "${src}" ]] || exit 1
mkdir -p "${dst}" "${STATE_DIR}"
key="$(printf '%s' "${dst}" | tr '/ ' '__')"
printf '%s\n' "${src}" >"${STATE_DIR}/${key}"
rm -rf "${dst:?}/"*
cp -a "${src}/." "${dst}/"
exit 0
EOF
cat >"${STUBS}/umount" <<'EOF'
#!/usr/bin/env bash
set -eu
STATE_DIR="${TEMPERCI_MOUNT_STATE:?}"
args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -*) shift ;;
    *) args+=("$1"); shift ;;
  esac
done
n=${#args[@]}
[[ "${n}" -lt 1 ]] && exit 1
dst="${args[$((n-1))]}"
key="$(printf '%s' "${dst}" | tr '/ ' '__')"
state="${STATE_DIR}/${key}"
[[ -f "${state}" ]] || exit 1
src="$(cat "${state}")"
if [[ -d "${src}" && -d "${dst}" ]]; then
  rm -rf "${src:?}/"*
  cp -a "${dst}/." "${src}/" 2>/dev/null || true
fi
rm -f "${state}"
exit 0
EOF
printf '#!/usr/bin/env bash\n: >"${TEMPERCI_SWAPFILE:-/tmp/swapfile}"\nexit 0\n' >"${STUBS}/fallocate"
printf '#!/usr/bin/env bash\nexit 0\n' >"${STUBS}/mkswap"
printf '#!/usr/bin/env bash\nexit 0\n' >"${STUBS}/swapon"
chmod +x "${STUBS}"/*

export PATH="${STUBS}:${PATH}"
export TEMPERCI_MOUNT_STATE="${STATE}"
export TEMPERCI_MOUNT_COUNT="${WORKDIR}/mount.count"
export TEMPERCI_INJECT_DEV="${INJECT}"
export TEMPERCI_INJECT_MNT="${MNT}"
export TEMPERCI_RUNNER_DIR="${RUNNER_DIR}"
export TEMPERCI_RUNNER="${RUNNER_DIR}/run.sh"
export TEMPERCI_POLL_SEC=0.05
export TEMPERCI_WORKDIR="${WORKDIR}/run"
export TEMPERCI_SWAP_MIB=0

mkdir -p "${TEMPERCI_WORKDIR}"
bash "${AGENT}" >/dev/null 2>&1 &
AGENT_PID=$!

deadline=$((SECONDS + 5))
while [[ ! -f "${TEMPERCI_WORKDIR}/agent.ready" && ! -f "${INJECT}/agent.ready" ]]; do
  if [[ "${SECONDS}" -ge "${deadline}" ]]; then
    echo "FAIL: agent.ready never appeared" >&2
    cat "${TEMPERCI_WORKDIR}/agent.log" >&2 || true
    exit 1
  fi
  sleep 0.05
done

# 1.2s of warm wait. 20Hz poll would mount ~24 times; idle 2s poll mounts at most once.
before=$(wc -l <"${TEMPERCI_MOUNT_COUNT}")
sleep 1.2
after=$(wc -l <"${TEMPERCI_MOUNT_COUNT}")
idle=$((after - before))
if [[ "${idle}" -gt 2 ]]; then
  echo "FAIL: ${idle} inject mounts in 1.2s after ready (warm poll still too hot)" >&2
  exit 1
fi

printf 'dGVzdC1qaXQ=\n' >"${INJECT}/jitconfig"
deadline=$((SECONDS + 8))
while [[ ! -f "${TEMPERCI_WORKDIR}/runner.exit" && ! -f "${INJECT}/runner.exit" ]]; do
  if [[ "${SECONDS}" -ge "${deadline}" ]]; then
    echo "FAIL: runner.exit never appeared after jitconfig" >&2
    cat "${TEMPERCI_WORKDIR}/agent.log" >&2 || true
    exit 1
  fi
  sleep 0.1
done

wait "${AGENT_PID}" || true
AGENT_PID=""
echo "idle_poll_test: OK (idle mounts=${idle})"
exit 0
