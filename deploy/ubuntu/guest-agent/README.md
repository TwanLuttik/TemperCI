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

The guest agent also:

- Enables a 2 GiB `/swapfile` at boot (`TEMPERCI_SWAP_MIB=0` disables).
- Starts `run.sh` with `DOTNET_gcServer=0` and `DOTNET_GCHeapHardLimit=1073741824` so Listener does not eat extra guest RAM.
- Remaps runner abort / `Out of memory` / exit 134 to `runner.exit=97` (upstream `run.sh` otherwise exits 0).

## Protocol

1. Warm VM boots; guest unit signals `agent.ready`, then checks `/dev/vdb` for `jitconfig` every `TEMPERCI_IDLE_POLL_SEC` (default 2s). Fast 50ms polls are only used until ready.
2. Host binds job → writes `instances/<id>/guest/jitconfig` → syncs into `inject.ext4`.
3. Guest mounts inject, runs `run.sh --jitconfig …`, writes `runner.exit`.
4. Host polls inject disk for `runner.exit`, then destroys the VM.
