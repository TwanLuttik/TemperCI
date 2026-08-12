# Kokanee MVP Implementation Plan

> **For agentic workers:** Implement phase-by-phase. Use checkbox (`- [ ]` / `- [x]`) syntax for tracking. Do not skip teardown/orphan requirements when implementing agent work. Prefer TDD for state machines and cleanup logic.

**Goal:** Ship a self-hostable GitHub Actions runner path (control plane + host agent) that uses JIT self-hosted runners, a warm microVM pool, and hard post-job cleanup on bare Ubuntu, with a documented Proxmox install path.

**Architecture:** Go monorepo with `kokanee-control` (GitHub webhooks + JIT + scheduling) and `kokanee-agent` (warm pool + VMM + teardown). Guests run upstream `actions/runner` for a single job, then are destroyed.

**Tech stack:** Go 1.22+, GitHub App + REST JIT API, KVM microVMs (Firecracker or Cloud Hypervisor behind `internal/vmm`), systemd install, Markdown docs.

**Spec:** [docs/superpowers/specs/2026-08-12-kokanee-platform-design.md](../specs/2026-08-12-kokanee-platform-design.md)

## Global constraints

- Primary language is **Go**; binaries `kokanee-control` and `kokanee-agent`.
- Use **official** `actions/runner` + **JIT** registration only (no long-lived `config.sh` as the product path).
- Warm VMs must have **no secrets / no JIT** until bind.
- After every job: **destroy guest + delete host scratch**; never reuse guest disk for the next job.
- Hypervisor details stay behind `internal/vmm`.
- GitHub API details stay behind `internal/github`.
- Follow layout in [docs/decisions/repository-structure.md](../../decisions/repository-structure.md).

## Progress summary

| Phase | Name | Status |
|-------|------|--------|
| 0 | Documentation foundation | In progress |
| 1 | Go module skeleton + CI | Not started |
| 2 | GitHub App, webhooks, JIT mint | Not started |
| 3 | VMM create/destroy + host cleanup | Not started |
| 4 | Warm pool state machine | Not started |
| 5 | End-to-end Ubuntu job | Not started |
| 6 | Proxmox install path | Not started |
| 7 | Hardening (recycle, metrics, multi-host) | Not started |

Update the table as phases complete. Check items below as you go.

---

## Phase 0 — Documentation foundation

**Outcome:** Contributors understand product, language, layout, and the trackable plan.

- [x] Root `README.md` with product summary and doc links
- [x] `docs/README.md` index
- [x] Architecture overview (`docs/architecture/overview.md`)
- [x] Job lifecycle / warm pool / teardown (`docs/architecture/job-lifecycle.md`)
- [x] Install targets (`docs/architecture/install-targets.md`)
- [x] Language decision (`docs/decisions/language.md`)
- [x] Repository structure decision (`docs/decisions/repository-structure.md`)
- [x] Design spec (`docs/superpowers/specs/2026-08-12-kokanee-platform-design.md`)
- [x] This plan file with checkboxes
- [ ] Choose SPDX license (Apache-2.0 or MIT) and add `LICENSE`
- [ ] Confirm public module path / GitHub org name for `go.mod`

**Exit criteria:** Design + plan reviewed; license and module path decided or explicitly deferred with owners.

---

## Phase 1 — Go module skeleton + CI

**Outcome:** Empty but buildable monorepo matching the decided layout.

### Tasks

- [ ] Initialize `go.mod` with agreed module path
- [ ] Create `cmd/kokanee-control/main.go` (version/help stub)
- [ ] Create `cmd/kokanee-agent/main.go` (version/help stub)
- [ ] Create placeholder packages under `internal/` (`config`, `logging`, `api`, `control`, `agent`, `github`, `vmm`, `cleanup`) with package docs
- [ ] Add `Makefile` targets: `build`, `test`, `lint` (lint may no-op until linter config exists)
- [ ] Add example configs under `deploy/` (`control.example.toml`, `agent.example.toml`)
- [ ] Add systemd unit templates under `deploy/systemd/`
- [ ] Add `.github/workflows/ci.yml`: `go test ./...`, `go build` both cmds
- [ ] Document local dev build in `README.md` or `docs/`

### Suggested first files

```text
go.mod
cmd/kokanee-control/main.go
cmd/kokanee-agent/main.go
internal/config/config.go
internal/logging/logging.go
internal/api/types.go
Makefile
deploy/control.example.toml
deploy/agent.example.toml
.github/workflows/ci.yml
```

**Exit criteria:** `make build` produces both binaries; CI green on the branch.

---

## Phase 2 — GitHub App, webhooks, JIT mint

**Outcome:** Control plane can verify webhooks and mint JIT configs for Kokanee labels.

### Tasks

- [ ] Implement GitHub App authentication (JWT → installation token) in `internal/github`
- [ ] Webhook signature verification for `workflow_job`
- [ ] Parse queued jobs; filter to Kokanee-owned labels only
- [ ] Call `generate-jitconfig` (org-level MVP) with required labels and runner group
- [ ] Persist minimal assignment state (in-memory OK for single-node MVP; disk/sqlite acceptable)
- [ ] Control plane HTTP server: webhook endpoint + healthz
- [ ] Unit tests with fixture payloads under `testdata/webhooks/`
- [ ] Manual dry-run doc: create GitHub App, install on test org, receive queued event

### Acceptance tests

- [ ] Invalid webhook signature → 401/403, no side effects
- [ ] Non-Kokanee labels → ignored (200, no JIT call)
- [ ] Kokanee label queued → JIT client called with expected labels (mock transport)

