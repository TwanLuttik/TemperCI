# Operator dashboard

TemperCI control plane serves a **Vite + React** SPA at `/` (same port as webhooks, default `8080`). The UI is built into `internal/webui/dist` and embedded into `temperci-control` at compile time.

## Build

Requires **Go 1.22+** and **Node.js 20+** (npm).

```bash
make build          # npm build (web/) + Go binaries
make build-ui       # Vite only → internal/webui/dist
make build-go       # Go only (needs prior build-ui)
```

Local UI dev (proxies API to control on :8080):

```bash
# terminal 1
./bin/temperci-control -config /etc/temperci/control.toml
# terminal 2
cd web && npm install && npm run dev
# open http://127.0.0.1:5173
```

## What you get

- **Setup wizard** when config is incomplete (`setup_completed = false` and GitHub fields empty)
- **Overview / Hosts / Jobs** from live agent registry + assignment store
- **Users** when `auth_mode = "password"` (create accounts; no email sending)
- **Settings** — view + edit control.toml fields (GitHub App, webhook secret, agent token, paths); secrets blank = keep current
- **Settings → Restart** via `temperci-hostctl` when installed (`Save & restart` after config changes)

## Auth

| `auth_mode` | Behavior |
|-------------|----------|
| `open` | No login. Use only on private networks (Tailscale Serve), not public Funnel. |
| `password` | Local email + password users in SQLite (`sqlite_path`). |

Prefer **Funnel for GitHub webhooks only**; put the **UI on Tailscale Serve** (tailnet) when using open mode.

## Install / upgrade

```bash
make build   # or: GOOS=linux GOARCH=amd64 make build
install -m 0755 bin/temperci-control bin/temperci-agent bin/temperci-hostctl /usr/local/bin/
systemctl restart temperci-control
```

Open `http://<control-host>:8080/` (or your Serve URL).

Existing installs that already have GitHub App fields filled keep fleet mode even without `setup_completed = true`.

## hostctl

```bash
temperci-hostctl restart all
temperci-hostctl status control
```

Without hostctl, Apply still writes config; restart units manually with `systemctl`.

## GitHub App (organization-owned)

TemperCI does **not** use a public Marketplace app. Create a **private GitHub App under the organization**:

```text
https://github.com/organizations/<ORG_LOGIN>/settings/apps/new
```

Example: `https://github.com/organizations/coatcheckapp/settings/apps/new`

Required:

| Setting | Value |
|---------|--------|
| Webhook URL | `https://<funnel-or-public-host>/webhooks/github` |
| Webhook secret | long random; same as control config |
| Repo **Actions** | Read-only |
| Org **Self-hosted runners** | Read and write |
| Subscribe | **Workflow job** |

Then **Install App** on that org (include every repo using `runs-on: temperci-…`).

**Why org-owned:** A user-owned App often only installs on the personal account. Org repos then never send `workflow_job` webhooks, and jobs stay on “Waiting for a runner…”. Org **Settings → Installed GitHub Apps** has no “Add app” button — create under `/organizations/<ORG>/settings/apps`, then Install.

Paste **App ID**, **PEM**, **webhook secret**, and **org login** into the dashboard setup wizard or Settings.
