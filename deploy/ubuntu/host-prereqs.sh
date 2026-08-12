#!/usr/bin/env bash
# Install Ubuntu packages and directory layout for TemperCI agent + Firecracker.
# Safe to re-run. Does not download Firecracker (see deploy/ubuntu/README.md).
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "host-prereqs.sh is for Linux/Ubuntu hosts (got $(uname -s))" >&2
  exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root (sudo)" >&2
  exit 1
fi

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y \
  qemu-kvm \
  bridge-utils \
  iproute2 \
  curl \
  ca-certificates

DATA_ROOT="${TEMPERCI_DATA_ROOT:-/var/lib/temperci}"
mkdir -p "${DATA_ROOT}/images" "${DATA_ROOT}/instances"

if ! getent group kvm >/dev/null 2>&1; then
  echo "warning: group kvm missing; ensure /dev/kvm permissions are set for the agent user" >&2
fi

if [[ ! -e /dev/kvm ]]; then
  echo "warning: /dev/kvm not present — Firecracker will not start until KVM is available" >&2
fi

echo "prereqs ok; data root=${DATA_ROOT}"
echo "next: install firecracker binary (see deploy/ubuntu/README.md)"
