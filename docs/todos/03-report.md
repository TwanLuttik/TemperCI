# Todo 3 report — Persistence / HA

**Status:** DONE

Minted JIT assignments now survive a `temperci-control` restart. The in-memory `AssignmentStore` API is unchanged; SQLite is an optional persister. Nil persister keeps current memory-only behavior so existing control tests pass without a DB. Multi-node HA was not built. EncodedJITConfig is never logged.

## Files changed

- `internal/store/store.go` — `assignments` table + indexes in `migrate()`
- `internal/store/assignments.go` — `AssignmentRow`, Upsert/Get/List/Delete, `PruneFinished`
- `internal/store/assignments_test.go` — round-trip, JIT clear on finish/fail, prune/delete
- `internal/control/assignment.go` — `AssignmentPersister`, load-on-set, persist after each mutation, `PruneFinished`
- `internal/control/persist.go` — `store.Store` → `AssignmentPersister` adapter
- `internal/control/persist_test.go` — restart reload + claim still has JIT; finish/fail clears JIT in DB
- `internal/control/reconcile.go` — prune finished/failed older than 7 days each tick
- `internal/control/reconcile_test.go` — prune keeps minted/assigned and recent finished
- `cmd/temperci-control/main.go` — open SQLite, attach persister, load before serving
- `docs/todos/03-report.md` — this report

Agent / VMM / guest-image files were not changed. No git commit.

## How restart reload works

1. Control opens `cfg.SQLitePath` (already used for dashboard users/sessions).
2. `NewStorePersister(db)` adapts `store.Store` to `AssignmentPersister`.
3. `NewAssignmentStoreWithPersister` calls `LoadAll`, rebuilds `byID`, and rebuilds the minted FIFO (`pending`) ordered by `CreatedAt` then `job_id`.
4. Every `Put`, `ClaimNext`, `MarkStarted`, `MarkFinished`, `MarkFailed`, and `RequeueAssigned` writes the in-memory copy to SQLite after the mutation.
5. `UpsertAssignment` forces `encoded_jit_config` empty when status is `finished` or `failed` (same as memory).
6. Agents claim as before: after restart, minted jobs are still in `pending` with JIT, so `ClaimNext` returns the encoded config.
7. Reconciler tick also `PruneFinished(7d)` in memory and DB. Minted / assigned / started are never pruned.

## Tests

```
go test ./internal/control/ ./internal/store/
```

Result: **ok** (both packages).
