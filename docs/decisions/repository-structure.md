# Repository structure

## Decision

Kokanee is a **Go monorepo** with clear binaries, internal packages, docs, and deploy assets. No application code lives at the repo root except module metadata and top-level README.

## Target layout

```text
kokanee/
├── README.md
├── LICENSE
├── go.mod
├── go.sum
├── Makefile                 # common targets: build, test, lint
├── .github/workflows/       # CI for Kokanee itself
│
├── cmd/
│   ├── kokanee-control/     # control plane binary
│   │   └── main.go
│   └── kokanee-agent/       # host agent binary
│       └── main.go
│
├── internal/                # not importable by external modules
│   ├── control/             # control-plane domain (webhooks, schedule, JIT)
│   ├── agent/               # agent domain (pool, bind, teardown)
│   ├── github/              # GitHub App, webhooks, JIT client wrappers
│   ├── vmm/                 # microVM lifecycle interface + impls
│   ├── cleanup/             # host scratch + orphan sweep
│   ├── config/              # shared config loading
│   ├── api/                 # control↔agent protocol types
│   └── logging/             # structured logging helpers
│
├── api/                     # optional: OpenAPI / protobuf if we expose HTTP
│   └── openapi.yaml
│
├── deploy/
│   ├── systemd/             # unit files for control + agent
│   ├── ubuntu/              # bare Ubuntu install notes/scripts
│   └── proxmox/             # Proxmox install notes/scripts
│
├── scripts/                 # dev helpers (not production entrypoints)
│
├── testdata/                # fixtures (webhook payloads, configs)
│
└── docs/
    ├── README.md
    ├── architecture/
    ├── decisions/
    └── superpowers/
        ├── specs/
        └── plans/
```

## Package boundaries

| Path | Responsibility | May depend on |
|------|----------------|---------------|
| `cmd/*` | `main` only: flags, wire deps, run | `internal/*` |
| `internal/control` | webhook handling, scheduling, JIT mint orchestration | `internal/github`, `internal/api`, `internal/config` |
| `internal/agent` | pool state machine, bind, job watch | `internal/vmm`, `internal/cleanup`, `internal/api` |
| `internal/vmm` | create/boot/destroy VM; **interface first** | OS/exec, hypervisor SDKs |
| `internal/cleanup` | delete scratch, reconcile orphans | filesystem, `internal/vmm` for destroy |
| `internal/github` | App auth, webhook verify, REST | HTTP client only — no VMM |
| `internal/api` | shared wire types control↔agent | stdlib / small DTOs |

Rules:

1. **Hypervisor details stay behind `internal/vmm`.** Agent code talks to an interface (`Create`, `Boot`, `Destroy`, `Exists`).
2. **GitHub details stay behind `internal/github`.** Control plane does not scatter raw REST calls.
3. **No shared “utils” junk drawer.** If something is shared, name it by domain.
4. **Docs and deploy assets are first-class** — install paths are part of the product.

## Binaries

| Binary | Runs where | Role |
|--------|------------|------|
| `kokanee-control` | Lab box, small VM, or container | GitHub webhooks, JIT, assignment |
| `kokanee-agent` | Each job host (Ubuntu/Proxmox) | Warm pool, bind, teardown |

MVP may allow both on one machine for single-node installs.

## Config files (runtime, not in git)

Examples of operator-local config (paths finalised in implementation):

- `/etc/kokanee/control.toml` — App ID, private key path, webhook secret, listen addr
- `/etc/kokanee/agent.toml` — control plane URL, pool sizes, image paths, resource limits

Ship **example** configs under `deploy/`.

## What we intentionally defer

| Item | When |
|------|------|
| Public Go module stability | After first tagged release |
| Helm charts / Kubernetes install | Post-MVP |
| Admin web UI package (`web/`) | After runner path works |
| Multi-language polyglot packages | Avoid unless required |

## Naming

- Project: **Kokanee**
- CLI/binaries: `kokanee-control`, `kokanee-agent`
- Default runner label prefix: `kokanee-` (e.g. `kokanee-4vcpu-ubuntu-2404`)
- Internal code names stay boring and domain-oriented
