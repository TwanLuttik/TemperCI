# Hypervisor choice

## Decision

**MVP hypervisor: Firecracker.**

All agent code talks to `internal/vmm` (`Create`, `Boot`, `Destroy`, `Exists`, identity/metadata). Firecracker is one backend; a **fake** backend exists for unit tests and local development on hosts without KVM.

## Why Firecracker

| Criterion | Firecracker | Cloud Hypervisor |
|-----------|-------------|------------------|
| Ephemeral microVM CI (create → one job → destroy) | Designed for this (Lambda / Fargate heritage) | Also strong for cloud VMs |
| Host footprint | Very small binary; minimal device model | Larger feature set / footprint |
| API for automation | Stable HTTP-over-Unix-socket API | REST/OpenAPI over Unix socket |
| Jailer / isolation options | Jailer well documented for multi-tenant hosts | Different isolation story |
| Ops / docs for “many short-lived VMs” | Large public body of practice | Solid, more “full cloud guest” oriented |
| KVM requirement | Yes (`/dev/kvm`) | Yes (`/dev/kvm`) |

For TemperCI’s model (warm pool of anonymous guests, hard destroy after every job, no long-lived guest identity), Firecracker’s minimal device model and lifecycle match better than a fuller VMM.

Cloud Hypervisor remains a viable alternate backend later if we need features Firecracker deliberately omits (e.g. richer virtio, live migration). The `internal/vmm` boundary keeps that swap isolated.

## Development on macOS / non-KVM hosts

This project’s primary development machine may be **macOS arm64 without `/dev/kvm`**. Real Firecracker boot cannot run there.

- Unit and package tests use the **fake** VMM backend (`internal/vmm/fake`), which implements the same interface and host scratch layout without spawning a hypervisor.
- The Firecracker backend (`internal/vmm/firecracker`) is implemented for **Linux + KVM** production hosts. Constructors fail clearly when the OS is not Linux, `/dev/kvm` is missing, or the `firecracker` binary is not found.
- Linux smoke (create/destroy loop) lives under `scripts/` and is documented in `deploy/ubuntu/`.

## Host scratch layout (shared by backends)

```text
<root>/                          # e.g. /var/lib/temperci
  images/                        # shared base images + kernels (not deleted on destroy)
  instances/
    <vm-id>/
      meta.json                  # identity + resource metadata
      rootfs.overlay             # COW/diff disk for this instance
      api.sock                   # Firecracker API socket (real backend)
      firecracker.pid            # VMM process id (real backend)
      net/                       # per-VM network state (taps, netns names, proxy)
      logs/                      # optional instance logs
```

Destroy removes the entire `instances/<vm-id>/` tree and any associated host net/process state. Shared `images/` is never removed by per-VM teardown.

## Date

2026-08-12
