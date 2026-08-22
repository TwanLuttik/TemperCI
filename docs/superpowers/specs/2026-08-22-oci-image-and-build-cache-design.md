# Host-local OCI pull-through and BuildKit registry cache

**Date:** 2026-08-22
**Status:** Approved for implementation
**Product:** TemperCI — cache Docker Hub / GHCR pulls and Docker build layers on the agent host

## 1. Problem

Every TemperCI job destroys its guest disk. `docker pull postgres`, `services:`, Compose, and `docker build` therefore hit the WAN on every job. The existing host cache only implements GitHub `actions/cache`. Docker Hub, GHCR, and BuildKit layers are spliced through.

## 2. Goals

1. Cache **registry pulls** from Docker Hub and GHCR on the agent host with **no workflow YAML change**.
2. Cache **Docker / BuildKit build layers** on the agent host with **no workflow YAML change**.
3. **Hybrid isolation:** anonymous/public layers are shared host-wide; private pulls and build output stay in `org/repo`.
4. Keep the job invariant: **destroy the guest after every job**. Cache lives only on the host.
5. Do not break real `docker push` to GHCR / Hub.

## 3. Non-goals

- Quay, GCR, ECR, or arbitrary OCI registries (add later)
- Sticky guest rootfs or a shared `/var/lib/docker` disk
- GitHub-identical branch / fork-PR isolation
- Cross-host / control-plane object storage
- Replacing the existing `actions/cache` gateway
- Pre-seeding images into the Ubuntu base rootfs

## 4. Decisions (locked)

| Topic | Choice |
|---|---|
| Approach | Host OCI pull-through + BuildKit `type=registry` cache |
| Registries | `registry-1.docker.io` and `ghcr.io` only |
| Isolation | Public (anonymous origin fetch) shared; credentialed fetch + build cache per `org/repo` |
| Transport | Existing guest `:443` SNI intercept + TemperCI CA |
| Auth tokens | Splice `auth.docker.io`; do not MITM Hub’s token service |
| Blob CDNs | Host follows origin redirects; guest never talks to Cloudflare |
| Build export | Guest wrapper adds `--cache-from/--cache-to type=registry` to `docker build` / `docker buildx build` |
| Build ref | `ghcr.io/__temperci_cache/<org>/<repo>/buildkit` — stored locally, never forwarded |
| Real pushes | PUT/POST/PATCH to any other name are reverse-proxied to origin |
| Failure | Pull miss or gateway error falls through to origin. Build-cache export failure is ignored; the job continues |
| Disk | Separate LRU from Actions cache. Default 100 GiB at `data_dir/ocicache/` |
| Concurrency | Content-addressed blobs. No shared writable Docker graph |

## 5. Architecture

```text
Guest dockerd / buildx
    --HTTPS :443-->  host SNI intercept
                      ├ actions cache hosts          → ghacache (unchanged)
                      ├ registry-1.docker.io         → ocicache gateway
                      ├ ghcr.io                      → ocicache gateway
                      └ other SNI (auth.docker.io, …) → splice

OCI gateway: Docker Registry HTTP API v2
Store:       <data_dir>/ocicache/
               blobs/public/sha256/<hex>
               blobs/repos/<org>/<repo>/sha256/<hex>
               manifests/public/<host>/<name>/…
               manifests/repos/<org>/<repo>/<host>/<name>/…

Repo key:    existing bind table (guest IP → org/repo)
```

Guest disk is still destroyed. Cache is never under `instances/`.

## 6. Components

### 6.1 Store (`internal/ocicache`)

Filesystem store with two scopes:

- `public` — blobs and tag/digest manifests fetched without the guest’s credentials
- `repo` — blobs/manifests fetched only after retrying with the guest `Authorization`, plus every `__temperci_cache/` write

Lookup order for GET: public, then the bound repo. A blob that required guest credentials is never copied into `public/`.