**Exit criteria:** In a test org, a queued workflow with Kokanee labels results in a successful JIT config mint (even if no agent consumes it yet).

---

## Phase 3 — VMM create/destroy + host cleanup

**Outcome:** Agent can create a microVM, destroy it, and leave no host leftovers.

### Tasks

- [ ] Spike: choose Firecracker or Cloud Hypervisor; record decision in `docs/decisions/hypervisor.md`
- [ ] Define `internal/vmm` interface: `Create`, `Boot`, `Destroy`, `Exists`, identity/metadata
- [ ] Implement chosen backend on Ubuntu + KVM
- [ ] Define host scratch layout (images dir, instance dir per VM id)
- [ ] Implement `internal/cleanup`: delete instance dir, nets, processes for a VM id
- [ ] Implement orphan sweep: compare desired state vs host; destroy unknowns
- [ ] Integration test or scripted smoke: create → destroy → assert no leftover files/processes
- [ ] Document host prerequisites (`/dev/kvm`, packages) under `deploy/ubuntu/`

### Destroy checklist (must be coded, not only documented)

- [ ] Stop guest / VMM process
- [ ] Remove VM definition / jailer resources if any
- [ ] Delete COW/overlay disks and instance metadata
- [ ] Remove taps/netns/proxy state for that id
- [ ] Idempotent destroy (second call is safe)

**Exit criteria:** Scripted create/destroy loop of N VMs ends with clean host (no orphan processes or instance dirs).

---

## Phase 4 — Warm pool state machine

**Outcome:** Agent maintains warm VMs and binds one for a job payload without cold boot on the happy path.

### Tasks

- [ ] Implement pool states: `pool_boot`, `warm`, `busy`, `destroying`
- [ ] Config: `min_ready`, `max_ready`, VM resources, image path
- [ ] Background reconciler to maintain `min_ready`
- [ ] `Bind(jobPayload)` transitions warm → busy and attaches JIT/start runner hook (runner start may be stubbed until phase 5)
- [ ] After job terminal signal: destroying → cleanup → replenish
- [ ] Idle warm recycle timer
- [ ] Unit tests for transitions and failure cases (bind failure must not return tainted VM to warm)
- [ ] Metrics/logs: warm/busy counts, cold vs warm starts

### Failure cases to test

- [ ] Bind fails after VM selected → destroy, do not re-warm that instance
- [ ] Destroy fails → retry/backoff + surface error; do not infinite-replenish blindly
- [ ] Agent restart → orphan sweep then rebuild pool

**Exit criteria:** Agent process alone can keep `min_ready` warm VMs and cycle bind→destroy→replenish with a fake job completion signal.

---

## Phase 5 — End-to-end Ubuntu job

**Outcome:** Real GitHub Actions job runs inside a microVM on Ubuntu.

### Tasks

- [ ] Guest image pipeline: base Ubuntu + official runner binary (document how to build/refresh)
- [ ] Inject JIT config and start runner inside guest on bind
- [ ] Agent reports job started / finished to control plane
- [ ] Control plane assigns minted jobs to local agent (single-node path)
- [ ] Run workflow: checkout + `echo` / `uname -a`
- [ ] Verify second job uses warm pool (log `warm_bind=true`)
- [ ] Verify post-job destroy + empty instance dir
- [ ] Write operator quickstart: single-node Ubuntu

### Success demo script (operator)

- [ ] Install deps + KVM
- [ ] Start `kokanee-control` and `kokanee-agent`
- [ ] Install GitHub App
- [ ] Push workflow with `runs-on: kokanee-…`
- [ ] Job green on GitHub
- [ ] Host clean after job

**Exit criteria:** MVP success criteria in the design spec §8 items 1–5 met on bare Ubuntu.

---

## Phase 6 — Proxmox install path

**Outcome:** Documented, repeatable install on Proxmox host using the same agent semantics.

### Tasks

- [ ] Validate KVM/microVM requirements on a Proxmox host
- [ ] `deploy/proxmox/` install guide (packages, permissions, storage paths)
- [ ] Note nested virt / policy limitations explicitly
- [ ] Smoke test: one real job on Proxmox host
- [ ] Confirm teardown leaves no stale disks on chosen storage path

**Exit criteria:** Following only `deploy/proxmox/` docs, a new operator runs one green Actions job and cleanup holds.

---

## Phase 7 — Hardening

**Outcome:** Safer multi-host operation and better operability.

### Tasks

- [ ] Control↔agent auth (mTLS or token) and TLS
- [ ] Multi-host scheduler: capacity-aware assignment
- [ ] Stuck job deadline → force destroy
- [ ] JIT / runner registration reconciliation loop
- [ ] Basic metrics endpoint or structured ops logs
- [ ] Image update + warm pool drain/reload
- [ ] Security review pass against design §7
- [ ] Orphan sweep proves cleanup after agent kill mid-job (design §8 item 6)

**Exit criteria:** Two agents registered; jobs schedule correctly; forced failures still clean disks.

---

## Tracking how-to

1. Check boxes in this file as work completes (`- [ ]` → `- [x]`).
2. Update the **Progress summary** table status column.
3. Keep the design spec in sync if product decisions change (edit spec + note date).
4. Prefer one PR/commit series per phase when possible.
5. Do not start phase 5 until phase 3 destroy checklist is implemented and tested.

## Out of scope for this plan (backlog)

- Docker layer cache product
- Actions cache co-location product
- Admin web UI
- Windows/macOS runners
- Kubernetes operator install
- Billing / multi-tenant SaaS

Add separate plans when those are prioritized.
