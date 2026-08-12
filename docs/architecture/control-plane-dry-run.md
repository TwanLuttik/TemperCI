# Control plane dry-run: GitHub App + webhooks + JIT

This guide walks through Phase 2 of TemperCI: receive a `workflow_job` webhook and mint an org-level JIT runner config. No agent or microVM is required yet.

## Prerequisites

- A GitHub organization you can install apps on (use a throwaway org for testing)
- A machine reachable by GitHub webhooks (or [smee.io](https://smee.io) / `ngrok` for local dev)
- Go 1.22+ and a built `temperci-control` binary (`make build`)

## 1. Create a GitHub App (organization-owned)

Prefer creating the App **under the organization** so Install App targets that org by default:

```text
https://github.com/organizations/<ORG_LOGIN>/settings/apps/new
```

Example: `https://github.com/organizations/coatcheckapp/settings/apps/new`

(User-owned apps often only install on the personal account and never receive `workflow_job` for org repos. The org **Installed GitHub Apps** page has no “Add app” button — create at the URL above, then Install.)

1. As an **org owner**, open the org apps page (or the `…/apps/new` URL above).
2. Suggested settings:
   - **Webhook URL:** `https://<your-host>/webhooks/github` (or your tunnel / Funnel URL)
   - **Webhook secret:** generate a long random string; save it for config
   - **Repository permissions → Actions:** Read-only (unlocks **Workflow job** in the event list)
   - **Organization permissions:**
     - **Self-hosted runners:** Read and write
   - **Subscribe to events:** **Workflow job**
3. Create the app and note the **App ID**.
4. Generate and download a **private key** (`.pem`). Store it outside git, e.g. `/etc/temperci/github-app.pem` (`chmod 600`).
5. **Install App** on the **organization** (not only your user). Include all repos that use `runs-on: temperci-…`, or select those repos explicitly.
6. After a job runs, App → **Recent Deliveries** should show `workflow_job` (not only `ping`).

The dashboard setup wizard and Settings page include the same operator guide.

## 2. Configure TemperCI control

```bash
sudo mkdir -p /etc/temperci
sudo cp deploy/control.example.toml /etc/temperci/control.toml
sudo cp /path/to/app-private-key.pem /etc/temperci/github-app.pem
sudo chmod 600 /etc/temperci/control.toml /etc/temperci/github-app.pem
```

Edit `/etc/temperci/control.toml`:

```toml
listen_addr = "0.0.0.0:8080"
github_app_id = 123456
github_app_private_key_path = "/etc/temperci/github-app.pem"
github_webhook_secret = "the-secret-from-app-settings"
github_org = "your-test-org"
# runner_group_id = 1
```

`runner_group_id` must be an org runner group id that the app can register runners into. The Default group is often `1`; confirm under **Org → Settings → Actions → Runner groups**.

## 3. Run the control plane

```bash
./bin/temperci-control -config /etc/temperci/control.toml
```

Health check:

```bash
curl -sS http://127.0.0.1:8080/healthz
# ok
```

If developing locally, start a tunnel and point the app webhook URL at  
`https://<tunnel>/webhooks/github`.

## 4. Trigger a queued job

In a repo under the org, add a workflow:

```yaml
name: temperci-smoke
on: workflow_dispatch
jobs:
  smoke:
    runs-on: temperci-4vcpu-ubuntu-2404
    steps:
      - run: echo "hello from queue"
```

Dispatch the workflow. The job will stay **queued** (no runner will pick it up yet — that is expected in Phase 2).

## 5. What success looks like

Control plane logs (JSON on stderr) should include something like:

```text
minted JIT config job_id=... run_id=... org=... labels=[temperci-4vcpu-ubuntu-2404] runner_id=...
```

Webhook HTTP response body: `{"ok":true,"minted":true}`.

GitHub org → **Settings → Actions → Runners** may briefly show the JIT runner name `temperci-job-<job_id>` until the one-shot registration expires or is consumed.

## 6. Negative checks

| Case | Expected |
|------|----------|
| Wrong webhook secret | HTTP 401, no mint, no assignment side effects |
| `runs-on: ubuntu-latest` | HTTP 200, `ignored` / `labels_not_owned`, no JIT call |
| App ping event | HTTP 200 `{"ok":true}` |

## Endpoints

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/healthz` | Liveness |
| `POST` | `/webhooks/github` | GitHub App webhooks (also `/webhook/github`) |

## Security notes

- Treat `encoded_jit_config` as a secret; TemperCI does not log it.
- Keep the app private key and webhook secret mode `600` and out of version control.
- Phase 2 stores assignments in memory only; restart clears state.
