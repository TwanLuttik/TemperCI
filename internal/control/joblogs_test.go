package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

func TestJobLogs_FinishedPersistsAndDetailAPI(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:        99,
		RepoFullName: "acme/demo",
		Status:       AssignmentMinted,
		RunnerName:   "temperci-job-99",
	})
	h := NewHandler(&mockMinter{}, as, HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		Store:         as,
		WebhookSecret: "super-secret",
		AgentToken:    "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			Store:  db,
		},
	})

	// Claim + start + finish with logs.
	req := agentReq(t, http.MethodPost, "/v1/agent/jobs/claim", "tok", api.ClaimRequest{AgentID: "h1", FreeSlots: 1})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("claim %d %s", rr.Code, rr.Body.String())
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/started", "tok", api.JobStartedRequest{AgentID: "h1", JobID: 99, VMID: "vm-1", WarmBind: true})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("started %d %s", rr.Code, rr.Body.String())
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/finished", "tok", api.JobFinishedRequest{
		AgentID:    "h1",
		JobID:      99,
		Outcome:    "failure",
		VMID:       "vm-1",
		Error:      "exit 95",
		RunnerLog:  "Connected to GitHub\njob failed",
		AgentLog:   "starting runner",
		ConsoleLog: "login:",
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("finished %d %s", rr.Code, rr.Body.String())
	}

	got, err := db.GetJobLog(99)
	if err != nil {
		t.Fatal(err)
	}
	if got.RunnerLog == "" || got.AgentLog == "" || got.ConsoleLog == "" {
		t.Fatalf("missing guest logs: %+v", got)
	}
	if len(got.Events) < 2 {
		t.Fatalf("expected timeline events, got %#v", got.Events)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/jobs/99", nil)
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK   bool `json:"ok"`
		Job  struct {
			JobID   int64  `json:"job_id"`
			Outcome string `json:"outcome"`
			Error   string `json:"error"`
		} `json:"job"`
		Logs store.JobLog `json:"logs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Job.JobID != 99 || body.Job.Outcome != "failure" || body.Logs.RunnerLog == "" {
		t.Fatalf("detail body = %+v", body)
	}
}
