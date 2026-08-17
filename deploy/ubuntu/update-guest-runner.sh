#!/usr/bin/env bash
# Replace /opt/actions-runner inside an existing Firecracker rootfs.
# Does not rebuild the image. Stop temperci-agent first so warm VMs are not
# copied from a half-written base, then restart so the pool refills.
#
# Usage (Linux, as root):
#   sudo ./deploy/ubuntu/update-guest-runner.sh
#   sudo TEMPERCI_RUNNER_VERSION=2.336.0 ./deploy/ubuntu/update-guest-runner.sh
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "update-guest-runner.sh must run on Linux" >&2
  exit 1
fi
if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root" >&2
  exit 1
fi

IMAGES_DIR="${TEMPERCI_IMAGES_DIR:-/var/lib/temperci/images}"
ROOTFS="${TEMPERCI_ROOTFS:-${IMAGES_DIR}/ubuntu-2404-runner.ext4}"
RUNNER_VERSION="${TEMPERCI_RUNNER_VERSION:-2.336.0}"
ARCH="${TEMPERCI_RUNNER_ARCH:-linux-x64}"
URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-${ARCH}-${RUNNER_VERSION}.tar.gz"

if [[ ! -f "$ROOTFS" ]]; then
  echo "rootfs not found: $ROOTFS" >&2
  exit 1
fi

if grep -q "$ROOTFS" /proc/mounts; then
  echo "rootfs is mounted; unmount it first: $ROOTFS" >&2
  exit 1
fi

WORKDIR="$(mktemp -d /var/tmp/temperci-runner-XXXXXX)"
MNT=""
cleanup() {
  if [[ -n "${MNT:-}" ]]; then
    umount "$MNT" 2>/dev/null || umount -l "$MNT" 2>/dev/null || true
    rmdir "$MNT" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

echo "update-guest-runner: downloading actions/runner v${RUNNER_VERSION}"
curl -fL --retry 3 --retry-delay 2 -o "${WORKDIR}/runner.tgz" "$URL"

MNT="$(mktemp -d)"
mount -o loop "$ROOTFS" "$MNT"

echo "update-guest-runner: replacing /opt/actions-runner"
rm -rf "${MNT}/opt/actions-runner"
mkdir -p "${MNT}/opt/actions-runner"
tar -xzf "${WORKDIR}/runner.tgz" -C "${MNT}/opt/actions-runner"
chown -R root:root "${MNT}/opt/actions-runner"
chmod -R u+rwX "${MNT}/opt/actions-runner"
printf '%s\n' "$RUNNER_VERSION" >"${MNT}/opt/actions-runner/.temperci-version"

if [[ ! -x "${MNT}/opt/actions-runner/run.sh" ]]; then
  echo "update-guest-runner: run.sh missing after unpack" >&2
  exit 1
fi

# Keep guest-agent install current (success-detection + JIT protocol).
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
if [[ -x "${SCRIPT_DIR}/guest-agent/install-into-rootfs.sh" ]]; then
  "${SCRIPT_DIR}/guest-agent/install-into-rootfs.sh" "$MNT"
fi

sync
echo "update-guest-runner: installed v${RUNNER_VERSION} into ${ROOTFS}"
echo "next: restart temperci-agent so warm VMs copy the updated image"
