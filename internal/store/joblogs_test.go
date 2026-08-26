package store

import (
	"path/filepath"
	"strings"
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

func TestAcceptWorkflowLog_RejectsRunnerDiag(t *testing.T) {
	diag := "[2026-08-22 17:07:28Z INFO JobServerQueue] Try to append 1 batches web console lines\n[2026-08-22 17:07:27Z INFO HostContext] Well known directory 'Work'\n"
	if AcceptWorkflowLog(diag) {
		t.Fatal("runner _diag must not be treated as workflow log")
	}
	steps := "2026-08-22T17:07:28Z ##[group]Run actions/checkout@v4\nSyncing repository\n"
	if !AcceptWorkflowLog(steps) {
		t.Fatal("GitHub step log must be accepted")
	}
}

func TestJobLogs_MergeIgnoresRunnerDiagWorkflow(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	diag := "[2026-08-22 17:07:28Z INFO JobServerQueue] Try to append 1 batches\n"
	if err := s.MergeJobLogs(9, "listener", "", "", diag); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJobLog(9)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowLog != "" {
		t.Fatalf("diag leaked into workflow_log: %q", got.WorkflowLog)
	}
	if err := s.SetWorkflowLog(9, diag); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJobLog(9)
	if got.WorkflowLog != "" {
		t.Fatalf("SetWorkflowLog accepted diag: %q", got.WorkflowLog)
	}
}

func TestApplyWorkflowAppend(t *testing.T) {
	if _, ok := ApplyWorkflowAppend("", 0, ""); ok {
		t.Fatal("empty chunk")
	}
	got, ok := ApplyWorkflowAppend("", 0, "##[group]a\n")
	if !ok || got != "##[group]a\n" {
		t.Fatalf("first = %q %v", got, ok)
	}
	got, ok = ApplyWorkflowAppend(got, len(got), "b\n")
	if !ok || got != "##[group]a\nb\n" {
		t.Fatalf("append = %q %v", got, ok)
	}
	if _, ok := ApplyWorkflowAppend(got, 99, "gap"); ok {
		t.Fatal("gap")
	}
}

func TestJobLogs_AppendWorkflowLog(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first := "##[group]Run checkout\n"
	if err := s.AppendWorkflowLog(3, 0, first); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendWorkflowLog(3, len(first), "Synced\n"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJobLog(3)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowLog != first+"Synced\n" {
		t.Fatalf("append = %q", got.WorkflowLog)
	}
	if err := s.AppendWorkflowLog(3, 99, "gap"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetJobLog(3)
	if got.WorkflowLog != first+"Synced\n" {
		t.Fatalf("gap changed log: %q", got.WorkflowLog)
	}
}

func TestJobLogs_WorkflowLogPersistsAcrossMerge(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetWorkflowLog(8, "##[group]Run actions/checkout@v4\nok\n"); err != nil {
		t.Fatal(err)
	}
	if err := s.MergeJobLogs(8, "listener-only", "agent", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJobLog(8)
	if err != nil {
		t.Fatal(err)
	}
	if got.WorkflowLog == "" || !strings.Contains(got.WorkflowLog, "actions/checkout") {
		t.Fatalf("workflow log lost: %+v", got)
	}
	if got.RunnerLog != "listener-only" {
		t.Fatalf("runner=%q", got.RunnerLog)
	}
}
