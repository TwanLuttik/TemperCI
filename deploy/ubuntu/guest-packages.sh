#!/usr/bin/env bash
# TemperCI guest toolchain hook (todo 4).
# Sourced by build-guest-image.sh after debootstrap + base runner deps:
#   source guest-packages.sh
#   temperci_install_guest_packages "$MNT"
# Safe to source: defines temperci_install_guest_packages and does no work at import.
# May also be executed: sudo ./guest-packages.sh <mounted-rootfs>

temperci_install_guest_packages() {
  local rootfs="${1:?usage: temperci_install_guest_packages <mounted-rootfs>}"

  if [[ ! -d "$rootfs" ]]; then
    echo "temperci_install_guest_packages: not a directory: $rootfs" >&2
    return 1
  fi
  if [[ ! -d "$rootfs/etc" || ! -x "$rootfs/bin/sh" ]]; then
    echo "temperci_install_guest_packages: $rootfs does not look like a rootfs" >&2
    return 1
  fi

  # Subshell keeps set -e / traps / helpers from leaking into the sourcing shell.
  (
    set -euo pipefail

    enable_universe() {
      local deb822="$rootfs/etc/apt/sources.list.d/ubuntu.sources"
      local list="$rootfs/etc/apt/sources.list"
      if [[ -f "$deb822" ]]; then
        if ! grep -qE '(^|[[:space:]])universe($|[[:space:]])' "$deb822"; then
          sed -i -E 's/^(Components:[[:space:]].*)$/\1 universe/' "$deb822"
        fi
        return 0
      fi
      mkdir -p "$(dirname "$list")"
      # Existing debootstrap images often have noble main only. Add updates +
      # universe so python3-venv/docker.io resolve against the same python3.
      touch "$list"
      grep -qE '^deb .* noble main' "$list" || echo "deb http://archive.ubuntu.com/ubuntu noble main" >>"$list"
      # Images often have noble-updates *universe* only; python3-venv lives in updates/main.
      grep -qE '^deb .* noble-updates.* main' "$list" || echo "deb http://archive.ubuntu.com/ubuntu noble-updates main universe" >>"$list"
      grep -qE '^deb .* noble-security.* main' "$list" || echo "deb http://security.ubuntu.com/ubuntu noble-security main universe" >>"$list"
      grep -qE '(^|[[:space:]])universe($|[[:space:]])' "$list" || {
        echo "deb http://archive.ubuntu.com/ubuntu noble universe"
      } >>"$list"
    }

    already_mounted() {
      local dest="$1"
      if command -v findmnt >/dev/null 2>&1; then
        findmnt -n "$dest" >/dev/null 2>&1
        return $?
      fi
      awk -v d="$dest" '$2 == d { found=1 } END { exit !found }' /proc/mounts 2>/dev/null
    }

    mount_if_needed() {
      local dest="$1" fstype="$2" src="${3:-none}"
      if already_mounted "$dest"; then
        return 0
      fi
      mkdir -p "$dest"
      if [[ "$fstype" == "bind" ]]; then
        mount --bind "$src" "$dest"
      else
        mount -t "$fstype" "$src" "$dest"
      fi
      mounted+=("$dest")
    }

    enable_docker_service() {
      local unit=""
      if [[ -f "$rootfs/usr/lib/systemd/system/docker.service" ]]; then
        unit=/usr/lib/systemd/system/docker.service
      elif [[ -f "$rootfs/lib/systemd/system/docker.service" ]]; then
        unit=/lib/systemd/system/docker.service
      else
        echo "temperci_install_guest_packages: docker.service unit not found after docker.io install" >&2
        return 1
      fi
      if command -v systemctl >/dev/null 2>&1; then
        if systemctl --root="$rootfs" enable docker.service; then
          return 0
        fi
        echo "temperci_install_guest_packages: systemctl --root enable docker.service failed" >&2
      fi
      mkdir -p "$rootfs/etc/systemd/system/multi-user.target.wants"
      ln -sfn "$unit" "$rootfs/etc/systemd/system/multi-user.target.wants/docker.service"
    }

    cleanup() {
      local i dest
      for ((i = ${#mounted[@]} - 1; i >= 0; i--)); do
        dest="${mounted[i]}"
        umount "$dest" 2>/dev/null || umount -l "$dest" 2>/dev/null || true
      done
      rm -f "$rootfs/usr/sbin/policy-rc.d"
    }

    echo "temperci_install_guest_packages: installing toolchain into $rootfs"

    # Static DNS so apt works even if the image has a dangling resolv.conf symlink.
    if [[ ! -e "$rootfs/etc/resolv.conf" || ! -s "$rootfs/etc/resolv.conf" ]]; then
      rm -f "$rootfs/etc/resolv.conf"
      printf 'nameserver 8.8.8.8\nnameserver 1.1.1.1\n' >"$rootfs/etc/resolv.conf"
    fi

    # docker.io, nodejs, npm, git-lfs, python3-pip live in Ubuntu universe.
    enable_universe

    mounted=()
    trap cleanup EXIT

    mkdir -p "$rootfs/proc" "$rootfs/sys" "$rootfs/dev" "$rootfs/dev/pts" "$rootfs/usr/sbin"
    mount_if_needed "$rootfs/proc" proc proc
    mount_if_needed "$rootfs/sys" sysfs sysfs
    if [[ -d /dev ]]; then
      mount_if_needed "$rootfs/dev" bind /dev
    fi
    if [[ -d /dev/pts ]]; then
      mount_if_needed "$rootfs/dev/pts" devpts devpts
    fi

    # Do not start services (docker) during the image-build chroot.
    printf '#!/bin/sh\nexit 101\n' >"$rootfs/usr/sbin/policy-rc.d"
    chmod 0755 "$rootfs/usr/sbin/policy-rc.d"

    chroot "$rootfs" env DEBIAN_FRONTEND=noninteractive apt-get update
    # Pull python3/base to noble-updates so python3-venv matches.
    chroot "$rootfs" env DEBIAN_FRONTEND=noninteractive apt-get install -y --only-upgrade python3 python3-minimal || true

    # noble provides libicu74; fall back if a future/other suite renamed it.
    icu_pkg=libicu-dev
    if chroot "$rootfs" apt-cache show libicu74 >/dev/null 2>&1; then
      icu_pkg=libicu74
    fi
    echo "temperci_install_guest_packages: using $icu_pkg"

    apt_group() {
      if ! chroot "$rootfs" env DEBIAN_FRONTEND=noninteractive apt-get install -y "$@"; then
        echo "temperci_install_guest_packages: warning: apt group failed: $*" >&2
        return 1
      fi
    }

    # Ubuntu noble apt: nodejs 18.x + npm 9.x. actions/setup-node downloads its
    # own runtime; system node is only a baseline. See guest-toolchain.md.
    apt_group docker.io docker-compose-v2 iptables iproute2 || true
    apt_group nodejs npm || true
    apt_group python3 python3-pip python3-venv python3.12-venv || true
    apt_group build-essential gcc g++ make pkg-config \
      jq unzip zip rsync tar gzip ca-certificates \
      git git-lfs "$icu_pkg" sudo gnupg lsb-release

    mkdir -p "$rootfs/etc/docker"
    cat >"$rootfs/etc/docker/daemon.json" <<'EOF'
{
  "storage-driver": "overlay2",
  "iptables": true,
  "ip6tables": false,
  "live-restore": false
}
EOF
    chmod 0644 "$rootfs/etc/docker/daemon.json"

    # Firecracker CI kernels have xtables (legacy) NAT, not nf_tables.
    # Ubuntu's default iptables-nft then fails: "Failed to initialize nft".
    if [[ -x "$rootfs/usr/sbin/iptables-legacy" ]]; then
      chroot "$rootfs" update-alternatives --set iptables /usr/sbin/iptables-legacy || true
    fi
    if [[ -x "$rootfs/usr/sbin/ip6tables-legacy" ]]; then
      chroot "$rootfs" update-alternatives --set ip6tables /usr/sbin/ip6tables-legacy || true
    fi

    mkdir -p "$rootfs/etc/systemd/system/docker.service.d"
    cat >"$rootfs/etc/systemd/system/docker.service.d/temperci.conf" <<'EOF'
[Unit]
After=network-pre.target containerd.service
Wants=containerd.service

[Service]
# Type=notify can fail closed in Firecracker if dockerd exits before sd_notify.
Type=simple
ExecStartPre=/bin/sh -c 'update-alternatives --set iptables /usr/sbin/iptables-legacy >/dev/null 2>&1 || true'
# Firecracker CI kernels omit CONFIG_IP_NF_RAW; skip Docker's raw DROP rules.
Environment=DOCKER_INSECURE_NO_IPTABLES_RAW=1
EOF

    # Prefer docker.service enabled so jobs that need Docker have dockerd at boot.
    # systemctl --root works without a running guest systemd; chroot enable often fails.
    # If enable fails, leave disabled — a guest oneshot may start /usr/bin/dockerd later.
    if ! enable_docker_service; then
      echo "temperci_install_guest_packages: warning: could not enable docker.service; leaving disabled" >&2
    fi

    # PATH wrapper so docker build / docker buildx build export host-local
    # BuildKit cache without workflow YAML changes.
    wrap_src="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/docker-cache-wrapper.sh"
    if [[ -f "$wrap_src" ]]; then
      mkdir -p "$rootfs/usr/local/bin"
      install -m 0755 "$wrap_src" "$rootfs/usr/local/bin/docker"
    else
      echo "temperci_install_guest_packages: warning: docker-cache-wrapper.sh not found next to guest-packages.sh" >&2
    fi
    mkdir -p "$rootfs/etc"
    touch "$rootfs/etc/environment"
    grep -q '^DOCKER_BUILDKIT=' "$rootfs/etc/environment" || echo 'DOCKER_BUILDKIT=1' >>"$rootfs/etc/environment"

    # Official actions/runner in this product runs as root (RUNNER_ALLOW_RUNASROOT).
    # Do not chown /opt/actions-runner. uid 1001 exists for workflow steps that
    # expect a non-root user and for passwordless sudo.
    if ! chroot "$rootfs" id -u runner >/dev/null 2>&1; then
      if chroot "$rootfs" getent passwd 1001 >/dev/null 2>&1; then
        echo "temperci_install_guest_packages: uid 1001 already in use; creating runner without fixed uid" >&2
        chroot "$rootfs" useradd -m -s /bin/bash runner
      else
        chroot "$rootfs" useradd -m -u 1001 -s /bin/bash runner
      fi
    fi
    mkdir -p "$rootfs/etc/sudoers.d"
    printf 'runner ALL=(ALL) NOPASSWD:ALL\n' >"$rootfs/etc/sudoers.d/runner"
    chmod 0440 "$rootfs/etc/sudoers.d/runner"
    if chroot "$rootfs" getent group docker >/dev/null 2>&1; then
      chroot "$rootfs" usermod -aG docker runner || true
    fi

    chroot "$rootfs" env DEBIAN_FRONTEND=noninteractive apt-get clean
    echo "temperci_install_guest_packages: done"
  )
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  temperci_install_guest_packages "$@"
fi
