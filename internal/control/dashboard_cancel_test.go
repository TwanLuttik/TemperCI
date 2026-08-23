package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestVMDetail_ReturnsConsoleFromAgent(t *testing.T) {
	store := NewAssignmentStore()
	store.Put(&Assignment{
		JobID:           88,
		Name:            "build",
		RepoFullName:    "acme/app",
		Status:          AssignmentStarted,
		AssignedAgentID: "host-1",
		VMID:            "vm-live",
	})
	srv := NewServer(ServerConfig{
		Store:      store,
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true},
		},
	})
	reg := agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{
		AgentID: "host-1",
		VMs: []api.VMUsage{{
			ID:          "vm-live",
			State:       "busy",
			JobID:       "88",
			VCPUs:       4,
			MemoryMiB:   6144,
			GuestIP:     "10.231.1.2",
			HostIP:      "10.231.1.1",
			ConsoleTail: "serial ready\n",
			AgentTail:   "guest ready signaled\n",
		}},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, reg)
	if rr.Code != http.StatusOK {
		t.Fatalf("register %d %s", rr.Code, rr.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/vms/vm-live", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK  bool `json:"ok"`
		VM  struct {
			ID          string `json:"id"`
			GuestIP     string `json:"guest_ip"`
			ConsoleTail string `json:"console_tail"`
			AgentTail   string `json:"agent_tail"`
		} `json:"vm"`
		Job *struct {
			JobID int64  `json:"job_id"`
			Name  string `json:"name"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.VM.ID != "vm-live" || body.VM.GuestIP != "10.231.1.2" {
		t.Fatalf("body=%s", rr.Body.String())
	}
	if body.VM.ConsoleTail != "serial ready\n" || !strings.Contains(body.VM.AgentTail, "guest ready") {
		t.Fatalf("logs=%s", rr.Body.String())
	}
	if body.Job == nil || body.Job.JobID != 88 || body.Job.Name != "build" {
		t.Fatalf("job=%+v", body.Job)
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
