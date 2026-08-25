#!/usr/bin/env bash
# Wrappers must pin Listener/Worker heap and must not leak to a child "job step".
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKDIR="$(mktemp -d /tmp/temperci-heap.XXXXXX)"
trap 'rm -rf "${WORKDIR}"' EXIT

BIN="${WORKDIR}/opt/actions-runner/bin"
mkdir -p "${BIN}"

# Fake official binaries: record DOTNET_* and exit. The wrapper execs these
# as ${name}.real — they must not spawn a job-step child.
cat >"${BIN}/Runner.Listener" <<'EOF'
#!/usr/bin/env bash
env | grep -E '^DOTNET_' | sort >"${TEMPERCI_WORKDIR}/listener.env"
exit 0
EOF
cat >"${BIN}/Runner.Worker" <<'EOF'
#!/usr/bin/env bash
env | grep -E '^DOTNET_' | sort >"${TEMPERCI_WORKDIR}/worker.env"
exit 0
EOF
cat >"${WORKDIR}/job-step.sh" <<'EOF'
#!/usr/bin/env bash
env | grep -E '^DOTNET_' | sort >"${TEMPERCI_WORKDIR}/step.env" || true
exit 0
EOF
chmod +x "${BIN}/Runner.Listener" "${BIN}/Runner.Worker" "${WORKDIR}/job-step.sh"

export TEMPERCI_WORKDIR="${WORKDIR}"
# Isolation: a bare job step must not inherit a heap cap from this test shell.
unset DOTNET_gcServer DOTNET_GCHeapHardLimit DOTNET_GCConserveMemory TEMPERCI_LISTENER_HEAP TEMPERCI_MEM_KB || true

# Snapshot the official fake binaries so a later re-wrap cannot nest or replace them.
cp "${BIN}/Runner.Listener" "${WORKDIR}/listener.orig"
cp "${BIN}/Runner.Worker" "${WORKDIR}/worker.orig"

bash "${SCRIPT_DIR}/wrap-runner-dotnet.sh" "${WORKDIR}"

if [[ ! -f "${BIN}/Runner.Listener.real" ]] || [[ ! -f "${BIN}/Runner.Worker.real" ]]; then
  echo "FAIL: .real binaries missing after wrap" >&2
  exit 1
fi
if ! grep -qF 'Runner.Listener.real' "${BIN}/Runner.Listener"; then
  echo "FAIL: Listener wrapper does not exec .real" >&2
  cat "${BIN}/Runner.Listener" >&2
  exit 1
fi
if ! grep -qF 'Runner.Worker.real' "${BIN}/Runner.Worker"; then
  echo "FAIL: Worker wrapper does not exec .real" >&2
  cat "${BIN}/Runner.Worker" >&2
  exit 1
fi

# 6g guest → Listener 2GiB. 10g guest → Listener 3GiB. Worker stays 2GiB.
TEMPERCI_MEM_KB=6291456 "${BIN}/Runner.Listener"
"${BIN}/Runner.Worker"

assert_heap() {
  local file="$1" who="$2"
  local want="$3"
  if ! grep -qx "DOTNET_GCHeapHardLimit=${want}" "${file}"; then
    echo "FAIL: ${who} heap not ${want}" >&2
    cat "${file}" >&2 || true
    exit 1
  fi
  if ! grep -qx 'DOTNET_gcServer=0' "${file}"; then
    echo "FAIL: ${who} gcServer not 0" >&2
    cat "${file}" >&2 || true
    exit 1
  fi
}

assert_heap "${WORKDIR}/listener.env" "Listener" "2147483648"
assert_heap "${WORKDIR}/worker.env" "Worker" "2147483648"
if ! grep -qx 'DOTNET_GCHighMemPercent=60' "${WORKDIR}/listener.env"; then
  echo "FAIL: Listener missing GCHighMemPercent=60" >&2
  cat "${WORKDIR}/listener.env" >&2 || true
  exit 1
fi

TEMPERCI_MEM_KB=10485760 "${BIN}/Runner.Listener"
assert_heap "${WORKDIR}/listener.env" "Listener-10g" "3221225472"

# Bare job step: not through a wrapper, same as run.sh / workflow steps.
"${WORKDIR}/job-step.sh"

if [[ -s "${WORKDIR}/step.env" ]]; then
  echo "FAIL: job step inherited DOTNET_*:" >&2
  cat "${WORKDIR}/step.env" >&2
  exit 1
fi

# Exec-time override must win over MemTotal, and a second wrap must not nest.
TEMPERCI_LISTENER_HEAP=3221225472 TEMPERCI_MEM_KB=6291456 "${BIN}/Runner.Listener"
assert_heap "${WORKDIR}/listener.env" "Listener-override" "3221225472"
bash "${SCRIPT_DIR}/wrap-runner-dotnet.sh" "${WORKDIR}"
if ! grep -qF 'TEMPERCI_MEM_KB' "${BIN}/Runner.Listener"; then
  echo "FAIL: Listener wrapper lost MemTotal picker after second wrap" >&2
  cat "${BIN}/Runner.Listener" >&2
  exit 1
fi
if ! cmp -s "${BIN}/Runner.Listener.real" "${WORKDIR}/listener.orig" || \
   ! cmp -s "${BIN}/Runner.Worker.real" "${WORKDIR}/worker.orig"; then
  echo "FAIL: second wrap mutated .real (nested wrapper or replaced official binary)" >&2
  exit 1
fi
if grep -q 'DOTNET_GCHeapHardLimit' "${BIN}/Runner.Listener.real" || \
   grep -q 'DOTNET_GCHeapHardLimit' "${BIN}/Runner.Worker.real"; then
  echo "FAIL: second wrap nested a wrapper as .real" >&2
  exit 1
fi

echo "heap_wrap_test: OK"
