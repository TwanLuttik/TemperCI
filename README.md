# TemperCI

Self-hosted [GitHub Actions](https://docs.github.com/en/actions) runners on hardware you own.

Install a GitHub App, change `runs-on`, and jobs run in isolated [Firecracker](https://firecracker-microvm.github.io/) microVMs — same integration model as managed services like [Blacksmith](https://blacksmith.sh), without sending compute off your machines.

## Features

- **Drop-in labels** — `runs-on: temperci-4vcpu-ubuntu-2404`
- **Official runner** — GitHub’s [actions/runner](https://github.com/actions/runner) via just-in-time registration
- **Warm pool** — pre-booted microVMs so jobs are not blocked on cold boot
- **One job, one VM** — hard teardown of the guest and host scratch after every job
- **Local caches** — Actions cache and container-registry pull-through on the host
- **Operator dashboard** — setup wizard, fleet, jobs, logs, cancel / kill

## Install

Ubuntu 22.04/24.04 or Debian 12/13 (including Proxmox VE), amd64, with `/dev/kvm`. One command installs packages, Firecracker, binaries, and systemd, then starts the setup wizard. The guest image builds in the background. On Proxmox it does not install Debian `qemu-kvm` (PVE already provides QEMU).

```bash
curl -fsSL https://github.com/TwanLuttik/TemperCI/releases/latest/download/install.sh | bash
```

Open the printed URL (port `8080`) and finish the wizard (GitHub App + auth). The host stays `auth_mode=open` on the LAN until you set a password.

From a git checkout:

```bash
make build-ui build-linux
TEMPERCI_BIN_DIR=./bin ./deploy/ubuntu/install.sh
```

Operator notes: [deploy/ubuntu/quickstart.md](deploy/ubuntu/quickstart.md) · [deploy/dashboard.md](deploy/dashboard.md)

## How it works

```text
Install TemperCI GitHub App on your org
        │
        ▼
runs-on: temperci-4vcpu-ubuntu-2404
        │
        ▼
GitHub queues job → workflow_job webhook → TemperCI control plane
        │
        ▼
Mint JIT self-hosted runner config (single job)
        │
        ▼
Host agent binds a warm microVM → starts official runner → job runs
        │
        ▼
Destroy microVM + host scratch → replenish warm pool
```

## Development

Requires **Go 1.22+** and **Node.js 20+** (Vite dashboard under `web/`).

```bash
make build          # dashboard + binaries → ./bin/
make test           # unit tests
make build-linux    # Linux amd64 artifacts
```

Example configs live under [`deploy/`](deploy/) (`control.example.toml`, `agent.example.toml`, systemd units). The installer writes these for you; do not commit real secrets.

## Documentation

Architecture, operator guides, and design notes: [docs/](docs/README.md).

Releases: [CHANGELOG.md](CHANGELOG.md)

## License

[Apache License 2.0](LICENSE)
