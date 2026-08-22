# TemperCI documentation

Start here when onboarding to the project.

## Product and architecture

| Document | Description |
|----------|-------------|
| [architecture/overview.md](architecture/overview.md) | Components, GitHub integration, comparison to classic self-hosted runners |
| [architecture/security-review-mvp.md](architecture/security-review-mvp.md) | Phase 7 security review vs design §7; residual risks |
| [architecture/job-lifecycle.md](architecture/job-lifecycle.md) | Warm pool, JIT bind, job execution, teardown, orphan sweep |
| [architecture/install-targets.md](architecture/install-targets.md) | Ubuntu + Firecracker deployment model |
| [architecture/control-plane-dry-run.md](architecture/control-plane-dry-run.md) | Manual Phase 2 dry-run: GitHub App, webhooks, JIT mint |
| [../deploy/ubuntu/quickstart.md](../deploy/ubuntu/quickstart.md) | Single-node Ubuntu operator quickstart (Phase 5) |
| [../deploy/ubuntu/guest-image.md](../deploy/ubuntu/guest-image.md) | Guest rootfs + official actions/runner image pipeline |
| [../deploy/dashboard.md](../deploy/dashboard.md) | Operator dashboard (setup wizard, fleet UI) |
| [superpowers/specs/2026-08-12-temperci-operator-dashboard-design.md](superpowers/specs/2026-08-12-temperci-operator-dashboard-design.md) | Dashboard design (approach 1b) |

## Decisions

| Document | Description |
|----------|-------------|
| [decisions/language.md](decisions/language.md) | Why Go (and what else is allowed) |
| [decisions/repository-structure.md](decisions/repository-structure.md) | Monorepo layout and package boundaries |
| [decisions/module-path.md](decisions/module-path.md) | Go module path and Apache-2.0 license |
| [decisions/hypervisor.md](decisions/hypervisor.md) | Firecracker for MVP; fake VMM for macOS/dev |

## Specs and plans

| Document | Description |
|----------|-------------|
| [superpowers/specs/2026-08-12-temperci-platform-design.md](superpowers/specs/2026-08-12-temperci-platform-design.md) | Approved product design |
| [superpowers/specs/2026-08-22-oci-image-and-build-cache-design.md](superpowers/specs/2026-08-22-oci-image-and-build-cache-design.md) | Host-local Docker Hub/GHCR pull-through + BuildKit cache |
| [superpowers/plans/2026-08-12-temperci-mvp-plan.md](superpowers/plans/2026-08-12-temperci-mvp-plan.md) | MVP phases with checkboxes for progress tracking |

## Conventions

- Prefer short, factual docs over marketing copy.
- Specs live under `docs/superpowers/specs/`.
- Implementation plans live under `docs/superpowers/plans/` and use `- [ ]` / `- [x]` checkboxes.
- Architecture decisions that are durable product choices go in `docs/decisions/` or `docs/architecture/`.
