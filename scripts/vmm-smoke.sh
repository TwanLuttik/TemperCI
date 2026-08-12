#!/usr/bin/env bash
# Create/destroy N microVM instances and assert no leftovers under a data root.
#
# Default path uses the fake VMM (works on macOS and Linux without Firecracker).
# On Linux with KVM + firecracker, pass --backend firecracker once a kernel/rootfs
# are available (real Boot may still be skipped if assets are missing).
#
# Usage:
#   ./scripts/vmm-smoke.sh [--root DIR] [--n N] [--backend fake|firecracker]
set -euo pipefail

ROOT=""
N=3
BACKEND=fake

while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) ROOT="$2"; shift 2 ;;
    --n) N="$2"; shift 2 ;;
    --backend) BACKEND="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,12p' "$0"
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "${REPO_ROOT}"

if [[ -z "${ROOT}" ]]; then
  ROOT="$(mktemp -d /tmp/temperci-smoke.XXXXXX)"
  CLEAN_ROOT=1
else
  CLEAN_ROOT=0
  mkdir -p "${ROOT}"
fi

echo "smoke: root=${ROOT} n=${N} backend=${BACKEND}"

# Drive smoke via `go test` so we do not need a separate binary for Phase 3.
# The test binary writes under SMOKE_ROOT.
export TEMPERCI_SMOKE_ROOT="${ROOT}"
export TEMPERCI_SMOKE_N="${N}"
export TEMPERCI_SMOKE_BACKEND="${BACKEND}"

go test ./internal/cleanup -run TestSmokeCreateDestroyLoop -count=1 -v

# Assert instances dir empty (or only empty structure).
INSTANCES="${ROOT}/instances"
if [[ -d "${INSTANCES}" ]]; then
  leftover="$(find "${INSTANCES}" -mindepth 1 -maxdepth 1 2>/dev/null | wc -l | tr -d ' ')"
  if [[ "${leftover}" != "0" ]]; then
    echo "FAIL: leftover entries under ${INSTANCES}:" >&2
    find "${INSTANCES}" -mindepth 1 | head -n 50 >&2
    exit 1
  fi
fi

echo "smoke: OK (no leftover instances)"

if [[ "${CLEAN_ROOT}" -eq 1 ]]; then
  rm -rf "${ROOT}"
fi
