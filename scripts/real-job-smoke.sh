#!/usr/bin/env bash
# Operator/host smoke: prove Linux+KVM artifacts, and optionally create one
# Firecracker instance via the in-repo VMM smoke (no GitHub required).
#
# Usage:
#   ./scripts/real-job-smoke.sh           # host checks + VMM create/destroy when images exist
#   ./scripts/real-job-smoke.sh -fast     # artifacts + systemd units + healthz only
#
# Environment (optional):
#   TEMPERCI_IMAGE_PATH     default /var/lib/temperci/images/ubuntu-2404-runner.ext4
#   TEMPERCI_KERNEL_PATH    default /var/lib/temperci/images/vmlinux
#   TEMPERCI_DATA_DIR       default /var/lib/temperci
#   TEMPERCI_HEALTHZ_URL    default http://127.0.0.1:8080/healthz
#   TEMPERCI_FIRECRACKER    firecracker binary (default: firecracker on PATH)
#   TEMPERCI_AGENT_BIN      temperci-agent path (demo-bind / presence check)
#
# Real GitHub job (not executed by this script):
#   1. Workflow: runs-on: temperci-4vcpu-ubuntu-2404
#   2. Control log:  minted JIT config
#   3. Agent log:    warm VM ready → job bound → starting guest runner
#                    → guest runner exited → job complete
#   See docs/todos/proof-runbook.md
set -euo pipefail

FAST=0
DEMO_BIND=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    -fast|--fast) FAST=1; shift ;;
    --demo-bind) DEMO_BIND=1; shift ;;
    -h|--help)
      sed -n '2,24p' "$0"
      exit 0
      ;;
    *)
      echo "unknown arg: $1" >&2
      echo "SMOKE_FAIL"
      exit 2
      ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE_PATH="${TEMPERCI_IMAGE_PATH:-/var/lib/temperci/images/ubuntu-2404-runner.ext4}"
KERNEL_PATH="${TEMPERCI_KERNEL_PATH:-/var/lib/temperci/images/vmlinux}"
DATA_DIR="${TEMPERCI_DATA_DIR:-/var/lib/temperci}"
HEALTHZ_URL="${TEMPERCI_HEALTHZ_URL:-http://127.0.0.1:8080/healthz}"
FC_BIN="${TEMPERCI_FIRECRACKER:-firecracker}"
AGENT_BIN="${TEMPERCI_AGENT_BIN:-}"

