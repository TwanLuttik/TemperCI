# Todo 4 report — Typical Actions workflow toolchain

**Status: DONE**

Hook script `deploy/ubuntu/guest-packages.sh` is sourceable, defines `temperci_install_guest_packages`, and `bash -n` is clean. Toolchain docs (including Docker kernel caveats) live in `deploy/ubuntu/guest-toolchain.md`. `guest-image.md` and all Go files were left untouched; `build-guest-image.sh` was not rewritten (todo 2 owns the caller). Image build was not executed here (no Linux chroot/rootfs in this environment); once todo 2 sources the hook after debootstrap, `apt-get install` is idempotent and enable-docker failures are non-fatal.

## Packages installed (into the mounted noble/amd64 rootfs)

Via `chroot "$1" apt-get` after enabling Ubuntu universe when missing:

- `docker.io` (CLI + dockerd; not started during image build; `docker.service` enable attempted)
- `nodejs`, `npm` — Ubuntu noble apt (Node 18.x / npm 9.x; not NodeSource)
- `python3`, `python3-pip`, `python3-venv`
- `build-essential`, `gcc`, `g++`, `make`, `pkg-config`
- `jq`, `unzip`, `zip`, `rsync`, `tar`, `gzip`, `ca-certificates`
- `git`, `git-lfs`
- `libicu74` (fallback `libicu-dev`)
- `sudo`, `gnupg`, `lsb-release`
- `iptables`, `iproute2`

Also: user `runner` uid 1001 (when free) with passwordless sudo; `/opt/actions-runner` ownership unchanged so the root guest-agent path stays intact. Writes `/etc/docker/daemon.json` with `storage-driver: overlay2` and `iptables: true`.

## Docker caveats

`dockerd` is installed and the unit is enabled when possible, but Docker-in-Firecracker only works if the guest `vmlinux` has `CONFIG_NAMESPACES`, `CONFIG_NET_NS`, `CONFIG_PID_NS`, `CONFIG_USER_NS`, `CONFIG_OVERLAY_FS`, `CONFIG_CGROUPS`, `CONFIG_MEMCG`, `CONFIG_CGROUP_SCHED`, `CONFIG_VETH`, and `CONFIG_BRIDGE` (plus typical netfilter/iptables options). The Firecracker sample / getting-started kernel usually lacks these — Docker jobs will fail on that kernel. This todo does not compile a custom kernel. Non-Docker jobs (checkout, setup-node, npm, pip, gcc) do not need those options.

## Files changed

Created only:

- `deploy/ubuntu/guest-packages.sh`
- `deploy/ubuntu/guest-toolchain.md`
- `docs/todos/04-report.md` (this report)

Not edited: Go sources, `deploy/ubuntu/build-guest-image.sh`, `deploy/ubuntu/guest-image.md`.
