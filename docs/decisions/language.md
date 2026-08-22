# Language choice

## Decision

**Primary language: Go (1.22+).**

All core TemperCI services are written in Go:

- Control plane
- Host agent
- Shared libraries (GitHub client wrappers, API types, config)

## Why Go

| Requirement | How Go fits |
|-------------|------------|
| Single static-ish binaries for Ubuntu hosts | Straightforward cross-compile and packaging |
| Long-running agents, systemd services | Mature standard library and ops ecosystem |
| Concurrent webhook handling + pool management | Goroutines fit control plane and agent loops |
| GitHub API / webhooks | Well-supported HTTP ecosystem; existing runner ecosystem often uses Go (e.g. Actions Runner Controller) |
| Ops friendliness for open source contributors | Large DevOps contributor pool; simple build (`go build`) |
| Predictable performance for orchestration (not the job workload itself) | Jobs run inside the guest; Go only orchestrates |

The **job workload** is not written by us in Go. Workloads run whatever the user’s workflow runs, inside the microVM via the official `actions/runner` binary (which is also Go/upstream).

## Alternatives considered

| Language | Pros | Cons | Verdict |
|----------|------|------|---------|
| **Rust** | Strong safety, great for low-level VMM glue | Higher contributor friction; slower iteration for control plane/CRUD | Optional later for hot paths only if needed |
| **TypeScript/Node** | Fast UI/API iteration | Weaker fit for privileged host agents and packaging on bare metal | UI-only later if we add a dashboard |
| **Python** | Familiar for glue scripts | Packaging and long-running privileged agents are weaker | Install scripts / small tools only if needed |

## Secondary languages / formats (allowed)

- **Shell** — install helpers, smoke scripts (keep thin).
- **YAML/JSON/TOML** — config and CI.
- **Markdown** — docs (this tree).
- **TypeScript/React** — optional admin UI **after** the runner path works; not required for MVP CLI/API.
- **Rust** — only if a specific VMM integration demands it; must justify in a decision doc.

## Tooling expectations

- Module path: `github.com/TwanLuttik/TemperCI` (see [module-path.md](module-path.md)).
- `go test ./...` for unit tests.
- `golangci-lint` (or equivalent) once the module exists.
- Pin Go version in `go.mod` and CI.

## What this decision does not cover

- Guest OS images (Ubuntu rootfs / runner image pipeline) — not a “language” choice.
- Hypervisor selection (Firecracker vs Cloud Hypervisor) — separate spike; both are driven from Go via their APIs/CLIs.
