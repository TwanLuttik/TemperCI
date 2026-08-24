package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

func mcpDashServer(t *testing.T, mcpToken string) *Server {
	t.Helper()
	return NewServer(ServerConfig{
		Handler:    NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{RunnerGroupID: 1}),
		AgentToken: "agent-tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: true,
				GitHubOrg:      "acme",
				MCPToken:       mcpToken,
			},
		},
	})
}

func TestMCP_NotFoundWhenTokenEmpty(t *testing.T) {
	srv := mcpDashServer(t, "")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if strings.Contains(ct, "text/html") {
		t.Fatalf("empty mcp_token must not serve the SPA: content-type=%s", ct)
	}
}

func TestMCP_UnauthorizedWithoutBearer(t *testing.T) {
	srv := mcpDashServer(t, "mcp-secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMCP_RejectsAgentToken(t *testing.T) {
	srv := mcpDashServer(t, "mcp-secret")
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer agent-tok")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMCP_InitializeWithToken(t *testing.T) {
	srv := mcpDashServer(t, "mcp-secret")
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mcp-secret")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		Result struct {
			ServerInfo struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.ServerInfo.Name != "temperci" {
		t.Fatalf("serverInfo=%+v", env.Result.ServerInfo)
	}
}

func mcpCall(t *testing.T, srv *Server, token, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%s status=%d body=%s", name, rr.Code, rr.Body.String())
	}
	var env struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Result.IsError || len(env.Result.Content) == 0 {
		t.Fatalf("%s error: %s", name, rr.Body.String())
	}
	text := env.Result.Content[0].Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("tool text not json: %v %s", err, text)
	}
	return data, text
}

func TestMCP_FleetOverviewAndJob(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:            42,
		RunID:            7,
		Org:              "acme",
		RepoFullName:     "acme/demo",
		Status:           AssignmentFailed,
		Outcome:          "failure",
		Error:            "exit 1",
		AssignedAgentID:  "host-1",
		VMID:             "vm-9",
		EncodedJITConfig: "SECRET-JIT-MUST-NOT-LEAK",
		CreatedAt:        time.Now().UTC().Add(-time.Minute),
		FinishedAt:       time.Now().UTC(),
	})
	if err := db.MergeJobLogs(42, "runner boom", "agent ok", "console", "workflow failed"); err != nil {
		t.Fatal(err)
	}

	srv := NewServer(ServerConfig{
		Store:      as,
		AgentToken: "agent-tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: true,
				GitHubOrg:      "acme",
				MCPToken:       "mcp-secret",
			},
			Store: db,
		},
	})
	srv.Agents().Register(api.RegisterRequest{
		AgentID: "host-1",
		Warm:    2,
		Busy:    0,
		VMs: []api.VMUsage{{
			ID:    "vm-9",
			State: "destroying",
			JobID: "42",
		}},
	})

	overview, _ := mcpCall(t, srv, "mcp-secret", "fleet_overview", nil)
	if overview["org"] != "acme" {
		t.Fatalf("overview=%v", overview)
	}
	if overview["jobs_failed"] == nil {
		t.Fatalf("missing jobs_failed: %v", overview)
	}

	job, text := mcpCall(t, srv, "mcp-secret", "get_job", map[string]any{"job_id": 42})
	if strings.Contains(text, "SECRET-JIT") {
		t.Fatal("JIT config leaked through MCP")
	}
	inner, _ := job["job"].(map[string]any)
	if inner["job_id"] != float64(42) || inner["outcome"] != "failure" {
		t.Fatalf("job=%v", job)
	}
	logs, _ := job["logs"].(map[string]any)
	if logs["runner_log"] != "runner boom" {
		t.Fatalf("logs=%v", logs)
	}

	hosts, _ := mcpCall(t, srv, "mcp-secret", "list_hosts", nil)
	list, _ := hosts["hosts"].([]any)
	if len(list) != 1 {
		t.Fatalf("hosts=%v", hosts)
	}

	vm, _ := mcpCall(t, srv, "mcp-secret", "get_vm", map[string]any{"vm_id": "vm-9"})
	if vm["agent_id"] != "host-1" {
		t.Fatalf("vm=%v", vm)
	}
}

func TestMCP_ListJobsFilterAndLogTail(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	as := NewAssignmentStore()
	as.Put(&Assignment{JobID: 1, RepoFullName: "acme/demo", Status: AssignmentFinished, Outcome: "success"})
	as.Put(&Assignment{JobID: 2, RepoFullName: "acme/other", Status: AssignmentFailed, Outcome: "failure"})
	long := strings.Repeat("x", 9000) + "TAIL"
	if err := db.MergeJobLogs(2, long, "", "", ""); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerConfig{
		Store:      as,
		AgentToken: "agent-tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme", MCPToken: "mcp-secret"},
			Store:  db,
		},
	})
	jobs, _ := mcpCall(t, srv, "mcp-secret", "list_jobs", map[string]any{"status": "failed", "repo": "other"})
	list, _ := jobs["jobs"].([]any)
	if len(list) != 1 {
		t.Fatalf("filtered jobs=%v", jobs)
	}
	row, _ := list[0].(map[string]any)
	if row["job_id"] != float64(2) {
		t.Fatalf("row=%v", row)
	}
	detail, _ := mcpCall(t, srv, "mcp-secret", "get_job", map[string]any{"job_id": 2})
	logs, _ := detail["logs"].(map[string]any)
	text, _ := logs["runner_log"].(string)
	if !strings.HasSuffix(text, "TAIL") || logs["runner_log_truncated"] != true {
		t.Fatalf("logs=%v", logs)
	}
}

func TestSettingsConfig_MCPTokenIsRedacted(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)
	srv.dash.Config.MCPToken = "super-mcp-token-value"

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/config", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "super-mcp-token-value") {
		t.Fatal("raw mcp_token leaked in settings")
	}
	var resp struct {
		Fields []struct {
			Key        string `json:"key"`
			Value      string `json:"value"`
			Secret     bool   `json:"secret"`
			Configured bool   `json:"configured"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, f := range resp.Fields {
		if f.Key != "mcp_token" {
			continue
		}
		found = true
		if !f.Secret || !f.Configured {
			t.Fatalf("mcp_token field=%+v", f)
		}
		if !strings.Contains(f.Value, "set") {
			t.Fatalf("value=%q", f.Value)
		}
	}
	if !found {
		t.Fatal("mcp_token missing from settings fields")
	}
}

func TestSettingsConfigSave_MCPTokenEnablesEndpoint(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/config", strings.NewReader(`{"mcp_token":"live-mcp","setup_completed":true}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("save status=%d %s", rr.Code, rr.Body.String())
	}
	if srv.dash.Config.MCPToken != "live-mcp" {
		t.Fatalf("in-memory token=%q", srv.dash.Config.MCPToken)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "control.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "live-mcp") {
		t.Fatalf("control.toml missing token:\n%s", raw)
	}
	ping := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	ping.Header.Set("Content-Type", "application/json")
	ping.Header.Set("Authorization", "Bearer live-mcp")
	pr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(pr, ping)
	if pr.Code != http.StatusOK {
		t.Fatalf("ping after save status=%d %s", pr.Code, pr.Body.String())
	}
}
