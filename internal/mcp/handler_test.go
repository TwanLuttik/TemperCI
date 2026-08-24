package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

type fakeFleet struct {
	overview map[string]any
	hosts    []api.AgentInfo
	jobs     []map[string]any
	job      map[string]any
	jobErr   error
	vms      []map[string]any
	vm       map[string]any
	vmErr    error
	cache    map[string]any
	system   map[string]any
}

func (f *fakeFleet) Overview() map[string]any { return f.overview }
func (f *fakeFleet) Hosts() []api.AgentInfo   { return f.hosts }
func (f *fakeFleet) Jobs(JobFilter) []map[string]any {
	return f.jobs
}
func (f *fakeFleet) Job(int64) (map[string]any, error) { return f.job, f.jobErr }
func (f *fakeFleet) VMs(string) []map[string]any       { return f.vms }
func (f *fakeFleet) VM(string) (map[string]any, error) { return f.vm, f.vmErr }
func (f *fakeFleet) Cache() map[string]any             { return f.cache }
func (f *fakeFleet) SystemStatus() map[string]any      { return f.system }

func mcpPOST(t *testing.T, h http.Handler, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func rpcResult(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var env struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Result  map[string]any `json:"result"`
		Error   *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, rr.Body.String())
	}
	if env.JSONRPC != "2.0" {
		t.Fatalf("jsonrpc=%q", env.JSONRPC)
	}
	if env.Error != nil {
		t.Fatalf("rpc error %d %s", env.Error.Code, env.Error.Message)
	}
	if env.Result == nil {
		t.Fatal("missing result")
	}
	return env.Result
}

func TestHandle_Initialize(t *testing.T) {
	h := New(&fakeFleet{}, "1.2.3").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "0"},
		},
	})
	res := rpcResult(t, rr)
	if res["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion=%v", res["protocolVersion"])
	}
	info, _ := res["serverInfo"].(map[string]any)
	if info["name"] != ServerName || info["version"] != "1.2.3" {
		t.Fatalf("serverInfo=%v", info)
	}
	caps, _ := res["capabilities"].(map[string]any)
	if _, ok := caps["tools"]; !ok {
		t.Fatalf("capabilities=%v", caps)
	}
}

func TestHandle_InitializedNotification(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("notification should have empty body, got %s", rr.Body.String())
	}
}

func TestHandle_ToolsList(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	res := rpcResult(t, rr)
	tools, _ := res["tools"].([]any)
	names := map[string]bool{}
	for _, raw := range tools {
		m, _ := raw.(map[string]any)
		name, _ := m["name"].(string)
		names[name] = true
		if strings.TrimSpace(fmtString(m["description"])) == "" {
			t.Fatalf("tool %q missing description", name)
		}
	}
	want := []string{
		"fleet_overview", "list_hosts", "list_jobs", "get_job",
		"list_vms", "get_vm", "get_cache", "get_system_status",
	}
	for _, n := range want {
		if !names[n] {
			t.Fatalf("missing tool %q in %v", n, names)
		}
	}
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
}

func fmtString(v any) string {
	s, _ := v.(string)
	return s
}

func TestHandle_FleetOverview(t *testing.T) {
	h := New(&fakeFleet{overview: map[string]any{"warm": 2, "busy": 1}}, "dev").Handler()
	text := callTool(t, h, "fleet_overview", nil)
	if !strings.Contains(text, `"warm": 2`) && !strings.Contains(text, `"warm":2`) {
		t.Fatalf("overview text=%s", text)
	}
}

func TestHandle_GetJob_NotFound(t *testing.T) {
	h := New(&fakeFleet{jobErr: ErrNotFound}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "get_job",
			"arguments": map[string]any{"job_id": 99},
		},
	})
	res := rpcResult(t, rr)
	if res["isError"] != true {
		t.Fatalf("expected isError, got %v body=%s", res, rr.Body.String())
	}
}

func TestHandle_UnknownMethod(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "resources/list",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	var env struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error == nil || env.Error.Code != -32601 {
		t.Fatalf("want method not found, got %+v %s", env.Error, rr.Body.String())
	}
}

func TestHandle_UnknownTool(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "tools/call",
		"params":  map[string]any{"name": "cancel_job"},
	})
	res := rpcResult(t, rr)
	if res["isError"] != true {
		t.Fatalf("expected isError for unknown tool, got %v", res)
	}
}

func TestTruncateTail(t *testing.T) {
	long := strings.Repeat("a", maxLogBytes+50)
	got, truncated := truncateTail(long, maxLogBytes)
	if !truncated {
		t.Fatal("expected truncated")
	}
	if len(got) != maxLogBytes {
		t.Fatalf("len=%d want %d", len(got), maxLogBytes)
	}
	if !strings.HasSuffix(got, "a") || got[0] != 'a' {
		t.Fatalf("should keep the tail")
	}
	short := "hello"
	got, truncated = truncateTail(short, maxLogBytes)
	if truncated || got != short {
		t.Fatalf("short = %q truncated=%v", got, truncated)
	}
}

func TestListJobs_AppliesFilter(t *testing.T) {
	var got JobFilter
	f := &filterFleet{JobFilterOut: []map[string]any{{"job_id": 1}}}
	f.onJobs = func(flt JobFilter) { got = flt }
	h := New(f, "dev").Handler()
	_ = callTool(t, h, "list_jobs", map[string]any{
		"status": "failed",
		"repo":   "acme/app",
		"limit":  5,
	})
	if got.Status != "failed" || got.Repo != "acme/app" || got.Limit != 5 {
		t.Fatalf("filter=%+v", got)
	}
}

type filterFleet struct {
	fakeFleet
	onJobs       func(JobFilter)
	JobFilterOut []map[string]any
}

func (f *filterFleet) Jobs(flt JobFilter) []map[string]any {
	if f.onJobs != nil {
		f.onJobs(flt)
	}
	return f.JobFilterOut
}

func callTool(t *testing.T, h http.Handler, name string, args map[string]any) string {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	rr := mcpPOST(t, h, map[string]any{
		"jsonrpc": "2.0",
		"id":      10,
		"method":  "tools/call",
		"params":  map[string]any{"name": name, "arguments": args},
	})
	res := rpcResult(t, rr)
	if res["isError"] == true {
		t.Fatalf("tool %s error: %s", name, rr.Body.String())
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content: %s", rr.Body.String())
	}
	item, _ := content[0].(map[string]any)
	text, _ := item["text"].(string)
	if text == "" {
		t.Fatalf("empty text: %s", rr.Body.String())
	}
	return text
}

func TestHandle_GETMethodNotAllowed(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestHandle_Ping(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	rr := mcpPOST(t, h, map[string]any{"jsonrpc": "2.0", "id": 8, "method": "ping"})
	_ = rpcResult(t, rr)
	_ = io.Discard
}

func TestHandle_SSEWhenOnlyEventStreamAccepted(t *testing.T) {
	h := New(&fakeFleet{}, "dev").Handler()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "ping"})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if !strings.Contains(rr.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("content-type=%s", rr.Header().Get("Content-Type"))
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"jsonrpc":"2.0"`) {
		t.Fatalf("sse=%s", body)
	}
}
