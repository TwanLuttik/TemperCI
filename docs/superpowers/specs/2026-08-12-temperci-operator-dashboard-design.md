# TemperCI operator dashboard design

**Date:** 2026-08-12  
**Status:** Approved for implementation  
**Product:** Self-hosted operator control UI for TemperCI (Blacksmith-like ops surface, not SaaS analytics)

## 1. Goals

1. First-run **setup wizard** in the browser (GitHub App, agent token, auth).
2. **Apply** config and **restart** `temperci-control` / `temperci-agent` via allowlisted host helper.
3. Fleet visibility: **hosts/agents**, capacity (warm/busy), **live/recent jobs**.
4. Host actions: drain / reload pool (proxy to agent admin when reachable).
5. Auth: **open** or **password** with local users (email + password; invite = create account; no email send).

## 2. Non-goals (v1)

- Log search, flaky-test analytics, cost dashboards  
- Multi-tenant SaaS / billing  
- Public GitHub Marketplace app  
- GitHub OAuth login  
- Outbound invite email  

## 3. Architecture (approach 1b)

- UI and admin API live in **`temperci-control`** (embedded SPA).
- **SQLite** at `/var/lib/temperci/control.db` (users, sessions, setup metadata).
- Live fleet/jobs from existing **in-memory** agent registry + assignment store.
- **`temperci-hostctl`**: only `systemctl restart|status` for TemperCI units.
- GitHub webhooks may stay on Funnel; UI should prefer Tailscale Serve (private).

## 4. Auth

| Mode | Behavior |
|------|----------|
| `open` | No login; anyone who can reach UI is admin (lab/tailnet). |
| `password` | bcrypt passwords; session cookie; roles `admin` \| `viewer`. |

Invite: admin creates user with email + temp password (shown once). No SMTP.

## 5. Setup mode

When `setup_completed = false` (or config incomplete on first boot):

- Serve wizard + setup APIs.
- Full GitHub validation not required until apply.
- Apply writes TOML + PEM + SQLite admin, sets `setup_completed = true`, triggers restart.

## 6. API (`/api/v1`)

Setup status/apply, auth login/logout/me, hosts, jobs, users, system restart, host drain/reload.

Agent routes `/v1/agent/*` unchanged (bearer `agent_token`).

## 7. Key decisions

1. **Embedded UI in control** — single deploy unit for self-host.  
2. **SQLite** — users/sessions only in v1.  
3. **hostctl + browser reconnect** — restart without a second long-lived UI service.  
4. **Operator-owned GitHub App** — not a public TemperCI Marketplace app.  
5. **Vanilla embedded SPA** for v1 packaging (no Node required for `make build`); React can replace assets later.

## 8. PR Plan

| PR | Scope |
|----|--------|
| D1 | Config setup mode, SQLite users/sessions |
| D2 | Dashboard APIs + embedded UI wizard/fleet |
| D3 | hostctl + restart + deploy docs |

## 9. Success criteria

1. Wizard → Apply → control returns → agent registers → Hosts page shows agent.  
2. Password mode: second user; viewer cannot restart.  
3. Open mode documented as private-network only.  
4. Webhooks + agent path unchanged.
