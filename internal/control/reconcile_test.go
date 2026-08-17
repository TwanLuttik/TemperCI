package control

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

type mockRunnerDeleter struct {
	mu      sync.Mutex
	calls   []string
	failIDs map[int64]bool
}

func (m *mockRunnerDeleter) DeleteOrgRunner(_ context.Context, org string, runnerID, installationID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, fmt.Sprintf("%s/%d/%d", org, runnerID, installationID))
	if m.failIDs[runnerID] {
		return fmt.Errorf("delete failed")
	}
	return nil
}

func TestReconciler_StuckAssignmentForceFinish(t *testing.T) {
	store := NewAssignmentStore()
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	store.Put(&Assignment{
		JobID:            50,
		Org:              "acme",
		RunnerID:         900,
		InstallationID:   11,
		EncodedJITConfig: "jit-must-clear",
		Status:           AssignmentStarted,
		AssignedAgentID:  "host-a",
		AssignedAt:       now.Add(-2 * time.Hour),
		StartedAt:        now.Add(-2 * time.Hour),
		VMID:             "vm-stuck",
	})
	// Fresh assignment should not be touched.
	store.Put(&Assignment{
		JobID:           51,
		Org:             "acme",
		RunnerID:        901,
		Status:          AssignmentStarted,
		AssignedAgentID: "host-a",
		AssignedAt:      now.Add(-1 * time.Minute),
		StartedAt:       now.Add(-1 * time.Minute),
	})

	del := &mockRunnerDeleter{}
	rec := &Reconciler{
		Store:      store,
		Delete:     del,
		StuckAfter: time.Hour,
		now:        func() time.Time { return now },
	}
	n := rec.ReconcileOnce(context.Background())
	if n != 1 {
		t.Fatalf("reconciled = %d want 1", n)
	}
	a := store.Get(50)
	if a.Status != AssignmentFinished || a.Outcome != "stuck" {
		t.Fatalf("stuck job = %+v", a)
	}
	if a.EncodedJITConfig != "" {
		t.Fatal("JIT must be cleared on stuck finish")
	}
	if store.Get(51).Status != AssignmentStarted {
		t.Fatal("fresh job should remain started")
	}
	del.mu.Lock()
	defer del.mu.Unlock()
	if len(del.calls) != 1 || del.calls[0] != "acme/900/11" {
		t.Fatalf("delete calls = %v", del.calls)
	}
}

func TestReconciler_StaleMinted(t *testing.T) {
	store := NewAssignmentStore()
	now := time.Now().UTC()
	store.Put(&Assignment{
		JobID:            60,
		Org:              "acme",
		RunnerID:         700,
		InstallationID:   1,
		EncodedJITConfig: "jit-stale",
		Status:           AssignmentMinted,
		CreatedAt:        now.Add(-3 * time.Hour),
	})
	if store.PendingLen() != 1 {
		t.Fatalf("pending = %d", store.PendingLen())
	}
	del := &mockRunnerDeleter{}
	rec := &Reconciler{
		Store:            store,
		Delete:           del,
		StaleMintedAfter: 2 * time.Hour,
		now:              func() time.Time { return now },
	}
	n := rec.ReconcileOnce(context.Background())
	if n != 1 {
		t.Fatalf("n = %d", n)
	}
	a := store.Get(60)
	if a.Status != AssignmentFailed {
		t.Fatalf("status = %s", a.Status)
	}
	if a.EncodedJITConfig != "" {
		t.Fatal("jit should clear on fail")
	}
	if store.PendingLen() != 0 {
		t.Fatalf("pending after expire = %d", store.PendingLen())
	}
	if len(del.calls) != 1 {
		t.Fatalf("delete calls = %v", del.calls)
	}
}

func TestReconciler_PruneFinished(t *testing.T) {
	s := NewAssignmentStore()
	now := time.Now().UTC()
	s.Put(&Assignment{
		JobID:      1,
		Status:     AssignmentFinished,
		CreatedAt:  now.Add(-10 * 24 * time.Hour),
		FinishedAt: now.Add(-10 * 24 * time.Hour),
	})
	s.Put(&Assignment{
		JobID:     2,
		Status:    AssignmentMinted,
		CreatedAt: now.Add(-10 * 24 * time.Hour),
	})
	s.Put(&Assignment{
		JobID:      3,
		Status:     AssignmentFinished,
		CreatedAt:  now.Add(-time.Hour),
		FinishedAt: now.Add(-time.Hour),
	})
	s.Put(&Assignment{
		JobID:     4,
		Status:    AssignmentAssigned,
		CreatedAt: now.Add(-10 * 24 * time.Hour),
	})

	rec := &Reconciler{Store: s}
	if n := rec.ReconcileOnce(context.Background()); n != 0 {
		t.Fatalf("reconciled = %d want 0", n)
	}
	if s.Get(1) != nil {
		t.Fatal("old finished should be pruned")
	}
	if s.Get(2) == nil {
		t.Fatal("minted must not be pruned")
	}
	if s.Get(3) == nil {
		t.Fatal("recent finished must remain")
	}
	if s.Get(4) == nil {
		t.Fatal("assigned must not be pruned")
	}
}

func TestAssignmentStore_ListStuck(t *testing.T) {
	s := NewAssignmentStore()
	now := time.Now().UTC()
	s.Put(&Assignment{JobID: 1, Status: AssignmentAssigned, AssignedAt: now.Add(-10 * time.Minute)})
	s.Put(&Assignment{JobID: 2, Status: AssignmentStarted, StartedAt: now.Add(-1 * time.Minute)})
	s.Put(&Assignment{JobID: 3, Status: AssignmentFinished, StartedAt: now.Add(-1 * time.Hour)})
	stuck := s.ListStuck(now, 5*time.Minute)
	if len(stuck) != 1 || stuck[0].JobID != 1 {
		t.Fatalf("stuck = %+v", stuck)
	}
}
