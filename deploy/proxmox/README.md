# Proxmox VE install path (TemperCI agent + Firecracker)

Install target: **Proxmox VE host** (Debian-based) with host KVM, using the **same** `temperci-agent` binary and semantics as bare Ubuntu.

Related:

- Operator smoke checklist: [quickstart.md](quickstart.md)
- Nested virt / policy limits: [nested-virt.md](nested-virt.md)
- Storage paths (`data_dir`): [storage.md](storage.md)
- Guest image (shared with Ubuntu): [../ubuntu/guest-image.md](../ubuntu/guest-image.md)
- Ubuntu twin (same agent model): [../ubuntu/README.md](../ubuntu/README.md)
- Control plane / GitHub App: [../../docs/architecture/control-plane-dry-run.md](../../docs/architecture/control-plane-dry-run.md)

## Design choice (read this first)

| Approach | Use for TemperCI MVP? |
|----------|----------------------|
| **Firecracker microVMs on the Proxmox host** (same as Ubuntu) | **Yes — preferred** |
| One full Proxmox QEMU VM (pct/qm) per Actions job | **No** — slower, harder to clean, different lifecycle |
| Agent inside a Proxmox guest VM that itself runs Firecracker | Only if host install is forbidden; requires nested virt (see [nested-virt.md](nested-virt.md)) |

TemperCI does **not** create `qm`/`pct` guests for jobs. The agent opens `/dev/kvm` and runs Firecracker under `data_dir` exactly like on Ubuntu. Proxmox continues to manage its own VMs; TemperCI coexists as a host service with a reserved resource budget.

## Supported hosts

- **Proxmox VE 8.x** (primary; Debian 12 base)
- Proxmox VE 7.x may work if `/dev/kvm` and a current Firecracker release are available — pin and test yourself
- Architecture: `x86_64` or `aarch64` matching your Firecracker + guest image

This repository’s CI/dev machine is **not** Proxmox; real-host smoke is operator-side only (see [quickstart.md](quickstart.md)).

## Hardware / kernel

- Physical (or properly nested) host with VT-x/AMD-V
- `/dev/kvm` present on the machine where the agent runs
- Prefer local NVMe for TemperCI `data_dir` (images + instance scratch)

Check on the Proxmox host:

```bash
ls -l /dev/kvm
# crw-rw---- 1 root kvm ... /dev/kvm

lsmod | grep kvm
# kvm_intel or kvm_amd + kvm

# Proxmox should already use KVM for its VMs:
pvesh get /nodes/$(hostname)/status | head
```

If `/dev/kvm` is missing, fix firmware virt flags or nested-virt policy before continuing — do not fall back to long-lived unclean VMs.

## Packages

Proxmox already ships QEMU/KVM. Install the small extra set TemperCI needs and ensure networking tools exist:

```bash
# As root on the Proxmox host:
./deploy/proxmox/host-prereqs.sh
```

Manual equivalent:

```bash
apt-get update
apt-get install -y \
  bridge-utils \
  iproute2 \
  curl \
  ca-certificates \
  jq
# qemu-system / kvm stack is already part of Proxmox VE; do not replace it.
```

Do **not** install a conflicting full `libvirt-daemon-system` stack unless you already use libvirt on that host. TemperCI’s Firecracker path does not require libvirt.

## Firecracker binary

Same as Ubuntu — place an official release on the host PATH:

```bash
ARCH=$(uname -m)   # x86_64 or aarch64
FC_VERSION=v1.9.1  # pin for your fleet

curl -fsSL -o /tmp/firecracker.tgz \
  "https://github.com/firecracker-microvm/firecracker/releases/download/${FC_VERSION}/firecracker-${FC_VERSION}-${ARCH}.tgz"
tar -xzf /tmp/firecracker.tgz -C /tmp
install -m 0755 "/tmp/release-${FC_VERSION}-${ARCH}/firecracker-${FC_VERSION}-${ARCH}" /usr/local/bin/firecracker
firecracker --version
```

## Permissions

MVP recommendation on Proxmox: run `temperci-agent` as **root** (same as [deploy/systemd/temperci-agent.service](../systemd/temperci-agent.service)). Reasons:

- Open `/dev/kvm`
- Create taps / netns (`CAP_NET_ADMIN`)
- Write under the chosen `data_dir` on local storage

