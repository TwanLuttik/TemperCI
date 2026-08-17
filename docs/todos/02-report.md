# Todo 2 report — Guest image pipeline

**Status: DONE**

Operator-facing build script and docs replace the debootstrap sketch. The Linux image build was not executed in this environment (Darwin); `bash -n` is clean and the Darwin path exits 1 as required.

## Files changed

| Path | Change |
|------|--------|
| `deploy/ubuntu/build-guest-image.sh` | **Created** (executable). Linux+root+x86_64 only. Sparse 12G ext4, noble minbase, systemd/init, serial getty, static DNS, fstab, pinned runner 2.321.0, guest-agent install, optional `guest-packages.sh` hook, atomic `mv` replace. |
| `deploy/ubuntu/fetch-kernel.sh` | **Created** (executable). Pins Firecracker CI v1.11 `vmlinux-6.1.102`; falls back to latest dated `firecracker-ci/` artifact; `TEMPERCI_KERNEL_URL` override. |
| `deploy/ubuntu/guest-image.md` | Rewritten as the operator runbook (prereqs → build → verify `run.sh` + unit → `agent.toml`). |
| `deploy/ubuntu/README.md` | Guest-image section + script pointer. |
| `deploy/ubuntu/quickstart.md` | Build command in steps + checklist. |
| `deploy/ubuntu/host-prereqs.sh` | Added `debootstrap` only. Kept existing packages (`e2fsprogs` / `iptables` already present). |

Not touched: Go packages, `guest-packages.sh` (todo 4; already present and sourced when found).

## How to run the build on Linux

On an x86_64 Ubuntu host from the repo root:

```bash
sudo ./deploy/ubuntu/host-prereqs.sh
sudo ./deploy/ubuntu/build-guest-image.sh
```

Outputs:

- `/var/lib/temperci/images/ubuntu-2404-runner.ext4`
- `/var/lib/temperci/images/vmlinux`

Overrides: `TEMPERCI_IMAGES_DIR`, `TEMPERCI_ROOTFS_SIZE=12G`, `TEMPERCI_RUNNER_VERSION=2.321.0`, `TEMPERCI_KERNEL_URL`, `TEMPERCI_UBUNTU_MIRROR`.

Verify:

```bash
sudo mount -o loop,ro /var/lib/temperci/images/ubuntu-2404-runner.ext4 /mnt
ls /mnt/opt/actions-runner/run.sh \
   /mnt/etc/systemd/system/temperci-runner-agent.service
sudo umount /mnt
```

Kernel only: `sudo ./deploy/ubuntu/fetch-kernel.sh`.

## Remaining risks

- Full debootstrap + runner + kernel download was **not run here** (macOS). First Linux run is the real integration test.
- Kernel pin depends on `s3.amazonaws.com/spec.ccfc.min`; if that bucket moves, set `TEMPERCI_KERNEL_URL`.
- `guest-packages.sh` (todo 4) is present in the tree, so a real build will also install Docker/Node/Python and need more time/space inside the 12G image.
- Firecracker still copies the whole 12G rootfs per VM; sparse on disk, but first-job overlay create is slow.
- Guest addressing still comes from kernel `ip=` (no DHCP). That matches the agent; if boot args omit `root=/dev/vda`, `/etc/fstab` is the fallback.
- Runner pin `2.321.0` is documented; bump `TEMPERCI_RUNNER_VERSION` when you want a newer official release.
