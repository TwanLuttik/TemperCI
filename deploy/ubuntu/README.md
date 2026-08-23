# Ubuntu host prerequisites (TemperCI agent + Firecracker)

Primary install target: **Ubuntu 22.04 or 24.04 LTS** with KVM.

One-command install (packages, Firecracker, binaries, wizard):

```bash
curl -fsSL https://github.com/TwanLuttik/TemperCI/releases/latest/download/install.sh | sudo bash
```

This document covers host setup for the agent VMM path. Related:

- Single-node job path: [quickstart.md](quickstart.md)
- Guest image + runner: [guest-image.md](guest-image.md) (`sudo ./deploy/ubuntu/build-guest-image.sh`)
- Control plane / GitHub App: [docs/architecture/control-plane-dry-run.md](../../docs/architecture/control-plane-dry-run.md)

## Hardware / kernel

- CPU with hardware virtualization (Intel VT-x or AMD-V).
- KVM available as `/dev/kvm` (required by Firecracker).
- Prefer local NVMe for `/var/lib/temperci` (images + instance scratch).

Check:

```bash
ls -l /dev/kvm
# crw-rw----+ 1 root kvm ... /dev/kvm

# Optional: confirm module
lsmod | grep kvm
```

If `/dev/kvm` is missing on bare metal, enable virtualization in firmware. On a cloud VM, use a nested-virt or metal instance type that exposes KVM.

## Packages

```bash
sudo apt-get update
sudo apt-get install -y \
  qemu-kvm \
  libvirt-daemon-system \
  bridge-utils \
  iproute2 \
  curl \
  ca-certificates
```

Add the agent user to the `kvm` (and usually `libvirt`) group so it can open `/dev/kvm`:

```bash
sudo usermod -aG kvm,libvirt temperci   # or the service user you choose
```

Log out/in (or reboot) for group membership to apply.

## Firecracker binary

Firecracker is not always packaged in Ubuntu. Place a release binary on the host:

```bash
# Example: install to /usr/local/bin (adjust version/arch as needed)
ARCH=$(uname -m)   # x86_64 or aarch64
FC_VERSION=v1.9.1  # pin a known-good version for your fleet

curl -fsSL -o /tmp/firecracker.tgz \
  "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz"
sudo tar -xzf /tmp/firecracker.tgz -C /tmp
sudo install -m 0755 "/tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH}" /usr/local/bin/firecracker
firecracker --version
```

Optional: also install `jailer` from the same release if you enable jailer later. MVP may run Firecracker without jailer while networking hardens.

## Host directory layout

```text
/var/lib/temperci/
  images/                 # shared base rootfs + kernel (not deleted per job)
  instances/              # per-VM scratch; destroyed with the VM
```

```bash
sudo mkdir -p /var/lib/temperci/{images,instances}
sudo chown -R temperci:temperci /var/lib/temperci
```

## Guest image

On a Linux/amd64 host, after `host-prereqs.sh` (includes `debootstrap` and `e2fsprogs`):

```bash
sudo ./deploy/ubuntu/build-guest-image.sh
```

This writes `/var/lib/temperci/images/ubuntu-2404-runner.ext4` and `vmlinux`. Operator details, pins, and verification: [guest-image.md](guest-image.md).

Agent config (`deploy/agent.example.toml`) points at these paths:

- `image_path` — base guest rootfs (`ubuntu-2404-runner.ext4`)
- `kernel_path` — Firecracker `vmlinux` next to the rootfs
- `scratch_dir` — should be `/var/lib/temperci/instances` (or the parent root if the agent joins `images`/`instances` itself)

Decision detail: [docs/decisions/hypervisor.md](../../docs/decisions/hypervisor.md).

## Networking notes (MVP)

Per-VM taps / netns are named from the VM id (`tc-tap-<id>`, `tc-ns-<id>`). The agent destroy path removes process state, instance directories, and best-effort `ip link del` / `ip netns del`. Full bridge + DHCP integration is completed with the warm-pool / job path (later phases).

Capabilities needed for real net setup: `CAP_NET_ADMIN` (typically root or carefully ambient caps on the agent unit).

## Smoke: create/destroy loop (Linux + KVM)

With Firecracker installed and a test kernel/rootfs in place:

```bash
# From repo root on a Linux/KVM host:
./scripts/vmm-smoke.sh --root /var/lib/temperci --n 5
```

The script uses the Go test/fake path when Firecracker is unavailable, and documents when a real boot is skipped. Exit criteria for Phase 3: after N create/destroy cycles, `instances/` has no leftovers and no orphan Firecracker processes remain.

```bash
ls /var/lib/temperci/instances
pgrep -a firecracker || true
```

## macOS / non-KVM development

Real Firecracker boots are **not** supported on macOS. Develop and run unit tests with the fake VMM:

```bash
go test ./internal/vmm/... ./internal/cleanup/...
```

## systemd

Unit templates: [deploy/systemd/temperci-agent.service](../systemd/temperci-agent.service). Ensure the service user can access `/dev/kvm` and write under `/var/lib/temperci`.