fail() {
  echo "SMOKE_FAIL: $*" >&2
  echo "SMOKE_FAIL"
  exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

resolve_agent() {
  if [[ -n "${AGENT_BIN}" && -x "${AGENT_BIN}" ]]; then
    echo "${AGENT_BIN}"
    return
  fi
  if have temperci-agent; then
    command -v temperci-agent
    return
  fi
  if [[ -x /usr/local/bin/temperci-agent ]]; then
    echo /usr/local/bin/temperci-agent
    return
  fi
  if [[ -x "${REPO_ROOT}/bin/temperci-agent" ]]; then
    echo "${REPO_ROOT}/bin/temperci-agent"
    return
  fi
  echo ""
}

# --- 1. Linux only ---
if [[ "$(uname -s)" != "Linux" ]]; then
  fail "requires Linux+KVM (got $(uname -s)); run on the Ubuntu host"
fi

echo "smoke: linux ok ($(uname -srm))"

# --- 2. Host tools + artifacts ---
if [[ ! -e /dev/kvm ]]; then
  fail "/dev/kvm missing (enable KVM / add user to kvm group)"
fi
echo "smoke: /dev/kvm ok"

if [[ "${FC_BIN}" == */* ]]; then
  if [[ ! -x "${FC_BIN}" ]]; then
    fail "firecracker binary not executable: ${FC_BIN}"
  fi
elif ! have "${FC_BIN}"; then
  fail "firecracker not on PATH (set TEMPERCI_FIRECRACKER)"
fi
echo "smoke: firecracker ok ($(command -v "${FC_BIN}" 2>/dev/null || echo "${FC_BIN}"))"

if ! have mkfs.ext4; then
  fail "mkfs.ext4 missing (install e2fsprogs)"
fi
echo "smoke: mkfs.ext4 ok"

IMAGES_OK=1
if [[ ! -f "${IMAGE_PATH}" ]]; then
  echo "smoke: guest image missing: ${IMAGE_PATH}" >&2
  IMAGES_OK=0
else
  echo "smoke: image ok (${IMAGE_PATH})"
fi
if [[ ! -f "${KERNEL_PATH}" ]]; then
  echo "smoke: kernel missing: ${KERNEL_PATH}" >&2
  IMAGES_OK=0
else
  echo "smoke: kernel ok (${KERNEL_PATH})"
fi

# --- 3. -fast: artifacts + systemd + healthz ---
check_unit() {
  local name="$1"
  if have systemctl && systemctl cat "${name}" >/dev/null 2>&1; then
    local st
    st="$(systemctl is-active "${name}" 2>/dev/null || true)"
    echo "smoke: systemd ${name} (${st:-unknown})"
    return 0
  fi
  local f
  for f in \
    "/etc/systemd/system/${name}" \
    "/lib/systemd/system/${name}" \
    "/usr/lib/systemd/system/${name}" \
    "${REPO_ROOT}/deploy/systemd/${name}"; do
    if [[ -f "${f}" ]]; then
      echo "smoke: unit file present ${f}"
      return 0
    fi
  done
  return 1
}

if [[ "${FAST}" -eq 1 ]]; then
  if [[ "${IMAGES_OK}" -ne 1 ]]; then
    fail "image/kernel artifacts missing (see TEMPERCI_IMAGE_PATH / TEMPERCI_KERNEL_PATH)"
  fi
  if ! check_unit temperci-control.service; then
    fail "temperci-control.service not installed"
  fi
  if ! check_unit temperci-agent.service; then
    fail "temperci-agent.service not installed"
  fi
  if ! have curl; then
    fail "curl required for healthz check"
  fi
  if ! curl -fsS --max-time 5 "${HEALTHZ_URL}" >/dev/null; then
    fail "healthz failed: ${HEALTHZ_URL}"
  fi
  echo "smoke: healthz ok (${HEALTHZ_URL})"
  echo
  echo "Next (real GitHub job): dispatch a workflow with"
  echo "  runs-on: temperci-4vcpu-ubuntu-2404"
  echo "Expect logs: minted JIT config / job bound / guest runner exited / job complete"
  echo "SMOKE_OK"
  exit 0
fi

# --- 4. Default: create/destroy one Firecracker instance via existing smoke ---
if [[ "${IMAGES_OK}" -ne 1 ]]; then
  fail "image/kernel missing; cannot create a Firecracker VM (build per deploy/ubuntu/guest-image.md)"
fi

SMOKE_SH="${REPO_ROOT}/scripts/vmm-smoke.sh"
if [[ ! -x "${SMOKE_SH}" && ! -f "${SMOKE_SH}" ]]; then
  fail "missing ${SMOKE_SH}"
fi

SMOKE_ROOT="$(mktemp -d /tmp/temperci-real-job.XXXXXX)"
echo "smoke: invoking vmm-smoke.sh --backend firecracker --n 1 --root ${SMOKE_ROOT}"
# Isolate from production data_dir; vmm-smoke Create/Destroy covers inject.ext4.
if ! bash "${SMOKE_SH}" --root "${SMOKE_ROOT}" --n 1 --backend firecracker; then
  rm -rf "${SMOKE_ROOT}"
  fail "vmm-smoke.sh firecracker create/destroy failed"
fi
rm -rf "${SMOKE_ROOT}"

if [[ "${DEMO_BIND}" -eq 1 ]]; then
  AGENT="$(resolve_agent)"
  if [[ -z "${AGENT}" ]]; then
    fail "--demo-bind requested but temperci-agent not found"
  fi
  echo "smoke: demo-bind via ${AGENT} (requires a valid agent.toml; Ctrl-C after demo complete)"
  echo "  ${AGENT} -demo-bind -config /etc/temperci/agent.toml"
  echo "  expect: warm VM ready → demo bind ok → demo complete"
fi

echo
echo "Host Firecracker create/destroy OK. This does not dispatch a GitHub job."
echo "To prove a real workflow on this host:"
echo "  1. curl -sS ${HEALTHZ_URL}"
echo "  2. journalctl -u temperci-agent | grep 'warm VM ready'"
echo "  3. Dispatch workflow: runs-on: temperci-4vcpu-ubuntu-2404"
echo "  4. Control: minted JIT config"
echo "  5. Agent: job bound / starting guest runner / guest runner exited / job complete"
echo "  6. GitHub job green"
echo "  7. ls ${DATA_DIR}/instances  # no leftover busy VM for the finished job"
echo "See docs/todos/proof-runbook.md"
echo "SMOKE_OK"
exit 0
