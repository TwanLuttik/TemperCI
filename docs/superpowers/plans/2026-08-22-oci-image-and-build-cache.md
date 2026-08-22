# OCI Image and Build Cache Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cache Docker Hub / GHCR pulls and BuildKit layers on the agent host without changing workflow YAML or reusing a guest disk.

**Architecture:** Extend the existing SNI intercept to terminate `registry-1.docker.io` and `ghcr.io`. A new `internal/ocicache` store + Registry API v2 gateway pull-throughs public layers into a shared pool and private/build layers into `org/repo`. A guest `/usr/local/bin/docker` wrapper adds BuildKit `type=registry` cache flags aimed at `ghcr.io/__temperci_cache/<org>/<repo>/buildkit`.

**Tech Stack:** Go 1.22+, existing `internal/ghacache` intercept/CA, filesystem LRU, bash guest hook, `httptest` for origin.

## Global Constraints

- Do not reuse a guest disk after a job.
- Registries: Docker Hub (`registry-1.docker.io`) and `ghcr.io` only. Splice `auth.docker.io`.
- Public (anonymous origin fetch) shared; credentialed fetch + `__temperci_cache/` writes per `org/repo`.
- Real `docker push` to any other name is reverse-proxied, never absorbed.
- Pull failure falls through to origin (or stale). Build-cache export failure must not fail the job.
- Separate LRU at `data_dir/ocicache/`, default 100 GiB (`oci_cache_max_bytes`).
- Same `cache_listen_addr` enables Actions + OCI intercept. Empty disables both.
- No live Docker Hub, GHCR, or Firecracker required for tests.
- TDD: failing test first for every behavior.

---

### Task 1: Registry SNI helpers

**Files:**
- Create: `internal/ocicache/sni.go`
- Test: `internal/ocicache/sni_test.go`

**Interfaces:**
- Produces: `func IsRegistryHost(host string) bool`, `func IsBuildCacheName(name string) bool`

- [ ] **Step 1: Write the failing test**

```go
package ocicache

import "testing"

func TestIsRegistryHost(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"registry-1.docker.io", true},
		{"ghcr.io", true},
		{"GHCR.IO", true},
		{"ghcr.io:443", true},
		{"auth.docker.io", false},
		{"registry-1.docker.io.evil.example", false},
		{"api.github.com", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := IsRegistryHost(tc.host); got != tc.want {
			t.Fatalf("IsRegistryHost(%q)=%v want %v", tc.host, got, tc.want)
		}
	}
}

func TestIsBuildCacheName(t *testing.T) {
	if !IsBuildCacheName("__temperci_cache/acme/app/buildkit") {
		t.Fatal("expected build-cache name")
	}
	if IsBuildCacheName("library/postgres") || IsBuildCacheName("acme/app") {
		t.Fatal("real image must not be build-cache")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ocicache/ -count=1 -run 'TestIsRegistryHost|TestIsBuildCacheName'`
Expected: FAIL — `undefined: IsRegistryHost`

- [ ] **Step 3: Write minimal implementation**

