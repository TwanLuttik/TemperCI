# Todo 3 — Persistence / HA

**Status area:** Persistence / HA — job queue is in-memory  
**Goal:** Survive `temperci-control` restarts without losing minted JIT assignments. Multi-node control HA is still out of scope.

## Context

Today (`cmd/temperci-control/main.go`):

```go
storeMem := control.NewAssignmentStore()
```

SQLite at `cfg.SQLitePath` (`internal/store`) only holds dashboard users/sessions/meta. A control restart after a 200 webhook response drops the job; GitHub will not re-send `queued`.

## Design (single-node durability)

Keep the `AssignmentStore` API used by handler/claim/reconcile/dashboard. Persist every mutation to SQLite. Load on startup.

Do **not** build leader election or multi-control replication.

### Schema

Add to `internal/store` migrate():

```sql
CREATE TABLE IF NOT EXISTS assignments (
  job_id INTEGER PRIMARY KEY,
  run_id INTEGER NOT NULL DEFAULT 0,
  org TEXT NOT NULL,
  repo_full_name TEXT NOT NULL DEFAULT '',
  labels_json TEXT NOT NULL DEFAULT '[]',
  installation_id INTEGER NOT NULL DEFAULT 0,
  runner_name TEXT NOT NULL DEFAULT '',
  runner_id INTEGER NOT NULL DEFAULT 0,
  encoded_jit_config TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  assigned_at TEXT NOT NULL DEFAULT '',
  started_at TEXT NOT NULL DEFAULT '',
  finished_at TEXT NOT NULL DEFAULT '',
  assigned_agent_id TEXT NOT NULL DEFAULT '',
  vm_id TEXT NOT NULL DEFAULT '',
  warm_bind INTEGER NOT NULL DEFAULT 0,
  outcome TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_assignments_status ON assignments(status);
CREATE INDEX IF NOT EXISTS idx_assignments_created ON assignments(created_at);
```

`encoded_jit_config` is secret. DB file already lives under `/var/lib/temperci` — do not log the column. Clear it in SQL when status becomes `finished` or `failed` (same as memory store).

### Store API

Add in `internal/store` (new file `assignments.go`):

```go
func (s *Store) UpsertAssignment(a AssignmentRow) error
func (s *Store) GetAssignment(jobID int64) (*AssignmentRow, error)
func (s *Store) ListAssignments() ([]AssignmentRow, error)
func (s *Store) DeleteAssignment(jobID int64) error // optional
```

`AssignmentRow` can live in `store` to avoid an import cycle. Map to `control.Assignment` in the control package.

### AssignmentStore hook

Add an optional persister:

```go
type AssignmentPersister interface {
    Persist(a *Assignment) error
    LoadAll() ([]*Assignment, error)
}
```

Wire it on `AssignmentStore` (field + `SetPersister` or `NewAssignmentStoreWithPersister`). Every `Put`, `ClaimNext`, `MarkStarted`, `MarkFinished`, `MarkFailed`, `RequeueAssigned` must persist **after** the in-memory mutation (still under the same lock, or persist the copy immediately after unlock — if you persist after unlock, persist the copy you just wrote).

On `New` + persister: `LoadAll`, rebuild `byID` and `pending` FIFO for `status == minted` ordered by `CreatedAt` ascending.

Adapter in `internal/control` (e.g. `persist.go`) maps `store.Store` → `AssignmentPersister`.

### Startup

`cmd/temperci-control/main.go`: create SQLite, construct persister, `NewAssignmentStore`, load, then handler/server as today.

### Prune

In reconciler (or a small method on the store): delete or leave finished rows older than 7 days. Prefer a `PruneFinished(olderThan time.Duration)` called from the existing reconciler tick. Do not prune `minted`/`assigned`/`started`.

### Agent registry

Leave in-memory. Agents heartbeat every 2s.

## Files you may change

- `internal/store/store.go` (migrate only)
- Create: `internal/store/assignments.go`, `internal/store/assignments_test.go`
- `internal/control/assignment.go` (+ existing `assignment_test.go`)
- Create: `internal/control/persist.go` (adapter)
- `internal/control/reconcile.go` (optional prune)
- `cmd/temperci-control/main.go`
- `internal/control/handler_test.go`, `agent_api_test.go`, `server_test.go` — keep working (nil persister = current memory-only behavior)

Do **not** change agent/VMM/guest-image files.

## Tests

- Round-trip: Put minted assignment with JIT → new store + LoadAll → ClaimNext still has JIT
- MarkFinished clears JIT in DB
- Existing `go test ./internal/control/ ./internal/store/` stay green (nil persister)

## Done when

- [ ] Control restart reloads minted jobs and agents can still claim them
- [ ] JIT is persisted only while the job is not finished/failed
- [ ] Tests cover load + claim after “restart”
- [ ] Do **not** git commit
