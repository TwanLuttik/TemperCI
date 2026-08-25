#!/bin/bash
# Install TemperCI guest runner agent into a mounted Ubuntu rootfs (or chroot path).
# Usage: sudo ./install-into-rootfs.sh /mnt/rootfs
set -euo pipefail

ROOT="${1:?usage: $0 /path/to/mounted/rootfs}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

install -d -m 0755 "$ROOT/usr/local/sbin"
install -m 0755 "$SCRIPT_DIR/temperci-runner-agent.sh" "$ROOT/usr/local/sbin/temperci-runner-agent.sh"
install -m 0755 "$SCRIPT_DIR/extract-worker-workflow.sh" "$ROOT/usr/local/sbin/extract-worker-workflow.sh"
install -m 0755 "$SCRIPT_DIR/collect-page-logs.sh" "$ROOT/usr/local/sbin/collect-page-logs.sh"
install -d -m 0755 "$ROOT/etc/systemd/system"
install -m 0644 "$SCRIPT_DIR/temperci-runner-agent.service" "$ROOT/etc/systemd/system/temperci-runner-agent.service"

# Enable at boot inside the image
if [ -d "$ROOT/etc/systemd/system/multi-user.target.wants" ]; then
  ln -sf /etc/systemd/system/temperci-runner-agent.service \
    "$ROOT/etc/systemd/system/multi-user.target.wants/temperci-runner-agent.service"
fi

# Ensure mount point exists
install -d -m 0755 "$ROOT/mnt/temperci"

# Runner tarball is often owned by uid 1001 and not writable by root in some setups;
# run.sh must be able to copy run-helper.sh.template → run-helper.sh.
if [ -d "$ROOT/opt/actions-runner" ]; then
  chown -R root:root "$ROOT/opt/actions-runner" || true
  chmod -R u+rwX "$ROOT/opt/actions-runner" || true
fi

# Pin Listener/Worker GC heap via exec wrappers. Job steps stay uncapped.
if [ -d "$ROOT/opt/actions-runner/bin" ]; then
  "$SCRIPT_DIR/wrap-runner-dotnet.sh" "$ROOT"
fi

# Ensure work dir for JIT copy
install -d -m 0755 "$ROOT/run/temperci"

echo "installed temperci-runner-agent into $ROOT"
echo "next: ensure /opt/actions-runner is present, then unmount and use as Firecracker rootfs"
