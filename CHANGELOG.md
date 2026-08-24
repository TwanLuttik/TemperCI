# Changelog

All notable changes to TemperCI are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Guest VMs enable a 2 GiB swapfile at boot so a Node/Docker spike does not SIGABRT `Runner.Listener`.
- Read-only MCP server on the control plane at `POST /mcp`. Set `mcp_token` in `control.toml` (or Settings). Tools cover fleet overview, hosts, jobs, truncated logs, VMs, cache, and system status. Empty token disables the endpoint.

### Changed

- Official runner starts with workstation GC and a 1 GiB `DOTNET_GCHeapHardLimit` so Listener does not size its heap off guest RAM.
- Warm pool does not refill while any VM is busy (avoids packing busy 8g+6g plus two replacement warms on a 32 GiB host).

### Fixed

- Mid-job runner OOM / abort 134 is reported as `failure` instead of `success` (upstream `run-helper.sh` maps unknown codes to exit 0).
- Warm guests no longer remount the inject disk 20 times per second while waiting for JIT (was ~70% of a host core per idle VM). Idle poll is 2s after `agent.ready`.
- GitHub `workflow_job` completed/cancelled finishes the assignment and kills the guest (OOM-dead runners no longer sit `started` until the one-hour stuck timer). Host `WaitRunner` treats a quiet aborting `runner.log` as exit 97. Stuck reconcile also enqueues a VM kill.

## [0.1.6] - 2026-08-23

### Added

- MicroVM detail page with live serial console and guest-agent log. Click a VM on the MicroVMs list (or the VM id on a job).
- Guest UDP mailbox for ready/exit so the host does not loop-mount `inject.ext4` every 20ms. Proxmox INPUT accepts UDP 9876 on the TAP; inject read is a 500ms fallback.
- Dashboard sidebar shows the running control version and a link when a newer GitHub release exists.
- Actions cache page expands each `org/repo` to show how disk is split across cache keys (size, share, version).
- Job list and detail show GitHub step progress and live durations.

### Changed

- Job pickup: ACK GitHub webhooks before JIT mint; persist assignments after unlocking the store; SQLite WAL; skip extra sleep after a long-poll miss.
- VM create: clone a preformatted inject template, copy overlay / inject / TAP in parallel, and do not hold the VMM lock across boot or destroy. Overlay clone vs sparse vs dense is logged; disk admission uses allocated blocks.
- Cache hit path is an in-memory index (`OpenBlob` does not walk the tree). OCI uploads stream to disk. Hub anonymous tokens are cached.
- Docker wrapper only rewrites to `buildx` when the plugin exists; default build-cache mode is `min`. Guest image installs `docker-buildx`.
- Checkout / npm / Go HTTPS skip the cache intercept (`ipset` bypass). TLS leaves are ECDSA P-256 with HTTP/2.
- Dashboard: no 1s REST polling when the WebSocket is live; job list does not N+1 GitHub; snapshots skip when no clients are connected.
- `make test` rebuilds the Vite dashboard only when UI sources change (`make test-go` runs Go tests against an existing `dist/`).

### Fixed

- Guest ready no longer depends on fighting the guest for a loop-mounted inject disk (the cause of 45s warm-pool timeouts on PVE).
- `GET /api/v1/jobs` no longer fetches GitHub job metadata for every live row.

## [0.1.5] - 2026-08-23

### Added

- Cancel an in-flight job or kill a microVM from the dashboard. Control queues a kill on the next agent heartbeat, marks the assignment cancelled, and best-effort deletes the JIT runner.
- Tabbed settings, clickable MicroVM jobs with hover details, and a stable VM list (oldest boot first; new guests append).
- Webhook setup detects Tailscale Funnel and Cloudflare, and marks the webhook received when a job arrives (no buried GitHub ping required).
- Guest trust for the cache intercept CA (Node, npm/pnpm, runner user).
- `zstd` in the guest toolchain so `actions/cache` restores with zstd instead of gzip.
- Public product README. Architecture notes, ADRs, and specs stay under [docs/](docs/README.md).

### Fixed

- Agent SIGTERM now drains in-flight GitHub jobs instead of yanking the VM, so GitHub does not keep a dead JIT runner busy.
- Wizard Apply can restart units: install sudoers for `temperci-hostctl`, allow sudo from the control unit, and mark the fleet ready once the wizard config is complete.
- npm/pnpm `cafile` pointed only at the intercept cert, which replaced the default trust store and broke `registry.npmjs.org` (`UNABLE_TO_GET_ISSUER_CERT_LOCALLY`). `cafile` now uses the system CA bundle (which includes the intercept CA).

## [0.1.4] - 2026-08-22

### Fixed

- Wizard Continue now draft-saves GitHub App fields (org, app ID, webhook secret, PEM) instead of only advancing the step.

## [0.1.3] - 2026-08-22

### Fixed

- Installer replaces running binaries via a temp file + `install` so upgrades no longer fail with `ETXTBSY`.

## [0.1.2] - 2026-08-22

### Fixed

- Setup wizard can write the GitHub App PEM under systemd `ProtectSystem=strict` (`ReadWritePaths` for `/etc/temperci`).

## [0.1.1] - 2026-08-22

### Added

- Debian 12/13 and Proxmox VE hosts. The installer skips Debian `qemu-kvm` on PVE.

### Changed

- Install docs use `bash` as root, not `sudo`.

## [0.1.0] - 2026-08-22

Initial tagged release.

- One-command Linux install that launches the setup wizard.
- Control plane + host agent, Firecracker warm pool, official `actions/runner` via JIT.
- Operator dashboard, host-local Actions cache, OCI pull-through, and host resource admission.

[Unreleased]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.6...HEAD
[0.1.6]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/TwanLuttik/TemperCI/releases/tag/v0.1.0
