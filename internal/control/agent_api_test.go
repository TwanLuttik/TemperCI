package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

func testAgentServer(t *testing.T) (*Server, *AssignmentStore) {
	t.Helper()
	store := NewAssignmentStore()
	h := NewHandler(&mockMinter{}, store, HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		Store:         store,
		WebhookSecret: "super-secret",
		AgentToken:    "agent-shared-token",
	})
	return srv, store
}

func agentReq(t *testing.T, method, path, token string, body any) *http.Request {
	t.Helper()
	var r ioReader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	} else {
		r = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set(api.AgentAuthHeader, api.AgentBearerPrefix+token)
	}
	return req
}

// ioReader avoids importing io only for the alias in helper.
type ioReader interface {
	Read(p []byte) (n int, err error)
}

func TestAgentAPI_Unauthorized(t *testing.T) {
	srv, _ := testAgentServer(t)
	req := agentReq(t, http.MethodPost, "/v1/agent/register", "wrong", api.RegisterRequest{AgentID: "a1"})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAgentAPI_RegisterClaimLifecycle(t *testing.T) {
	srv, store := testAgentServer(t)

	// Register
	req := agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
		AgentID:  "host-1",
		Capacity: 2,
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register status = %d %s", rr.Code, rr.Body.String())
	}
	if srv.Agents().Get("host-1") == nil {
		t.Fatal("agent not registered")
	}

	// Empty claim (has capacity, no jobs)
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{AgentID: "host-1", FreeSlots: 2})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("claim empty status = %d", rr.Code)
	}
	var claim api.ClaimResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job != nil {
		t.Fatalf("expected no job, got %+v", claim.Job)
	}

	// Mint via store (simulates webhook)
	store.Put(&Assignment{
		JobID:            42,
		RunID:            7,
		Org:              "acme",
		RepoFullName:     "acme/demo",
		Labels:           []string{"temperci-4vcpu-ubuntu-2404"},
		RunnerName:       "temperci-job-42",
		RunnerID:         99,
		EncodedJITConfig: "jit-secret-material",
		Status:           AssignmentMinted,
	})

	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{AgentID: "host-1", FreeSlots: 2})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d %s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job == nil || claim.Job.JobID != 42 {
		t.Fatalf("claim job = %+v", claim.Job)
	}
	if claim.Job.EncodedJITConfig != "jit-secret-material" {
		t.Fatal("jit missing on claim response")
	}
	// Body should not be logged; ensure JSON still contains it for agent use.
	if store.Get(42).Status != AssignmentAssigned {
		t.Fatalf("status = %s", store.Get(42).Status)
	}

	// Started
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/started", "agent-shared-token", api.JobStartedRequest{
		AgentID:  "host-1",
		JobID:    42,
		VMID:     "vm-1",
		WarmBind: true,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("started status = %d %s", rr.Code, rr.Body.String())
	}
	a := store.Get(42)
	if a.Status != AssignmentStarted || !a.WarmBind || a.VMID != "vm-1" {
		t.Fatalf("after started = %+v", a)
	}

	// Finished
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/finished", "agent-shared-token", api.JobFinishedRequest{
		AgentID:  "host-1",
		JobID:    42,
		Outcome:  "success",
		VMID:     "vm-1",
		WarmBind: true,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("finished status = %d %s", rr.Code, rr.Body.String())
	}
	a = store.Get(42)
	if a.Status != AssignmentFinished || a.Outcome != "success" {
		t.Fatalf("after finished = %+v", a)
	}
	if a.EncodedJITConfig != "" {
		t.Fatal("jit should be cleared after finish")
	}
}

func TestAgentAPI_WebhookThenClaim(t *testing.T) {
	srv, store := testAgentServer(t)

	body := fixture(t, "workflow_job_queued_temperci.json")
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign("super-secret", body))
	req.Header.Set("X-GitHub-Event", "workflow_job")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("webhook status = %d", rr.Code)
	}
	if store.PendingLen() != 1 {
		t.Fatalf("pending = %d", store.PendingLen())
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{AgentID: "local", FreeSlots: 1})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var claim api.ClaimResponse
	if err := json.NewDecoder(rr.Body).Decode(&claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job == nil || claim.Job.JobID != 991001 {
		t.Fatalf("claim = %+v", claim.Job)
	}
	if claim.Job.EncodedJITConfig != "jit-secret" {
		t.Errorf("jit = %q", claim.Job.EncodedJITConfig)
	}
}

func TestAgentAPI_MissingAuthHeader(t *testing.T) {
	srv, _ := testAgentServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/agent/register", bytes.NewReader([]byte(`{"agent_id":"x"}`)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestAgentAPI_ClaimRequiresFreeSlots(t *testing.T) {
	srv, store := testAgentServer(t)
	store.Put(&Assignment{
		JobID:            7,
		EncodedJITConfig: "jit",
		Status:           AssignmentMinted,
	})
	// No free slots → job stays pending.
	req := agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID:   "full-host",
		FreeSlots: 0,
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	var claim api.ClaimResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job != nil {
		t.Fatalf("expected no job when free_slots=0, got %+v", claim.Job)
	}
	if store.PendingLen() != 1 {
		t.Fatalf("pending = %d", store.PendingLen())
	}
	// With capacity → claim succeeds.
	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "agent-shared-token", api.ClaimRequest{
		AgentID:   "full-host",
		FreeSlots: 1,
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if err := json.Unmarshal(rr.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	if claim.Job == nil || claim.Job.JobID != 7 {
		t.Fatalf("claim = %+v", claim.Job)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv, store := testAgentServer(t)
	_ = srv.Agents().Register(api.RegisterRequest{AgentID: "a1", Capacity: 2, MaxCapacity: 2})
	store.Put(&Assignment{JobID: 1, Status: AssignmentMinted})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var m api.ControlMetrics
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.AgentsRegistered != 1 || m.JobsPending != 1 || m.JobsMinted != 1 {
		t.Fatalf("metrics = %+v", m)
	}
}
