# TemperCI

**TemperCI** is a self-hostable, open-source GitHub Actions runner platform. It follows the same integration model as managed services like [Blacksmith](https://blacksmith.sh): install a GitHub App, change `runs-on`, and jobs execute on **your** hardware — Ubuntu servers with KVM, isolated in Firecracker microVMs.

## What you get

- **Drop-in Actions labels** — e.g. `runs-on: temperci-4vcpu-ubuntu-2404`
- **Official runner binary** — uses GitHub’s [actions/runner](https://github.com/actions/runner) via just-in-time (JIT) registration
- **Warm microVM pool** — pre-booted VMs so jobs are not blocked on cold boot
- **Hard teardown** — every job destroys its microVM and host-side scratch; no stale leftovers
- **Self-hosted** — control plane and dataplane run on infrastructure you own

## Status

Ubuntu single-node job path is in place: control plane + host agent, Firecracker warm pool, hard teardown.

| Doc | Purpose |
|-----|---------|
| [docs/README.md](docs/README.md) | Documentation index |
| [docs/architecture/overview.md](docs/architecture/overview.md) | System architecture and GitHub flow |
| [docs/architecture/job-lifecycle.md](docs/architecture/job-lifecycle.md) | Warm pool, bind, run, destroy |
| [docs/architecture/control-plane-dry-run.md](docs/architecture/control-plane-dry-run.md) | Phase 2 operator dry-run (App + webhooks + JIT) |
| [docs/decisions/language.md](docs/decisions/language.md) | Language choice and rationale |
| [docs/decisions/repository-structure.md](docs/decisions/repository-structure.md) | Monorepo layout |
| [docs/decisions/module-path.md](docs/decisions/module-path.md) | Go module path and license |
| [docs/superpowers/specs/2026-08-12-temperci-platform-design.md](docs/superpowers/specs/2026-08-12-temperci-platform-design.md) | Full product design spec |
| [docs/superpowers/plans/2026-08-12-temperci-mvp-plan.md](docs/superpowers/plans/2026-08-12-temperci-mvp-plan.md) | Trackable MVP plan (checkboxes) |

## Local development

Requirements: **Go 1.22+** and **Node.js 20+** (Vite dashboard under `web/`).

```bash
# Build dashboard (Vite) + all binaries into ./bin/
make build
# UI only: make build-ui
# Go only (after UI): make build-go

# Run unit tests
make test

# Version
./bin/temperci-control -version
./bin/temperci-agent -version

# Control plane (requires a filled-in config; see dry-run doc)
# ./bin/temperci-control -config /etc/temperci/control.toml
```

Example operator configs live under [`deploy/`](deploy/) (`control.example.toml`, `agent.example.toml`, systemd units). Copy them to `/etc/temperci/` when installing on a host — do not commit real secrets.

**Operator dashboard** (embedded in control plane): open `http://<control>:8080/` after install — setup wizard, hosts, jobs, optional password users. See [deploy/dashboard.md](deploy/dashboard.md).

- Webhook + JIT dry-run: [docs/architecture/control-plane-dry-run.md](docs/architecture/control-plane-dry-run.md)
- Single-node Ubuntu quickstart: [deploy/ubuntu/quickstart.md](deploy/ubuntu/quickstart.md)
- Guest image pipeline: [deploy/ubuntu/guest-image.md](deploy/ubuntu/guest-image.md)

## High-level flow

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

## License

[Apache License 2.0](LICENSE).