Digests are `sha256:` + 64 hex. Names and repos are path-sanitized (`..` rejected). LRU eviction when `oci_cache_max_bytes` is exceeded; in-flight uploads are not deleted. Prefer evicting the oldest ready blob (public or repo).

### 6.2 Gateway

HTTP handler implementing:

| Method | Path | Behavior |
|---|---|---|
| GET/HEAD | `/v2/` | `200` + `Docker-Distribution-API-Version: registry/2.0` |
| GET/HEAD | `/v2/<name>/manifests/<ref>` | Cache, else origin |
| GET/HEAD | `/v2/<name>/blobs/<digest>` | Cache, else origin |
| POST | `/v2/<name>/blobs/uploads/` | Build-cache: start local upload. Else proxy |
| PATCH/PUT | `/v2/<name>/blobs/uploads/<uuid>` | Build-cache: append/commit. Else proxy |
| PUT | `/v2/<name>/manifests/<ref>` | Build-cache: store locally. Else proxy |
| GET | `/v2/<name>/tags/list` | Proxy (not cached in v1) |

`name` may contain slashes (`library/postgres`, `org/img`, `__temperci_cache/org/repo/buildkit`).

Host comes from TLS SNI / `Host`. Repo comes from the bind table (same rule as `ghacache`: guest source IP). Unbound guests may read `public/` and perform anonymous origin fetches only.

### 6.3 Origin fetch and classification

`Origin` is an `http.RoundTripper` (tests inject a server). Production uses `http.DefaultTransport` (follows GET redirects, so CDN blobs are pulled by the host).

For GET/HEAD of a cacheable object:

1. Serve from store if present. Tag refs: still **revalidate** with origin (`If-None-Match` / digest compare) when origin is reachable; on origin failure serve stale.
2. On miss (or tag revalidation):
   - Request origin **without** the guest `Authorization`. For `registry-1.docker.io`, attach an anonymous token from `auth.docker.io` (`scope=repository:<name>:pull`).
   - `200` → store in `public/`, return to guest.
   - `401`/`403` and guest sent `Authorization` → retry with that header. `200` → store in the bound `org/repo`. No bind → return origin status (do not cache).
   - Other errors → return origin status. If a stale entry exists, serve it.

A blob fetched on the credentialed retry is never written to `public/`, even if the digest later appears in a public manifest.

### 6.4 Build-cache names

`IsBuildCacheName(name)` is true iff `name` has prefix `__temperci_cache/`.

Those writes stay on the host. The gateway never forwards them. If intercept is down, the guest’s push to real GHCR fails and the wrapper treats that as non-fatal.

### 6.5 SNI intercept

`ghacache.Intercept` gains an optional `Classify func(sni string) bool`. Nil keeps today’s `ShouldIntercept` (Actions hosts only).

The agent sets `Classify` to terminate Actions hosts **or** registry hosts. Handler is a mux: registry Host → OCI gateway; everything else → existing Actions gateway (which still reverse-proxies non-cache Twirp paths).

Do **not** terminate `auth.docker.io`.

### 6.6 Guest Docker wrapper

Installed as `/usr/local/bin/docker` in the guest image (ahead of `/usr/bin/docker`).

When `GITHUB_REPOSITORY` is `org/repo` and the invocation is `docker build` or `docker buildx build`:

- If the user already passed `--cache-to`, do not add ours.
- Otherwise add:
  - `--cache-from type=registry,ref=ghcr.io/__temperci_cache/<org>/<repo>/buildkit`
  - `--cache-to type=registry,ref=ghcr.io/__temperci_cache/<org>/<repo>/buildkit,mode=max`
- Plain `docker build` is rewritten to `docker buildx build --load` when `buildx` exists, so the image still lands in the local engine. If `buildx` is missing, run the original command (pull-through still helps `FROM`).

`DOCKER_BUILDKIT=1` is set in `/etc/environment`. Wrapper failures (missing env, unknown subcommand) exec the real binary unchanged.

