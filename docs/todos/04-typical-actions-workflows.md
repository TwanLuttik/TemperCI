# Todo 4 — Typical Actions workflow toolchain

**Status area:** Typical Actions workflows — will fail (no Docker, Node, runner-image tools)  
**Goal:** Install a practical CI toolchain into the guest image via a hook script so common GitHub Actions jobs (`actions/checkout`, `actions/setup-node`, `actions/setup-python`, `npm ci`, docker CLI when the kernel allows) can run.

## Context

Full GitHub `runner-images` parity is out of scope. We need the 80% toolchain for Linux jobs.

Todo 2’s `build-guest-image.sh` will call:

```bash
source guest-packages.sh
temperci_install_guest_packages "$MNT"
```

You own **only** that hook and its docs.

## Files you may create/change

- Create: `deploy/ubuntu/guest-packages.sh`
- Create: `deploy/ubuntu/guest-toolchain.md`
- You may append a short “Toolchain” section at the **end** of `deploy/ubuntu/guest-image.md` (do not rewrite the rest of that file — todo 2 owns it)

Do **not** change Go code. Do **not** rewrite `build-guest-image.sh`.

## `guest-packages.sh` contract

```bash
# temperci_install_guest_packages <mounted-rootfs>
# Must be safe to source. Must define exactly that function.
# Use chroot "$1" apt-get ...
```

Install inside the rootfs (noble/amd64):

- `docker.io` (or moby) + start **disabled** by default; guest agent or a oneshot may start `docker` if `/usr/bin/dockerd` exists. Prefer enabling `docker.service` in the image so jobs that need Docker have it — if enable fails in chroot, log and continue.
- `nodejs` + `npm` from Ubuntu noble (or NodeSource only if Ubuntu node is too old; pin and document)
- `python3`, `python3-pip`, `python3-venv`
- `build-essential`, `gcc`, `g++`, `make`, `pkg-config`
- `jq`, `unzip`, `zip`, `rsync`, `tar`, `gzip`, `ca-certificates`
- `git`, `git-lfs`
- `libicu74` or whatever noble provides (`libicu-dev` as fallback)
- `sudo`, `gnupg`, `lsb-release`
- `iptables`, `iproute2`

Create a non-root user `runner` (uid 1001) with passwordless sudo **only if** it does not break the existing root guest-agent path. Official runner in this product currently runs as root (`RUNNER_ALLOW_RUNASROOT`). Prefer keeping root execution; still install sudo for workflow steps that call it.

Write `/etc/docker/daemon.json` with `"storage-driver": "overlay2"` and `"iptables": true` so Docker-in-Firecracker has a chance when the kernel has overlay + cgroup + namespace support.

Document kernel requirements in `guest-toolchain.md`:

- `CONFIG_NAMESPACES`, `CONFIG_NET_NS`, `CONFIG_PID_NS`, `CONFIG_USER_NS`
- `CONFIG_OVERLAY_FS`
- `CONFIG_CGROUPS`, `CONFIG_MEMCG`, `CONFIG_CGROUP_SCHED`
- `CONFIG_VETH`, `CONFIG_BRIDGE`

If the Firecracker sample kernel lacks these, Docker jobs will fail — say so clearly. Do not try to compile a custom kernel in this todo.

## Tests

`bash -n deploy/ubuntu/guest-packages.sh`

## Done when

- [ ] Hook script defines `temperci_install_guest_packages`
- [ ] Toolchain documented, including Docker kernel caveats
- [ ] Image still builds if someone sources the hook after debootstrap
- [ ] Do **not** git commit
