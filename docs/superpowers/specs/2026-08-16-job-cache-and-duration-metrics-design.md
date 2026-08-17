# Job duration metrics and local Actions cache

**Date:** 2026-08-16  
**Status:** Approved for implementation (architecture + components)  
**Product:** TemperCI — durations in the operator dashboard plus a host-local `actions/cache` gateway

## 1. Problem

GitHub Actions jobs on TemperCI still pull and push `actions/cache` over the WAN. Assignment rows already store `created_at`, `assigned_at`, `started_at`, and `finished_at`, but the dashboard never shows how long queue, bind, or run took. Operators cannot see whether a job was slow or whether a later cache would have helped.

## 2. Goals

1. Show **queue / bind / run / total** time for every assignment in the dashboard.
2. Show **p50 / p95 run time** of recent finished jobs on the overview.
3. Intercept official `actions/cache` / `setup-*` cache traffic and store blobs on the **agent host**, scoped **repo-wide** (`org/repo`).
4. Prefer the agent that already has that repo’s cache when claiming work (**sticky scheduling**).
5. Do not change workflow YAML. Do not reuse a guest disk after a job.

## 3. Non-goals (this slice)

- GitHub-identical branch / fork-PR cache isolation
- Shared control-plane object storage
- Docker layer / sticky rootfs cache
- Prometheus histograms or a separate metrics DB
- Full Azure Blob SDK parity beyond Put Blob, Put Block, Put Block List, ranged GET

## 4. Architecture

```text
Guest actions/cache  --HTTPS-->  host SNI intercept
                                   ├ cache hosts → cache gateway
                                   └ other SNI   → splice to GitHub

Gateway: Twirp CacheService + Azure-shaped blob URLs
Store:   <data_dir>/cache/<org>/<repo>/
Claim:   prefer pending job whose repo this agent already has, else FIFO
UI:      durations derived from existing timestamps + cache hit/miss/bytes
```

Guest rootfs is still destroyed after every job. Cache lives only on the host, never under `instances/`.

If the gateway is down, cache steps fail locally. This slice does **not** fall through to GitHub’s cache (avoids a stale next hit).

## 5. Components

### 5.1 Cache store (`internal/ghacache`)

Filesystem + JSON metadata under `data_dir/cache/<org>/<repo>/`. Keys are namespaced by repo only. Restore-keys use GitHub prefix match, same `version`. LRU eviction when `cache_max_bytes` (default 50 GiB) is exceeded. In-flight uploads are never deleted.

### 5.2 Cache gateway (agent)

HTTP(S) server implementing:

- `POST /twirp/github.actions.results.api.v1.CacheService/CreateCacheEntry`
- `POST /twirp/github.actions.results.api.v1.CacheService/FinalizeCacheEntry`
- `POST /twirp/github.actions.results.api.v1.CacheService/GetCacheEntryDownloadURL`
- Blob PUT/GET on signed URLs (Azure Block Blob subset)

Repo namespace is taken from a **bind table** (guest source IP → `org/repo` of the assigned job), not from `ACTIONS_RUNTIME_TOKEN`.

### 5.3 SNI intercept

Guest `:443` is REDIRECTed to the gateway. SNI for GitHub results / `*.blob.core.windows.net` is terminated with a TemperCI CA installed in the guest image. Other SNI is spliced through.

### 5.4 Sticky claim

`ClaimRequest.cached_repos` is the list of `org/repo` directories on that agent. `ClaimNext` picks the oldest pending job whose repo is in that list; if none match, FIFO.

### 5.5 Duration metrics

Derived, not stored:

| Field | Formula |
|---|---|
| Queue | `assigned_at - created_at` (or `now - created_at` while unassigned) |
| Bind | `started_at - assigned_at` (or `now - assigned_at` while assigned) |
| Run | `finished_at - started_at` (or `now - started_at` while started) |
| Total | `(finished_at or now) - created_at` |

Cache counters (`hits`, `misses`, `bytes_in`, `bytes_out`) are stored on the assignment when the job finishes.

## 6. Data flow

1. Agent starts gateway, loads existing `data_dir/cache/*/*`, reports `cached_repos` on register/claim.
2. Control assigns a job (sticky if possible). Agent binds a warm VM and maps guest IP → repo.
3. Guest `actions/cache` restore hits Twirp `GetCacheEntryDownloadURL` via intercept. Hit → ranged GET of local blob. Miss → `ok: false`.
4. Save: `CreateCacheEntry` → Azure-shaped upload URL → PUT blocks → `FinalizeCacheEntry`.
5. Agent reports finish + cache counters. Control persists them. Dashboard shows timings + cache stats.
6. Next job for the same repo prefers this agent.

## 7. Error handling

- Unknown guest IP on the gateway → 403 (no repo bind).
- Duplicate `key+version` on create → `ok: false` (same as GitHub; skip save).
- Eviction failure is logged; upload still succeeds if space can be freed.
- Intercept/CA missing → cache steps fail; job otherwise continues.
- Sticky host full or offline → another host claims FIFO and builds its own copy.

## 8. Testing

- Unit: duration math, percentile, cache put/get/restore-keys/repo isolation/LRU, Twirp round-trip, SNI classify, sticky claim.
- Existing assignment FIFO tests stay green with empty `cached_repos`.
- No live GitHub or Firecracker required for this slice.

## 9. Config

Agent:

```toml
cache_max_bytes = 53687091200   # 50 GiB
cache_listen_addr = "127.0.0.1:8743"
```

Empty `cache_listen_addr` disables the gateway (tests / hosts without cache).
