package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

func TestAgentRegistry_OmittedVMsClearsStaleList(t *testing.T) {
	r := NewAgentRegistry()
	r.Register(api.RegisterRequest{
		AgentID: "pve",
		VMs:     []api.VMUsage{{ID: "vm-dead", State: "busy", JobID: "1"}},
	})
	if n := len(r.Get("pve").VMs); n != 1 {
		t.Fatalf("seed VMs = %d", n)
	}

	// Same wire as a heartbeat after the pool is empty: json omitempty drops "vms".
	raw, err := json.Marshal(api.RegisterRequest{AgentID: "pve", Capacity: 2})
	if err != nil {
		t.Fatal(err)
	}
	var req api.RegisterRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	if req.VMs != nil {
		t.Fatalf("setup: expected omitted vms to unmarshal as nil, got %+v", req.VMs)
	}

	r.Register(req)
	got := r.Get("pve")
	if got == nil || len(got.VMs) != 0 {
		t.Fatalf("stale VMs kept after empty heartbeat: %+v", got.VMs)
	}
}

func TestAgentAPI_RegisterEmptyVMsClearsDashboard(t *testing.T) {
	srv, _ := testAgentServer(t)
	req := agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
		AgentID: "pve",
		VMs:     []api.VMUsage{{ID: "vm-ghost", State: "busy", JobID: "99"}},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register %d %s", rr.Code, rr.Body.String())
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
		AgentID: "pve",
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reregister %d %s", rr.Code, rr.Body.String())
	}

	if n := len(srv.Agents().Get("pve").VMs); n != 0 {
		t.Fatalf("dashboard still has %d VMs after empty register", n)
	}
}
