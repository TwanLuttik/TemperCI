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
- Preferred approach: use the same microVM stack on the Proxmox host’s KVM, rather than inventing a separate “full Proxmox VM per job” path that is slower and harder to clean up.
- Proxmox-specific packaging: install docs, maybe a helper to reserve CPU/RAM for the CI pool, storage location for base images and scratch on a chosen datastore/path.

If nested virtualization or policy blocks microVMs on a given Proxmox setup, document that limitation rather than silently falling back to unclean long-lived VMs.

## Resource layout on a host

Operators should be able to configure:

| Setting | Purpose |
|---------|---------|
| `min_ready` / `max_ready` | Warm pool size |
| vCPU / memory per VM | Job shape |
| Disk path for images + scratch | Prefer fast local NVMe |
| Max concurrent busy VMs | Protect the host |

## Security notes for self-host

- Treat JIT configs as secrets; never log them in full.
- Job VMs should not be able to reach the control plane admin API without auth; prefer least privilege networking.
- Host agent requires privileges for KVM and network device setup; isolate that binary and its config.
- After destroy, no job workspace remains on the host outside intentional shared cache (future).

## Non-goals for first install story

- Kubernetes-first install (may come later; not required for Proxmox/Ubuntu lab)
- Windows/macOS job hosts
- Multi-tenant public SaaS hardening
