package control

import (
	"context"
	"strconv"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

// recoverStolenRunner remints the assignment that minted runner_name when
// GitHub starts a different job on that runner (label-FIFO JIT).
func (s *Server) recoverStolenRunner(ctx context.Context, ev *github.WorkflowJobEvent) *HandleResult {
	if ev == nil || ev.WorkflowJob.ID == 0 {
		return &HandleResult{Ignored: true, Reason: "no_job"}
	}
	runnerName := strings.TrimSpace(ev.WorkflowJob.RunnerName)
	if runnerName == "" || s.handler == nil {
		return &HandleResult{Ignored: true, Reason: "no_runner"}
	}
	minted := s.store.GetByRunnerName(runnerName)
	if minted == nil {
		return &HandleResult{Ignored: true, Reason: "unknown_runner"}
	}
	if minted.JobID == ev.WorkflowJob.ID {
		return &HandleResult{Ignored: true, Reason: "runner_matches_job"}
	}
	reason := "runner " + runnerName + " accepted job " +
		strconv.FormatInt(ev.WorkflowJob.ID, 10) + " (" + ev.WorkflowJob.Name + ")"
	got, err := s.handler.Remint(ctx, minted.JobID, reason)
	if err != nil {
		s.log.Error("remint stolen runner", "job_id", minted.JobID, "err", err)
		return &HandleResult{Ignored: true, Reason: "remint_failed"}
	}
	s.recordJobEvent(minted.JobID, "control", "warn", reason+"; reminted JIT")
	s.PublishSnapshot()
	return &HandleResult{Assignment: got}
}

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
// GitHub's conclusion is authoritative. Already-terminal assignments still
// take that outcome (agent kill/stale-log races) but do not re-kill.
func (s *Server) finishFromGitHub(ctx context.Context, ev *github.WorkflowJobEvent) *HandleResult {
	if ev == nil || ev.WorkflowJob.ID == 0 {
		return &HandleResult{Ignored: true, Reason: "no_job"}
	}
	a := s.store.Get(ev.WorkflowJob.ID)
	if a == nil {
		return &HandleResult{Ignored: true, Reason: "unknown_job"}
	}
	outcome := githubJobOutcome(ev.Action, ev.WorkflowJob.Conclusion)
	msg := "github workflow_job " + ev.Action
	if ev.WorkflowJob.Conclusion != "" {
		msg += ": " + ev.WorkflowJob.Conclusion
	}
	// Already terminal: still apply GitHub's conclusion (agent may have
	// reported cancelled/failure from a kill race or stale runner.log).
	// Do not re-kill — retries must stay idempotent.
	if a.Status == AssignmentFinished || a.Status == AssignmentFailed {
		if err := s.store.ApplyGitHubOutcome(a.JobID, outcome, msg); err != nil {
			s.log.Warn("github complete apply outcome", "job_id", a.JobID, "err", err)
			return &HandleResult{Ignored: true, Reason: "apply_outcome"}
		}
		s.recordJobEvent(a.JobID, "control", "warn", msg)
		s.PublishSnapshot()
		return &HandleResult{Ignored: true, Reason: "already_terminal", Assignment: s.store.Get(a.JobID)}
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
