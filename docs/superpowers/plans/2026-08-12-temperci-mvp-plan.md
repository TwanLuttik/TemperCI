# TemperCI MVP Implementation Plan

> **For agentic workers:** Implement phase-by-phase. Use checkbox (`- [ ]` / `- [x]`) syntax for tracking. Do not skip teardown/orphan requirements when implementing agent work. Prefer TDD for state machines and cleanup logic.

**Goal:** Ship a self-hostable GitHub Actions runner path (control plane + host agent) that uses JIT self-hosted runners, a warm Firecracker microVM pool, and hard post-job cleanup on bare Ubuntu.

**Architecture:** Go monorepo with `temperci-control` (GitHub webhooks + JIT + scheduling) and `temperci-agent` (warm pool + VMM + teardown). Guests run upstream `actions/runner` for a single job, then are destroyed.

**Tech stack:** Go 1.22+, GitHub App + REST JIT API, KVM microVMs (Firecracker or Cloud Hypervisor behind `internal/vmm`), systemd install, Markdown docs.

**Spec:** [docs/superpowers/specs/2026-08-12-temperci-platform-design.md](../specs/2026-08-12-temperci-platform-design.md)

## Global constraints

- Primary language is **Go**; binaries `temperci-control` and `temperci-agent`.
- Use **official** `actions/runner` + **JIT** registration only (no long-lived `config.sh` as the product path).
- Warm VMs must have **no secrets / no JIT** until bind.
- After every job: **destroy guest + delete host scratch**; never reuse guest disk for the next job.
- Hypervisor details stay behind `internal/vmm`.
- GitHub API details stay behind `internal/github`.
- Follow layout in [docs/decisions/repository-structure.md](../../decisions/repository-structure.md).

## Progress summary

| Phase | Name | Status |
|-------|------|--------|
| 0 | Documentation foundation | Done |
| 1 | Go module skeleton + CI | Done |
| 2 | GitHub App, webhooks, JIT mint | Done |
| 3 | VMM create/destroy + host cleanup | Done |
| 4 | Warm pool state machine | Done |
| 5 | End-to-end Ubuntu job | Done |
| 6 | Host install docs (Proxmox path later withdrawn) | Done |
| 7 | Hardening (recycle, metrics, multi-host) | Done |

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
- [x] Design spec (`docs/superpowers/specs/2026-08-12-temperci-platform-design.md`)
- [x] This plan file with checkboxes
- [x] Choose SPDX license (Apache-2.0 or MIT) and add `LICENSE`
- [x] Confirm public module path / GitHub org name for `go.mod`

**Exit criteria:** Design + plan reviewed; license and module path decided or explicitly deferred with owners.

---

## Phase 1 — Go module skeleton + CI

**Outcome:** Empty but buildable monorepo matching the decided layout.

### Tasks

- [x] Initialize `go.mod` with agreed module path
- [x] Create `cmd/temperci-control/main.go` (version/help stub)
- [x] Create `cmd/temperci-agent/main.go` (version/help stub)
- [x] Create placeholder packages under `internal/` (`config`, `logging`, `api`, `control`, `agent`, `github`, `vmm`, `cleanup`) with package docs
- [x] Add `Makefile` targets: `build`, `test`, `lint` (lint may no-op until linter config exists)
- [x] Add example configs under `deploy/` (`control.example.toml`, `agent.example.toml`)
- [x] Add systemd unit templates under `deploy/systemd/`
- [x] Add `.github/workflows/ci.yml`: `go test ./...`, `go build` both cmds
- [x] Document local dev build in `README.md` or `docs/`

### Suggested first files

