# Guest runner agent (required for real GitHub jobs)

This unit runs **inside** the Firecracker rootfs. The host agent writes JIT to a second
disk (`inject.ext4` → `/dev/vdb`); this script mounts it, starts official
`actions/runner`, and writes `runner.exit` for the host to wait on.

## Install into an existing image on the Ubuntu host

```bash
IMG=/var/lib/temperci/images/ubuntu-2404-runner.ext4
MNT=$(mktemp -d)
mount -o loop "$IMG" "$MNT"

# From TemperCI checkout:
./deploy/ubuntu/guest-agent/install-into-rootfs.sh "$MNT"

# Confirm runner still present:
ls -la "$MNT/opt/actions-runner/run.sh"

umount "$MNT"
rmdir "$MNT"
```

Then restart the agent so warm VMs boot from the updated image:

```bash
systemctl restart temperci-agent
```

## Host agent.toml

```toml
vmm_backend = "firecracker"
job_simulate_seconds = 0
# optional; defaults to 6h when waiting on real runner
# job_deadline_seconds = 7200
```

While a job is running the guest snapshots `_diag/pages/*.log` — that is the action stdout GitHub streams in the Actions UI. The runner deletes each page after upload; TemperCI keeps a copy so finished steps stay visible. Worker `_diag` is only used before the first page exists.

The guest agent also:

- Enables a 2 GiB `/swapfile` at boot (`TEMPERCI_SWAP_MIB=0` disables).
- Wraps `bin/Runner.Listener` and `bin/Runner.Worker` with workstation GC and `DOTNET_GCConserveMemory=7`. Worker is capped at 2 GiB. Listener is 2 GiB on guests under 8 GiB and 3 GiB on 8g+, with `DOTNET_GCHighMemPercent=60` so it GCs when the guest is under pressure. Job steps do not inherit those variables.
- Remaps runner abort / `Out of memory` / exit 134 to `runner.exit=97`, and remaps exit 0 after `Running job:` without `completed with result: succeeded` to `runner.exit=98`.

## Protocol

1. Warm VM boots; guest unit signals `agent.ready`, then checks `/dev/vdb` for `jitconfig` every `TEMPERCI_IDLE_POLL_SEC` (default 2s). Fast 50ms polls are only used until ready.
2. Host binds job → writes `instances/<id>/guest/jitconfig` → syncs into `inject.ext4`.
3. Guest mounts inject, runs `run.sh --jitconfig …`, writes `runner.exit`.
4. Host polls inject disk for `runner.exit`, then destroys the VM.
