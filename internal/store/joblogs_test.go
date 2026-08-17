package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestJobLogs_MergeAndEvents(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.MergeJobLogs(42, "runner-a", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeJobLogs(42, "", "agent-b", "console-c"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJobLog(42)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunnerLog != "runner-a" || got.AgentLog != "agent-b" || got.ConsoleLog != "console-c" {
		t.Fatalf("merged logs = %+v", got)
	}

	if err := s.AppendJobEvent(42, JobEvent{Source: "control", Message: "minted", Time: time.Unix(1, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendJobEvent(42, JobEvent{Source: "agent", Level: "warn", Message: "slow"}); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetJobLog(42)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("events = %d", len(got.Events))
	}
	if got.Events[0].Message != "minted" || got.Events[1].Source != "agent" {
		t.Fatalf("events = %+v", got.Events)
	}
	// Merge must not wipe events.
	if err := s.MergeJobLogs(42, "runner-z", "", ""); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJobLog(42)
	if got.RunnerLog != "runner-z" || len(got.Events) != 2 {
		t.Fatalf("after remarge: %+v", got)
	}
}

func TestJobLogs_MissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetJobLog(7)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunnerLog != "" || len(got.Events) != 0 {
		t.Fatalf("expected empty, got %+v", got)
	}
}
