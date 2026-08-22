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

// Multi-host capacity-aware assignment: full agent is skipped; free agent gets FIFO job.
func TestMultiHost_CapacityAwareClaim(t *testing.T) {
	srv, store := testAgentServer(t)

	// Register two agents via API.
	for _, id := range []string{"agent-full", "agent-free"} {
		req := agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
			AgentID:     id,
			MaxCapacity: 1,
			Capacity:    0,
		})
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("register %s: %d", id, rr.Code)
		}
	}

	// Two jobs pending FIFO.
	store.Put(&Assignment{JobID: 100, Status: AssignmentMinted, EncodedJITConfig: "jit-100", Org: "acme", RunnerID: 1})
	store.Put(&Assignment{JobID: 101, Status: AssignmentMinted, EncodedJITConfig: "jit-101", Org: "acme", RunnerID: 2})

	// Full agent cannot claim.
	req := agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID: "agent-full", FreeSlots: 0, Busy: 1, Warm: 0,
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var claim api.ClaimResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job != nil {
		t.Fatalf("full agent got job %+v", claim.Job)
	}
	if store.PendingLen() != 2 {
		t.Fatalf("pending after full claim = %d", store.PendingLen())
	}

	// Free agent gets job 100 (FIFO).
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID: "agent-free", FreeSlots: 1, Busy: 0, Warm: 1,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job == nil || claim.Job.JobID != 100 {
		t.Fatalf("free agent claim = %+v", claim.Job)
	}
	if claim.Job.EncodedJITConfig != "jit-100" {
		t.Fatal("missing jit on assignment")
	}
	a := store.Get(100)
	if a.AssignedAgentID != "agent-free" || a.Status != AssignmentAssigned {
		t.Fatalf("assignment = %+v", a)
	}

	// Free agent now full (FreeSlots 0) — second job stays pending for another host.
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID: "agent-free", FreeSlots: 0, Busy: 1,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	claim = api.ClaimResponse{} // reset; omitted job must not keep prior pointer
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job != nil {
		t.Fatalf("expected no second job for full free-agent, got %+v", claim.Job)
	}

	// agent-full frees a slot and takes job 101.
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID: "agent-full", FreeSlots: 1, Busy: 0,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	claim = api.ClaimResponse{}
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job == nil || claim.Job.JobID != 101 {
		t.Fatalf("agent-full claim = %+v", claim.Job)
	}
	if store.Get(101).AssignedAgentID != "agent-full" {
		t.Fatalf("job 101 agent = %s", store.Get(101).AssignedAgentID)
	}
}

func TestMultiHost_RegistryCapacitySnapshot(t *testing.T) {
	r := NewAgentRegistry()
	info := r.Register(api.RegisterRequest{
		AgentID: "h1", MaxCapacity: 4, Capacity: 3, Warm: 2, Busy: 1,
	})
	if info.MaxCapacity != 4 || info.Capacity != 3 || info.Warm != 2 || info.Busy != 1 {
		t.Fatalf("info = %+v", info)
	}
	r.UpdateCapacity("h1", 0, 0, 4)
	got := r.Get("h1")
	if got.Capacity != 0 || got.Busy != 4 {
		t.Fatalf("after update = %+v", got)
	}
	if r.Len() != 1 {
		t.Fatalf("len = %d", r.Len())
	}
	list := r.List()
	if len(list) != 1 || list[0].AgentID != "h1" {
		t.Fatalf("list = %+v", list)
	}
}

func TestRegister_StoresHostResources(t *testing.T) {
	srv, _ := testAgentServer(t)
	req := agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
		AgentID:     "box-1",
		MaxCapacity: 1,
		Capacity:    1,
		Resources: &api.HostResources{
			RAMTotalMiB:        16384,
			RAMAvailMiB:        9000,
			DiskFreeMiB:        100000,
			NumCPU:             8,
			ConfiguredMaxReady: 4,
			EffectiveMaxReady:  1,
			ClampReason:        "ram",
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d", rr.Code)
	}
	info := srv.Agents().Get("box-1")
	if info == nil || info.Resources == nil {
		t.Fatal("expected resources on agent")
	}
	if info.Resources.EffectiveMaxReady != 1 || info.Resources.ClampReason != "ram" || info.Resources.NumCPU != 8 {
		t.Fatalf("resources %+v", info.Resources)
	}
}

func TestHostsAPI_IncludesResources(t *testing.T) {
	// testAgentServer has no dashboard (GET /api/v1/hosts is 404). Mount the
	// UI like cache_test.go so this is an authenticated open-mode request.
	h := NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		WebhookSecret: "super-secret",
		AgentToken:    "agent-shared-token",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
		},
	})
	_ = srv.Agents().Register(api.RegisterRequest{
		AgentID: "box-1", Capacity: 1, MaxCapacity: 1,
		Resources: &api.HostResources{RAMAvailMiB: 9000, DiskFreeMiB: 80000, NumCPU: 16, EffectiveMaxReady: 1, ConfiguredMaxReady: 4, ClampReason: "ram"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hosts", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"clamp_reason":"ram"`) {
		t.Fatalf("expected resources in hosts JSON: %s", rr.Body.String())
	}
}
