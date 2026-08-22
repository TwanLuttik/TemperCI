package control

import (
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

func TestShouldRefreshJobMeta(t *testing.T) {
	started := &Assignment{Status: AssignmentStarted}
	finished := &Assignment{Status: AssignmentFinished}
	fresh := &jobMetaCache{
		at:  time.Now(),
		job: &github.WorkflowJobDetail{Steps: []github.WorkflowJobStep{{Name: "a"}}},
	}
	if shouldRefreshJobMeta(started, fresh) {
		t.Fatal("fresh in-progress snapshot should not refresh")
	}
	if shouldRefreshJobMeta(finished, fresh) {
		t.Fatal("finished snapshot with steps should not refresh")
	}
	stale := &jobMetaCache{
		at:  time.Now().Add(-3 * time.Second),
		job: &github.WorkflowJobDetail{Steps: []github.WorkflowJobStep{{Name: "a"}}},
	}
	if !shouldRefreshJobMeta(started, stale) {
		t.Fatal("started job older than 2s should refresh")
	}
	if shouldRefreshJobMeta(finished, stale) {
		t.Fatal("finished job should keep cached steps")
	}
	empty := &jobMetaCache{at: time.Now().Add(-time.Minute), job: &github.WorkflowJobDetail{}}
	if !shouldRefreshJobMeta(finished, empty) {
		t.Fatal("finished job with no steps should keep trying")
	}

	open := &jobMetaCache{
		at: time.Now().Add(-3 * time.Second),
		job: &github.WorkflowJobDetail{
			Status: "completed",
			Steps: []github.WorkflowJobStep{
				{Name: "Post Checkout code", Status: "in_progress", StartedAt: "2026-08-22T20:46:14Z"},
			},
		},
	}
	if !shouldRefreshJobMeta(finished, open) {
		t.Fatal("finished job with an in-progress step must keep refreshing")
	}
	settled := &jobMetaCache{
		at: time.Now().Add(-time.Minute),
		job: &github.WorkflowJobDetail{
			Status: "completed",
			Steps: []github.WorkflowJobStep{
				{Name: "Post Checkout code", Status: "completed", Conclusion: "success"},
			},
		},
	}
	if shouldRefreshJobMeta(finished, settled) {
		t.Fatal("finished job with settled steps should not refresh")
	}
}
