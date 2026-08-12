#!/usr/bin/env bash
# Install packages and directory layout for TemperCI agent on a Proxmox VE host.
# Safe to re-run. Does not download Firecracker (see deploy/proxmox/README.md).
# Does not modify Proxmox cluster config or pvesm.
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "host-prereqs.sh is for Linux/Proxmox hosts (got $(uname -s))" >&2
  exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root (sudo)" >&2
  exit 1
fi

if [[ -f /etc/pve/.version ]] || command -v pveversion >/dev/null 2>&1; then
  echo "detected Proxmox VE: $(pveversion 2>/dev/null | head -n1 || cat /etc/pve/.version 2>/dev/null || echo unknown)"
else
  echo "warning: Proxmox tools not detected; continuing (Debian/Ubuntu-like host)" >&2
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
# Proxmox already provides the KVM/QEMU stack. Install only utilities TemperCI needs.
# Do not pull in a full libvirt management stack unless the operator already uses it.
apt-get install -y \
  bridge-utils \
  iproute2 \
  curl \
  ca-certificates \
  jq

DATA_ROOT="${TEMPERCI_DATA_ROOT:-/var/lib/temperci}"
mkdir -p "${DATA_ROOT}/images" "${DATA_ROOT}/instances"
chmod 0755 "${DATA_ROOT}" "${DATA_ROOT}/images" "${DATA_ROOT}/instances"

if [[ ! -e /dev/kvm ]]; then
  echo "warning: /dev/kvm not present — Firecracker will not start until KVM is available" >&2
  echo "  see deploy/proxmox/nested-virt.md if the agent runs inside a VM" >&2
else
  ls -l /dev/kvm
fi

if ! command -v firecracker >/dev/null 2>&1; then
  echo "note: firecracker not on PATH yet — install per deploy/proxmox/README.md"
fi

echo "prereqs ok; data root=${DATA_ROOT}"
echo "next:"
echo "  1) install firecracker binary (deploy/proxmox/README.md)"
echo "  2) place guest image under ${DATA_ROOT}/images/ (deploy/ubuntu/guest-image.md)"
echo "  3) configure agent data_dir=${DATA_ROOT} (deploy/proxmox/quickstart.md)"
