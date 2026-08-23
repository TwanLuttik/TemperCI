# Changelog

All notable changes to TemperCI are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Dashboard sidebar shows the running control version and a link when a newer GitHub release exists. Control checks `releases/latest` at most once every 6 hours (15 minutes after a failed check).
- Actions cache page expands each `org/repo` to show how disk is split across cache keys (size, share, version).

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

[Unreleased]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.5...HEAD
[0.1.5]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/TwanLuttik/TemperCI/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/TwanLuttik/TemperCI/releases/tag/v0.1.0