```text
go.mod
cmd/temperci-control/main.go
cmd/temperci-agent/main.go
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

**Outcome:** Control plane can verify webhooks and mint JIT configs for TemperCI labels.

### Tasks

- [x] Implement GitHub App authentication (JWT → installation token) in `internal/github`
- [x] Webhook signature verification for `workflow_job`
- [x] Parse queued jobs; filter to TemperCI-owned labels only
- [x] Call `generate-jitconfig` (org-level MVP) with required labels and runner group
- [x] Persist minimal assignment state (in-memory OK for single-node MVP; disk/sqlite acceptable)
- [x] Control plane HTTP server: webhook endpoint + healthz
- [x] Unit tests with fixture payloads under `testdata/webhooks/`
- [x] Manual dry-run doc: create GitHub App, install on test org, receive queued event

### Acceptance tests

- [x] Invalid webhook signature → 401/403, no side effects
- [x] Non-TemperCI labels → ignored (200, no JIT call)
- [x] TemperCI label queued → JIT client called with expected labels (mock transport)

**Exit criteria:** In a test org, a queued workflow with TemperCI labels results in a successful JIT config mint (even if no agent consumes it yet).

---

## Phase 3 — VMM create/destroy + host cleanup

**Outcome:** Agent can create a microVM, destroy it, and leave no host leftovers.

### Tasks

- [x] Spike: choose Firecracker or Cloud Hypervisor; record decision in `docs/decisions/hypervisor.md`
- [x] Define `internal/vmm` interface: `Create`, `Boot`, `Destroy`, `Exists`, identity/metadata
- [x] Implement chosen backend on Ubuntu + KVM
- [x] Define host scratch layout (images dir, instance dir per VM id)
- [x] Implement `internal/cleanup`: delete instance dir, nets, processes for a VM id
- [x] Implement orphan sweep: compare desired state vs host; destroy unknowns
- [x] Integration test or scripted smoke: create → destroy → assert no leftover files/processes
- [x] Document host prerequisites (`/dev/kvm`, packages) under `deploy/ubuntu/`

### Destroy checklist (must be coded, not only documented)

- [x] Stop guest / VMM process
- [x] Remove VM definition / jailer resources if any
- [x] Delete COW/overlay disks and instance metadata
- [x] Remove taps/netns/proxy state for that id
- [x] Idempotent destroy (second call is safe)

**Exit criteria:** Scripted create/destroy loop of N VMs ends with clean host (no orphan processes or instance dirs).

---

## Phase 4 — Warm pool state machine

**Outcome:** Agent maintains warm VMs and binds one for a job payload without cold boot on the happy path.

### Tasks

- [x] Implement pool states: `pool_boot`, `warm`, `busy`, `destroying`
- [x] Config: `min_ready`, `max_ready`, VM resources, image path
- [x] Background reconciler to maintain `min_ready`
- [x] `Bind(jobPayload)` transitions warm → busy and attaches JIT/start runner hook (runner start may be stubbed until phase 5)
- [x] After job terminal signal: destroying → cleanup → replenish
- [x] Idle warm recycle timer
- [x] Unit tests for transitions and failure cases (bind failure must not return tainted VM to warm)
- [x] Metrics/logs: warm/busy counts, cold vs warm starts

### Failure cases to test

- [x] Bind fails after VM selected → destroy, do not re-warm that instance
- [x] Destroy fails → retry/backoff + surface error; do not infinite-replenish blindly
- [x] Agent restart → orphan sweep then rebuild pool

**Exit criteria:** Agent process alone can keep `min_ready` warm VMs and cycle bind→destroy→replenish with a fake job completion signal.

---

## Phase 5 — End-to-end Ubuntu job

**Outcome:** Real GitHub Actions job runs inside a microVM on Ubuntu.

### Tasks

- [x] Guest image pipeline: base Ubuntu + official runner binary (document how to build/refresh)
- [x] Inject JIT config and start runner inside guest on bind
- [x] Agent reports job started / finished to control plane
- [x] Control plane assigns minted jobs to local agent (single-node path)
- [x] Run workflow: checkout + `echo` / `uname -a` (operator path + documented; local e2e uses mock JIT)
- [x] Verify second job uses warm pool (log `warm_bind=true`)
- [x] Verify post-job destroy + empty instance dir
- [x] Write operator quickstart: single-node Ubuntu

### Success demo script (operator)

- [x] Install deps + KVM (documented checklist in `deploy/ubuntu/quickstart.md`)
- [x] Start `temperci-control` and `temperci-agent`
- [x] Install GitHub App
- [x] Push workflow with `runs-on: temperci-…`
- [x] Job green on GitHub
- [x] Host clean after job

**Exit criteria:** MVP success criteria in the design spec §8 items 1–5 met on bare Ubuntu (operator runbook); single-node control↔agent path covered by `go test ./internal/e2e` on any host.

---

## Phase 6 — Host install docs

**Outcome (historical):** A documented install path for extra host types. The Proxmox QEMU/`qm`/`pct` job path was never the runtime; jobs have always used Firecracker. That extra install tree (`deploy/proxmox/`) was later **withdrawn**. Supported job host: Ubuntu + KVM + Firecracker (`deploy/ubuntu/`). Cleanup check: `scripts/verify-cleanup.sh`.

---

## Phase 7 — Hardening

**Outcome:** Safer multi-host operation and better operability.

### Tasks

- [x] Control↔agent auth (mTLS or token) and TLS
- [x] Multi-host scheduler: capacity-aware assignment
- [x] Stuck job deadline → force destroy
- [x] JIT / runner registration reconciliation loop
- [x] Basic metrics endpoint or structured ops logs
- [x] Image update + warm pool drain/reload
- [x] Security review pass against design §7
- [x] Orphan sweep proves cleanup after agent kill mid-job (design §8 item 6)

**Exit criteria:** Two agents registered; jobs schedule correctly; forced failures still clean disks.

**Implemented:** bearer token required on agent API; optional control HTTPS + mTLS client CA; claim gated on `free_slots`; agent `job_deadline_seconds` force destroy; control reconciler marks stuck/stale and deletes org runners (GitHub API, mockable); `GET /metrics` on control and agent; agent `POST /v1/admin/pool/drain|reload`; security review at `docs/architecture/security-review-mvp.md`; mid-job kill orphan test.

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
