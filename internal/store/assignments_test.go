package store_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

func TestAssignmentRoundTrip(t *testing.T) {
	s := openTestStore(t)
	created := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	row := store.AssignmentRow{
		JobID:            42,
		RunID:            7,
		Org:              "acme",
		RepoFullName:     "acme/app",
		LabelsJSON:       `["temperci-4c"]`,
		JobName:          "build",
		WorkflowName:     "CI",
		InstallationID:   99,
		RunnerName:       "temperci-job-42",
		RunnerID:         1001,
		EncodedJITConfig: "secret-jit",
		Status:           "minted",
		CreatedAt:        created,
	}
	if err := s.UpsertAssignment(row); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAssignment(42)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.Org != "acme" || got.RepoFullName != "acme/app" || got.Status != "minted" {
		t.Fatalf("row = %+v", *got)
	}
	if got.EncodedJITConfig != "secret-jit" {
		t.Fatal("expected JIT to persist while minted")
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("created_at = %v want %v", got.CreatedAt, created)
	}
	if got.LabelsJSON != `["temperci-4c"]` {
		t.Fatalf("labels = %q", got.LabelsJSON)
	}
	if got.JobName != "build" || got.WorkflowName != "CI" {
		t.Fatalf("identity = %q / %q", got.JobName, got.WorkflowName)
	}

	all, err := s.ListAssignments()
	if err != nil || len(all) != 1 || all[0].JobID != 42 {
		t.Fatalf("list = %+v err=%v", all, err)
	}
}

func TestAssignmentJITClearedWhenFinishedOrFailed(t *testing.T) {
	s := openTestStore(t)
	base := store.AssignmentRow{
		JobID:            1,
		Org:              "acme",
		EncodedJITConfig: "secret-jit",
		Status:           "minted",
		CreatedAt:        time.Now().UTC(),
	}
	if err := s.UpsertAssignment(base); err != nil {
		t.Fatal(err)
	}

	base.Status = "finished"
	base.EncodedJITConfig = "secret-jit"
	if err := s.UpsertAssignment(base); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAssignment(1)
	if err != nil || got == nil {
		t.Fatalf("get finished = %+v err=%v", got, err)
	}
	if got.EncodedJITConfig != "" {
		t.Fatal("expected JIT cleared when finished")
	}

	base.JobID = 2
	base.Status = "failed"
	base.EncodedJITConfig = "secret-jit"
	if err := s.UpsertAssignment(base); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetAssignment(2)
	if err != nil || got == nil {
		t.Fatalf("get failed = %+v err=%v", got, err)
	}
	if got.EncodedJITConfig != "" {
		t.Fatal("expected JIT cleared when failed")
	}
}

func TestAssignmentDeleteAndPrune(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UTC()
	rows := []store.AssignmentRow{
		{JobID: 1, Org: "acme", Status: "finished", CreatedAt: now.Add(-10 * 24 * time.Hour), FinishedAt: now.Add(-10 * 24 * time.Hour)},
		{JobID: 2, Org: "acme", Status: "failed", CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{JobID: 3, Org: "acme", Status: "minted", CreatedAt: now.Add(-10 * 24 * time.Hour), EncodedJITConfig: "keep"},
		{JobID: 4, Org: "acme", Status: "assigned", CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{JobID: 5, Org: "acme", Status: "started", CreatedAt: now.Add(-10 * 24 * time.Hour)},
		{JobID: 6, Org: "acme", Status: "finished", CreatedAt: now.Add(-1 * time.Hour), FinishedAt: now.Add(-1 * time.Hour)},
	}
	for _, r := range rows {
		if err := s.UpsertAssignment(r); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.PruneFinished(7 * 24 * time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("pruned = %d want 2", n)
	}
	if got, _ := s.GetAssignment(1); got != nil {
		t.Fatal("old finished should be pruned")
	}
	if got, _ := s.GetAssignment(2); got != nil {
		t.Fatal("old failed should be pruned")
	}
	for _, id := range []int64{3, 4, 5, 6} {
		got, err := s.GetAssignment(id)
		if err != nil || got == nil {
			t.Fatalf("job %d missing after prune", id)
		}
		if id == 3 && got.EncodedJITConfig != "keep" {
			t.Fatal("minted JIT must survive prune")
		}
	}

	if err := s.DeleteAssignment(3); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetAssignment(3); got != nil {
		t.Fatal("expected delete")
	}
}

func TestAssignmentCacheStatsRoundTrip(t *testing.T) {
	s := openTestStore(t)
	row := store.AssignmentRow{
		JobID:         8,
		Org:           "acme",
		RepoFullName:  "acme/app",
		Status:        "finished",
		CreatedAt:     time.Now().UTC(),
		CacheHits:     2,
		CacheMisses:   1,
		CacheBytesIn:  100,
		CacheBytesOut: 200,
	}
	if err := s.UpsertAssignment(row); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAssignment(8)
	if err != nil || got == nil {
		t.Fatalf("get = %+v err=%v", got, err)
	}
	if got.CacheHits != 2 || got.CacheMisses != 1 || got.CacheBytesIn != 100 || got.CacheBytesOut != 200 {
		t.Fatalf("cache stats = %+v", *got)
	}
}

func TestGetAssignmentMissing(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetAssignment(999)
	if err != nil || got != nil {
		t.Fatalf("missing = %+v err=%v", got, err)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
