#!/usr/bin/env bash
# Guest agent must not report success when the official runner aborts with OOM.
# Upstream run.sh / run-helper.sh map unknown codes (including 134) to exit 0.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
AGENT="${SCRIPT_DIR}/temperci-runner-agent.sh"
WORKDIR="$(mktemp -d /tmp/temperci-oom.XXXXXX)"
cleanup() { rm -rf "${WORKDIR}"; }
trap cleanup EXIT

INJECT="${WORKDIR}/inject"
MNT="${WORKDIR}/mnt"
RUNNER_DIR="${WORKDIR}/runner"
STUBS="${WORKDIR}/stubs"
STATE="${WORKDIR}/mount-state"
mkdir -p "${INJECT}" "${MNT}" "${RUNNER_DIR}" "${STUBS}" "${STATE}"

printf 'dGVzdA==\n' >"${INJECT}/jitconfig"

cat >"${RUNNER_DIR}/run.sh" <<'EOF'
#!/usr/bin/env bash
set -eu
echo "Connected to GitHub"
echo "Listening for Jobs"
echo "Running job: e2e"
echo "Out of memory."
echo "/opt/actions-runner/run-helper.sh: line 36: 2112901 Aborted                 \"\$DIR\"/bin/Runner.Listener run \$*"
echo "Exiting with unknown error code: 134"
echo "Exiting runner..."
exit 0
EOF
chmod +x "${RUNNER_DIR}/run.sh"

# mount/umount stubs (same contract as protocol_test.sh).
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
[[ "${n}" -lt 2 ]] && exit 0
src="${args[$((n-2))]}"
dst="${args[$((n-1))]}"
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
chmod +x "${STUBS}/mount" "${STUBS}/umount"

# Swap helpers must be cheap no-ops in this test (we only care about exit remap).
for cmd in fallocate mkswap swapon; do
  printf '#!/usr/bin/env bash\nexit 0\n' >"${STUBS}/${cmd}"
  chmod +x "${STUBS}/${cmd}"
done

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

set +e
bash "${AGENT}"
code=$?
set -e

exit_file="${INJECT}/runner.exit"
[[ -f "${exit_file}" ]] || exit_file="${TEMPERCI_WORKDIR}/runner.exit"
if [[ ! -f "${exit_file}" ]]; then
  echo "FAIL: runner.exit not written" >&2
  exit 1
fi
got="$(tr -d '[:space:]' <"${exit_file}")"
if [[ "${got}" != "97" ]]; then
  echo "FAIL: runner.exit=${got} want 97 (OOM abort remapped)" >&2
  echo "--- agent.log ---" >&2
  cat "${TEMPERCI_WORKDIR}/agent.log" >&2 || true
  echo "--- runner.log ---" >&2
  cat "${TEMPERCI_WORKDIR}/runner.log" >&2 || true
  echo "agent process exit=${code}" >&2
  exit 1
fi

echo "remap_exit_test: OK (OOM → runner.exit=97)"
exit 0
