#!/usr/bin/env bash
# Prove the guest-agent protocol: jitconfig in → runner.exit out.
#
# Stubs mount/umount so this runs without KVM, ext4, or root. The real agent
# script is exec'd with a fake inject directory and a stub actions/runner.
#
# Usage:
#   ./deploy/ubuntu/guest-agent/protocol_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT="${SCRIPT_DIR}/temperci-runner-agent.sh"
if [[ ! -f "${AGENT}" ]]; then
  echo "FAIL: missing guest agent ${AGENT}" >&2
  exit 1
fi

WORKDIR="$(mktemp -d /tmp/temperci-protocol.XXXXXX)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

INJECT="${WORKDIR}/inject"
MNT="${WORKDIR}/mnt"
RUNNER_DIR="${WORKDIR}/runner"
STUBS="${WORKDIR}/stubs"
STATE="${WORKDIR}/mount-state"
mkdir -p "${INJECT}" "${MNT}" "${RUNNER_DIR}" "${STUBS}" "${STATE}"

# Fake inject disk already has JIT (host sync-before-guest-poll).
printf 'dGVzdC1qaXQtY29uZmln\n' >"${INJECT}/jitconfig"

# Stub runner: official run.sh prints connect markers and exits 0.
cat >"${RUNNER_DIR}/run.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
echo "Connected to GitHub"
echo "Listening for Jobs"
echo "Running job: protocol-smoke"
echo "Job protocol-smoke completed"
exit 0
EOF
chmod +x "${RUNNER_DIR}/run.sh"

# mount/umount stubs: treat INJECT as a directory "disk".
cat >"${STUBS}/mount" <<'EOF'
#!/usr/bin/env bash
set -eu
STATE_DIR="${TEMPERCI_MOUNT_STATE:?}"
args=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) shift; [[ $# -gt 0 ]] && shift ;;
    -*) shift ;;
    *) args+=("$1"); shift ;;
  esac
done
n=${#args[@]}
if [[ "${n}" -lt 2 ]]; then
  # remount / similar — no-op
  exit 0
fi
src="${args[$((n-2))]}"
dst="${args[$((n-1))]}"
if [[ ! -d "${src}" ]]; then
  echo "stub-mount: source is not a directory: ${src}" >&2
  exit 1
fi
mkdir -p "${dst}" "${STATE_DIR}"
# Portable key (no bash 4 assoc arrays).
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
if [[ "${n}" -lt 1 ]]; then
  exit 1
fi
dst="${args[$((n-1))]}"
key="$(printf '%s' "${dst}" | tr '/ ' '__')"
state="${STATE_DIR}/${key}"
if [[ ! -f "${state}" ]]; then
  exit 1
fi
src="$(cat "${state}")"
if [[ -d "${src}" && -d "${dst}" ]]; then
  rm -rf "${src:?}/"*
  cp -a "${dst}/." "${src}/" 2>/dev/null || true
fi
rm -f "${state}"
exit 0
EOF
chmod +x "${STUBS}/mount" "${STUBS}/umount"

export PATH="${STUBS}:${PATH}"
export TEMPERCI_MOUNT_STATE="${STATE}"
export TEMPERCI_INJECT_DEV="${INJECT}"
export TEMPERCI_INJECT_MNT="${MNT}"
export TEMPERCI_RUNNER_DIR="${RUNNER_DIR}"
export TEMPERCI_RUNNER="${RUNNER_DIR}/run.sh"
export TEMPERCI_POLL_SEC=0.1
export TEMPERCI_WORKDIR="${WORKDIR}/run"

# Agent exits with the runner code; that is success for this protocol.
set +e
bash "${AGENT}"
code=$?
set -e

if [[ "${code}" -ne 0 ]]; then
  echo "FAIL: guest agent exited ${code}" >&2
  if [[ -f "${TEMPERCI_WORKDIR}/agent.log" ]]; then
    echo "--- agent.log ---" >&2
    cat "${TEMPERCI_WORKDIR}/agent.log" >&2
  fi
  if [[ -f "${TEMPERCI_WORKDIR}/runner.log" ]]; then
    echo "--- runner.log ---" >&2
    cat "${TEMPERCI_WORKDIR}/runner.log" >&2
  fi
  exit 1
fi

exit_file="${INJECT}/runner.exit"
if [[ ! -f "${exit_file}" ]]; then
  # Fallback: local workdir always gets a copy.
  exit_file="${TEMPERCI_WORKDIR}/runner.exit"
fi
if [[ ! -f "${exit_file}" ]]; then
  echo "FAIL: runner.exit not written" >&2
  exit 1
fi
got="$(tr -d '[:space:]' <"${exit_file}")"
if [[ "${got}" != "0" ]]; then
  echo "FAIL: runner.exit=${got} want 0" >&2
  exit 1
fi

if [[ ! -f "${INJECT}/jitconfig" ]]; then
  echo "FAIL: jitconfig missing on inject after run" >&2
  exit 1
fi

echo "protocol_test: OK (jitconfig in → runner.exit=${got})"
exit 0
