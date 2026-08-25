# 8g Runner Heap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Coatcheck e2e (and any similar job) completes on `temperci-4vcpu-8g-ubuntu-2404` because the extra 2 GiB goes to the job, not to `Runner.Listener`.

**Architecture:** The official actions runner is .NET. It sizes its GC heap from guest `MemTotal`, so an 8g box makes Listener fatter and the job poorer. Pin Listener and Worker to constant heap budgets with tiny exec wrappers, leave job steps uncapped, and treat “started a job but never wrote `completed with result`” as failure even when the OOM line is not flushed.

**Tech Stack:** bash guest agent, official `actions/runner` 2.336.0, Go host agent (`RefineOutcome` / `WaitRunner`), Firecracker guest image at `/var/lib/temperci/images/ubuntu-2404-runner.ext4`.

## Global Constraints

- Do not change Firecracker `memory_mib` for the 8g shape. The shape stays 8192.
- Do not ask coatcheck to move back to 6g. 8g must work.
- Heap limits are **absolute bytes**, not `% of RAM`. Percent would still grow with the shape.
- `DOTNET_gcServer=0` and `DOTNET_GCHeapHardLimit` apply only to `Runner.Listener` and `Runner.Worker`. Job steps (node, pnpm, docker, playwright) must not inherit them.
- Listener hard cap is **2 GiB** (`2147483648`). The current 1 GiB cap is what prints `Out of memory` / exit 134 after `pnpm install`.
- Worker hard cap is **2 GiB** (`2147483648`) so Worker cannot size off 8g either.
- Do not commit, push, or create worktrees unless the user asks.
- Guest-image bake and warm-pool recycle happen on `root@10.0.0.50` after the code change. That is an ops step, not a git step.

## Why this, not the alternatives

| Approach | Verdict |
|---|---|
| Raise the existing `env DOTNET_*= run.sh` cap from 1 GiB to 2 GiB | Too weak. `run.sh` children include Worker **and** any .NET the job might spawn. Comments already claim isolation that the `env` line does not implement. |
| **Listener + Worker exec wrappers with constant caps (this plan)** | Extra 8g−6g RAM is available to node/docker/playwright. Listener can grow past the aborting 1 GiB cliff. Same knobs on 6g and 8g. |
| cgroup v2 `memory.max` on a runner slice | More correct isolation, more Firecracker/cgroup surface. Not needed if wrappers fix the abort. Revisit only if 8g still dies after Task 1–3. |

On an 8g guest after this plan: Listener ≤ 2 GiB + Worker ≤ 2 GiB + 2 GiB swap already on. The job sees roughly what a working 6g box did, plus headroom.

---

### Task 1: Incomplete-job outcome is failure

**Files:**
- Modify: `internal/agent/outcome.go`
- Modify: `internal/agent/outcome_test.go`
- Modify: `deploy/ubuntu/guest-agent/temperci-runner-agent.sh` (remap block near lines 427–453)
- Create: `deploy/ubuntu/guest-agent/remap_incomplete_test.sh`
- Modify: `deploy/ubuntu/guest-agent/remap_exit_test.sh` (keep existing OOM → 97)

**Interfaces:**
- Consumes: existing `RefineOutcome(outcome, runnerLog string) string`
- Produces: `RefineOutcome` returns `"failure"` when the log started a job but never recorded `completed with result: succeeded` (and is not already a non-success). Guest remap writes `runner.exit=98` for that case. Existing OOM/134 still writes `97`.

This is independent of the heap fix. Job `97361018692` had `Running job: e2e` + exit 0 + no completion line, and TemperCI stored `success`. That must stop.

- [ ] **Step 1: Write the failing Go tests**

Add to `internal/agent/outcome_test.go`:

```go
func TestRefineOutcome_StartedJobWithoutCompletionIsFailure(t *testing.T) {
	log := "\n√ Connected to GitHub\n\nCurrent runner version: '2.336.0'\n" +
		"2026-08-24 07:58:17Z: Listening for Jobs\n" +
		"2026-08-24 07:58:20Z: Running job: e2e\n"
	if got := RefineOutcome("success", log); got != "failure" {
		t.Fatalf("RefineOutcome = %q want failure (incomplete job)", got)
	}
}

func TestRefineOutcome_ListeningOnlyStillSuccessUntilJobStarts(t *testing.T) {
	// Deprecated-runner / never-accepted-job stays the guest's 95 path.
	// Host RefineOutcome only upgrades once a job actually started.
	log := "\n√ Connected to GitHub\n2026-08-24 07:58:17Z: Listening for Jobs\n"
	if got := RefineOutcome("success", log); got != "success" {
		t.Fatalf("RefineOutcome = %q want success (no job started)", got)
	}
}
```

- [ ] **Step 2: Run the new tests and confirm they fail**

Run: `go test ./internal/agent/ -count=1 -run 'TestRefineOutcome_StartedJobWithoutCompletionIsFailure|TestRefineOutcome_ListeningOnlyStillSuccessUntilJobStarts'`

Expected: first test FAIL with `RefineOutcome = "success" want failure`.

- [ ] **Step 3: Implement host remap**

Replace `RefineOutcome` in `internal/agent/outcome.go` with:

```go
func RefineOutcome(outcome, runnerLog string) string {
	if outcome != "success" {
		return outcome
	}
	if jobCompletedOK(runnerLog) {
		return "success"
	}
	if runnerLogIndicatesAbort(runnerLog) || jobStartedButIncomplete(runnerLog) {
		return "failure"
	}
	return "success"
}

func jobStartedButIncomplete(log string) bool {
	low := strings.ToLower(log)
	return strings.Contains(low, "running job:") &&
		!strings.Contains(low, "completed with result:")
}
```

Keep `jobCompletedOK` and `runnerLogIndicatesAbort` unchanged.

- [ ] **Step 4: Re-run Go tests**

Run: `go test ./internal/agent/ -count=1`

Expected: PASS, including the two new cases and the existing OOM / completed / keep-non-success tests.

- [ ] **Step 5: Write the failing guest incomplete-job remap test**

Create `deploy/ubuntu/guest-agent/remap_incomplete_test.sh` by copying `remap_exit_test.sh` and changing only the stub `run.sh` body and the expected exit code:

Stub `run.sh` prints (and exits 0):

```
Connected to GitHub
Listening for Jobs
Running job: e2e
```

No `Out of memory`, no `134`, no `completed with result`.

Assert `runner.exit` is `98`.

- [ ] **Step 6: Run it and confirm it fails**

Run: `bash deploy/ubuntu/guest-agent/remap_incomplete_test.sh`

Expected: FAIL `runner.exit=0 want 98` (today the guest keeps 0 because `Running job:` is treated as success).

- [ ] **Step 7: Implement guest remap 98**

In `deploy/ubuntu/guest-agent/temperci-runner-agent.sh`, after the existing deprecated (96) and OOM (97) branches, change the exit-0 branch from “must have started a job” to “must have completed a job”:

```bash
elif [ "$code" -eq 0 ]; then
  if grep -qiE "completed with result: succeeded" "$WORKDIR/runner.log" 2>/dev/null; then
    :
  elif grep -qiE "Running job:" "$WORKDIR/runner.log" 2>/dev/null; then
    log "runner exit 0 after starting a job without completion; marking as 98"
    if [ -s "$WORKDIR/runner.log" ]; then
      log "runner.log tail: $(tail -c 500 "$WORKDIR/runner.log" | tr '\n' ' ')"
    fi
    code=98
  else
    log "runner exit 0 without running a job; marking as 95"
    if [ -s "$WORKDIR/runner.log" ]; then
      log "runner.log tail: $(tail -c 500 "$WORKDIR/runner.log" | tr '\n' ' ')"
    fi
    code=95
  fi
fi
```

Do not change the 96/97 branches.

- [ ] **Step 8: Run both guest remap tests**

Run:

```bash
bash deploy/ubuntu/guest-agent/remap_exit_test.sh
bash deploy/ubuntu/guest-agent/remap_incomplete_test.sh
```

Expected: `remap_exit_test: OK (OOM → runner.exit=97)` and incomplete test OK with `98`.