If you later drop privileges:

1. User in group `kvm` (or ACL on `/dev/kvm`)
2. `CAP_NET_ADMIN` (and usually `CAP_NET_RAW`) for taps/netns
3. Write ownership on `data_dir` (`images/`, `instances/`)

Control plane can run as an unprivileged user; it does not need KVM.

## Directory layout (aligned with agent `data_dir`)

Default (matches Ubuntu and `deploy/agent.example.toml`):

```text
/var/lib/temperci/                 # data_dir
  images/                          # shared base rootfs + kernel (never deleted per job)
    ubuntu-2404-runner.ext4
    vmlinux
  instances/                       # per-VM scratch; destroyed with the VM
    <vm-id>/
      meta.json
      rootfs.overlay
      ...
```

```bash
mkdir -p /var/lib/temperci/{images,instances}
# chown only if not running agent as root
```

If Proxmox local storage lives under `/var/lib/vz`, you may put TemperCI data on a dedicated path on the same disk or a separate mount — see [storage.md](storage.md). Always set:

```toml
data_dir = "/var/lib/temperci"   # or your chosen path
vmm_backend = "firecracker"
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
```

Same layout constants as Ubuntu: [docs/decisions/hypervisor.md](../../docs/decisions/hypervisor.md).

## Resource budget (coexist with Proxmox VMs)

Proxmox does not auto-reserve CPU/RAM for TemperCI. Size the warm pool so host headroom remains for PVE guests:

| Knob | Config | Guidance |
|------|--------|----------|
| Warm pool | `min_ready` / `max_ready` | Start `1` / `2` on a shared host |
| Per-job shape | `vcpu` / `memory_mib` | Match labels; leave RAM for PVE + OS |
| Hard cap | `max_total_vms` | Caps tracked VMs if destroy fails |
| Disk | `data_dir` on fast local storage | Avoid shared/replicated storage for `instances/` |

Rough reservation example on a 32-thread / 128 GiB host also running PVE guests:

```text
TemperCI: min_ready=1, max_ready=2, vcpu=4, memory_mib=8192
→ keep ~16–24 GiB + 8 vCPU free for the CI pool beyond PVE workload
```

Document the budget in your runbook; Phase 7 multi-host scheduling does not replace host-level overcommit discipline.

## Networking notes

- Per-VM taps/netns use TemperCI names (`tc-tap-<id>`, `tc-ns-<id>`), not Proxmox `vmbr` guest NICs.
- Destroy removes process state, instance dirs, and best-effort `ip link del` / `ip netns del`.
- Prefer a dedicated host bridge or isolated path so job VMs cannot reach the control-plane admin API without auth (see install-targets security notes).
- Do not point Firecracker guests at Proxmox SDN configs unless you know how teardown interacts; MVP keeps net state under the agent.

## systemd

Use the same unit templates as Ubuntu:

- [deploy/systemd/temperci-control.service](../systemd/temperci-control.service)
- [deploy/systemd/temperci-agent.service](../systemd/temperci-agent.service)

```bash
install -m 0755 bin/temperci-control bin/temperci-agent /usr/local/bin/
mkdir -p /etc/temperci
# copy configs — see quickstart.md
cp deploy/systemd/temperci-*.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now temperci-control temperci-agent
```

## Create/destroy smoke (no GitHub)

On a Proxmox host with Go toolchain (or after installing binaries and using backend tests):

```bash
# Fake layout smoke (no Firecracker):
./scripts/vmm-smoke.sh --root /var/lib/temperci --n 5 --backend fake

# Real backend when firecracker + assets exist:
./scripts/vmm-smoke.sh --root /var/lib/temperci --n 5 --backend firecracker
```

After any real job or smoke, verify cleanup:

```bash
./deploy/proxmox/verify-cleanup.sh --data-dir /var/lib/temperci
```

## Same agent binary

| Component | Ubuntu | Proxmox |
|-----------|--------|---------|
| `temperci-agent` | yes | **same binary** |
| `temperci-control` | yes | same binary (on host or separate VM) |
| `vmm_backend` | `firecracker` | `firecracker` |
| Guest image pipeline | `deploy/ubuntu/guest-image.md` | **reuse** (build once, copy rootfs+kernel) |
| Config keys | `data_dir`, pool, token | identical |

No second product stack, no Proxmox-specific agent fork.
