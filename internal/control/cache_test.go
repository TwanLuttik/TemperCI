package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestCacheInventoryAndClearQueue(t *testing.T) {
	h := NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		WebhookSecret: "super-secret",
		AgentToken:    "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
		},
	})

	req := agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{
		AgentID: "host-1",
		Cache: &api.CacheUsage{
			Bytes:    100,
			MaxBytes: 1000,
			Entries:  2,
			Repos: []api.CacheRepoUsage{
				{Repo: "acme/app", Bytes: 80, Entries: 1, LastAccess: time.Now().UTC()},
				{Repo: "acme/other", Bytes: 20, Entries: 1},
			},
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/cache", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	var listed struct {
		OK    bool            `json:"ok"`
		Bytes int64           `json:"bytes"`
		Hosts []api.CacheHost `json:"hosts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if !listed.OK || listed.Bytes != 100 || len(listed.Hosts) != 1 {
		t.Fatalf("list=%+v", listed)
	}
	if listed.Hosts[0].AgentID != "host-1" || listed.Hosts[0].Entries != 2 {
		t.Fatalf("host=%+v", listed.Hosts[0])
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/cache/clear", bytes.NewBufferString(`{"repo":"acme/app"}`))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("clear %d %s", rr.Code, rr.Body.String())
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{AgentID: "host-1"})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var reg api.RegisterResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	if len(reg.CacheOps) != 1 || reg.CacheOps[0].Action != api.CacheOpPurgeRepo || reg.CacheOps[0].Repo != "acme/app" {
		t.Fatalf("cache_ops=%+v", reg.CacheOps)
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/register", "tok", api.RegisterRequest{AgentID: "host-1"})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	reg = api.RegisterResponse{}
	_ = json.Unmarshal(rr.Body.Bytes(), &reg)
	if len(reg.CacheOps) != 0 {
		t.Fatalf("expected ops consumed, got %+v", reg.CacheOps)
	}
}
