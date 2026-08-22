# Architecture overview

TemperCI is a **self-hostable GitHub Actions runner platform**. It uses the same integration model as managed runner providers (for example [Blacksmith](https://blacksmith.sh)): jobs stay in GitHub Actions, and compute runs on infrastructure you control.

## Design principles

1. **Stay on GitHub’s runner protocol** — do not invent a parallel CI system. Use the official [actions/runner](https://github.com/actions/runner) binary and GitHub’s self-hosted runner APIs.
2. **Ephemeral by default** — one job per microVM; destroy after every job.
3. **Warm pool for latency** — keep ready microVMs so webhook handling does not pay cold-boot cost.
4. **Hard cleanup for self-host** — no leftover guest disks, overlays, sockets, or scratch dirs after teardown.
5. **Single-operator install** — one control plane + one or more host agents on Ubuntu with KVM.

## How this differs from “classic” self-hosted runners

| Classic self-hosted | TemperCI |
|---------------------|---------|
| Manually run `config.sh` on a VM | GitHub App + API mints **JIT** runner config |
| Long-lived runner process polls for jobs | Runner identity exists only for **one job** |
| Shared dirty machine over time | Fresh microVM every job |
| You size a static pool yourself | Control plane + agent manage pool and labels |
| Labels set at config time | Labels chosen per JIT registration to match `runs-on` |

TemperCI **is** self-hosted runners under the hood. The product is the orchestration, isolation, warm pool, and cleanup around them.

## Components

```text
┌──────────────────────────────────────────────────────────────┐
│ GitHub                                                       │
│  · Actions control plane (workflows, secrets, job queue)     │
│  · TemperCI GitHub App (webhooks, JIT runner permissions)     │
└────────────────────────────┬─────────────────────────────────┘
                             │ webhooks + REST (JIT config)
┌────────────────────────────▼─────────────────────────────────┐
│ TemperCI control plane (self-hosted)                          │
│  · App webhook receiver                                      │
│  · Label matching / scheduling                               │
│  · Mint generate-jitconfig for org (or repo)                 │
│  · Job assignment to host agents                             │
│  · Optional: basic status API / admin UI (later)             │
└────────────────────────────┬─────────────────────────────────┘
                             │ assign job + JIT payload
┌────────────────────────────▼─────────────────────────────────┐
│ Host agent (per Ubuntu/KVM machine)                          │
│  · Warm microVM pool manager                                 │
│  · Bind JIT → start official runner in guest                 │
│  · Teardown + orphan sweeper                                 │
│  · Pool replenish                                            │
└────────────────────────────┬─────────────────────────────────┘
                             │ KVM microVMs
                    ┌────────┴────────┐
                    ▼                 ▼
               [warm VM]         [busy VM → destroy]
```

### Control plane

Responsible for talking to GitHub and deciding **which host** should run a job.

- Receives `workflow_job` webhooks (`queued`, `in_progress`, `completed`).
- Ignores jobs whose `runs-on` labels are not TemperCI-owned.
- Calls GitHub’s **just-in-time runner** API to create a single-use runner config with the required labels.
- Pushes a job payload (JIT config + metadata) to a host agent with capacity.
- Tracks assignment state for retries and reconciliation.

### Host agent

Responsible for **compute isolation and lifecycle** on one machine.

- Maintains a configurable warm pool of pre-booted microVMs (no secrets, no JIT identity until bind).
- On assignment: bind payload → inject JIT → start runner → mark busy.
- On completion or failure: destroy VM, delete host scratch, confirm cleanup, replenish pool.
- On agent start: orphan sweep (destroy leftover VMs/files from crashes).

### MicroVM guest

- Root filesystem based on (or compatible with) GitHub’s [runner-images](https://github.com/actions/runner-images) tooling/process for Linux Ubuntu targets in MVP.
- Contains the official `actions/runner` binary.
- Network access as required for Actions (checkout, caches, package registries).
- No durable identity between jobs.

## End-to-end request path

1. Operator installs TemperCI and the GitHub App on a GitHub organization.
2. Workflow uses a TemperCI label:

   ```yaml
   jobs:
     build:
       runs-on: temperci-4vcpu-ubuntu-2404
       steps:
         - uses: actions/checkout@v4
   ```

3. GitHub queues the job and emits a `workflow_job` webhook with `action: queued` and the requested labels.
4. Control plane matches labels, mints JIT config via GitHub REST API.
5. Control plane assigns the job to a host agent that has a warm VM (or can create one).
6. Host agent binds the warm VM, starts the runner with the JIT config.
7. Official runner adopts the single job and executes steps; secrets stay on the runner path GitHub already uses for self-hosted runners.
8. Job finishes (success, failure, cancel, timeout) → agent destroys the microVM and host-side files → agent boots a replacement warm VM.

## What TemperCI does **not** do (MVP)

- Replace GitHub Actions with another CI product.
- Provide multi-tenant SaaS billing.
- Require long-lived registration tokens on hosts.
- Reuse a guest filesystem across jobs.
- Ship advanced product features first (Docker layer cache product, full observability suite). Those can come after a solid runner path.

## Related docs

- [job-lifecycle.md](job-lifecycle.md) — warm pool and teardown details
- [install-targets.md](install-targets.md) — Ubuntu + Firecracker deploy model
- [../decisions/language.md](../decisions/language.md)
- [../decisions/repository-structure.md](../decisions/repository-structure.md)
