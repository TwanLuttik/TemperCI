package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestJobCancel_QueuesKillAndMarksCancelled(t *testing.T) {
	store := NewAssignmentStore()
	store.Put(&Assignment{
		JobID:           77,
		Status:          AssignmentStarted,
		AssignedAgentID: "host-1",
		VMID:            "vm-kill-me",
		RunnerID:        9,
		Org:             "acme",
	})
	srv := NewServer(ServerConfig{
		Store:      store,
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
		},
	})
	reg := agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{
		AgentID: "host-1",
		VMs:     []api.VMUsage{{ID: "vm-kill-me", State: "busy", JobID: "77"}},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	if rr.Code != http.StatusOK {
		t.Fatalf("register %d %s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/77/cancel", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("cancel %d %s", rr.Code, rr.Body.String())
	}
	got := store.Get(77)
	if got.Status != AssignmentFinished || got.Outcome != "cancelled" {
		t.Fatalf("assignment = %+v", got)
	}

	reg = agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{AgentID: "host-1"})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	var resp api.RegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Commands) != 1 || resp.Commands[0].Action != api.AgentCmdKillVM || resp.Commands[0].VMID != "vm-kill-me" {
		t.Fatalf("commands=%+v", resp.Commands)
	}
}

func TestVMKill_UnknownVM(t *testing.T) {
	srv := NewServer(ServerConfig{
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/vms/vm-missing/kill", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
}