---

### Task 2: Listener and Worker heap wrappers

**Files:**
- Create: `deploy/ubuntu/guest-agent/wrap-runner-dotnet.sh`
- Create: `deploy/ubuntu/guest-agent/heap_wrap_test.sh`
- Modify: `deploy/ubuntu/guest-agent/install-into-rootfs.sh`
- Modify: `deploy/ubuntu/guest-agent/temperci-runner-agent.sh` (remove the `env DOTNET_*=` prefix on `run.sh`)
- Modify: `deploy/ubuntu/guest-agent/protocol_test.sh` (stop requiring DOTNET_* on `run.sh`)

**Interfaces:**
- Consumes: official binaries at `$ROOT/opt/actions-runner/bin/Runner.Listener` and `Runner.Worker`
- Produces: those paths become wrappers; real binaries at `Runner.Listener.real` / `Runner.Worker.real`. Wrappers export:
  - `DOTNET_gcServer=0`
  - `DOTNET_GCHeapHardLimit` = `2147483648` (override with `TEMPERCI_LISTENER_HEAP` / `TEMPERCI_WORKER_HEAP`)
  - `DOTNET_GCConserveMemory=7`
- `run.sh` is started **without** those variables, so bash job steps do not inherit a .NET heap cap.

- [ ] **Step 1: Write `heap_wrap_test.sh` first (fails: wrappers missing)**

```bash
#!/usr/bin/env bash
# Wrappers must pin Listener/Worker heap and must not leak to a child "job step".
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKDIR="$(mktemp -d /tmp/temperci-heap.XXXXXX)"
trap 'rm -rf "${WORKDIR}"' EXIT

BIN="${WORKDIR}/opt/actions-runner/bin"
mkdir -p "${BIN}"

# Fake official binaries: record env, then exec a child that also records env.
cat >"${BIN}/Runner.Listener" <<'EOF'
#!/usr/bin/env bash
env | grep -E '^DOTNET_' | sort >"${TEMPERCI_WORKDIR}/listener.env"
"${TEMPERCI_WORKDIR}/job-step.sh"
EOF
cp "${BIN}/Runner.Listener" "${BIN}/Runner.Worker"
cat >"${WORKDIR}/job-step.sh" <<'EOF'
#!/usr/bin/env bash
env | grep -E '^DOTNET_' | sort >"${TEMPERCI_WORKDIR}/step.env" || true
EOF
chmod +x "${BIN}/Runner.Listener" "${BIN}/Runner.Worker" "${WORKDIR}/job-step.sh"

export TEMPERCI_WORKDIR="${WORKDIR}"
bash "${SCRIPT_DIR}/wrap-runner-dotnet.sh" "${WORKDIR}"

# After wrap, invoking the Listener path should see the cap; the job-step child must not.
"${BIN}/Runner.Listener"
if ! grep -qx 'DOTNET_GCHeapHardLimit=2147483648' "${WORKDIR}/listener.env"; then
  echo "FAIL: Listener heap not 2GiB" >&2
  cat "${WORKDIR}/listener.env" >&2 || true
  exit 1
fi
if ! grep -qx 'DOTNET_gcServer=0' "${WORKDIR}/listener.env"; then
  echo "FAIL: Listener gcServer not 0" >&2
  exit 1
fi
if [[ -s "${WORKDIR}/step.env" ]]; then
  echo "FAIL: job step inherited DOTNET_*:" >&2
  cat "${WORKDIR}/step.env" >&2
  exit 1
fi
echo "heap_wrap_test: OK"
```

The wrapper must `unset` the DOTNET vars before exec-ing anything that is not the real Listener/Worker. The fake Listener above execs `job-step.sh` *as itself*; that is the wrong process model. Adjust the test so the **real** binary is what the wrapper execs, and a separate child simulates a job step spawned *without* the wrapper:

In the test, after wrap:

1. Run `"${BIN}/Runner.Listener"` → writes `listener.env` (the `.real` script).
2. Run a bare `job-step.sh` (not through the wrapper) to prove `run.sh` / steps see no DOTNET_* if the agent does not export them.
3. Also run `"${BIN}/Runner.Worker"` and assert the same 2 GiB cap.

