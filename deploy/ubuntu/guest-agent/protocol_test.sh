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
printf '%s\n' '-----BEGIN CERTIFICATE-----' 'TESTCA' '-----END CERTIFICATE-----' >"${INJECT}/cache-ca.crt"

# Stub runner: official run.sh prints connect markers and exits 0.
# Also record NODE_EXTRA_CA_CERTS so we know the guest agent exported the CA.
# Record DOTNET_* so we know run.sh / job steps do not inherit a heap cap.
cat >"${RUNNER_DIR}/run.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
echo "NODE_EXTRA_CA_CERTS=${NODE_EXTRA_CA_CERTS:-}" >"${TEMPERCI_WORKDIR}/node-ca.env"
{
  echo "DOTNET_gcServer=${DOTNET_gcServer:-}"
  echo "DOTNET_GCHeapHardLimit=${DOTNET_GCHeapHardLimit:-}"
} >"${TEMPERCI_WORKDIR}/dotnet.env"
echo "Connected to GitHub"
echo "Listening for Jobs"
echo "Running job: protocol-smoke"
echo "Job protocol-smoke completed with result: Succeeded"
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

# Swap tools: record the call; do not allocate 2G during the protocol smoke.
printf '#!/usr/bin/env bash\n: >"${TEMPERCI_SWAPFILE:-/tmp/swapfile}"\nexit 0\n' >"${STUBS}/fallocate"
printf '#!/usr/bin/env bash\nexit 0\n' >"${STUBS}/mkswap"
printf '#!/usr/bin/env bash\necho swapon >"${TEMPERCI_WORKDIR}/swap.on"\nexit 0\n' >"${STUBS}/swapon"
chmod +x "${STUBS}/fallocate" "${STUBS}/mkswap" "${STUBS}/swapon"

export PATH="${STUBS}:${PATH}"
export TEMPERCI_MOUNT_STATE="${STATE}"
export TEMPERCI_INJECT_DEV="${INJECT}"
export TEMPERCI_INJECT_MNT="${MNT}"
export TEMPERCI_RUNNER_DIR="${RUNNER_DIR}"
export TEMPERCI_RUNNER="${RUNNER_DIR}/run.sh"
export TEMPERCI_POLL_SEC=0.1
export TEMPERCI_WORKDIR="${WORKDIR}/run"
export TEMPERCI_SWAP_MIB=1
export TEMPERCI_SWAPFILE="${WORKDIR}/swapfile"
# Isolation: run.sh / job steps must not inherit a heap cap from this test shell.
unset DOTNET_gcServer DOTNET_GCHeapHardLimit DOTNET_GCConserveMemory || true

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

if [[ ! -f "${INJECT}/agent.ready" && ! -f "${TEMPERCI_WORKDIR}/agent.ready" ]]; then
  echo "FAIL: agent.ready was not written" >&2
  exit 1
fi

if grep -q 'restarting with DOCKER_INSECURE' "${TEMPERCI_WORKDIR}/agent.log" 2>/dev/null; then
  echo "FAIL: guest agent restarted docker despite already-up path" >&2
  exit 1
fi

if [[ ! -f "${TEMPERCI_WORKDIR}/node-ca.env" ]] || ! grep -q 'TESTCA\|temperci-cache' "${TEMPERCI_WORKDIR}/node-ca.env"; then
  # The stub records the env path; the file itself is the PEM copy under WORKDIR.
  if [[ ! -f "${TEMPERCI_WORKDIR}/temperci-cache.crt" ]]; then
    echo "FAIL: guest agent did not stage cache CA from inject" >&2
    cat "${TEMPERCI_WORKDIR}/node-ca.env" 2>/dev/null || true
    exit 1
  fi
  if [[ ! -f "${TEMPERCI_WORKDIR}/node-ca.env" ]] || ! grep -q NODE_EXTRA_CA_CERTS "${TEMPERCI_WORKDIR}/node-ca.env"; then
    echo "FAIL: NODE_EXTRA_CA_CERTS not exported to runner" >&2
    exit 1
  fi
fi

if [[ ! -f "${TEMPERCI_WORKDIR}/swap.on" ]] && \
   ! grep -qE 'swap enabled|swap already on' "${TEMPERCI_WORKDIR}/agent.log" 2>/dev/null; then
  echo "FAIL: guest agent did not enable swap" >&2
  cat "${TEMPERCI_WORKDIR}/agent.log" >&2 || true
  exit 1
fi
if [[ ! -f "${TEMPERCI_WORKDIR}/dotnet.env" ]]; then
  echo "FAIL: runner did not record DOTNET env" >&2
  exit 1
fi
if ! grep -qx 'DOTNET_gcServer=' "${TEMPERCI_WORKDIR}/dotnet.env"; then
  echo "FAIL: DOTNET_gcServer leaked to run.sh:" >&2
  cat "${TEMPERCI_WORKDIR}/dotnet.env" >&2
  exit 1
fi
if ! grep -qx 'DOTNET_GCHeapHardLimit=' "${TEMPERCI_WORKDIR}/dotnet.env"; then
  echo "FAIL: DOTNET_GCHeapHardLimit leaked to run.sh:" >&2
  cat "${TEMPERCI_WORKDIR}/dotnet.env" >&2
  exit 1
fi

echo "protocol_test: OK (jitconfig in → runner.exit=${got})"
exit 0
