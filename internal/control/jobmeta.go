package control

import (
	"context"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

type jobGetter interface {
	GetJob(ctx context.Context, owner, repo string, jobID, installationID int64) (*github.WorkflowJobDetail, error)
}

type jobMetaCache struct {
	at  time.Time
	job *github.WorkflowJobDetail
}

func (s *Server) ensureJobMeta(r interface{ Context() context.Context }, a *Assignment) *github.WorkflowJobDetail {
	if s == nil || a == nil {
		return nil
	}
	if cached := s.lookupJobMeta(a.JobID); cached != nil && !shouldRefreshJobMeta(a, cached) {
		return cached.job
	}
	g, ok := s.jobLogs.(jobGetter)
	if !ok || g == nil {
		return s.cachedJob(a.JobID)
	}
	owner, repo, ok := splitRepo(a.RepoFullName)
	if !ok {
		return s.cachedJob(a.JobID)
	}
	ctx := context.Background()
	if r != nil && r.Context() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.Context(), 8*time.Second)
		defer cancel()
	}
	job, err := g.GetJob(ctx, owner, repo, a.JobID, a.InstallationID)
	if err != nil || job == nil {
		return s.cachedJob(a.JobID)
	}
	s.storeJobMeta(a.JobID, job)
	return job
}

func shouldRefreshJobMeta(a *Assignment, cached *jobMetaCache) bool {
	if cached == nil || cached.job == nil {
		return true
	}
	finished := a.Status == AssignmentFinished || a.Status == AssignmentFailed
	if finished && jobStepsSettled(cached.job) {
		return false
	}
	min := 8 * time.Second
	if a.Status == AssignmentStarted || (finished && !jobStepsSettled(cached.job)) {
		min = 2 * time.Second
	}
	return time.Since(cached.at) >= min
}

func jobStepsSettled(job *github.WorkflowJobDetail) bool {
	if job == nil || len(job.Steps) == 0 {
		return false
	}
	for _, s := range job.Steps {
		switch strings.ToLower(s.Status) {
		case "in_progress", "queued", "pending", "waiting":
			return false
		}
	}
	return true
}

func (s *Server) lookupJobMeta(jobID int64) *jobMetaCache {
	s.jobMetaMu.Lock()
	defer s.jobMetaMu.Unlock()
	if s.jobMeta == nil {
		return nil
	}
	c, ok := s.jobMeta[jobID]
	if !ok {
		return nil
	}
	cp := c
	return &cp
}

func (s *Server) cachedJob(jobID int64) *github.WorkflowJobDetail {
	if c := s.lookupJobMeta(jobID); c != nil {
		return c.job
	}
	return nil
}

func (s *Server) storeJobMeta(jobID int64, job *github.WorkflowJobDetail) {
	s.jobMetaMu.Lock()
	defer s.jobMetaMu.Unlock()
	if s.jobMeta == nil {
		s.jobMeta = make(map[int64]jobMetaCache)
	}
	s.jobMeta[jobID] = jobMetaCache{at: time.Now(), job: job}
}