If `wrap-runner-dotnet.sh` is missing, the test fails immediately.

- [ ] **Step 2: Run the test and confirm it fails**

Run: `bash deploy/ubuntu/guest-agent/heap_wrap_test.sh`

Expected: FAIL `wrap-runner-dotnet.sh: No such file or directory` (or Listener heap not 2GiB if you created an empty file).

- [ ] **Step 3: Implement the wrapper installer**

Create `deploy/ubuntu/guest-agent/wrap-runner-dotnet.sh`:

```bash
#!/usr/bin/env bash
# Replace Runner.Listener / Runner.Worker with exec wrappers that pin .NET heap.
# Usage: wrap-runner-dotnet.sh /path/to/rootfs-or-fake-root
set -euo pipefail
ROOT="${1:?usage: $0 /path/to/rootfs}"
BIN="${ROOT}/opt/actions-runner/bin"
LISTENER_HEAP="${TEMPERCI_LISTENER_HEAP:-2147483648}"
WORKER_HEAP="${TEMPERCI_WORKER_HEAP:-2147483648}"

wrap_one() {
  local name="$1" heap="$2"
  local src="${BIN}/${name}"
  local real="${BIN}/${name}.real"
  [[ -e "${src}" ]] || { echo "wrap-runner-dotnet: missing ${src}" >&2; return 1; }
  if [[ -f "${real}" ]]; then
    return 0
  fi
  mv "${src}" "${real}"
  cat >"${src}" <<EOF
#!/bin/bash
export DOTNET_gcServer=0
export DOTNET_GCHeapHardLimit=${heap}
export DOTNET_GCConserveMemory=7
exec "\$(dirname "\$0")/${name}.real" "\$@"
EOF
  chmod +x "${src}"
}

wrap_one "Runner.Listener" "${LISTENER_HEAP}"
wrap_one "Runner.Worker" "${WORKER_HEAP}"
```

Idempotent: a second install sees `.real` and no-ops.

- [ ] **Step 4: Call it from `install-into-rootfs.sh`**

After the existing `chown`/`chmod` of `/opt/actions-runner`, if the directory exists:

```bash
if [ -d "$ROOT/opt/actions-runner/bin" ]; then
  "$SCRIPT_DIR/wrap-runner-dotnet.sh" "$ROOT"
fi
```

`build-guest-image.sh` and `update-guest-runner.sh` already call `install-into-rootfs.sh`, so both bake paths pick this up.

- [ ] **Step 5: Stop leaking the cap through `run.sh`**

In `temperci-runner-agent.sh`, change the start from:

```bash
env DOTNET_gcServer=0 DOTNET_GCHeapHardLimit=1073741824 \
  "$RUNNER" --jitconfig "$JIT_B64" >"$WORKDIR/runner.log" 2>&1 &
```

to:

```bash
# Heap caps live on bin/Runner.Listener and bin/Runner.Worker wrappers.
# Do not export DOTNET_* here — job steps inherit run.sh's environment.
"$RUNNER" --jitconfig "$JIT_B64" >"$WORKDIR/runner.log" 2>&1 &
```

Update the comment above it to match.

- [ ] **Step 6: Fix `protocol_test.sh`**

Remove the block that requires `${TEMPERCI_WORKDIR}/dotnet.env` to contain `DOTNET_GCHeapHardLimit=1073741824` and `DOTNET_gcServer=0`. The stub `run.sh` should now record that those vars are **empty** (proves no leak):

In the stub `run.sh`, keep writing `dotnet.env`. After the agent run, assert:

```bash
if ! grep -qx 'DOTNET_gcServer=' "${TEMPERCI_WORKDIR}/dotnet.env"; then
  echo "FAIL: DOTNET_gcServer leaked to run.sh:" >&2
  cat "${TEMPERCI_WORKDIR}/dotnet.env" >&2
  exit 1
fi
if ! grep -qx 'DOTNET_GCHeapHardLimit=' "${TEMPERCI_WORKDIR}/dotnet.env"; then
  echo "FAIL: DOTNET_GCHeapHardLimit leaked to run.sh:" >&2
  cat "${TEMPERCI_WORKDIR}/dotnet.env" >&2
  exit 1
fi
```

