package control

import (
	"time"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

const workflowPersistEvery = time.Second

func (s *Server) liveWorkflow(jobID int64) string {
	if s == nil {
		return ""
	}
	s.liveMu.Lock()
	defer s.liveMu.Unlock()
	return s.liveWF[jobID]
}

func (s *Server) rememberWorkflow(jobID int64, text string, persistNow bool) {
	if s == nil || jobID == 0 || text == "" {
		return
	}
	s.liveMu.Lock()
	if s.liveWF == nil {
		s.liveWF = make(map[int64]string)
		s.liveWFAt = make(map[int64]time.Time)
	}
	s.liveWF[jobID] = text
	due := persistNow || s.liveWFAt[jobID].IsZero() || time.Since(s.liveWFAt[jobID]) >= workflowPersistEvery
	if due {
		s.liveWFAt[jobID] = time.Now()
	}
	s.liveMu.Unlock()
	if !due {
		return
	}
	if db := s.jobDB(); db != nil {
		_ = db.SetWorkflowLog(jobID, text)
	}
}

func (s *Server) applyLiveAppend(jobID int64, offset int, chunk string) {
	if s == nil || jobID == 0 || chunk == "" {
		return
	}
	cur := s.liveWorkflow(jobID)
	if cur == "" {
		if db := s.jobDB(); db != nil {
			if got, err := db.GetJobLog(jobID); err == nil {
				cur = got.WorkflowLog
			}
		}
	}
	next, ok := store.ApplyWorkflowAppend(cur, offset, chunk)
	if !ok {
		return
	}
	s.rememberWorkflow(jobID, next, false)
}

func (s *Server) overlayLiveWorkflow(jobID int64, logs *store.JobLog) {
	if logs == nil {
		return
	}
	live := s.liveWorkflow(jobID)
	if live == "" {
		return
	}
	if logs.WorkflowLog == "" || len(live) >= len(logs.WorkflowLog) {
		logs.WorkflowLog = live
	}
}
