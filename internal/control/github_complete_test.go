package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestWebhookCompleted_FinishesStartedJobAndQueuesKill(t *testing.T) {
	store := NewAssignmentStore()
	store.Put(&Assignment{
		JobID:            991001,
		Status:           AssignmentStarted,
		AssignedAgentID:  "host-1",
		VMID:             "vm-dead",
		RunnerID:         88,
		Org:              "acme",
		EncodedJITConfig: "jit-must-clear",
	})
	h := NewHandler(&mockMinter{}, store, HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		Store:         store,
		WebhookSecret: "super-secret",
		AgentToken:    "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
		},
	})
	reg := agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{
		AgentID: "host-1",
		VMs:     []api.VMUsage{{ID: "vm-dead", State: "busy", JobID: "991001"}},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	if rr.Code != http.StatusOK {
		t.Fatalf("register %d %s", rr.Code, rr.Body.String())
	}

	body := fixture(t, "workflow_job_completed_failure.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("webhook %d %s", rr.Code, rr.Body.String())
	}

	got := store.Get(991001)
	if got == nil || got.Status != AssignmentFinished || got.Outcome != "failure" {
		t.Fatalf("assignment=%+v", got)
	}
	if got.EncodedJITConfig != "" {
		t.Fatal("JIT must be cleared")
	}

	reg = agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{AgentID: "host-1"})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	var resp api.RegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Commands) != 1 || resp.Commands[0].Action != api.AgentCmdKillVM || resp.Commands[0].VMID != "vm-dead" {
		t.Fatalf("commands=%+v", resp.Commands)
	}

	// Second completed webhook must not enqueue another kill.
	req = httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("repeat webhook %d %s", rr.Code, rr.Body.String())
	}
	reg = agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{AgentID: "host-1"})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	resp = api.RegisterResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Commands) != 0 {
		t.Fatalf("second completed queued extra commands: %+v", resp.Commands)
	}
}

func TestWebhookCompleted_UnknownJobIgnored(t *testing.T) {
	store := NewAssignmentStore()
	h := NewHandler(&mockMinter{}, store, HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{Handler: h, Store: store, WebhookSecret: "super-secret"})
	body := fixture(t, "workflow_job_completed_failure.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	if store.Get(991001) != nil {
		t.Fatal("unknown job must not be created")
	}
}
