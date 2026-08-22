# Host resource admission for microVMs

**Date:** 2026-08-21  
**Status:** Approved for implementation planning  
**Product:** TemperCI — agent-local host resource admission

## 1. Problem

TemperCI only gates new microVMs with a configured slot cap (`max_ready`). It does not look at leftover host RAM or disk. An operator can set `max_ready = 8` and `memory_mib = 8192` on a 16 GiB box; the agent will still try to boot those VMs. Firecracker preallocates guest RAM, and the current Firecracker path copies the whole rootfs per instance, so oversubscribe becomes host OOM or disk-full.

CPU is not a hard problem for this product: vCPUs are scheduled on host cores and CI load is bursty.

## 2. Goals

1. Clamp the agent’s effective `max_ready` from **host size** at start (RAM total and current free disk on `data_dir`), after a reserved host headroom.
2. Refuse a **new create** (warm replenish and cold bind) when leftover RAM or disk cannot cover the next VM plus that headroom.
3. Report `FreeSlots = 0` when this host cannot bind another job without creating a VM it cannot afford, so control leaves the job pending.
4. Treat CPU as **observability only** (dashboard / register payload). Never refuse a boot because of vCPU count.
5. Keep the check on the **agent**. Control continues to trust `FreeSlots`.

## 3. Non-goals

- Control-plane leftover math or cross-host packing
- Hard vCPU admission or a vCPU overcommit multiplier
- Mixed VM sizes per host (still one `vcpu` / `memory_mib` per agent)
- Changing the Firecracker full-rootfs copy to a real COW overlay
- Auto-writing a new `max_ready` back into `agent.toml`
- Kubernetes-style node allocatable / cgroup isolation

## 4. Decisions (locked)

| Topic | Choice |
|---|---|
| Where | Agent-local. Control unchanged except storing/displaying a resource snapshot. |
| Slot clamp | `effective_max_ready = min(configured max_ready, host fit)` |
| Create gate | Both **committed RAM** and **live leftover** (MemAvailable + disk free) |
| CPU | Soft only. Report `num_cpu`. Do not gate. |
| Job when full | Do not claim. Job stays pending. |
| Warm bind | Always allowed if a warm VM already exists (memory already committed). |
| Inventory failure | Fail closed: do not create. |
| Tests / fake / macOS | `InventorySource` is injected. Nil inventory preserves today’s slot-only tests. |

## 5. Admission math

Per-VM costs:

- RAM: configured `memory_mib`
- Disk: `stat(image_path).size` rounded up to MiB, plus **256 MiB** slop (inject drive, meta, logs)

Host headroom (config, 0 means default):

- `host_reserve_memory_mib` default **2048**
- `host_reserve_disk_mib` default **5120**

`host_fit` at start:

```text
ram_fit  = max(0, ram_total_mib - reserve_ram) / memory_mib
disk_fit = max(0, disk_free_mib - reserve_disk) / disk_per_vm_mib
host_fit = min(ram_fit, disk_fit)
```

If `disk_per_vm_mib` cannot be measured (image missing), disk does not limit `host_fit`.

`CanCreate(allocated)` before every provision (`allocated` = warm + busy + pool_boot + destroying + createInFlight):

```text
refuse ram_committed if allocated*memory_mib + memory_mib > ram_total_mib - reserve_ram
refuse ram_available if ram_avail_mib < memory_mib
refuse disk_free     if disk_free_mib < disk_per_vm_mib + reserve_disk
```

Live RAM does **not** re-add the host reserve (the host OS is already running; MemAvailable is leftover). Live disk **does** keep `reserve_disk` free after the new overlay.

`RemainingCreates` is how many additional creates would still pass `CanCreate`, using:

```text
min( floor((ram_total - reserve_ram - allocated*memory) / memory),
     floor(ram_avail / memory),
     floor((disk_free - reserve_disk) / disk_per_vm) )
```

`FreeSlots = min(effective_max_ready - used, warm + RemainingCreates)`.

`used` stays `max(Busy, inflight)` as today.

`Worker.Capacity == 0` is valid (host cannot fit one VM). Do **not** coerce 0 to 1.

## 6. Components

### 6.1 `internal/agent/admission.go`

Pure functions: `Admission`, `HostInventory`, `MaxFit`, `CanCreate`, `Remaining`, `OverlayEstimateMiB`, `ClampPoolToHost`. No `/proc`, no pool lock.

### 6.2 `internal/agent/inventory.go`

`InventorySource` interface. `StaticInventory` for tests. `ProcInventory` for production:

- RAM: parse `/proc/meminfo` `MemTotal` and `MemAvailable` (kB → MiB)
- Disk: `syscall.Statfs` on `data_dir`
- `NumCPU`: `runtime.NumCPU()`
- Linux + `/proc/meminfo` error: `Sample` returns error (fail closed)
- Non-Linux (fake/dev): treat RAM as 1 TiB so disk is the only real check; Statfs still runs

### 6.3 Pool

`PoolDeps.Inventory` optional. On `NewPool`, sample once and clamp `MinReady`/`MaxReady`. `canCreateLocked` calls `CanCreate` after the existing `max_total_vms` check. Store `clamp_reason` and `last_admit_reason` for the dashboard.

### 6.4 Worker + register

`Capacity` comes from `pool.EffectiveMaxReady()`. Register heartbeat includes `HostResources`. Claim still sends `FreeSlots` only; control already refuses `FreeSlots <= 0`.

### 6.5 Dashboard

Hosts table shows leftover RAM, leftover disk, effective vs configured max, and a clamp/refuse reason. CPU count is shown; it does not affect color/gating.

## 7. Config

New optional keys on `AgentConfig`:

```toml
host_reserve_memory_mib = 2048
host_reserve_disk_mib = 5120
```

`0` or omitted → defaults above. Negative → validation error. Existing `max_ready` remains the operator’s *desired* cap; the agent never rewrites the file.

## 8. Testing

- Table tests for `MaxFit` / `CanCreate` / `Remaining` / `ClampPoolToHost` / `OverlayEstimateMiB`
- `parseMeminfo` fixture tests (no Linux required)
- Pool: inventory that fits 1 VM with `min_ready=3` boots only 1; live RAM too low blocks cold create; warm bind still works
- Worker: no warm + `RemainingCreates==0` → no `/claim`; one warm + cannot create → `FreeSlots==1`
- Control registry stores `resources` and `/api/v1/hosts` returns it

## 9. Success criteria

1. A host whose RAM or disk cannot fit `max_ready` VMs of the configured size reports a smaller `effective_max_ready` and does not boot past that.
2. After start, if another process or the Actions cache eats leftover RAM/disk, the next create is refused and the agent stops claiming extra work.
3. Existing warm VMs can still bind.
4. Jobs stay pending instead of being assigned to a host that cannot boot them.
5. Slot-only unit tests that do not inject inventory keep their current behavior.