### 6.7 Agent wiring

When `cache_listen_addr` is set (same flag as Actions cache — one intercept listener):

- Open `layout.OCICacheDir()` with `oci_cache_max_bytes`
- Bind/unbind the OCI gateway on the same guest IP as `ghacache` at job start/end
- Union `cached_repos` with OCI repo namespaces (not `public`) for sticky claim
- Purge ops (`purge_all`, `purge_repo`) also apply to the OCI store (`purge_all` clears public)

## 7. Data flow

**Public pull (`docker run postgres`):**

1. Guest GET `registry-1.docker.io/v2/library/postgres/manifests/16` (after spliced Hub token).
2. Gateway revalidates/fetches with an anonymous Hub token → `public/`.
3. Guest GET blobs by digest → public hit on job 2.

**Private GHCR pull:**

1. Anonymous origin GET → 401.
2. Retry with guest `Authorization` → store under `repos/org/repo/`.
3. Another repo on the same host does not see those blobs.

**Build:**

1. Wrapper adds registry cache flags pointing at `ghcr.io/__temperci_cache/org/repo/buildkit`.
2. BuildKit GET (import) / POST+PUT (export) against the intercept.
3. Layers live under that repo’s store. Next job on the same host + repo imports them.

**Real `docker push ghcr.io/org/app:tag`:**

1. Not a build-cache name → reverse proxy to origin with the guest’s headers.
2. Nothing stored in ocicache.

## 8. Error handling

| Case | Behavior |
|---|---|
| Unknown guest IP, public object | Allowed (anonymous / public store) |
| Unknown guest IP, private or build-cache write | 403 / 401; no store write |
| Origin down, stale public/repo entry exists | Serve stale |
| Origin down, no stale entry | Return 502/504; dockerd errors; job continues |
| Intercept/CA missing | Pulls splice to origin (slow). Build-cache export fails; job continues |
| LRU cannot free enough space | Upload/fetch fails; job continues |
| Path traversal / bad digest | 400 |
| Concurrent jobs, same repo | Safe. Blobs are content-addressed. Last tag write wins |

## 9. Config

Agent:

```toml
cache_listen_addr = "127.0.0.1:8743"   # existing; empty disables Actions + OCI intercept
cache_max_bytes = 53687091200          # Actions LRU (unchanged, default 50 GiB)
oci_cache_max_bytes = 107374182400     # OCI LRU (default 100 GiB)
```

Empty `cache_listen_addr` disables both gateways.

## 10. Dashboard

Existing `/cache` inventory grows by OCI rows:

- `oci:public` — shared public bytes/entries
- `oci:<org>/<repo>` — private + build-cache bytes for that repo

`POST /api/v1/cache/clear` `{repo}` purges Actions + OCI for that repo. `{ }` / purge-all clears Actions + OCI public + all OCI repos.

No new UI page in this slice.

## 11. Testing

Unit, no live Hub/GHCR/Firecracker required:

- `IsRegistryHost` / `IsBuildCacheName`
- Store put/get, public vs repo isolation, digest validation, LRU
- Gateway: `/v2/`, public hit/miss against a fake origin, tag revalidate, private isolation, unbound private denied, build-cache PUT not forwarded, real push proxied, stale-on-origin-failure
- Intercept `Classify` terminates registry SNI and still splices `auth.docker.io`
- Config default for `oci_cache_max_bytes`
- `ApplyCacheOps` purges OCI
- Docker wrapper argument rewrite (bash)

Existing Actions cache and assignment FIFO tests stay green.

## 12. Security

- Guest CA already trusts TemperCI; registry MITM is the same class as Actions cache MITM.
- Private layers keyed by bind-table repo, not by the token contents.
- Guest tokens are used only to fetch origin and are not written to disk.
- Build-cache refs are a reserved name prefix so they cannot overwrite a real GHCR repo the job intended to push.
- Host agent is already privileged (KVM/root). This does not widen that.
