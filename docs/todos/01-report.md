# Todo 1 report — Real guest job execution

**Status:** DONE

Hardened the existing uncommitted Firecracker bind path so the host agent can claim up to `max_ready` jobs at once, inject encoded JIT (string, not path), wait on guest `runner.exit`, and tear the guest down. Config now requires `kernel_path` for `vmm_backend=firecracker`; boot args include `root=/dev/vda rw`; host prereqs include `e2fsprogs` and `iptables`.

## Files changed

- `internal/agent/worker.go` — `handleJob` per claim in a goroutine; inflight + `Busy` bound `FreeSlots`; skip claim when `FreeSlots <= 0`; shutdown waits for in-flight jobs
- `internal/agent/worker_test.go` — concurrent two-job test (capacity 2, third job waits); no-capacity test expects no `/claim`
- `internal/agent/runner.go` — `runner.cmd` / `Exec` document `--jitconfig <encoded-jit-string>`, not a file path
- `internal/agent/runner_test.go` — assert no `/mnt/temperci/` path for `--jitconfig`
- `internal/config/config.go` — `kernel_path` required when `vmm_backend=firecracker`
- `internal/config/config_test.go` — firecracker with/without kernel
- `internal/vmm/firecracker/network.go` — `root=/dev/vda rw` on kernel cmdline
- `internal/vmm/firecracker/network_test.go` — boot-args prefix + `ip=` when net files exist
- `cmd/temperci-agent/main.go` — wait for worker goroutines before pool shutdown
- `deploy/agent.example.toml` — production `vmm_backend = "firecracker"` + uncommented `kernel_path`
- `deploy/ubuntu/host-prereqs.sh` — added `e2fsprogs` and `iptables` only
- `deploy/ubuntu/guest-agent/temperci-runner-agent.sh` — `set -eu`; still `--jitconfig "$JIT_B64"`; mount failures stay non-fatal

Did not change store/assignment/persist, guest image build scripts, or e2e smoke (other todos).

## Tests

```
go test ./internal/agent/ ./internal/config/ ./internal/vmm/firecracker/
```

Result: **ok** (agent 3.297s, config 0.995s, firecracker 0.809s). Existing pool/orphan/runner tests stayed green. New: `TestWorker_ConcurrentJobsUpToCapacity`, `TestLoadAgentFile_FirecrackerRequiresKernel`, `TestBootArgsIncludesRootAndRW`.

## Remaining risks

- End-to-end “queued GitHub job → official runner inside Firecracker → teardown” is not proven here (image + smoke belong to todos 2/4/5).
- Host `ReadInjectFile` still loop-mounts `inject.ext4` while the guest may remount it for `runner.exit` / heartbeat; the 3s quiet window + 2s poll reduces but does not eliminate missed reads.
- Shutdown waits up to 25s for in-flight `handleJob`; a stuck destroy can still race with `pool.Shutdown`.
- `kernel_path` is required in config but not checked for existence until VM create.
