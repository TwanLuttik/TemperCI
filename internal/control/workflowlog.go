package control

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

// JobLogDownloader fetches official GitHub Actions job logs (step output).
type JobLogDownloader interface {
	DownloadJobLogs(ctx context.Context, owner, repo string, jobID, installationID int64) (string, error)
}

func officialWorkflowLog(s string) bool {
	return strings.Contains(s, "##[group]") || strings.Contains(s, "##[section]")
}

func (s *Server) ensureWorkflowLog(r *http.Request, a *Assignment, logs *store.JobLog) {
	if s == nil || logs == nil || a == nil || s.jobLogs == nil {
		return
	}
	owner, repo, ok := splitRepo(a.RepoFullName)
	if !ok {
		return
	}
	finished := a.Status == AssignmentFinished || a.Status == AssignmentFailed
	started := a.Status == AssignmentStarted
	if !finished && !started {
		return
	}
	if finished && officialWorkflowLog(logs.WorkflowLog) {
		return
	}
	if !s.allowWorkflowFetch(a.JobID, started) {
		return
	}
	ctx := context.Background()
	if r != nil && r.Context() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
	}
	text, err := s.jobLogs.DownloadJobLogs(ctx, owner, repo, a.JobID, a.InstallationID)
	if err != nil || !store.AcceptWorkflowLog(text) {
		if s.log != nil && err != nil && finished {
			s.log.Info("github job logs unavailable", "job_id", a.JobID, "err", err)
		}
		return
	}
	logs.WorkflowLog = text
	if db := s.jobDB(); db != nil {
		_ = db.SetWorkflowLog(a.JobID, text)
	}
}

func (s *Server) allowWorkflowFetch(jobID int64, live bool) bool {
	if s.wfFetchAt == nil {
		return true
	}
	s.wfFetchMu.Lock()
	defer s.wfFetchMu.Unlock()
	min := 8 * time.Second
	if live {
		min = 4 * time.Second
	}
	if last, ok := s.wfFetchAt[jobID]; ok && time.Since(last) < min {
		return false
	}
	s.wfFetchAt[jobID] = time.Now()
	return true
}

func splitRepo(full string) (owner, repo string, ok bool) {
	full = strings.TrimSpace(full)
	i := strings.IndexByte(full, '/')
	if i <= 0 || i == len(full)-1 {
		return "", "", false
	}
	if strings.Contains(full[i+1:], "/") {
		return "", "", false
	}
	return full[:i], full[i+1:], true
}
