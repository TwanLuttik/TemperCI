# Storage paths on Proxmox (agent `data_dir`)

TemperCI agent layout is fixed by `internal/vmm` and config:

```toml
data_dir = "/var/lib/temperci"
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
# kernel_path = "/var/lib/temperci/images/vmlinux"
# scratch_dir = "/var/lib/temperci/instances"   # optional; parent becomes data_dir
```

```text
<data_dir>/
  images/                 # shared base assets (NOT deleted per job)
  instances/<vm-id>/      # entire tree deleted on destroy
```

There is **no** Proxmox storage plugin integration. Do not place job disks under `pvesm` guest disk IDs; TemperCI manages files itself.

## Where to put `data_dir` on a Proxmox node

| Location | Recommendation |
|----------|----------------|
| `/var/lib/temperci` on local root or dedicated mount | **Default / preferred** |
| Directory on local NVMe (e.g. `/mnt/nvme/temperci`) | Best for warm pool I/O |
| Path under `/var/lib/vz/` (PVE default storage tree) | OK if you keep a **dedicated subdirectory** not used by `qm` disks |
| Shared NFS / CephFS / CIFS for `instances/` | **Avoid** — latency + cleanup races |
| Replicated RBD volume for `instances/` | **Avoid** for MVP scratch |
| Same LV as large PVE guests without free space | Risk of ENOSPC mid-job |

### Shared vs local

- **`images/`** (base rootfs + kernel): can live on slightly slower local disk; rarely rewritten. May be rsynced between nodes as a copy, not as live shared mounts for writers.
- **`instances/`**: must be **local, fast, exclusive** to this agent. Destroy = `rm -rf instances/<id>`. Shared filesystems make “no stale disks” harder to prove.

### Example: dedicated disk

```bash
# Example only — adjust device and FS to your node
mkfs.ext4 /dev/nvme1n1p1
mkdir -p /mnt/temperci
echo '/dev/nvme1n1p1 /mnt/temperci ext4 defaults,noatime 0 2' >> /etc/fstab
mount /mnt/temperci
mkdir -p /mnt/temperci/{images,instances}

# agent.toml
# data_dir = "/mnt/temperci"
# image_path = "/mnt/temperci/images/ubuntu-2404-runner.ext4"
```

### Example: stay on root filesystem

```bash
mkdir -p /var/lib/temperci/{images,instances}
# Ensure root LV/thin pool has headroom for:
#   size(base image) + max_ready * size(overlay) + busy jobs
```

## Capacity planning

Each busy/warm VM keeps a COW/overlay under `instances/<id>/` (often on the order of the base image size depending on backend copy strategy). Budget:

```text
disk_needed ≳ size(images) + (max_ready + max_busy) * overlay_footprint + margin
```

Check free space before scaling the pool:

```bash
df -h /var/lib/temperci
du -sh /var/lib/temperci/images /var/lib/temperci/instances
```

## Interaction with Proxmox storage (`pvesm`)

- TemperCI does **not** call `pvesm alloc` / `qemu-img` through the PVE API for job disks.
- Do not register Firecracker overlays as Proxmox VM disks.
- Backups (`vzdump`) will not include TemperCI instances unless you deliberately back up `data_dir`; usually **exclude** `instances/` from backup (ephemeral by design). `images/` may be backed up or rebuilt from [guest-image.md](../ubuntu/guest-image.md).

## Teardown expectations

After a job finishes (or orphan sweep runs):

1. No directory `instances/<finished-vm-id>/`
2. No leftover `rootfs.overlay` for that id
3. No Firecracker process for that id
4. Warm pool members may still exist under `instances/` (expected)

Verification script: [verify-cleanup.sh](verify-cleanup.sh).

## Alignment summary

| Config key | Meaning on Proxmox |
|------------|--------------------|
| `data_dir` | Host path root; same semantics as Ubuntu |
| `image_path` | File under `data_dir/images/` (or absolute path you manage) |
| `scratch_dir` | Legacy alias for instances path; prefer `data_dir` |
| VMM layout | `internal/vmm.Layout` — not Proxmox `vm-XXX` dirs |
