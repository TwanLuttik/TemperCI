#!/usr/bin/env bash
# Build a Firecracker Ubuntu 24.04 (noble/amd64) rootfs + fetch a guest kernel.
#
# Usage (Linux, as root):
#   sudo ./deploy/ubuntu/build-guest-image.sh
#
# Outputs (override directory with TEMPERCI_IMAGES_DIR):
#   /var/lib/temperci/images/ubuntu-2404-runner.ext4
#   /var/lib/temperci/images/vmlinux
#
# Env:
#   TEMPERCI_IMAGES_DIR       default /var/lib/temperci/images
#   TEMPERCI_ROOTFS_SIZE      default 12G (sparse ext4)
#   TEMPERCI_RUNNER_VERSION   default 2.336.0
#   TEMPERCI_UBUNTU_MIRROR    default http://archive.ubuntu.com/ubuntu
#   TEMPERCI_KERNEL_URL       optional; passed through to fetch-kernel.sh
set -euo pipefail

if [[ "$(uname -s)" != "Linux" ]]; then
  echo "build-guest-image.sh must run on Linux (got $(uname -s)). Use an Ubuntu/KVM host." >&2
  exit 1
fi

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root (sudo ./deploy/ubuntu/build-guest-image.sh)" >&2
  exit 1
fi

if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "this script builds an amd64 rootfs; run on an x86_64 Linux host (got $(uname -m))" >&2
  exit 1
fi

for cmd in debootstrap mkfs.ext4 mount umount curl tar truncate chroot; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: ${cmd} (run sudo ./deploy/ubuntu/host-prereqs.sh)" >&2
    exit 1
  fi
done

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
IMAGES_DIR="${TEMPERCI_IMAGES_DIR:-/var/lib/temperci/images}"
ROOTFS_SIZE="${TEMPERCI_ROOTFS_SIZE:-12G}"
RUNNER_VERSION="${TEMPERCI_RUNNER_VERSION:-2.336.0}"
MIRROR="${TEMPERCI_UBUNTU_MIRROR:-http://archive.ubuntu.com/ubuntu}"
SUITE="noble"
ARCH="amd64"
ROOTFS_NAME="ubuntu-2404-runner.ext4"
KERNEL_NAME="vmlinux"

ROOTFS_DEST="${IMAGES_DIR}/${ROOTFS_NAME}"
KERNEL_DEST="${IMAGES_DIR}/${KERNEL_NAME}"
RUNNER_URL="https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"

mkdir -p "$IMAGES_DIR"

TMP_IMG="${IMAGES_DIR}/.${ROOTFS_NAME}.work.$$"
MNT=""
WORKDIR="${TMPDIR:-/var/tmp}/temperci-guest-build.$$"
mkdir -p "$WORKDIR"

cleanup() {
  if [[ -n "${MNT:-}" ]]; then
    local m
    for m in "${MNT}/dev/pts" "${MNT}/dev" "${MNT}/proc" "${MNT}/sys" "${MNT}/run" "${MNT}"; do
      umount -l "$m" 2>/dev/null || true
    done
  fi
  if [[ -n "${TMP_IMG:-}" && -e "${TMP_IMG}" ]]; then
    rm -f "$TMP_IMG"
  fi
  rm -rf "$WORKDIR"
}
trap cleanup EXIT INT TERM

echo "build-guest-image: rootfs=${ROOTFS_DEST} size=${ROOTFS_SIZE} runner=${RUNNER_VERSION}"

# orphan_file (mkfs default on recent e2fsprogs) is unreadable by 5.10 kernels.
# Disable it so a TEMPERCI_KERNEL_URL pointing at a 5.10 vmlinux still mounts.
echo "build-guest-image: creating sparse ${ROOTFS_SIZE} ext4"
rm -f "$TMP_IMG"
truncate -s "$ROOTFS_SIZE" "$TMP_IMG"
if ! mkfs.ext4 -F -L temperci-root -O ^orphan_file "$TMP_IMG" >/dev/null 2>&1; then
  mkfs.ext4 -F -L temperci-root "$TMP_IMG" >/dev/null
fi

MNT="$(mktemp -d /tmp/temperci-guest-mnt.XXXXXX)"
mount -o loop "$TMP_IMG" "$MNT"

