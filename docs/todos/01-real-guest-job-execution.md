# Todo 1 — Real guest job execution

**Status area:** Real guest job execution — Incomplete / uncommitted  
**Goal:** Finish the Firecracker bind path so a queued GitHub Actions job actually starts `actions/runner` inside a microVM, waits for it, and tears the guest down.

## Context

Much of this already exists as **uncommitted** working-tree code. Do not rewrite it. Harden, complete, and make it the production path.

Current pieces:

- `internal/vmm/firecracker/inject.go` — 64 MiB ext4 inject disk; `SyncGuestDirToInjectDrive`, `ReadInjectFile`
- `internal/vmm/firecracker/network.go` — tap + `/30` + NAT + kernel `ip=` cmdline
- `internal/agent/guest.go` — `FirecrackerGuestExec` stages JIT and polls `runner.exit`
- `internal/agent/runner.go` — writes `jitconfig`; official runner wants the **encoded string**, not a path
- `deploy/ubuntu/guest-agent/temperci-runner-agent.sh` — guest boot agent (this is what actually execs `run.sh`)
- `cmd/temperci-agent/main.go` — `WaitRealRunner` when `vmm_backend=firecracker` and `job_simulate_seconds=0`

Known gaps to close:

1. Agent worker is **single-threaded**: `Worker.Run` blocks in `handleJob`, so `max_ready > 1` still runs one job at a time. Run each job in a goroutine (bounded by `Capacity`).
2. Config does not require `kernel_path` when `vmm_backend=firecracker` (`internal/config/config.go`). Validate it.
3. `deploy/agent.example.toml` defaults to `vmm_backend = "fake"`. Default comments + example for Linux should show `firecracker` and an uncommented `kernel_path`.
4. Firecracker `boot_args` (`bootArgs` in `network.go`) should include `root=/dev/vda rw` so guests boot without relying on implicit append.
5. Host prereqs must include `e2fsprogs` and `iptables` (inject mkfs + NAT). If you touch `host-prereqs.sh`, only add those packages — do not rewrite the image pipeline (todo 2 owns that file beyond a one-line package add).
6. Guest agent script is the source of truth for `--jitconfig`. Keep passing the base64 string, not a path. Host `InjectRunner` may write a misleading `runner.cmd`; fix the comment/command so it does not claim a file path is valid.

## Files you may change

- `internal/agent/worker.go`, `internal/agent/worker_test.go`
- `internal/agent/guest.go`, `internal/agent/runner.go`
- `internal/agent/client.go` (only if claim/report needs to be safe under concurrent jobs)
- `cmd/temperci-agent/main.go`
- `internal/config/config.go`, `internal/config/config_test.go`
- `internal/vmm/firecracker/firecracker.go`, `inject.go`, `network.go` + existing tests
- `deploy/ubuntu/guest-agent/**`
- `deploy/agent.example.toml`
- `deploy/ubuntu/host-prereqs.sh` (package list only)

Do **not** change: `internal/store`, `internal/control/assignment.go`, guest image build scripts (todo 2/4), e2e smoke scripts (todo 5).

## Implementation notes

### Concurrent jobs

In `Worker.Run`, after a successful `Claim`, start `handleJob` in a goroutine. Track in-flight jobs so shutdown waits. Do not claim when `snapshot().FreeSlots <= 0`. Existing `Capacity = MaxReady` and pool `Busy` already model slots — use them.

Add/extend `internal/agent/worker_test.go` so two claims can be in flight (fake pool / stub runner).

### Kernel cmdline

`bootArgs` should look like:

```text
console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw [ip=...]
```

### Guest agent

Keep:

- `RUNNER_ALLOW_RUNASROOT=1`
- `--jitconfig "$JIT_B64"` (string, not path)
- write `runner.exit` + `runner.log` + `agent.log` on the inject disk
- do not block forever on `network-online` (unit already avoids this)

Make the script `set -e` safe around expected mount failures (it currently uses `set -u` only). Leave protocol compatible with `FirecrackerGuestExec.WaitRunner`.

## Tests

- `go test ./internal/agent/ ./internal/config/ ./internal/vmm/firecracker/`
- Existing pool/orphan/runner tests must stay green
- New worker concurrency test

## Done when

- [ ] Firecracker path injects JIT, guest agent starts official runner, host waits on `runner.exit`
- [ ] Agent can run more than one job concurrently up to `max_ready`
- [ ] `kernel_path` is required for firecracker
- [ ] Example agent toml documents the production Firecracker settings
- [ ] Boot args include `root=/dev/vda rw`
- [ ] Targeted tests pass
- [ ] Do **not** git commit
