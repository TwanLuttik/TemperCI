# TemperCI documentation

Start here when onboarding to the project.

## Product and architecture

| Document | Description |
|----------|-------------|
| [architecture/overview.md](architecture/overview.md) | Components, GitHub integration, comparison to classic self-hosted runners |
| [architecture/job-lifecycle.md](architecture/job-lifecycle.md) | Warm pool, JIT bind, job execution, teardown, orphan sweep |
| [architecture/install-targets.md](architecture/install-targets.md) | Proxmox and bare Ubuntu deployment model |

## Decisions

| Document | Description |
|----------|-------------|
| [decisions/language.md](decisions/language.md) | Why Go (and what else is allowed) |
| [decisions/repository-structure.md](decisions/repository-structure.md) | Monorepo layout and package boundaries |
| [decisions/module-path.md](decisions/module-path.md) | Go module path and Apache-2.0 license |

## Specs and plans

| Document | Description |
|----------|-------------|
| [superpowers/specs/2026-08-12-temperci-platform-design.md](superpowers/specs/2026-08-12-temperci-platform-design.md) | Approved product design |
| [superpowers/plans/2026-08-12-temperci-mvp-plan.md](superpowers/plans/2026-08-12-temperci-mvp-plan.md) | MVP phases with checkboxes for progress tracking |

## Conventions

- Prefer short, factual docs over marketing copy.
- Specs live under `docs/superpowers/specs/`.
- Implementation plans live under `docs/superpowers/plans/` and use `- [ ]` / `- [x]` checkboxes.
- Architecture decisions that are durable product choices go in `docs/decisions/` or `docs/architecture/`.
