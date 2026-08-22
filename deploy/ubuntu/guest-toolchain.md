# Guest toolchain (typical Actions workflows)

This is the **80%** Linux CI toolchain for TemperCI guests. Full GitHub
[`runner-images`](https://github.com/actions/runner-images) parity is out of
scope. The hook installs enough for common jobs:

- `actions/checkout`
- `actions/setup-node` / `actions/setup-python`
- `npm ci` / `pip` / compile steps
- Docker CLI / `dockerd` **only when the guest kernel allows it** (see below)

Image build and kernel download stay in [guest-image.md](guest-image.md) (todo 2).
This file owns only the package hook and Docker-in-Firecracker caveats.

## Hook

`build-guest-image.sh` sources this after debootstrap + base runner deps, before
the guest-agent install:

```bash
source guest-packages.sh
temperci_install_guest_packages "$MNT"
```

The script is safe to source: it defines `temperci_install_guest_packages` and
does no work at import. It may also be run against an already-mounted rootfs:

```bash
sudo ./deploy/ubuntu/guest-packages.sh /mnt
```

If `guest-packages.sh` is missing, the image must still build (runner + guest
agent + network + systemd). Re-run the hook after a later rebuild to refresh
packages.

## Packages (Ubuntu 24.04 noble / amd64)

Installed with `chroot "$MNT" apt-get` from Ubuntu archives (universe enabled
when missing — `docker.io`, `nodejs`, `npm`, `git-lfs`, and `python3-pip` live
there).

| Package | Role |
|---------|------|
| `docker.io` | Docker CLI + `dockerd` (moby). Not NodeSource / Docker CE. |
| `docker-compose-v2` | `docker compose` plugin (`docker compose -f … up`) |
| `nodejs`, `npm` | System Node **18.x** + npm **9.x** from noble apt |
| `python3`, `python3-pip`, `python3-venv` | System Python **3.12** |
| `build-essential`, `gcc`, `g++`, `make`, `pkg-config` | Native builds / node-gyp |
| `jq`, `unzip`, `zip`, `rsync`, `tar`, `gzip`, `ca-certificates` | Archive / JSON / TLS |
| `git`, `git-lfs` | `actions/checkout` |
| `libicu74` | .NET / runner ICU (`libicu-dev` if `libicu74` is absent) |
| `sudo`, `gnupg`, `lsb-release` | Workflow `sudo`, apt keys, distro detection |
| `iptables`, `iproute2` | Docker networking + guest IP tooling |

No NodeSource pin. Noble’s Node 18 is old relative to current LTS, but
`actions/setup-node` downloads its own runtime for a requested version. System
`node` / `npm` exist so bare `npm ci` works without that action. Use
`actions/setup-node` when the job needs Node 20/22+. Same story for Python:
`actions/setup-python` fetches its own interpreter.

## `runner` user

The official `actions/runner` in this product **runs as root**
(`RUNNER_ALLOW_RUNASROOT` is set by the guest agent). The hook does **not**
change ownership of `/opt/actions-runner` and does not switch the guest-agent
unit off root.

It still creates user `runner` (uid **1001** when free) with passwordless sudo
(`/etc/sudoers.d/runner`) and adds that user to group `docker` when the group
exists. Workflow steps that call `sudo` or expect a non-root uid then work
without breaking the root guest-agent path.

## Docker

The hook writes `/etc/docker/daemon.json`:

```json
{
  "storage-driver": "overlay2",
  "iptables": true
}
```

It also installs `/usr/local/bin/docker` (`docker-cache-wrapper.sh`) ahead of
`/usr/bin/docker`. When `GITHUB_REPOSITORY` is set, `docker build` and
`docker buildx build` get BuildKit `--cache-from/--cache-to type=registry`
flags aimed at `ghcr.io/__temperci_cache/<org>/<repo>/buildkit`. The host
SNI intercept stores those layers locally and never forwards them to GHCR.
`DOCKER_BUILDKIT=1` is added to `/etc/environment`. Existing `--cache-to` is
left unchanged. `docker run` / `docker pull` are unchanged (Hub/GHCR layers
are cached by the host pull-through).

`policy-rc.d` blocks `dockerd` from starting **during the image build**. The hook
then tries to **enable** `docker.service` (`systemctl --root=… enable`, with a
multi-user.target symlink fallback) so a booted guest starts Docker for jobs
that need it. If enable fails in chroot, it logs a warning and continues.
`/usr/bin/dockerd` is still present; a guest oneshot or the operator can start
it later.

The Firecracker CI kernel has legacy iptables (`xtables`) but **not** `nf_tables`.
The hook therefore pins `iptables` / `ip6tables` to the `-legacy` alternatives.
Leaving Ubuntu’s default `iptables-nft` makes dockerd fail with
`Failed to initialize nft: Protocol not supported` and no `docker.sock`.
The kernel also omits `CONFIG_IP_NF_RAW`; Docker is started with
`DOCKER_INSECURE_NO_IPTABLES_RAW=1` so compose networks do not require the
`raw` table.

The guest rootfs is presented to Firecracker as a block device (ext4). Overlay2
inside the guest is therefore not overlay-on-overlay from the guest’s point of
view, provided the **kernel** implements overlayfs.

### Kernel requirements

Docker-in-Firecracker needs these options **in the guest `vmlinux`**, not on the
host:

| Option | Why |
|--------|-----|
| `CONFIG_NAMESPACES` | Container isolation |
| `CONFIG_NET_NS` | Per-container networking |
| `CONFIG_PID_NS` | Isolated PIDs |
| `CONFIG_USER_NS` | User namespaces (rootless / some runtimes) |
| `CONFIG_OVERLAY_FS` | `overlay2` storage driver |
| `CONFIG_CGROUPS` | Resource control |
| `CONFIG_MEMCG` | Memory cgroup |
| `CONFIG_CGROUP_SCHED` | CPU scheduling cgroup |
| `CONFIG_VETH` | Container veth pairs |
| `CONFIG_BRIDGE` | `docker0` bridge |

Also typically required in practice (often missing on tiny kernels): netfilter /
iptables (`CONFIG_IP_NF_IPTABLES`, `CONFIG_IP_NF_NAT`, `CONFIG_IP_NF_FILTER`,
`CONFIG_NETFILTER_XT_MATCH_ADDRTYPE`) and a mounted cgroup hierarchy (`/sys/fs/cgroup`;
systemd will do this if the kernel has cgroups).

**If the Firecracker sample / getting-started kernel lacks these, Docker jobs
will fail.** The `hello-vmlinux` style sample kernels are too small for
`dockerd`. Do **not** compile a custom kernel from this hook. Rebuild or replace
`vmlinux` with a Firecracker-compatible kernel that has the options above, then
confirm from a running guest (when `IKCONFIG` is enabled):

```bash
zcat /proc/config.gz | grep -E 'CONFIG_(NAMESPACES|NET_NS|PID_NS|USER_NS|OVERLAY_FS|CGROUPS|MEMCG|CGROUP_SCHED|VETH|BRIDGE)='
```

Expect `dockerd` errors about overlay, cgroup, veth, or iptables when a required
option is `n` or missing. Non-Docker jobs (checkout, setup-node, npm, pip,
gcc) do not need those options.

## Verify

After `build-guest-image.sh` (or after sourcing the hook into a mounted rootfs):

```bash
sudo mount -o loop,ro /var/lib/temperci/images/ubuntu-2404-runner.ext4 /mnt
chroot /mnt docker --version
chroot /mnt node --version
chroot /mnt npm --version
chroot /mnt python3 --version
chroot /mnt git --version
test -f /mnt/etc/docker/daemon.json
getent passwd runner || chroot /mnt getent passwd runner
sudo umount /mnt
```

## Out of scope

- Custom kernel build
- Docker Desktop
- Full `runner-images` package set (browsers, Android SDK, every language)
- Changing the guest-agent so the official runner runs as `runner` instead of root