Normalize host (lowercase, strip port). Match exact `registry-1.docker.io` or `ghcr.io`. Build-cache name is prefix `__temperci_cache/`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ocicache/ -count=1 -run 'TestIsRegistryHost|TestIsBuildCacheName'`
Expected: PASS

---

### Task 2: OCI blob/manifest store

**Files:**
- Create: `internal/ocicache/store.go`
- Test: `internal/ocicache/store_test.go`

**Interfaces:**
- Produces: `Open(dir string, maxBytes int64) (*Store, error)`, `PutBlob`, `GetBlob`, `FindBlob`, `PutManifest`, `FindManifest`, `Usage`, `DeleteRepo`, `DeleteAll`, `Repos`, `ScopePublic`, `ScopeRepo`

- [ ] **Step 1: Write failing tests** for public put/get, repo isolation (repo A cannot `FindBlob` repo B’s private digest), digest validation, LRU evicts oldest ready blob, `DeleteRepo` / `DeleteAll`.

- [ ] **Step 2: Run tests — expect FAIL** (`undefined: Open`)

- [ ] **Step 3: Implement filesystem store** under `blobs/public|repos/<repo>/sha256/<hex>` and `manifests/...`. Default max 100 GiB. Sanitize repo as `org/name`. Reject `..` and non-sha256 digests.

- [ ] **Step 4: `go test ./internal/ocicache/ -count=1` PASS**

---

### Task 3: Registry gateway + fake origin

**Files:**
- Create: `internal/ocicache/gateway.go`
- Create: `internal/ocicache/origin.go`
- Test: `internal/ocicache/gateway_test.go`

**Interfaces:**
- Consumes: Store APIs from Task 2, `IsBuildCacheName`, `IsRegistryHost`
- Produces: `NewGateway(store *Store) *Gateway`, `BindRemote`, `UnbindRemote`, `Handler()`, `Origin http.RoundTripper`, `TokenSource func(host, name string) (string, error)`

- [ ] **Step 1: Write failing httptest tests**
  - `GET /v2/` → 200 + distribution API header
  - Public miss fetches origin without guest Authorization, stores public, second GET is a hit (origin not called)
  - Origin 401 + guest Authorization + bind → stored under repo; other bound repo misses
  - Unbound guest + origin 401 → not cached
  - Build-cache PUT manifest + blob upload stored locally; Origin not invoked
  - PUT `library/app` manifest is reverse-proxied to origin
  - Origin down + stale public manifest → serve stale

- [ ] **Step 2: Run — expect FAIL**

- [ ] **Step 3: Implement gateway** (path parse, classification, upload sessions, reverse proxy for non-build-cache writes)

- [ ] **Step 4: `go test ./internal/ocicache/ -count=1` PASS**

---

### Task 4: Intercept Classify + mux

**Files:**
- Modify: `internal/ghacache/intercept.go` — add `Classify func(string) bool`
- Modify: `internal/ghacache/sni_test.go` — Actions hosts unchanged
- Create: `internal/ocicache/mux.go`
- Test: `internal/ocicache/mux_test.go`
- Modify: `internal/ghacache/intercept_test.go` — Classify terminates `ghcr.io`

**Interfaces:**
- Produces: `func Mux(oci, fallback http.Handler) http.Handler`

- [ ] Tests first: mux routes `Host: ghcr.io` to OCI; Actions host to fallback. Intercept with `Classify: ShouldTerminate` terminates `ghcr.io` and still splices `auth.docker.io`.

- [ ] Implement `Classify` (nil → `ShouldIntercept`). `ShouldTerminate(sni) = ghacache.ShouldIntercept(sni) || IsRegistryHost(sni)` in `internal/ocicache/sni.go`.

---

### Task 5: Config, layout, agent wire, purge

**Files:**
- Modify: `internal/config/config.go` — `OCICacheMaxBytes int64 \`toml:"oci_cache_max_bytes"\`` default `100 << 30`
- Modify: `internal/config/config_test.go`
- Modify: `internal/vmm/layout.go` — `OCICacheDir() string` → `root/ocicache`
- Modify: `internal/agent/worker.go` — `OCI *ocicache.Gateway`; bind/unbind; union repos
- Modify: `internal/agent/cacheapply.go` — purge OCI
- Modify: `internal/agent/cacheapply_test.go`
- Modify: `cmd/temperci-agent/main.go` — open store, mux handler, `Classify: ocicache.ShouldTerminate`
- Modify: `deploy/agent.example.toml`

- [ ] Tests first for default `oci_cache_max_bytes` and purge-repo clearing OCI repo blobs.

- [ ] Wire agent only when `cache_listen_addr != ""`.

---

### Task 6: Guest docker wrapper

**Files:**
- Create: `deploy/ubuntu/docker-cache-wrapper.sh`
- Create: `deploy/ubuntu/docker-cache-wrapper_test.sh`
- Modify: `deploy/ubuntu/guest-packages.sh` — install wrapper to `/usr/local/bin/docker`, set `DOCKER_BUILDKIT=1`
- Modify: `deploy/ubuntu/guest-toolchain.md` — document wrapper

**Interfaces:**
- Produces: `temperci_docker_rewrite` prints rewritten argv (or empty to mean “exec unchanged”)

- [ ] **Step 1: Write bash tests**
  - `GITHUB_REPOSITORY=acme/app` + `build -t x .` → `buildx build --load --cache-from ... --cache-to ... -t x .`
  - Existing `--cache-to` → unchanged
  - `run postgres` → unchanged
  - Empty `GITHUB_REPOSITORY` → unchanged

- [ ] **Step 2: Run `bash deploy/ubuntu/docker-cache-wrapper_test.sh` — FAIL**

- [ ] **Step 3: Implement rewrite + install from `guest-packages.sh`**

- [ ] **Step 4: `bash -n` both scripts + test script PASS**

---

### Task 7: Full package test

- [ ] `go test ./internal/ocicache/ ./internal/ghacache/ ./internal/agent/ ./internal/config/ ./internal/vmm/ -count=1`
- [ ] `bash -n deploy/ubuntu/docker-cache-wrapper.sh deploy/ubuntu/guest-packages.sh`
- [ ] `bash deploy/ubuntu/docker-cache-wrapper_test.sh`
