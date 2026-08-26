package control

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/github"
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
		OK  bool `json:"ok"`
		Job struct {
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

type stubJobLogs struct {
	text   string
	err    error
	job    *github.WorkflowJobDetail
	jobErr error
	saw    struct {
		owner, repo string
		jobID       int64
		install     int64
	}
	sawJob struct {
		owner, repo string
		jobID       int64
		install     int64
		n           int
	}
}

func (s *stubJobLogs) DownloadJobLogs(_ context.Context, owner, repo string, jobID, installationID int64) (string, error) {
	s.saw.owner, s.saw.repo, s.saw.jobID, s.saw.install = owner, repo, jobID, installationID
	return s.text, s.err
}

func (s *stubJobLogs) GetJob(_ context.Context, owner, repo string, jobID, installationID int64) (*github.WorkflowJobDetail, error) {
	s.sawJob.owner, s.sawJob.repo, s.sawJob.jobID, s.sawJob.install = owner, repo, jobID, installationID
	s.sawJob.n++
	if s.jobErr != nil {
		return nil, s.jobErr
	}
	return s.job, nil
}

func TestJobDetail_FetchesGitHubWorkflowLog(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:          77,
		RepoFullName:   "acme/demo",
		InstallationID: 5,
		Status:         AssignmentFinished,
		Outcome:        "success",
	})
	stub := &stubJobLogs{text: "##[group]Run actions/checkout@v4\nSynced\n"}
	srv := NewServer(ServerConfig{
		Store:      as,
		AgentToken: "tok",
		JobLogs:    stub,
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			Store:  db,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/77", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Logs store.JobLog `json:"logs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Logs.WorkflowLog, "actions/checkout") {
		t.Fatalf("workflow log = %q", body.Logs.WorkflowLog)
	}
	if stub.saw.owner != "acme" || stub.saw.repo != "demo" || stub.saw.jobID != 77 {
		t.Fatalf("download args = %+v", stub.saw)
	}
	got, err := db.GetJobLog(77)
	if err != nil || !strings.Contains(got.WorkflowLog, "actions/checkout") {
		t.Fatalf("persisted = %+v err=%v", got, err)
	}
}

func TestJobDetail_IncludesWorkflowSteps(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:          88,
		RepoFullName:   "acme/demo",
		InstallationID: 5,
		Status:         AssignmentStarted,
	})
	stub := &stubJobLogs{
		job: &github.WorkflowJobDetail{
			ID:     88,
			Name:   "e2e",
			Status: "in_progress",
			Steps: []github.WorkflowJobStep{
				{Name: "Set up job", Status: "completed", Conclusion: "success", Number: 1},
				{Name: "Run tests", Status: "in_progress", Number: 2},
				{Name: "Complete job", Status: "pending", Number: 3},
			},
		},
	}
	srv := NewServer(ServerConfig{
		Store:      as,
		AgentToken: "tok",
		JobLogs:    stub,
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			Store:  db,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/88", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Job struct {
			JobID int64                    `json:"job_id"`
			Name  string                   `json:"name"`
			Steps []github.WorkflowJobStep `json:"steps"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Job.JobID != 88 || body.Job.Name != "e2e" {
		t.Fatalf("job = %+v", body.Job)
	}
	if len(body.Job.Steps) != 3 || body.Job.Steps[1].Name != "Run tests" || body.Job.Steps[1].Status != "in_progress" {
		t.Fatalf("steps = %#v", body.Job.Steps)
	}
	if stub.sawJob.owner != "acme" || stub.sawJob.repo != "demo" || stub.sawJob.jobID != 88 || stub.sawJob.install != 5 {
		t.Fatalf("get job args = %+v", stub.sawJob)
	}
}

func TestJobDetail_StepsFetchFailureIsIgnored(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:          89,
		RepoFullName:   "acme/demo",
		InstallationID: 5,
		Status:         AssignmentStarted,
	})
	stub := &stubJobLogs{jobErr: context.DeadlineExceeded}
	srv := NewServer(ServerConfig{
		Store:      as,
		AgentToken: "tok",
		JobLogs:    stub,
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			Store:  db,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs/89", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		OK  bool `json:"ok"`
		Job struct {
			JobID int64                    `json:"job_id"`
			Steps []github.WorkflowJobStep `json:"steps"`
		} `json:"job"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.Job.JobID != 89 || len(body.Job.Steps) != 0 {
		t.Fatalf("body = %+v", body)
	}
}

func TestJobLogs_LiveUploadBroadcastsWS(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var last []byte
	hub := NewHub(nil)
	hub.onSend = func(b []byte) { last = append([]byte(nil), b...) }
	srv := NewServer(ServerConfig{
		Hub:        hub,
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			Store:  db,
		},
	})

	first := "##[group]Run checkout\n"
	req := agentReq(t, http.MethodPost, "/v1/agent/jobs/logs", "tok", api.JobLogsRequest{
		AgentID:        "h1",
		JobID:          77,
		WorkflowOffset: 0,
		WorkflowAppend: first,
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logs %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(string(last), `"type":"job_logs"`) || !strings.Contains(string(last), "Run checkout") {
		t.Fatalf("ws frame = %s", last)
	}

	req = agentReq(t, http.MethodPost, "/v1/agent/jobs/logs", "tok", api.JobLogsRequest{
		AgentID:        "h1",
		JobID:          77,
		WorkflowOffset: len(first),
		WorkflowAppend: "Synced\n",
	})
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("append %d %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(string(last), "Synced") || !strings.Contains(string(last), "workflow_append") {
		t.Fatalf("ws delta frame = %s", last)
	}
	if srv.liveWorkflow(77) != first+"Synced\n" {
		t.Fatalf("live = %q", srv.liveWorkflow(77))
	}
}
