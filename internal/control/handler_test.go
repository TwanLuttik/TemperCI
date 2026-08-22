package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

type mockMinter struct {
	calls []github.GenerateJITConfigRequest
	resp  *github.GenerateJITConfigResponse
	err   error
}

func (m *mockMinter) GenerateJITConfig(_ context.Context, req github.GenerateJITConfigRequest) (*github.GenerateJITConfigResponse, error) {
	m.calls = append(m.calls, req)
	if m.err != nil {
		return nil, m.err
	}
	if m.resp != nil {
		return m.resp, nil
	}
	return &github.GenerateJITConfigResponse{
		Runner:           github.RunnerInfo{ID: 7, Name: req.Name},
		EncodedJITConfig: "jit-secret",
	}, nil
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "webhooks", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestHandleWorkflowJob_TemperCILabelMintsJIT(t *testing.T) {
	m := &mockMinter{}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{Org: "fallback", RunnerGroupID: 1})

	res, err := h.HandleWorkflowJob(context.Background(), fixture(t, "workflow_job_queued_temperci.json"))
	if err != nil {
		t.Fatalf("HandleWorkflowJob: %v", err)
	}
	if res.Ignored {
		t.Fatalf("unexpected ignore: %s", res.Reason)
	}
	if len(m.calls) != 1 {
		t.Fatalf("JIT calls = %d, want 1", len(m.calls))
	}
	call := m.calls[0]
	if call.Org != "acme" {
		t.Errorf("org = %q", call.Org)
	}
	if len(call.Labels) != 1 || call.Labels[0] != "temperci-4vcpu-ubuntu-2404" {
		t.Errorf("labels = %v", call.Labels)
	}
	if call.InstallationID != 12345 {
		t.Errorf("installation = %d", call.InstallationID)
	}
	if call.Name != "temperci-job-991001" {
		t.Errorf("name = %q", call.Name)
	}

	a := store.Get(991001)
	if a == nil {
		t.Fatal("expected assignment stored")
	}
	if a.Status != AssignmentMinted {
		t.Errorf("status = %s", a.Status)
	}
	if a.EncodedJITConfig != "jit-secret" {
		t.Errorf("jit missing from store")
	}
	if a.Name != "build" || a.WorkflowName != "CI" {
		t.Errorf("identity name=%q workflow=%q", a.Name, a.WorkflowName)
	}
}

func TestHandleWorkflowJob_NonTemperCIIgnoredNoJIT(t *testing.T) {
	m := &mockMinter{}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{})

	res, err := h.HandleWorkflowJob(context.Background(), fixture(t, "workflow_job_queued_ubuntu_latest.json"))
	if err != nil {
		t.Fatalf("HandleWorkflowJob: %v", err)
	}
	if !res.Ignored || res.Reason != "labels_not_owned" {
		t.Fatalf("result = %+v", res)
	}
	if len(m.calls) != 0 {
		t.Fatalf("expected no JIT calls, got %d", len(m.calls))
	}
	if store.Len() != 0 {
		t.Fatalf("expected empty store, got %d", store.Len())
	}
}

func TestHandleWorkflowJob_MintError(t *testing.T) {
	m := &mockMinter{err: errors.New("api down")}
	store := NewAssignmentStore()
	h := NewHandler(m, store, HandlerConfig{})

	_, err := h.HandleWorkflowJob(context.Background(), fixture(t, "workflow_job_queued_temperci.json"))
	if err == nil {
		t.Fatal("expected error")
	}
	a := store.Get(991001)
	if a == nil || a.Status != AssignmentFailed {
		t.Fatalf("expected failed assignment, got %+v", a)
	}
}
