package github

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorkflowJobEvent_QueuedTemperCI(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "webhooks", "workflow_job_queued_temperci.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	ev, err := ParseWorkflowJobEvent(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Action != "queued" {
		t.Errorf("action = %q, want queued", ev.Action)
	}
	if ev.Installation.ID != 12345 {
		t.Errorf("installation.id = %d, want 12345", ev.Installation.ID)
	}
	if ev.WorkflowJob.ID != 991001 {
		t.Errorf("job.id = %d, want 991001", ev.WorkflowJob.ID)
	}
	if ev.WorkflowJob.RunID != 55001 {
		t.Errorf("job.run_id = %d, want 55001", ev.WorkflowJob.RunID)
	}
	if ev.EventWorkflowName() != "CI" || ev.WorkflowJob.Name != "build" {
		t.Errorf("workflow=%q job=%q", ev.EventWorkflowName(), ev.WorkflowJob.Name)
	}
	if got := ev.WorkflowJob.Labels; len(got) != 1 || got[0] != "temperci-4vcpu-ubuntu-2404" {
		t.Errorf("labels = %v, want [temperci-4vcpu-ubuntu-2404]", got)
	}
	if ev.Repository.FullName != "acme/demo" {
		t.Errorf("repo = %q, want acme/demo", ev.Repository.FullName)
	}
	if ev.Organization.Login != "acme" {
		t.Errorf("org = %q, want acme", ev.Organization.Login)
	}
}

func TestParseWorkflowJobEvent_CompletedFailure(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "webhooks", "workflow_job_completed_failure.json")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ev, err := ParseWorkflowJobEvent(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ev.Action != "completed" {
		t.Fatalf("action=%q", ev.Action)
	}
	if ev.WorkflowJob.Conclusion != "failure" {
		t.Fatalf("conclusion=%q want failure", ev.WorkflowJob.Conclusion)
	}
	if ev.WorkflowJob.ID != 991001 {
		t.Fatalf("job.id=%d", ev.WorkflowJob.ID)
	}
}

func TestParseWorkflowJobEvent_InvalidJSON(t *testing.T) {
	_, err := ParseWorkflowJobEvent([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected parse error")
	}
}
