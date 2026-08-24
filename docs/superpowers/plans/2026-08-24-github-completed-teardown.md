# GitHub-completed job teardown

**Goal:** When GitHub says a TemperCI job is done (or the guest runner dies mid-job), the assignment and microVM go away immediately. An OOM-killed runner must not sit `started` / `busy` until the 1-hour stuck timer.

**Why this job stayed active:** GitHub `workflow_job` `completed` at 06:40:12 (conclusion `failure`) after `Runner.Listener` aborted with “Out of memory” / exit 134. Control **ignores every webhook action except `queued`**. The guest agent could not `fork` to write `runner.exit`. `WaitRunner` therefore blocked. Reconcile only marks stuck after 3600s and does **not** enqueue a VM kill.

**Already on `main` (helps the next job, does not unstick this one):** 2 GiB guest swap, 1 GiB Listener heap cap, remap abort 134 → `failure` *if* `runner.exit` is written, no warm refill while busy.

**Out of scope:** Coatcheck’s “Cleanup stale e2e Docker resources” step (that repo). On TemperCI it is a no-op leftover for shared runners; recommend they skip it later.

---

## Approach

Primary signal is **GitHub**. The guest is already dead; GitHub already knew. TemperCI must treat `completed` / `cancelled` the same way dashboard cancel does: finish the assignment, `enqueueKill`, delete the JIT runner.

Agent-side OOM detection is a faster backup for the window before GitHub’s “runner offline” completion (this incident: ~9 minutes).

## Task 1 — Finish + kill on GitHub completed/cancelled [done]

**Files:**
- Modify: `internal/github/workflow_job.go` — add `Conclusion string` on `WorkflowJob`
- Test: `internal/github/workflow_job_test.go` — fixture or inline completed payload
- Modify: `internal/control/server.go` — on `completed`/`cancelled`, call a new helper (do not mint)
- Modify: `internal/control/handler.go` only if we keep ignore reasons accurate
- Create: `internal/control/github_complete.go` — `finishFromGitHub(ev)`
- Test: `internal/control/github_complete_test.go`

**Behavior:**
- Ignore if no assignment, or status already `finished`/`failed`.
- `MarkFinished` with outcome from `conclusion` (`failure`, `cancelled`, `success`, empty → `failure` if action is `cancelled`).
- If `AssignedAgentID` and `VMID` set, `cmdq.enqueueKill` (same as `handleJobCancel`).
- `deleteJobRunner`, `recordJobEvent`, `PublishSnapshot`.
- Idempotent: second completed webhook is a no-op.

**Do not** treat `in_progress` as finish.

**Test first:** webhook `completed` + `conclusion=failure` against a `started` assignment → status `finished`, outcome `failure`, one kill command queued, JIT cleared. Repeat webhook → no second kill. `queued` path unchanged.

## Task 2 — Agent: host-side exit when runner log shows abort [done]

**Files:**
- Modify: `internal/agent/guest.go` `WaitRunner` (or `worker.waitForJob` / `streamLogs`)
- Reuse: `internal/agent/outcome.go` `runnerLogIndicatesAbort`
- Test: `internal/agent/guest_protocol_test.go` or worker test with a fake guest that never writes `runner.exit` but whose `runner.log` contains `Out of memory` / `unknown error code: 134`

**Behavior:** While waiting, if host-side `guest/runner.log` (already streamed) indicates abort, write `guest/runner.exit` with `97` (same code the guest remap uses) and unblock `WaitRunner`. Existing `RefineOutcome` then reports `failure`. Destroy proceeds as today.

Do not invent a new outcome string. Do not treat a still-growing log as dead.

## Task 3 — Reconcile kills the VM [done]

**Files:**
- Modify: `internal/control/reconcile.go` — after `MarkFinished(..., "stuck")`, enqueue kill when VMID/agent set
- Reconciler needs the cmd queue (or a `func(agentID, vmID string, jobID int64)` hook) so tests stay free of HTTP
- Test: stuck job with VMID results in one kill enqueue

This is the 1-hour safety net, not the primary path.

## Immediate ops (not code)

Kill `vm-498182074f84c8cc` (job `97341761368`) from the dashboard or `POST /api/v1/vms/vm-498182074f84c8cc/kill`. GitHub already failed the job.

## Order

1 → 2 → 3. Task 1 alone would have torn this VM down at 06:40.

## Done when

- `go test ./internal/github/ ./internal/control/ ./internal/agent/ -count=1` passes.
- A recorded `workflow_job` completed fixture finishes a started assignment and queues a kill.
- A WaitRunner test unblocks on an aborting `runner.log` without `runner.exit` from the guest.
- No new GitHub App permissions. No new dependencies.