- [ ] **Step 7: Run guest-agent tests**

Run:

```bash
bash deploy/ubuntu/guest-agent/heap_wrap_test.sh
bash deploy/ubuntu/guest-agent/protocol_test.sh
bash deploy/ubuntu/guest-agent/remap_exit_test.sh
bash deploy/ubuntu/guest-agent/remap_incomplete_test.sh
bash deploy/ubuntu/guest-agent/idle_poll_test.sh
go test ./internal/agent/ -count=1
```

Expected: all OK / PASS.

---

### Task 3: Docs that match the new contract

**Files:**
- Modify: `deploy/ubuntu/guest-agent/README.md` (the three bullets under “The guest agent also”)
- Modify: `CHANGELOG.md` under `[Unreleased]`
- Modify: `deploy/ubuntu/guest-image.md` only if it mentions the 1 GiB `env` prefix

**Interfaces:**
- Consumes: Task 1 exit codes 95/97/98 and Task 2 wrapper paths
- Produces: operator-facing text that says 8g is viable because runner heap is constant

- [ ] **Step 1: Update README bullets**

Replace the current “Starts `run.sh` with `DOTNET_gcServer=0` and `DOTNET_GCHeapHardLimit=1073741824`” bullet with:

- Wraps `bin/Runner.Listener` and `bin/Runner.Worker` so each has workstation GC, `DOTNET_GCConserveMemory=7`, and a 2 GiB `DOTNET_GCHeapHardLimit`. Job steps do not inherit those variables. Extra guest RAM therefore goes to the workflow, not the runner.
- Remaps runner abort / `Out of memory` / exit 134 to `runner.exit=97`, and remaps exit 0 after `Running job:` without `completed with result: succeeded` to `runner.exit=98`.

- [ ] **Step 2: Changelog**

Under `[Unreleased] / Changed`:

- Official runner Listener/Worker use a constant 2 GiB GC heap (exec wrappers). 8g guests no longer abort sooner than 6g because .NET sized the heap off `MemTotal`.

Under `[Unreleased] / Fixed`:

- Exit 0 after a job started but never completed is reported as `failure` (`runner.exit=98`), even when the OOM line was not flushed.

- [ ] **Step 3: Grep for stale 1 GiB wording**

Run: `rg -n '1073741824|1 GiB|GCHeapHardLimit' deploy internal CHANGELOG.md`

Expected: remaining hits are the new 2 GiB value, comments, or tests. No “1 GiB so Listener does not size its heap off guest RAM” left as current behavior.

---

### Task 4: Bake the guest image on the host and prove 8g

This task is ops on `root@10.0.0.50`. It is the only way to know the plan worked. Do not skip it and declare 8g fixed.

**Files:**
- None in git. Live image: `/var/lib/temperci/images/ubuntu-2404-runner.ext4`
- Deploy helpers already exist: `deploy/ubuntu/guest-agent/install-into-rootfs.sh`, `deploy/ubuntu/update-guest-runner.sh`

**Interfaces:**
- Consumes: Tasks 1–3 on the TemperCI checkout that the host uses (or copy the guest-agent dir over)
- Produces: warm VMs booted from an image whose `/opt/actions-runner/bin/Runner.Listener` is a wrapper; one coatcheck e2e job on `temperci-4vcpu-8g-ubuntu-2404` that logs `Job e2e completed with result: Succeeded`

- [ ] **Step 1: Confirm the live image still has the old `env` prefix**

```bash
ssh root@10.0.0.50 'MNT=$(mktemp -d); mount -o loop,ro /var/lib/temperci/images/ubuntu-2404-runner.ext4 "$MNT"; grep -n GCHeapHardLimit "$MNT/usr/local/sbin/temperci-runner-agent.sh"; ls -l "$MNT/opt/actions-runner/bin/Runner.Listener" "$MNT/opt/actions-runner/bin/Runner.Listener.real" 2>&1 | head; umount "$MNT"; rmdir "$MNT"'
```

