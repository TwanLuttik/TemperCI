# Install targets

TemperCI is designed to run on infrastructure you own. MVP target:

1. **Bare Ubuntu server** with KVM (Firecracker microVMs)

Job isolation is **Firecracker only**. TemperCI does not create QEMU, Proxmox `qm`/`pct`, or LXC guests for jobs.

## Shared model

Each physical (or nested) machine that runs jobs installs the **TemperCI host agent**. The **control plane** may run on the same machine for small labs, or on a separate small VM/container for multi-host fleets.

```text
Small lab (single box)
┌─────────────────────────────────────┐
│ Ubuntu host                         │
│  · control plane                    │
│  · host agent                       │
│  · Firecracker microVMs for jobs    │
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
- MicroVMs via Firecracker (requires KVM). See [hypervisor.md](../decisions/hypervisor.md).
- Networking: host bridge or tap setup managed by the agent (documented install script).

If nested virtualization or policy blocks `/dev/kvm` on a given host, document that limitation rather than silently falling back to unclean long-lived VMs.

**Operator install path:** [deploy/ubuntu/README.md](../../deploy/ubuntu/README.md) · [quickstart.md](../../deploy/ubuntu/quickstart.md)

## Resource layout on a host

Operators should be able to configure:

| Setting | Purpose |
|---------|---------|
| `min_ready` / `max_ready` | Warm pool size |
| vCPU / memory per VM | Job shape |
| Disk path for images + scratch (`data_dir`) | Prefer fast **local** NVMe; avoid shared Ceph/NFS for `instances/` |
| Max concurrent busy VMs | Protect the host |

## Security notes for self-host

- Treat JIT configs as secrets; never log them in full.
- Job VMs should not be able to reach the control plane admin API without auth; prefer least privilege networking.
- Host agent requires privileges for KVM and network device setup; isolate that binary and its config.
- After destroy, no job workspace remains on the host outside intentional shared cache (future).

## Non-goals for first install story

- Kubernetes-first install (may come later; not required for an Ubuntu lab)
- Proxmox QEMU / LXC guests as a job runtime
- Windows/macOS job hosts
- Multi-tenant public SaaS hardening