echo "build-guest-image: debootstrap --variant=minbase ${SUITE} ${ARCH}"
debootstrap --variant=minbase --arch="$ARCH" "$SUITE" "$MNT" "$MIRROR"

mkdir -p "${MNT}/dev" "${MNT}/dev/pts" "${MNT}/proc" "${MNT}/sys" "${MNT}/run"
mount --bind /dev "${MNT}/dev"
if [[ -d /dev/pts ]]; then
  mount --bind /dev/pts "${MNT}/dev/pts"
fi
mount -t proc proc "${MNT}/proc"
mount -t sysfs sysfs "${MNT}/sys"
mount -t tmpfs tmpfs "${MNT}/run"

# Reachable DNS while apt runs inside the chroot.
printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\n' >"${MNT}/etc/resolv.conf"

cat >"${MNT}/etc/apt/sources.list" <<EOF
deb ${MIRROR} ${SUITE} main universe
deb ${MIRROR} ${SUITE}-updates main universe
deb http://security.ubuntu.com/ubuntu ${SUITE}-security main universe
EOF

# Prevent package postinst from starting services in the chroot.
cat >"${MNT}/usr/sbin/policy-rc.d" <<'EOF'
#!/bin/sh
exit 101
EOF
chmod 0755 "${MNT}/usr/sbin/policy-rc.d"

export DEBIAN_FRONTEND=noninteractive
chroot "$MNT" apt-get update
chroot "$MNT" apt-get install -y --no-install-recommends \
  systemd \
  systemd-sysv \
  udev \
  dbus \
  iproute2 \
  iputils-ping \
  ca-certificates \
  curl \
  git \
  sudo \
  openssh-client

if ! chroot "$MNT" apt-get install -y --no-install-recommends libicu74; then
  echo "build-guest-image: libicu74 unavailable; installing libicu-dev" >&2
  chroot "$MNT" apt-get install -y --no-install-recommends libicu-dev
fi

# systemd as /sbin/init (systemd-sysv normally provides this).
if [[ ! -e "${MNT}/sbin/init" ]]; then
  ln -sfn /lib/systemd/systemd "${MNT}/sbin/init"
fi

# Static rootfs + serial console. Network comes from kernel ip= (no DHCP).
cat >"${MNT}/etc/fstab" <<'EOF'
# <file system> <mount point> <type> <options> <dump> <pass>
/dev/vda / ext4 rw,relatime 0 1
EOF

echo "temperci" >"${MNT}/etc/hostname"
cat >"${MNT}/etc/hosts" <<'EOF'
127.0.0.1 localhost
127.0.1.1 temperci
::1       localhost ip6-localhost ip6-loopback
EOF

echo "UTC" >"${MNT}/etc/timezone"
if [[ -e "${MNT}/usr/share/zoneinfo/UTC" ]]; then
  ln -sfn /usr/share/zoneinfo/UTC "${MNT}/etc/localtime"
fi

# Do not let systemd-resolved replace resolv.conf with a stub that cannot work
# without a resolver daemon (and breaks static 8.8.8.8 / 1.1.1.1).
ln -sfn /dev/null "${MNT}/etc/systemd/system/systemd-resolved.service"
ln -sfn /dev/null "${MNT}/etc/systemd/system/systemd-networkd-wait-online.service"
# Rely on kernel ip= cmdline; do not enable networkd DHCP.
rm -f "${MNT}/etc/resolv.conf"
printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\noptions single-request-reopen\n' \
  >"${MNT}/etc/resolv.conf"

mkdir -p "${MNT}/etc/systemd/system/getty.target.wants"
ln -sfn /lib/systemd/system/serial-getty@.service \
  "${MNT}/etc/systemd/system/getty.target.wants/serial-getty@ttyS0.service"

# Unique machine-id is generated on first boot of each COW clone.
: >"${MNT}/etc/machine-id"
rm -f "${MNT}/var/lib/dbus/machine-id"

echo "build-guest-image: unpacking actions/runner v${RUNNER_VERSION}"
runner_tgz="${WORKDIR}/actions-runner.tar.gz"
if ! curl -fL --retry 3 --retry-delay 2 -o "$runner_tgz" "$RUNNER_URL"; then
  echo "build-guest-image: failed to download official actions/runner ${RUNNER_VERSION}" >&2
  echo "URL: ${RUNNER_URL}" >&2
  echo "Override with TEMPERCI_RUNNER_VERSION=<release> (see https://github.com/actions/runner/releases)." >&2
  exit 1
