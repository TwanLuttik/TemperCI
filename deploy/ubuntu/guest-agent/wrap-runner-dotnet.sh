#!/usr/bin/env bash
# Replace Runner.Listener / Runner.Worker with exec wrappers that pin .NET heap.
# Usage: wrap-runner-dotnet.sh /path/to/rootfs-or-fake-root
set -euo pipefail
ROOT="${1:?usage: $0 /path/to/rootfs}"
BIN="${ROOT}/opt/actions-runner/bin"
# Worker stays 2 GiB on every shape. Listener is chosen at exec time from
# guest MemTotal: 2 GiB below 8g, 3 GiB on 8g+ (4 GiB plus compose filled a
# 10g guest; job 97642154783). GCHighMemPercent=60 makes Listener GC when
# the guest is under pressure instead of walking to the ceiling.
WORKER_HEAP="${TEMPERCI_WORKER_HEAP:-2147483648}"

wrap_one() {
  local name="$1"
  local src="${BIN}/${name}"
  local real="${BIN}/${name}.real"
  [[ -e "${src}" ]] || { echo "wrap-runner-dotnet: missing ${src}" >&2; return 1; }
  if [[ ! -f "${real}" ]]; then
    mv "${src}" "${real}"
  fi
  if [[ "${name}" == "Runner.Listener" ]]; then
    cat >"${src}" <<'EOF'
#!/bin/bash
export DOTNET_gcServer=0
export DOTNET_GCConserveMemory=7
export DOTNET_GCHighMemPercent=60
if [ -n "${TEMPERCI_LISTENER_HEAP:-}" ]; then
  heap="${TEMPERCI_LISTENER_HEAP}"
else
  mem_kb="${TEMPERCI_MEM_KB:-}"
  if [ -z "${mem_kb}" ]; then
    mem_kb=$(awk '/MemTotal:/ {print $2; exit}' /proc/meminfo)
  fi
  # 8 GiB = 8388608 KiB.
  if [ "${mem_kb:-0}" -ge 8388608 ]; then
    heap=3221225472
  else
    heap=2147483648
  fi
fi
export DOTNET_GCHeapHardLimit="${heap}"
exec "$(dirname "$0")/Runner.Listener.real" "$@"
EOF
  else
    cat >"${src}" <<EOF
#!/bin/bash
export DOTNET_gcServer=0
export DOTNET_GCHeapHardLimit=${WORKER_HEAP}
export DOTNET_GCConserveMemory=7
exec "\$(dirname "\$0")/${name}.real" "\$@"
EOF
  fi
  chmod +x "${src}"
}

wrap_one "Runner.Listener"
wrap_one "Runner.Worker"
