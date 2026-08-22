# Job lifecycle: warm pool, bind, teardown

This document defines how a GitHub Actions job moves through TemperCI, including the **warm pool** and **mandatory cleanup** behavior required for self-hosted installs.

## States

```text
                    ┌─────────────┐
                    │  pool boot  │
                    └──────┬──────┘
                           ▼
┌──────────┐  assign   ┌──────────┐  job end   ┌────────────┐
│   warm   │ ────────► │   busy   │ ─────────► │ destroying │
└──────────┘           └──────────┘            └─────┬──────┘
     ▲                                               │
     │              replenish after clean destroy    │
     └───────────────────────────────────────────────┘
```

| State | Meaning |
|-------|---------|
| `pool boot` | Creating a new microVM from the base image; not yet eligible for jobs |
| `warm` | Booted, idle, **no JIT config, no secrets**, ready to bind |
| `busy` | Bound to a job; runner running |
| `destroying` | Teardown in progress; must not accept new work |

Invariant: **after a job, the guest is destroyed**. We do not “reset and reuse” the same guest disk for the next job. Warm pool refill always creates a **new** clean VM (using COW/base image for speed).

## Warm pool

### Why

Cold microVM boot (image hydrate + boot + network) can take seconds. Self-hosted users still expect competitive pickup times. The warm pool moves boot **off the webhook critical path**.

### Rules

1. Each host agent maintains `min_ready` warm VMs (default: at least 1 when the agent is healthy).
2. Soft cap `max_ready` prevents idle resource waste.
3. Warm VMs are **anonymous**: no GitHub runner registration until bind.
4. Warm VMs may be **recycled on a timer** or when the base image updates, so idle guests do not sit forever on stale images.
5. Pools are split per **shape** (`vcpu` + `memory_mib`). Settings define which sizes to keep warm (`min_ready`). A job’s `runs-on` label selects the size (`temperci-4vcpu-ubuntu-2404` or `temperci-2vcpu-4g-ubuntu-2404`). A matching warm VM is bound when one exists; otherwise that size is cold-booted. Unlisted sizes in a workflow are still spawned (cold) if the host has leftover RAM.

### Host resource admission

`max_ready` is the operator’s desired cap, not a promise the hardware can keep.

1. On agent start, sample host RAM (`MemTotal`) and free disk on `data_dir`. Compute how many VMs of this host’s `memory_mib` and overlay size fit after `host_reserve_memory_mib` (default 2 GiB) and `host_reserve_disk_mib` (default 5 GiB). Clamp `min_ready` / `max_ready` down to that fit.
2. Before every create (warm replenish or cold bind), refuse if committed guest RAM, live `MemAvailable`, or leftover disk cannot cover the next VM plus reserve.
3. Existing warm VMs may still bind (their RAM is already committed).
4. The worker reports `FreeSlots = min(effective slots, warm + remaining creates)`. Control does not assign when that is 0; the job stays pending.
5. vCPU count is reported on the host snapshot and is not a create gate.

### Bind path (fast path)

When the control plane assigns a job:

1. Agent selects a `warm` VM (or waits briefly for one to become ready).
2. Agent injects the JIT payload into the guest (or starts the runner process with JIT config via the guest channel).
3. Official `actions/runner` starts and adopts **one** job.
4. VM state becomes `busy`.
5. Agent begins replenishing the pool in the background so `min_ready` is restored.

If no warm VM is available, the agent may cold-boot (slower path). That is acceptable under load; it is not the happy path.

## JIT registration

TemperCI uses GitHub’s [just-in-time self-hosted runners](https://github.blog/changelog/2023-06-02-github-actions-just-in-time-self-hosted-runners/):

- Control plane calls the org (or repo) `generate-jitconfig` REST endpoint.
- Labels on the JIT config match the job’s `runs-on` requirements.
- Config is single-use: after one job, GitHub removes the runner registration.
- JIT material is short-lived (order of one hour); still treat it as secret in logs and on disk.

The GitHub App needs permission to manage organization self-hosted runners so it can mint JIT configs. The App does **not** need (and should not claim) direct access to repository secrets; secrets are delivered to the runner by GitHub’s existing job protocol.

## Teardown (required)

Every terminal job outcome must run full teardown: success, failure, cancelled, timed out, or agent-forced kill.

### Destroy checklist

For each finished or aborted job:

1. **Stop runner** — terminate the guest runner process if still running.
2. **Destroy microVM** — power off and remove the VM instance (guest kernel, memory, nic, COW disk).
3. **Delete host scratch** — remove overlay/diff disks, sockets, log dirs, temp workdirs, and any per-VM metadata files for that VM id.
4. **Drop proxies / binds** — remove any host-side proxy state, port forwards, or mounts associated with the VM.
5. **Reconcile GitHub** — JIT runners auto-remove after one job; if a registration is stuck offline, delete via API during reconciliation.
6. **Mark destroy complete** — only then count capacity free and allow replenish toward `min_ready`.

### What may remain

- Shared **base images** and templates used to spawn new VMs.
- Host-local **Actions cache** (`data_dir/cache/`) and **OCI / build cache** (`data_dir/ocicache/`), never under `instances/`.

### What must not remain

- Guest rootfs diffs from completed jobs
- Workspace checkout directories from a finished job
- JIT configs or tokens on disk after teardown
- Orphan QEMU/Firecracker processes
- Stale tap devices, bridges, or netns for dead VMs

## Failure and recovery

| Scenario | Behavior |
|----------|----------|
| Job completes normally | Destroy + replenish |
| Job cancelled / failed | Same destroy path |
| Runner hung past deadline | Force kill guest → destroy → replenish |
| Agent crash mid-job | On restart: orphan sweep destroys leftover VMs/files; control plane may reassign or mark failed depending on GitHub job state |
| Host reboot | Same orphan sweep on agent start |
| Warm VM never bound | May stay warm until recycle timer; if it received any secrets/JIT by mistake, destroy instead of reusing |
| Bind failed after JIT mint | Destroy that VM; do not return it to warm; mint path should not leave partial identity |

## Orphan sweeper

On agent start and on a periodic interval:

1. List known VM ids from agent state DB/file.
2. List actual hypervisors / processes / disk files on the host.
3. Destroy anything running or on disk that is not accounted for as `warm` or `busy`.
4. Remove scratch directories without a live owner.
5. Log every forced cleanup for operator visibility.

Self-host operators must be able to trust that a crashed agent does not silently fill the disk.

## Observability (MVP minimum)

Per host, expose or log:

- Warm / busy / destroying counts
- Last job id bound per VM
- Teardown duration and failures
- Orphan sweep actions
- Cold-boot vs warm-bind job starts

## Related docs

- [overview.md](overview.md)
- [install-targets.md](install-targets.md)
