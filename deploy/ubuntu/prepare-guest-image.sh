#!/usr/bin/env bash
# Fetch kernel + build the Ubuntu runner rootfs, then stamp .ready.
# Invoked by temperci-guest-image.service. Safe to re-run.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGES_DIR="${TEMPERCI_IMAGES_DIR:-/var/lib/temperci/images}"
ROOTFS="${IMAGES_DIR}/ubuntu-2404-runner.ext4"
KERNEL="${IMAGES_DIR}/vmlinux"
STAMP="${IMAGES_DIR}/.ready"

FETCH="${TEMPERCI_FETCH_KERNEL:-${SCRIPT_DIR}/ubuntu/fetch-kernel.sh}"
BUILD="${TEMPERCI_BUILD_GUEST:-${SCRIPT_DIR}/ubuntu/build-guest-image.sh}"
# When this script lives next to fetch/build (git checkout), use siblings.
if [[ ! -x "$FETCH" && -x "${SCRIPT_DIR}/fetch-kernel.sh" ]]; then
  FETCH="${SCRIPT_DIR}/fetch-kernel.sh"
fi
if [[ ! -x "$BUILD" && -x "${SCRIPT_DIR}/build-guest-image.sh" ]]; then
  BUILD="${SCRIPT_DIR}/build-guest-image.sh"
fi

mkdir -p "$IMAGES_DIR"

need_kernel=0
need_rootfs=0
if [[ ! -s "$KERNEL" ]]; then
  need_kernel=1
fi
if [[ ! -s "$ROOTFS" || ! -f "$STAMP" ]]; then
  need_rootfs=1
fi

if [[ "$need_kernel" -eq 0 && "$need_rootfs" -eq 0 ]]; then
  echo "prepare-guest-image: already ready"
  exit 0
fi

if [[ "$need_kernel" -eq 1 ]]; then
  echo "prepare-guest-image: fetching kernel"
  "$FETCH" "$KERNEL"
fi

if [[ "$need_rootfs" -eq 1 ]]; then
  echo "prepare-guest-image: building rootfs"
  "$BUILD"
fi

if [[ ! -s "$KERNEL" ]]; then
  echo "prepare-guest-image: kernel missing at ${KERNEL}" >&2
  exit 1
fi
if [[ ! -s "$ROOTFS" ]]; then
  echo "prepare-guest-image: rootfs missing at ${ROOTFS}" >&2
  exit 1
fi

date -u +"%Y-%m-%dT%H:%M:%SZ" >"$STAMP"
echo "prepare-guest-image: ready"
