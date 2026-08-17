package control

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

func TestAssignmentStore_RestartReloadsMintedJIT(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	s1, err := NewAssignmentStoreWithPersister(NewStorePersister(db))
	if err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	s1.Put(&Assignment{
		JobID:            100,
		RunID:            9,
		Org:              "acme",
		RepoFullName:     "acme/app",
		Labels:           []string{"temperci-4c", "self-hosted"},
		InstallationID:   3,
		RunnerName:       "temperci-job-100",
		RunnerID:         55,
		EncodedJITConfig: "secret-jit-token",
		Status:           AssignmentMinted,
		CreatedAt:        created,
	})
	s1.Put(&Assignment{
		JobID:            101,
		Org:              "acme",
		EncodedJITConfig: "secret-jit-later",
		Status:           AssignmentMinted,
		CreatedAt:        created.Add(time.Minute),
	})
	_ = db.Close()

	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	s2, err := NewAssignmentStoreWithPersister(NewStorePersister(db2))
	if err != nil {
		t.Fatal(err)
	}
	if s2.Len() != 2 || s2.PendingLen() != 2 {
		t.Fatalf("reloaded len=%d pending=%d", s2.Len(), s2.PendingLen())
	}
	got := s2.Get(100)
	if got == nil || got.EncodedJITConfig != "secret-jit-token" {
		t.Fatal("expected JIT after reload")
	}
	if got.Org != "acme" || got.RepoFullName != "acme/app" || got.RunnerID != 55 {
		t.Fatalf("metadata = job=%d org=%s repo=%s runner=%d", got.JobID, got.Org, got.RepoFullName, got.RunnerID)
	}
	if len(got.Labels) != 2 || got.Labels[0] != "temperci-4c" {
		t.Fatalf("labels = %v", got.Labels)
	}

	claimed := s2.ClaimNext("agent-1")
	if claimed == nil || claimed.JobID != 100 {
		t.Fatalf("claim after restart = job=%v", jobIDOf(claimed))
	}
	if claimed.EncodedJITConfig != "secret-jit-token" {
		t.Fatal("claim after restart missing JIT")
	}
	if claimed.Status != AssignmentAssigned || claimed.AssignedAgentID != "agent-1" {
		t.Fatalf("claim state job=%d status=%s agent=%s", claimed.JobID, claimed.Status, claimed.AssignedAgentID)
	}

	second := s2.ClaimNext("agent-1")
	if second == nil || second.JobID != 101 || second.EncodedJITConfig != "secret-jit-later" {
		t.Fatalf("second claim job=%v", jobIDOf(second))
	}
}

func TestAssignmentStore_MarkFinishedClearsJITInDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := NewAssignmentStoreWithPersister(NewStorePersister(db))
	if err != nil {
		t.Fatal(err)
	}
	s.Put(&Assignment{
		JobID:            200,
		Org:              "acme",
		EncodedJITConfig: "secret-jit-token",
		Status:           AssignmentMinted,
	})
	if s.ClaimNext("host-1") == nil {
		t.Fatal("expected claim")
	}
	row, err := db.GetAssignment(200)
	if err != nil || row == nil || row.EncodedJITConfig != "secret-jit-token" {
		t.Fatal("expected JIT in DB while assigned")
	}
	if err := s.MarkFinished(200, "host-1", "success", "vm-1", false, ""); err != nil {
		t.Fatal(err)
	}
	row, err = db.GetAssignment(200)
	if err != nil || row == nil {
		t.Fatalf("get after finish err=%v", err)
	}
	if row.EncodedJITConfig != "" {
		t.Fatal("expected JIT cleared in DB after finish")
	}
	if row.Status != string(AssignmentFinished) {
		t.Fatalf("status = %s", row.Status)
	}

	s.Put(&Assignment{
		JobID:            201,
		Org:              "acme",
		EncodedJITConfig: "secret-fail",
		Status:           AssignmentMinted,
	})
	s.MarkFailed(201, "mint failed")
	row, err = db.GetAssignment(201)
	if err != nil || row == nil {
		t.Fatalf("get after fail err=%v", err)
	}
	if row.EncodedJITConfig != "" {
		t.Fatal("expected JIT cleared in DB after fail")
	}
	if row.Status != string(AssignmentFailed) {
		t.Fatalf("status = %s", row.Status)
	}
}

func TestAssignmentStore_PruneFinishedPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s, err := NewAssignmentStoreWithPersister(NewStorePersister(db))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.Put(&Assignment{
		JobID:            300,
		Org:              "acme",
		EncodedJITConfig: "old-finished",
		Status:           AssignmentFinished,
		CreatedAt:        now.Add(-10 * 24 * time.Hour),
		FinishedAt:       now.Add(-10 * 24 * time.Hour),
	})
	s.Put(&Assignment{
		JobID:            301,
		Org:              "acme",
		EncodedJITConfig: "keep-minted",
		Status:           AssignmentMinted,
		CreatedAt:        now.Add(-10 * 24 * time.Hour),
	})
	if n := s.PruneFinished(7 * 24 * time.Hour); n != 1 {
		t.Fatalf("pruned = %d", n)
	}
	if row, _ := db.GetAssignment(300); row != nil {
		t.Fatal("old finished should be gone from DB")
	}
	row, err := db.GetAssignment(301)
	if err != nil || row == nil {
		t.Fatal("minted should remain in DB")
	}
	if row.EncodedJITConfig != "keep-minted" {
		t.Fatal("minted JIT must survive prune")
	}
}

func TestAssignmentStore_NilPersister(t *testing.T) {
	s, err := NewAssignmentStoreWithPersister(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Put(&Assignment{JobID: 1, Status: AssignmentMinted, EncodedJITConfig: "jit"})
	if s.ClaimNext("a") == nil {
		t.Fatal("nil persister should still claim from memory")
	}
}

func jobIDOf(a *Assignment) any {
	if a == nil {
		return nil
	}
	return a.JobID
}