fi
mkdir -p "${MNT}/opt/actions-runner"
tar -xzf "$runner_tgz" -C "${MNT}/opt/actions-runner"
chown -R root:root "${MNT}/opt/actions-runner"
chmod -R u+rwX "${MNT}/opt/actions-runner"
printf '%s\n' "$RUNNER_VERSION" >"${MNT}/opt/actions-runner/.temperci-version"
# No config.sh — JIT is injected at bind time by the guest agent.

# After debootstrap + base runner deps, before guest-agent install:
PACKAGES_HOOK="$(dirname "$0")/guest-packages.sh"
if [[ -f "$PACKAGES_HOOK" ]]; then
  # shellcheck source=guest-packages.sh
  # Args: mounted rootfs path
  # shellcheck disable=SC1090
  source "$PACKAGES_HOOK"
  temperci_install_guest_packages "$MNT"
fi

mkdir -p "${MNT}/etc/systemd/system/multi-user.target.wants"
"${SCRIPT_DIR}/guest-agent/install-into-rootfs.sh" "$MNT"
# Bake the host intercept CA so Node/npm (actions/cache) trust the MITM.
if [[ -x "${SCRIPT_DIR}/install-cache-ca.sh" ]]; then
  "${SCRIPT_DIR}/install-cache-ca.sh" "$MNT" || echo "build-guest-image: warning: install-cache-ca.sh failed" >&2
fi
if [[ -x "${SCRIPT_DIR}/preseed-docker-images.sh" ]]; then
  "${SCRIPT_DIR}/preseed-docker-images.sh" "$MNT"
fi

if [[ ! -x "${MNT}/opt/actions-runner/run.sh" ]]; then
  echo "build-guest-image: /opt/actions-runner/run.sh missing after unpack" >&2
  exit 1
fi
if [[ ! -f "${MNT}/etc/systemd/system/temperci-runner-agent.service" ]]; then
  echo "build-guest-image: guest-agent unit missing after install-into-rootfs.sh" >&2
  exit 1
fi
if [[ ! -e "${MNT}/sbin/init" ]]; then
  echo "build-guest-image: /sbin/init missing (systemd not installed as init)" >&2
  exit 1
fi

chroot "$MNT" apt-get clean
rm -rf "${MNT}/var/cache/apt/archives/"*
rm -f "${MNT}/usr/sbin/policy-rc.d"

# Drop bind mounts before the loop unmount.
umount -l "${MNT}/dev/pts" 2>/dev/null || true
umount -l "${MNT}/dev" 2>/dev/null || true
umount -l "${MNT}/proc" 2>/dev/null || true
umount -l "${MNT}/sys" 2>/dev/null || true
umount -l "${MNT}/run" 2>/dev/null || true
sync
umount "$MNT"
rmdir "$MNT" || true
MNT=""

echo "build-guest-image: fetching Firecracker kernel -> ${KERNEL_DEST}"
"${SCRIPT_DIR}/fetch-kernel.sh" "$KERNEL_DEST"

# Atomic replace of any previous image.
mv -f "$TMP_IMG" "${ROOTFS_DEST}.tmp"
mv -f "${ROOTFS_DEST}.tmp" "$ROOTFS_DEST"
TMP_IMG=""

echo "guest image ready:"
echo "  rootfs: ${ROOTFS_DEST} (${ROOTFS_SIZE} sparse ext4)"
echo "  kernel: ${KERNEL_DEST}"
echo "  runner: ${RUNNER_VERSION} at /opt/actions-runner (no config.sh)"
echo "Point agent.toml at:"
echo "  image_path  = \"${ROOTFS_DEST}\""
echo "  kernel_path = \"${KERNEL_DEST}\""
echo "Confirm inside the image:"
echo "  sudo mount -o loop,ro ${ROOTFS_DEST} /mnt"
echo "  ls /mnt/opt/actions-runner/run.sh"
echo "  ls /mnt/etc/systemd/system/temperci-runner-agent.service"
echo "  sudo umount /mnt"