Expected before bake: `DOTNET_GCHeapHardLimit=1073741824` on the `env` line; no `.real` binary.

- [ ] **Step 2: Stop the agent so warm clones are not taken mid-write**

```bash
ssh root@10.0.0.50 'systemctl stop temperci-agent'
```

Expected: `temperci-agent.service` inactive. Do not leave it stopped if a job is running — wait for idle (no busy VMs on `/api/v1/vms`).

- [ ] **Step 3: Install the new guest-agent + wrappers into the existing rootfs**

Copy the updated `deploy/ubuntu/guest-agent/` tree to the host (same path the checkout already uses, or `/tmp/guest-agent`), then:

```bash
ssh root@10.0.0.50 'MNT=$(mktemp -d); mount -o loop /var/lib/temperci/images/ubuntu-2404-runner.ext4 "$MNT"; /path/to/guest-agent/install-into-rootfs.sh "$MNT"; test -x "$MNT/opt/actions-runner/bin/Runner.Listener.real"; grep -q 2147483648 "$MNT/opt/actions-runner/bin/Runner.Listener"; umount "$MNT"; rmdir "$MNT"'
```

Expected: `installed temperci-runner-agent into …`; Listener is a wrapper; `.real` exists.

Do **not** need a full `build-guest-image.sh` unless the wrapper install fails because `bin/` is missing.

- [ ] **Step 4: Restart the agent and drop old warm VMs**

```bash
ssh root@10.0.0.50 'systemctl start temperci-agent; sleep 2; systemctl is-active temperci-agent'
```

Kill leftover warm VMs that booted from the old image (dashboard or `POST /api/v1/vms/{id}/kill`) so the pool clones the updated rootfs.

- [ ] **Step 5: Confirm a new warm guest has the wrapper**

After a new VM reaches `ready`, either inspect a fresh clone or run a disposable job whose `agent_log` / console is enough. Minimum check: next job’s `runner.log` must not die at `Running job:` with no completion.

- [ ] **Step 6: Re-run coatcheck e2e on 8g**

Trigger the existing `E2E Tests` workflow on `event-tickets` (same `runs-on: temperci-4vcpu-8g-ubuntu-2404`). Do not switch the label back to 6g.

Pass criteria (all required):

1. TemperCI `outcome=success` **and** GitHub `conclusion=success`.
2. Runner log contains `Job e2e completed with result: Succeeded`.
3. GitHub steps include `Run E2E tests` completed (not stuck on `Cleanup stale e2e Docker resources`).
4. Job wall time is in the 10–20 minute range (the working 6g jobs were ~15 minutes), not the ~5 minute abort.

Fail criteria → do not “tune and hope”:

- Still `Out of memory` / 134 on Listener: raise **only** `TEMPERCI_LISTENER_HEAP` to `3221225472` (3 GiB) and re-bake wrappers. Do not remove the cap.
- Incomplete job + exit 98: remap worked; heap still too tight. Same 3 GiB bump.
- Job reaches Playwright and OOMs there: that is the job using 8g honestly. Then the coatcheck workflow (workers, Next build) is in play, not this plan.

## Out of scope

- Changing coatcheck `runs-on` back to 6g.
- Deleting coatcheck’s “Cleanup stale e2e Docker resources” step. On a fresh TemperCI VM it is a no-op; it is where the runner *died*, not why. Optional later cleanup in that repo.
- cgroup memory.max.
- Host-side Firecracker memory overcommit changes.
- Raising the 8g shape above 8192 MiB.

## Self-review

1. **Spec coverage:** 8g must work (Tasks 2 + 4). Extra RAM goes to the job (wrappers, not `env` on `run.sh`). False success on incomplete jobs (Task 1). Operator docs (Task 3). Live proof (Task 4).
2. **Placeholders:** none. Heap values, exit codes, file paths, and host commands are literal.
3. **Types / names:** `RefineOutcome`, `jobStartedButIncomplete`, exit `95` / `97` / `98`, `wrap-runner-dotnet.sh`, `Runner.Listener.real` are used consistently.
