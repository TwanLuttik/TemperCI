# Kokanee platform design

**Date:** 2026-08-12  
**Status:** Draft for implementation planning  
**Product:** Self-hostable open-source GitHub Actions runner platform

## 1. Problem

Managed GitHub Actions runner products (e.g. Blacksmith) deliver:

- Drop-in `runs-on` labels
- Ephemeral isolated VMs
- Fast pickup
- Less operational burden than hand-rolled self-hosted runners

Teams that must keep CI on their own hardware (Proxmox, bare Ubuntu, regulated environments) still want that **product shape**, not a pile of scripts around `config.sh`.

## 2. Goals

1. Run GitHub Actions jobs on operator-owned hosts using **official** `actions/runner` and **JIT** self-hosted registration.
2. Support install on **bare Ubuntu** and **Proxmox VE** hosts.
3. Keep a **warm microVM pool** so job pickup is not blocked on cold VM create.
4. **Destroy** every job VM and clean host-side scratch so self-hosted disks do not accumulate leftovers.
5. Be open source and operable by a single team without a SaaS dependency.

## 3. Non-goals (MVP)

- Multi-tenant public SaaS / billing
- Windows or macOS runners
- Full Blacksmith-parity cache product (Docker layer cache, sticky disks) in v1
- Replacing GitHub Actions with another CI engine
- Kubernetes-first distribution (may come later)

## 4. How GitHub integration works

Kokanee uses the **same class of integration as Blacksmith**, not a proprietary job protocol.

### 4.1 Model

| Piece | Choice |
|-------|--------|
| Job definition | GitHub Actions workflows unchanged except `runs-on` |
| Execution binary | Upstream [actions/runner](https://github.com/actions/runner) |
| Registration | [Just-in-time self-hosted runners](https://github.blog/changelog/2023-06-02-github-actions-just-in-time-self-hosted-runners/) |
| Eventing | GitHub App webhooks (`workflow_job`) |
| Secrets | Delivered by GitHub to the runner; App does not read repo secrets |

### 4.2 Request path

```text
1. Operator installs Kokanee GitHub App on org
2. Workflow: runs-on: kokanee-4vcpu-ubuntu-2404
3. Job queued → workflow_job webhook → control plane
4. Control plane mints JIT config (labels match runs-on)
5. Control plane assigns job to a host agent
6. Agent binds a warm microVM, starts runner with JIT config
7. Runner adopts one job; steps execute in guest
8. Terminal state → destroy guest + host scratch → replenish warm pool
```

### 4.3 Labels

Default prefix: `kokanee-`.  
MVP example labels (exact set can shrink for first release):

- `kokanee-2vcpu-ubuntu-2404`
- `kokanee-4vcpu-ubuntu-2404`

Labels are registered on the JIT runner so GitHub’s scheduler can assign the queued job.

## 5. Architecture

### 5.1 Components

1. **`kokanee-control`** — GitHub App webhooks, JIT minting, scheduling, assignment to agents.
2. **`kokanee-agent`** — per-host warm pool, bind, runner start, teardown, orphan sweep.
3. **MicroVM guests** — ephemeral Linux VMs running official runner + job steps.
4. **Deploy assets** — systemd units, Ubuntu/Proxmox install docs and scripts.

### 5.2 Warm pool (product requirement)

- Agents keep `min_ready` pre-booted VMs with **no** JIT identity and **no** secrets.
- On assignment, bind JIT → start runner (fast path).
- After job, **destroy** (never reuse guest disk); boot a new warm VM to refill.
- If pool is empty, cold-boot is allowed (degraded path).
- Recycle idle warm VMs on timer / image update.

### 5.3 Teardown (product requirement)

Mandatory destroy checklist after every job outcome:

1. Stop runner if needed  
2. Destroy microVM  
3. Delete host scratch (overlays, sockets, logs, temp dirs)  
4. Drop per-VM network/proxy state  
5. Reconcile stuck GitHub runner registrations  
6. Only then replenish pool  

Orphan sweeper on agent start + periodically handles crash/reboot leftovers.

**Invariant:** After a job, nothing from that job remains except intentional shared base images and (future) scoped shared cache.

### 5.4 Install targets

- **Bare Ubuntu** with KVM: primary implementation target.
- **Proxmox VE**: same agent model on host KVM; prefer same microVM stack rather than slow full Proxmox VM-per-job unless forced.

Details: [docs/architecture/install-targets.md](../../architecture/install-targets.md).

## 6. Technology decisions

| Area | Decision | Doc |
|------|----------|-----|
| Language | Go 1.22+ | [language.md](../../decisions/language.md) |
| Repo layout | Go monorepo, `cmd/` + `internal/` | [repository-structure.md](../../decisions/repository-structure.md) |
| Hypervisor | Spike: Firecracker vs Cloud Hypervisor; interface behind `internal/vmm` | Plan phase 1 |
| Config | TOML examples under `deploy/` | repository-structure |
| License | TBD (Apache-2.0 or MIT recommended) | — |

## 7. Security properties

- JIT tokens treated as secrets; never full-logged.
- Warm VMs have no GitHub credentials until bind.
- One job per VM; destroy after job.
- GitHub App least privilege: needs self-hosted runner admin for JIT; does not require direct secrets access.
- Host agent is privileged (KVM/net); isolate binary and config permissions.
- Orphan cleanup prevents disk fill and stale process retention after failures.

## 8. MVP success criteria

MVP is successful when an operator can:

1. Install control plane + agent on a single Ubuntu KVM host.
2. Install the GitHub App on a test organization.
3. Run a workflow with `runs-on: kokanee-…` that executes `actions/checkout` and a simple step.
4. Observe warm-bind (not only cold boot) under a second job after pool refill.
5. Confirm after jobs that guest disks/processes for completed jobs are gone.
6. Kill the agent mid-job / reboot host and see orphan sweep clean leftovers on restart.

## 9. Phased roadmap (summary)

Detailed checkboxes live in the plan doc.

| Phase | Outcome |
|-------|---------|
| 0 | Docs, decisions, tracking plan (this work) |
| 1 | Repo skeleton, config, CI for Go module |
| 2 | GitHub App webhook + JIT mint (can be dry-run / recorded) |
| 3 | VMM interface + create/destroy + scratch cleanup |
| 4 | Warm pool state machine |
| 5 | End-to-end job on Ubuntu |
| 6 | Proxmox install path + docs |
| 7 | Hardening: recycle, metrics, multi-host assignment |

## 10. Open questions

Resolved for design:

- Integration model: JIT self-hosted + official runner — **yes**.
- Warm pool — **yes**.
- Always destroy guest after job — **yes**.
- Language — **Go**.
- Monorepo structure — **as documented**.

Still open (implementation spikes):

1. Firecracker vs Cloud Hypervisor for MVP.
2. Exact base image pipeline (how closely we track `runner-images`).
3. Control↔agent transport (HTTPS+mTLS vs SSH vs message queue) for multi-host.
4. License SPDX choice.
5. Public GitHub org/module path naming.

## 11. References

- Blacksmith security write-up (JIT + microVM model): https://www.blacksmith.sh/security  
- GitHub JIT runners: https://github.blog/changelog/2023-06-02-github-actions-just-in-time-self-hosted-runners/  
- GitHub self-hosted runner REST API: https://docs.github.com/en/rest/actions/self-hosted-runners  
- actions/runner: https://github.com/actions/runner  
- Internal architecture docs: [docs/architecture/](../../architecture/)
