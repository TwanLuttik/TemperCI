# Todo 5 — Automated proof of a real job

**Status area:** Automated proof of a real job — none in-repo (e2e uses mock JIT + fake VMM)  
**Goal:** Add tests and an operator smoke script that prove the *protocol* end-to-end, and on a Linux+KVM host can prove Firecracker boot + guest-agent + runner start (GitHub optional).

## Context

`internal/e2e/e2e_test.go` already covers webhook → mint → claim → bind → finish with a **mock minter** and **fake VMM**. Keep that. Add layers above it.

This todo may run **after** todos 1–3. If persistence or inject helpers are missing, write tests that compile against current APIs and skip features with `t.Skip` + a clear reason — but prefer testing what already exists:

- `firecracker.SyncGuestDirToInjectDrive` / `ReadInjectFile` (need `mkfs.ext4` — skip if missing)
- Guest agent script protocol (bash): jitconfig in → runner.exit out
- Control assignment persist if `store` has assignment APIs; otherwise skip
- Host smoke script for 10.0.0.50

## Files you may create/change

- Create: `scripts/real-job-smoke.sh` — operator/host smoke (see below)
- Create: `internal/vmm/firecracker/inject_test.go` if not present
- Create: `deploy/ubuntu/guest-agent/protocol_test.sh` or a Go test that execs the guest agent in a fake mount (optional)
- Modify: `internal/e2e/e2e_test.go` only to add a second test if needed (do not break the existing one)
- Create: `docs/todos/proof-runbook.md` with the exact commands to prove a GitHub job on Ubuntu+KVM

Do **not** rewrite agent/control production code except tiny test hooks. Do **not** own image build or persistence schema.

## `scripts/real-job-smoke.sh`

Must:

1. Exit 1 on non-Linux
2. Check `/dev/kvm`, `firecracker`, `mkfs.ext4`, image + kernel paths (env overrides)
3. Optionally `-fast`: only check artifacts + systemd units + `healthz`
4. Default: if images exist, create one Firecracker VM via `temperci-agent -demo-bind` **or** a small Go/smoke invocation already in-repo (`scripts/vmm-smoke.sh`). Prefer extending/calling existing smoke rather than duplicating VMM logic.
5. Print a final `SMOKE_OK` or `SMOKE_FAIL` line
6. Document how to dispatch a real workflow (`runs-on: temperci-4vcpu-ubuntu-2404`) and what control/agent log lines to expect (`minted JIT config`, `job bound`, `guest runner exited`, `job complete`)

## Inject unit test

If `mkfs.ext4` exists:

- `createInjectDrive` → write a file via `SyncGuestDirToInjectDrive` → `ReadInjectFile` returns it

If not (macOS CI): `t.Skip`.

## GitHub-level proof

Cannot call live GitHub from unit tests. The runbook must list:

1. `curl -sS http://127.0.0.1:8080/healthz`
2. Agent log `warm VM ready`
3. Dispatch workflow
4. Control: `minted JIT config`
5. Agent: `starting guest runner` / `guest runner exited`
6. GitHub job green
7. `ls /var/lib/temperci/instances` has no leftover busy VM

## Tests

```bash
go test ./internal/e2e/ ./internal/vmm/firecracker/ ./internal/agent/ -count=1
bash -n scripts/real-job-smoke.sh
```

## Done when

- [ ] Smoke script exists and is executable
- [ ] Inject round-trip test exists (skip without e2fsprogs)
- [ ] Proof runbook written
- [ ] Existing e2e still passes
- [ ] Do **not** git commit
