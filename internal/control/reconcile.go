package control

import (
	"context"
	"log/slog"
	"time"
)

// RunnerDeleter removes a stuck org self-hosted runner registration (GitHub API).
// Implementations must not log secrets. Missing runner (404) should return nil.
// installationID may be 0 when the implementation has a default.
type RunnerDeleter interface {
	DeleteOrgRunner(ctx context.Context, org string, runnerID, installationID int64) error
}

// Reconciler periodically cleans stuck assignments and optional GitHub runner regs.
type Reconciler struct {
	Store  *AssignmentStore
	Delete RunnerDeleter
	// StuckAfter marks assigned/started jobs as stuck when older than this.
	StuckAfter time.Duration
	// StaleMintedAfter fails minted jobs never claimed (0 = disabled).
	StaleMintedAfter time.Duration
	// Interval between reconcile passes.
	Interval time.Duration
	// KillVM optionally tears down the guest when a started/assigned job is
	// marked stuck (dashboard cancel uses the same cmd queue).
	KillVM func(agentID, vmID string, jobID int64)
	Log    *slog.Logger
	now    func() time.Time
}

// ReconcileOnce runs a single reconciliation pass.
// Returns the number of jobs marked stuck/failed.
func (r *Reconciler) ReconcileOnce(ctx context.Context) int {
	if r.Store == nil {
		return 0
	}
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	nowFn := r.now
	if nowFn == nil {
		nowFn = time.Now
	}
	now := nowFn().UTC()
	n := 0

	if r.StuckAfter > 0 {
		for _, a := range r.Store.ListStuck(now, r.StuckAfter) {
			if err := ctx.Err(); err != nil {
				return n
			}
			msg := "assignment stuck past deadline"
			_ = r.Store.MarkFinished(a.JobID, a.AssignedAgentID, "stuck", a.VMID, a.WarmBind, msg)
			if r.KillVM != nil && a.AssignedAgentID != "" && a.VMID != "" {
				r.KillVM(a.AssignedAgentID, a.VMID, a.JobID)
			}
			// Clear secret if still present (MarkFinished also clears).
			if r.Delete != nil && a.RunnerID != 0 && a.Org != "" {
				if err := r.Delete.DeleteOrgRunner(ctx, a.Org, a.RunnerID, a.InstallationID); err != nil {
					log.Warn("delete stuck runner failed",
						"job_id", a.JobID,
						"runner_id", a.RunnerID,
						"org", a.Org,
						"err", err,
					)
				} else {
					log.Info("deleted stuck runner registration",
						"job_id", a.JobID,
						"runner_id", a.RunnerID,
						"org", a.Org,
					)
				}
			}
			log.Warn("marked stuck assignment finished",
				"job_id", a.JobID,
				"agent_id", a.AssignedAgentID,
				"status_was", a.Status,
				// never log EncodedJITConfig
			)
			n++
		}
	}

	if r.StaleMintedAfter > 0 {
		for _, a := range r.Store.ListStaleMinted(now, r.StaleMintedAfter) {
			if err := ctx.Err(); err != nil {
				return n
			}
			r.Store.MarkFailed(a.JobID, "minted job expired without claim")
			if r.Delete != nil && a.RunnerID != 0 && a.Org != "" {
				if err := r.Delete.DeleteOrgRunner(ctx, a.Org, a.RunnerID, a.InstallationID); err != nil {
					log.Warn("delete stale minted runner failed",
						"job_id", a.JobID,
						"runner_id", a.RunnerID,
						"err", err,
					)
				}
			}
			log.Warn("expired unclaimed minted job", "job_id", a.JobID, "runner_id", a.RunnerID)
			n++
		}
	}

	r.Store.PruneFinished(assignmentFinishedRetention)
	return n
}

// assignmentFinishedRetention is how long finished/failed rows are kept.
const assignmentFinishedRetention = 7 * 24 * time.Hour

// Run loops until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	// Immediate first pass.
	r.ReconcileOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.ReconcileOnce(ctx)
		}
	}
}
