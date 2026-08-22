# Install targets

TemperCI is designed to run on infrastructure you own. MVP targets:

1. **Bare Ubuntu server** (primary development target)
2. **Proxmox VE host** (same agent model; host provides KVM)

## Shared model

Regardless of install target, each physical (or nested) machine that runs jobs installs the **TemperCI host agent**. The **control plane** may run on the same machine for small labs, or on a separate small VM/container for multi-host fleets.

```text
Small lab (single box)
┌─────────────────────────────────────┐
│ Ubuntu or Proxmox host              │
│  · control plane                    │
│  · host agent                       │
│  · microVMs for jobs                │
└─────────────────────────────────────┘

Multi-host
┌─────────────────┐     ┌─────────────────┐
│ control plane   │────►│ agent host A    │
│ (VM/container)  │     └─────────────────┘
└────────┬────────┘     ┌─────────────────┐
         └─────────────►│ agent host B    │
                        └─────────────────┘
```

## Bare Ubuntu

- Ubuntu LTS (document exact versions during implementation; target 22.04/24.04).
- KVM available (`/dev/kvm`).
- Host agent runs as a systemd service.
- MicroVMs via a lightweight hypervisor suitable for ephemeral CI (Firecracker or Cloud Hypervisor — decision locked in implementation spike; both require KVM).
- Networking: host bridge or tap setup managed by the agent (documented install script).

## Proxmox VE

- Agent runs **on the Proxmox host** (or in a privileged context that can create KVM guests quickly).
- Goal: same warm-pool semantics as bare Ubuntu.
- Preferred approach: use the same microVM stack (Firecracker) on the Proxmox host’s KVM, rather than inventing a separate “full Proxmox VM per job” path that is slower and harder to clean up.
- Proxmox-specific packaging: install docs, host prereq script, storage guidance, cleanup verification, and resource-budget notes for coexisting with PVE guests.

**Operator install path:** [deploy/proxmox/README.md](../../deploy/proxmox/README.md) · [quickstart.md](../../deploy/proxmox/quickstart.md) · [nested-virt.md](../../deploy/proxmox/nested-virt.md) · [storage.md](../../deploy/proxmox/storage.md)

If nested virtualization or policy blocks microVMs on a given Proxmox setup, document that limitation rather than silently falling back to unclean long-lived VMs. See [nested-virt.md](../../deploy/proxmox/nested-virt.md).

## Resource layout on a host

Operators should be able to configure:

| Setting | Purpose |
|---------|---------|
| `min_ready` / `max_ready` | Desired warm pool / concurrent jobs. Agent clamps this to host RAM+disk. |
| `vcpu` / `memory_mib` | Job shape. RAM is a hard create gate; vCPU is not. |
| `host_reserve_memory_mib` / `host_reserve_disk_mib` | Headroom left for the host OS, cache, and overlays (defaults 2048 / 5120). |
| Disk path for images + scratch (`data_dir`) | Prefer fast **local** NVMe; avoid shared Ceph/NFS for `instances/` |

## Security notes for self-host

- Treat JIT configs as secrets; never log them in full.
- Job VMs should not be able to reach the control plane admin API without auth; prefer least privilege networking.
- Host agent requires privileges for KVM and network device setup; isolate that binary and its config.
- After destroy, no job workspace remains on the host outside intentional shared cache (future).

## Non-goals for first install story

- Kubernetes-first install (may come later; not required for Proxmox/Ubuntu lab)
- Windows/macOS job hosts
- Multi-tenant public SaaS hardening
