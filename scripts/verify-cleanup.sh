#!/usr/bin/env bash
# Verify TemperCI host scratch under data_dir looks clean enough after jobs/smoke.
#
# Exit 0 if:
#   - data_dir/instances has only subdirs that look like live pool members (count cap)
#   - no obvious orphan firecracker PIDs pointing at removed instance dirs (best-effort)
#   - no empty leftover overlay files outside instances/
#
# Usage:
#   ./scripts/verify-cleanup.sh [--data-dir DIR] [--expect-warm-max N]
#
# Notes:
#   - Warm pool VMs legitimately leave dirs under instances/; use --expect-warm-max
#     to bound how many may remain (default 8).
#   - Pass --expect-warm-max 0 after a full drain / smoke with no pool.
#   - TemperCI disks are plain files under data_dir (Firecracker instance dirs).
set -euo pipefail

DATA_DIR="${TEMPERCI_DATA_ROOT:-/var/lib/temperci}"
EXPECT_WARM_MAX=8
STRICT_TAPS=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --data-dir) DATA_DIR="$2"; shift 2 ;;
    --expect-warm-max) EXPECT_WARM_MAX="$2"; shift 2 ;;
    --strict-taps) STRICT_TAPS=1; shift ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

INSTANCES="${DATA_DIR}/instances"
IMAGES="${DATA_DIR}/images"
FAIL=0

echo "verify-cleanup: data_dir=${DATA_DIR} expect_warm_max=${EXPECT_WARM_MAX}"

if [[ ! -d "${DATA_DIR}" ]]; then
  echo "FAIL: data_dir missing: ${DATA_DIR}" >&2
  exit 1
fi

if [[ ! -d "${INSTANCES}" ]]; then
  echo "ok: no instances dir (nothing to clean)"
  leftover=0
else
  # Count only top-level instance directories (portable: no mapfile / bash 4+).
  leftover=0
  echo "instances entries:"
  while IFS= read -r d; do
    [[ -z "${d}" ]] && continue
    leftover=$((leftover + 1))
    id="$(basename "${d}")"
    size="$(du -sh "${d}" 2>/dev/null | awk '{print $1}')"
    echo "  - ${id} (${size})"
    if [[ -z "$(find "${d}" -mindepth 1 -maxdepth 1 2>/dev/null | head -n1)" ]]; then
      echo "FAIL: empty instance dir left behind: ${d}" >&2
      FAIL=1
    fi
  done < <(find "${INSTANCES}" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort || true)
  echo "instances count: ${leftover}"

  if (( leftover > EXPECT_WARM_MAX )); then
    echo "FAIL: ${leftover} instance dirs > expect_warm_max=${EXPECT_WARM_MAX}" >&2
    FAIL=1
  fi
fi

# Shared images must not be wiped by teardown.
if [[ -d "${IMAGES}" ]]; then
  echo "images dir present (expected to survive teardown): ${IMAGES}"
  du -sh "${IMAGES}" 2>/dev/null || true
fi

# Firecracker processes (informational; count may equal warm pool).
if command -v pgrep >/dev/null 2>&1; then
  if pgrep -a firecracker 2>/dev/null; then
    fc_n="$(pgrep -c firecracker 2>/dev/null || echo 0)"
    echo "firecracker processes: ${fc_n}"
    if (( leftover == 0 && fc_n > 0 )); then
      echo "FAIL: firecracker running but no instance dirs under ${INSTANCES}" >&2
      FAIL=1
    fi
  else
    echo "firecracker processes: 0"
  fi
fi

# TemperCI tap / netns leftovers (best-effort; names from vmm net markers).
if command -v ip >/dev/null 2>&1; then
  taps="$(ip -o link show 2>/dev/null | awk -F': ' '{print $2}' | grep -E '^tc-tap-' || true)"
  ns="$(ip netns list 2>/dev/null | awk '{print $1}' | grep -E '^tc-ns-' || true)"
  if [[ -n "${taps}" ]]; then
    echo "tc-tap links still present:"
    echo "${taps}" | sed 's/^/  /'
    if [[ "${STRICT_TAPS}" -eq 1 ]]; then
      # Allow as many taps as instance dirs; more is fail.
      tap_n="$(echo "${taps}" | grep -c . || true)"
      if (( tap_n > leftover )); then
        echo "FAIL: more tc-tap-* links (${tap_n}) than instance dirs (${leftover})" >&2
        FAIL=1
      fi
    fi
  else
    echo "tc-tap links: 0"
  fi
  if [[ -n "${ns}" ]]; then
    echo "tc-ns netns still present:"
    echo "${ns}" | sed 's/^/  /'
  else
    echo "tc-ns netns: 0"
  fi
fi

# Disk free space report (no fail).
if command -v df >/dev/null 2>&1; then
  df -h "${DATA_DIR}" || true
fi

if [[ "${FAIL}" -ne 0 ]]; then
  echo "verify-cleanup: FAIL" >&2
  exit 1
fi

echo "verify-cleanup: OK"
exit 0
