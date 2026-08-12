# Guest image pipeline (Ubuntu + official actions/runner)

TemperCI guests run **upstream** [actions/runner](https://github.com/actions/runner) inside an ephemeral Ubuntu rootfs. Warm VMs boot from a **shared base image** with **no** GitHub credentials. JIT config is injected only at bind time.

This document describes how to build and refresh the base image used by the Firecracker backend on Ubuntu+KVM hosts.

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

## What the base image must contain

1. **Ubuntu 22.04 or 24.04** minimal userspace (matches your `temperci-…-ubuntu-2404` labels).
2. **Official `actions/runner`** unpacked under a fixed path, e.g. `/opt/actions-runner`.
3. **Dependencies** the runner needs (`libicu`, `git`, `curl`, ca-certs, etc.). Prefer tracking [actions/runner](https://github.com/actions/runner) release notes rather than cloning full `runner-images` for MVP.
4. **No** long-lived `config.sh` registration and **no** JIT secrets baked into the image.
5. Optional: a small **boot service** that waits for inject files under a known path (see inject channel below).

## Build sketch (operator)

Exact tooling can vary (Packer, `virt-builder`, chroot + `dd`). Minimal outline:

```bash
# 1) Create a sparse ext4 rootfs and mount it
IMG=/var/lib/temperci/images/ubuntu-2404-runner.ext4
sudo truncate -s 8G "$IMG"
sudo mkfs.ext4 -F "$IMG"
MNT=$(mktemp -d)
sudo mount -o loop "$IMG" "$MNT"

# 2) Bootstrap Ubuntu (debootstrap or unpack a cloud image)
sudo debootstrap --arch=amd64 noble "$MNT" http://archive.ubuntu.com/ubuntu

# 3) Install runner dependencies + official runner release
RUNNER_VERSION=2.321.0   # pin a known-good release
curl -fsSL -o /tmp/runner.tgz \
  "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz"
sudo mkdir -p "$MNT/opt/actions-runner"
sudo tar -xzf /tmp/runner.tgz -C "$MNT/opt/actions-runner"
# Install runner deps inside the chroot as needed (see runner docs).

# 4) Kernel for Firecracker (separate artifact)
# Obtain a Firecracker-compatible vmlinux and place it next to the rootfs.
# sudo install -m 0644 ./vmlinux /var/lib/temperci/images/vmlinux

sudo umount "$MNT"
```

Refresh policy: rebuild when you bump Ubuntu patch level or `actions/runner` version; recycle warm VMs after deploying a new `image_path` (agent idle recycle or restart).

## JIT inject + start runner (bind path)

On bind the agent:

1. Writes the encoded JIT config into the **host-side guest channel**  
   `instances/<vm-id>/guest/jitconfig` (mode `0600`).
2. Invokes the guest runner entrypoint conceptually as:

   ```text
   /opt/actions-runner/run.sh --jitconfig <path-to-jitconfig>
   ```

3. Logs only non-secret fields (`job_id`, `jit_bytes`, `warm_bind`) — never the JIT payload.

### GuestExec backends

| Backend | Behavior |
|---------|----------|
| `FileGuestExec` (fake / staging) | Writes under `instances/<id>/guest/` and records exec in `exec.log` + `runner.started` |
| `FirecrackerGuestExec` | Stages the same host files; real vsock/SSH guest exec is enabled on Ubuntu+KVM once the guest channel is wired. Until then, production images should use a **guest boot agent** that watches a vsock/virtio-vsock or 9p/shared inject path and starts `run.sh`. |

MVP on macOS/tests uses the fake VMM + `FileGuestExec` so the full control↔agent path is covered without KVM.

## Recommended production guest agent (Ubuntu+KVM)

Inside the base image, run a oneshot/path unit that:

1. Waits for `/run/temperci/jitconfig` (or equivalent delivered from the host stage).
2. Starts `/opt/actions-runner/run.sh --jitconfig /run/temperci/jitconfig`.
3. Exits when the runner process exits (one job).

Host agent `JobSimulate` is **only** for fake/dev without a real runner wait. On real guests, set `job_simulate_seconds = 0` and wait on the runner/guest-agent exit signal (operator path until full process watch is automated).

## Verification

```bash
# On a Linux+KVM host after building the image:
ls -lh /var/lib/temperci/images/
# Mount and confirm runner present:
sudo mount -o loop,ro /var/lib/temperci/images/ubuntu-2404-runner.ext4 /mnt
ls /mnt/opt/actions-runner/run.sh
sudo umount /mnt
```

End-to-end operator steps: [quickstart.md](quickstart.md).
