# Guest image pipeline (Ubuntu 24.04 + official actions/runner)

TemperCI guests run **upstream** [actions/runner](https://github.com/actions/runner) inside an ephemeral Ubuntu rootfs. Warm VMs boot from a **shared base image** with **no** GitHub credentials. JIT config is injected only at bind time.

Build the image on a Linux/KVM host (or the deploy host). The script does not run on macOS.

## Layout on the host

```text
/var/lib/temperci/images/
  ubuntu-2404-runner.ext4   # base rootfs (shared; never deleted per job)
  vmlinux                   # guest kernel for Firecracker
```

Agent config:

```toml
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
vmm_backend = "firecracker"
```

Per-job COW/overlay lives under `/var/lib/temperci/instances/<vm-id>/` and is destroyed after the job.

## 1. Install host prereqs

From the repo root on Ubuntu 22.04/24.04 (amd64):

```bash
sudo ./deploy/ubuntu/host-prereqs.sh
```

That installs KVM tools plus image-build packages (`debootstrap`, `e2fsprogs`). Also place a Firecracker binary on `PATH` ([README.md](README.md)).

## 2. Build the rootfs + kernel

```bash
sudo ./deploy/ubuntu/build-guest-image.sh
```

The script:

1. Creates a **12G sparse** ext4 (`TEMPERCI_ROOTFS_SIZE` overrides).
2. `debootstrap --variant=minbase` Ubuntu 24.04 (`noble`) amd64.
3. Installs systemd (`/sbin/init`), udev, dbus, networking tools, `git`, `curl`, `ca-certificates`, `sudo`, `openssh-client`, and `libicu74` (falls back to `libicu-dev`).
4. Writes `/etc/fstab` (`/dev/vda` ext4 rw), static DNS (`8.8.8.8` / `1.1.1.1`; systemd-resolved is masked), and enables `serial-getty@ttyS0`.
5. Relies on the Firecracker kernel `ip=` cmdline for addressing — **no DHCP**.
6. Unpacks official `actions/runner` **v2.336.0** at `/opt/actions-runner`. Does **not** run `config.sh`.
7. Sources `guest-packages.sh` when that hook exists (toolchain packages; owned separately).
8. Installs the TemperCI guest agent via `guest-agent/install-into-rootfs.sh`.
9. Optionally pre-loads Docker images listed in `TEMPERCI_PRESEED_IMAGES` or `TEMPERCI_PRESEED_IMAGES_FILE` into the guest Docker store (`preseed-docker-images.sh`).
10. Downloads a Firecracker-compatible `vmlinux` via `fetch-kernel.sh`.

To load images into an existing rootfs (stop `temperci-agent` first so warm clones are not taken mid-write):

```bash
sudo mount -o loop /var/lib/temperci/images/ubuntu-2404-runner.ext4 /mnt
sudo TEMPERCI_PRESEED_IMAGES=$'registry.example/app:1\nredis:7-alpine' \
  ./deploy/ubuntu/preseed-docker-images.sh /mnt
sudo umount /mnt
```

Rebuilds replace the previous artifacts atomically (`*.tmp` then `mv`).

### Environment

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEMPERCI_IMAGES_DIR` | `/var/lib/temperci/images` | Output directory |
| `TEMPERCI_ROOTFS_SIZE` | `12G` | Sparse ext4 size |
| `TEMPERCI_RUNNER_VERSION` | `2.336.0` | Official [actions/runner](https://github.com/actions/runner/releases) pin |
| `TEMPERCI_UBUNTU_MIRROR` | `http://archive.ubuntu.com/ubuntu` | debootstrap + apt mirror |
| `TEMPERCI_KERNEL_URL` | (see pin below) | Override kernel download URL |

Kernel-only refresh:

```bash
sudo ./deploy/ubuntu/fetch-kernel.sh
# or: sudo ./deploy/ubuntu/fetch-kernel.sh /var/lib/temperci/images/vmlinux
```

### Pins

- **actions/runner:** `2.336.0` (`actions-runner-linux-x64-2.336.0.tar.gz`). Bump with `TEMPERCI_RUNNER_VERSION`. To refresh an existing image without a full rebuild: `sudo ./deploy/ubuntu/update-guest-runner.sh`.
- **Kernel:** Firecracker CI **v1.11** Linux **6.1.102**  
  `https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.11/x86_64/vmlinux-6.1.102`  
  If that URL fails, `fetch-kernel.sh` tries the latest dated `firecracker-ci/YYYYMMDD-…` artifact from the same bucket ([getting-started.md](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md)). Override with `TEMPERCI_KERNEL_URL`. The kernel is **not** vendored in git.

After a rebuild, recycle warm VMs (agent idle recycle or `systemctl restart temperci-agent`).

## 3. Confirm `run.sh` and the guest-agent unit

```bash
sudo mount -o loop,ro /var/lib/temperci/images/ubuntu-2404-runner.ext4 /mnt
ls /mnt/opt/actions-runner/run.sh
ls /mnt/etc/systemd/system/temperci-runner-agent.service
ls /mnt/etc/systemd/system/multi-user.target.wants/temperci-runner-agent.service
ls /mnt/usr/local/sbin/temperci-runner-agent.sh
ls /mnt/sbin/init
cat /mnt/etc/fstab /mnt/etc/resolv.conf
sudo umount /mnt
ls -lh /var/lib/temperci/images/ubuntu-2404-runner.ext4 /var/lib/temperci/images/vmlinux
```

## 4. Point `agent.toml` at the artifacts

```toml
vmm_backend = "firecracker"
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
job_simulate_seconds = 0
```

Example file: [`../agent.example.toml`](../agent.example.toml). Host agent waits for `runner.exit` on the inject disk (default deadline 6h).

## What the base image contains

1. Ubuntu 24.04 (`noble`) amd64 minbase + the packages listed above.
2. Official `actions/runner` under `/opt/actions-runner`.
3. No long-lived `config.sh` registration and no JIT secrets.
4. Guest boot agent that waits for inject files (below).
5. Optional toolchain (`docker`, Node, Python, …) only if `guest-packages.sh` is present.

Networking: the agent creates tap + NAT; the kernel `ip=` cmdline sets the guest IP. The image does not require DHCP or `network-online.target`.

## JIT inject + start runner (bind path)

On bind the agent:

1. Writes the encoded JIT config into the **host-side guest channel**  
   `instances/<vm-id>/guest/jitconfig` (mode `0600`).
2. The guest agent starts official runner as:

   ```text
   RUNNER_ALLOW_RUNASROOT=1 /opt/actions-runner/run.sh --jitconfig <encoded-string>
   ```

   (`RUNNER_ALLOW_RUNASROOT` is set at runtime; it is not baked into the image.)
3. Logs only non-secret fields (`job_id`, `jit_bytes`, `warm_bind`) — never the JIT payload.

### GuestExec backends

| Backend | Behavior |
|---------|----------|
| `FileGuestExec` (fake / staging) | Writes under `instances/<id>/guest/` and records exec in `exec.log` + `runner.started` |
| `FirecrackerGuestExec` | Stages the same host files onto the inject disk; the **guest boot agent** starts `run.sh` |

MVP on macOS/tests uses the fake VMM + `FileGuestExec` so the full control↔agent path is covered without KVM.

## Guest agent (Ubuntu+KVM)

Firecracker attaches a second disk (`inject.ext4` → guest `/dev/vdb`). The host stages JIT under `instances/<id>/guest/` and syncs into that disk on bind. The guest agent:

1. Polls/mounts `/dev/vdb` at `/mnt/temperci` until `jitconfig` appears.
2. Runs `/opt/actions-runner/run.sh --jitconfig <encoded-string>`.
3. Writes the exit code to `/mnt/temperci/runner.exit` (host polls this).

The image build already calls:

```bash
sudo ./deploy/ubuntu/guest-agent/install-into-rootfs.sh /mnt
```

Re-install into an existing rootfs with the same command after loop-mounting it. Files:

- `deploy/ubuntu/guest-agent/temperci-runner-agent.sh`
- `deploy/ubuntu/guest-agent/temperci-runner-agent.service`

End-to-end operator steps: [quickstart.md](quickstart.md).

## Local Actions cache CA

The host cache gateway intercepts guest HTTPS to GitHub results / Azure blob hosts.
The guest must trust a TemperCI CA (or cache steps fail closed — they do not fall through to GitHub).

After mounting the rootfs:

```bash
sudo ./deploy/ubuntu/install-cache-ca.sh /mnt
```

Set `cache_listen_addr = "127.0.0.1:8743"` on the agent. Drain warm VMs after rebuilding the image.
