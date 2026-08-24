package control

import (
	"context"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

func githubJobOutcome(action, conclusion string) string {
	if c := strings.ToLower(strings.TrimSpace(conclusion)); c != "" {
		return c
	}
	if strings.EqualFold(strings.TrimSpace(action), "cancelled") {
		return "cancelled"
	}
	return "failure"
}

// finishFromGitHub applies a workflow_job completed/cancelled webhook.
// Already-terminal assignments are a no-op so GitHub retries do not re-kill.
func (s *Server) finishFromGitHub(ctx context.Context, ev *github.WorkflowJobEvent) *HandleResult {
	if ev == nil || ev.WorkflowJob.ID == 0 {
		return &HandleResult{Ignored: true, Reason: "no_job"}
	}
	a := s.store.Get(ev.WorkflowJob.ID)
	if a == nil {
		return &HandleResult{Ignored: true, Reason: "unknown_job"}
	}
	if a.Status == AssignmentFinished || a.Status == AssignmentFailed {
		return &HandleResult{Ignored: true, Reason: "already_terminal"}
	}
	outcome := githubJobOutcome(ev.Action, ev.WorkflowJob.Conclusion)
	msg := "github workflow_job " + ev.Action
	if ev.WorkflowJob.Conclusion != "" {
		msg += ": " + ev.WorkflowJob.Conclusion
	}
	switch a.Status {
	case AssignmentMinted:
		if outcome == "cancelled" {
			_ = s.store.Cancel(a.JobID, msg)
		} else {
			s.store.MarkFailed(a.JobID, msg)
		}
	default:
		if err := s.store.MarkFinished(a.JobID, a.AssignedAgentID, outcome, a.VMID, a.WarmBind, msg); err != nil {
			s.log.Warn("github complete mark finished", "job_id", a.JobID, "err", err)
			return &HandleResult{Ignored: true, Reason: "mark_finished"}
		}
	}
	if a.AssignedAgentID != "" && a.VMID != "" {
		s.cmdq.enqueueKill(a.AssignedAgentID, a.VMID, a.JobID)
	}
	s.deleteJobRunner(ctx, a)
	s.recordJobEvent(a.JobID, "control", "warn", msg)
	s.PublishSnapshot()
	return &HandleResult{Assignment: s.store.Get(a.JobID)}
}
