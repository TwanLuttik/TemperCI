# TemperCI Operator Dashboard Implementation Plan

> **For agentic workers:** Implement task-by-task. Steps use checkbox syntax.

**Goal:** Ship operator dashboard (setup wizard, auth, fleet view, hostctl restart) embedded in temperci-control.

**Architecture:** Approach 1b — SQLite users/sessions, SPA in `internal/webui`, APIs under `/api/v1`, `temperci-hostctl` for systemctl.

**Tech Stack:** Go 1.22+, modernc.org/sqlite, golang.org/x/crypto/bcrypt, embedded HTML SPA

## Global Constraints

- Do not break existing agent `/v1/agent/*` or webhook paths.
- `setup_completed = true` keeps current production validation.
- No Node required for `make build`.

---

### Task 1: Design + SQLite store

- [x] Spec under `docs/superpowers/specs/2026-08-12-temperci-operator-dashboard-design.md`
- [x] `internal/store` users/sessions/setup meta + tests

### Task 2: Config setup mode

- [x] `auth_mode`, `sqlite_path`, `setup_completed`, `hostctl_path`, `data_dir`
- [x] Validate GitHub fields only when setup completed

### Task 3: Dashboard APIs + UI + hostctl

- [x] `internal/control/dashboard.go`
- [x] `internal/webui` SPA
- [x] `cmd/temperci-hostctl`
- [x] Wire `cmd/temperci-control`

### Task 4: Deploy docs

- [x] Example TOML fields
- [ ] Operator doc page (optional follow-up)
