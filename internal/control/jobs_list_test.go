package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/github"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

func TestJobsList_IncludesStepsAndTimestampsForStartedJob(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	started := time.Date(2026, 8, 22, 18, 0, 0, 0, time.UTC)
	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:          101,
		RunID:          9,
		RepoFullName:   "acme/demo",
		InstallationID: 5,
		Status:         AssignmentStarted,
		CreatedAt:      started.Add(-3 * time.Second),
		AssignedAt:     started.Add(-2 * time.Second),
		StartedAt:      started,
	})
	stub := &stubJobLogs{
		job: &github.WorkflowJobDetail{
			ID:     101,
			Name:   "API Tests",
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/jobs", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list %d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Jobs []struct {
			JobID      int64                    `json:"job_id"`
			AssignedAt time.Time                `json:"assigned_at"`
			StartedAt  time.Time                `json:"started_at"`
			Steps      []github.WorkflowJobStep `json:"steps"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Jobs) != 1 || body.Jobs[0].JobID != 101 {
		t.Fatalf("jobs = %+v", body.Jobs)
	}
	if body.Jobs[0].StartedAt.IsZero() || !body.Jobs[0].StartedAt.Equal(started) {
		t.Fatalf("started_at = %v want %v", body.Jobs[0].StartedAt, started)
	}
	if body.Jobs[0].AssignedAt.IsZero() {
		t.Fatal("assigned_at missing")
	}
	if stub.sawJob.jobID != 0 {
		t.Fatalf("list must not fetch GitHub job meta, got %+v", stub.sawJob)
	}
	_ = stub
}

func TestBuildSnapshot_IncludesTimestampsAndCachedSteps(t *testing.T) {
	as := NewAssignmentStore()
	started := time.Date(2026, 8, 22, 18, 1, 0, 0, time.UTC)
	as.Put(&Assignment{
		JobID:        202,
		RepoFullName: "acme/demo",
		Status:       AssignmentStarted,
		CreatedAt:    started.Add(-time.Second),
		AssignedAt:   started.Add(-500 * time.Millisecond),
		StartedAt:    started,
	})
	srv := NewServer(ServerConfig{Store: as, AgentToken: "tok"})
	srv.storeJobMeta(202, &github.WorkflowJobDetail{
		ID:   202,
		Name: "e2e",
		Steps: []github.WorkflowJobStep{
			{Name: "Checkout", Status: "in_progress", Number: 1},
		},
	})

	snap := srv.BuildSnapshot()
	if len(snap.Jobs) != 1 || snap.Jobs[0].JobID != 202 {
		t.Fatalf("snapshot jobs = %+v", snap.Jobs)
	}
	if snap.Jobs[0].StartedAt.IsZero() || !snap.Jobs[0].StartedAt.Equal(started) {
		t.Fatalf("snapshot started_at = %v", snap.Jobs[0].StartedAt)
	}
	if snap.Jobs[0].AssignedAt.IsZero() {
		t.Fatal("snapshot assigned_at missing")
	}
	if len(snap.Jobs[0].Steps) != 1 || snap.Jobs[0].Steps[0].Name != "Checkout" {
		t.Fatalf("snapshot steps = %#v", snap.Jobs[0].Steps)
	}
}
