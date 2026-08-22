# Todo 5 report — Automated proof of a real job

**Status: DONE**

In-repo proof of the control↔agent protocol and inject/guest-agent contract is in place. Existing e2e (mock JIT + fake VMM) still passes. Firecracker inject round-trip compiles and skips without `mkfs.ext4`. A host smoke script and Ubuntu+KVM runbook cover the Linux+KVM / live GitHub steps this machine cannot execute.

## Files changed

Created:

- `scripts/real-job-smoke.sh` — Linux-only operator smoke (`-fast` = artifacts + systemd + healthz; default calls `scripts/vmm-smoke.sh --backend firecracker`). Prints `SMOKE_OK` / `SMOKE_FAIL`.
- `internal/vmm/firecracker/inject_test.go` — `createInjectDrive` → `SyncGuestDirToInjectDrive` → `ReadInjectFile`; `t.Skip` without e2fsprogs or loop mount.
- `deploy/ubuntu/guest-agent/protocol_test.sh` — bash protocol: jitconfig in → runner.exit out (stub mount/runner).
- `internal/agent/guest_protocol_test.go` — FileGuestExec wait + exec of `protocol_test.sh`.
- `docs/todos/proof-runbook.md` — exact Ubuntu+KVM / GitHub proof commands.

No production agent/control/VMM logic rewritten. Assignment persist APIs (`store.UpsertAssignment` / `control.AssignmentPersister`) already exist from todo 3 with their own tests; this todo did not touch `assignment.go`.

## Tests run + result

```text
bash -n scripts/real-job-smoke.sh                          OK
bash deploy/ubuntu/guest-agent/protocol_test.sh            OK (runner.exit=0)
./scripts/real-job-smoke.sh                                exit 1 on Darwin (SMOKE_FAIL) — expected
go test ./internal/e2e/ ./internal/vmm/firecracker/ ./internal/agent/ -count=1
  ok  e2e           (TestE2E_WebhookMintAssignBindFinish)
  ok  firecracker   (TestInjectDriveRoundTrip SKIP: mkfs.ext4 not found)
  ok  agent         (guest protocol + existing tests)
```

## Still requires a live GitHub job

On a Linux+KVM host (Ubuntu): `scripts/real-job-smoke.sh` (and `-fast` for systemd/healthz), then dispatch `runs-on: temperci-4vcpu-ubuntu-2404` and confirm control `minted JIT config`, agent `warm VM ready` / `job bound` / `starting guest runner` / `guest runner exited` / `job complete`, a green GitHub job, and no leftover busy dir under `/var/lib/temperci/instances`. See `docs/todos/proof-runbook.md`.
