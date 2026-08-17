# Todo 2 — Guest image pipeline

**Status area:** Guest image — operator-built sketch, no shipped rootfs/kernel  
**Goal:** Replace the debootstrap sketch with a repeatable Linux build script that produces a Firecracker rootfs + documents the kernel, and installs the TemperCI guest agent.

## Context

`deploy/ubuntu/guest-image.md` is a manual outline. Production needs one script an operator (or the deploy host at `10.0.0.50`) can run:

```bash
sudo ./deploy/ubuntu/build-guest-image.sh
```

Output layout (already referenced by agent config):

```text
/var/lib/temperci/images/ubuntu-2404-runner.ext4
/var/lib/temperci/images/vmlinux
```

Guest agent install already exists: `deploy/ubuntu/guest-agent/install-into-rootfs.sh`. Call it from the image build after the runner tarball is unpacked.

## Files you may create/change

- Create: `deploy/ubuntu/build-guest-image.sh`
- Create: `deploy/ubuntu/fetch-kernel.sh` (optional helper)
- Modify: `deploy/ubuntu/guest-image.md` — become the operator doc for the script
- Modify: `deploy/ubuntu/README.md`, `deploy/ubuntu/quickstart.md` — point at the script
- Modify: `deploy/ubuntu/host-prereqs.sh` — add build-time packages (`debootstrap`, `e2fsprogs`, `systemd-container` or `arch-install-scripts` only if needed). **Do not remove** existing packages. If todo 1 also edits this file, only add missing packages.

Do **not** change Go packages. Do **not** put Docker/Node/Python install logic in the main script body — source this hook if present so todo 4 can own toolchain packages:

```bash
# After debootstrap + base runner deps, before guest-agent install:
PACKAGES_HOOK="$(dirname "$0")/guest-packages.sh"
if [[ -f "$PACKAGES_HOOK" ]]; then
  # shellcheck source=guest-packages.sh
  # Args: mounted rootfs path
  # shellcheck disable=SC1090
  source "$PACKAGES_HOOK"
  temperci_install_guest_packages "$MNT"
fi
```

If `guest-packages.sh` is missing, the image must still build (runner + guest agent + network + systemd).

## Image requirements

Rootfs must boot under Firecracker with:

1. Ubuntu 24.04 (`noble`) amd64 via `debootstrap --variant=minbase` plus required packages
2. systemd as `/sbin/init` (enable `systemd-networkd` **or** rely on kernel `ip=` cmdline; do not require DHCP)
3. Serial getty on `ttyS0` so `console.log` is useful
4. `/etc/fstab` for `/` (`/dev/vda ext4 rw`)
5. Static-friendly DNS: write `/etc/resolv.conf` with `8.8.8.8` / `1.1.1.1` (not a broken systemd-resolved stub)
6. Packages: `systemd`, `udev`, `dbus`, `iproute2`, `iputils-ping`, `ca-certificates`, `curl`, `git`, `libicu74` or `libicu-dev`, `sudo`, `openssh-client`, `python3` is optional here (todo 4)
7. Official `actions/runner` unpacked at `/opt/actions-runner` (pin version, e.g. `2.321.0` or latest stable; document the pin)
8. `RUNNER_ALLOW_RUNASROOT` is set by the guest agent, not baked as a long-lived runner config — **no** `config.sh`
9. Call `deploy/ubuntu/guest-agent/install-into-rootfs.sh "$MNT"`
10. Rootfs size: default **12G** sparse ext4 (override with `TEMPERCI_ROOTFS_SIZE=12G`)
11. Kernel: download a Firecracker-compatible `vmlinux` to the images dir. Prefer a documented URL (Firecracker getting-started kernel or a pinned CI artifact). Fail with a clear message if download fails. Do not vendor a multi-hundred-MB kernel in git.

Script must:

- Run only on Linux (exit 1 on Darwin with a message)
- Require root
- Be idempotent enough to rebuild (`rm` old image or write to a temp then `mv`)
- Use `set -euo pipefail`
- Accept `TEMPERCI_IMAGES_DIR` (default `/var/lib/temperci/images`)

## Docs

Rewrite `guest-image.md` so an operator can:

1. Install host prereqs
2. Run `build-guest-image.sh`
3. Confirm `run.sh` and guest-agent unit exist inside the image
4. Point `agent.toml` at the two artifacts

## Tests

No Go tests required. `bash -n deploy/ubuntu/build-guest-image.sh` must pass. If you add a small unit-check (e.g. `shellcheck` is optional).

## Done when

- [ ] `deploy/ubuntu/build-guest-image.sh` exists and is executable
- [ ] It installs runner + guest agent and writes kernel + rootfs paths
- [ ] It sources `guest-packages.sh` when present
- [ ] Docs updated
- [ ] `bash -n` clean
- [ ] Do **not** git commit
