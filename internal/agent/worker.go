package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// Worker polls the control plane for jobs, binds warm VMs, and reports lifecycle.
type Worker struct {
	Client *ControlClient
	Pool   *Pool
	Log    *slog.Logger
	// PollInterval between empty claims (default 500ms).
	PollInterval time.Duration
	// JobSimulate holds how long to wait after bind before treating the job as done.
	// On fake/dev without a real runner exit signal, this simulates job completion.
	// Set to 0 to finish immediately after bind (tests). Production with real runner
	// should replace this with runner process wait.
	JobSimulate time.Duration
	// JobDeadline is the max time from bind to force destroy + report timeout.
	// 0 disables the deadline (JobSimulate-only path).
	JobDeadline time.Duration
	// Capacity is max concurrent jobs (usually MaxReady). Free slots = Capacity - Busy.
	Capacity int
}

// Run registers then polls until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) error {
	if w.Client == nil || w.Pool == nil {
		return fmt.Errorf("agent: worker requires Client and Pool")
	}
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	poll := w.PollInterval
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	if w.Capacity <= 0 {
		w.Capacity = 1
	}

	if err := w.Client.Register(ctx, w.snapshot()); err != nil {
		return fmt.Errorf("agent: register: %w", err)
	}
	log.Info("agent worker registered",
		"agent_id", w.Client.AgentID,
		"capacity", w.Capacity,
		"free_slots", w.snapshot().FreeSlots,
	)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cap := w.snapshot()
		job, err := w.Client.Claim(ctx, cap)
		if err != nil {
			log.Error("claim failed", "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll):
			}
			continue
		}
		if job == nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll):
			}
			continue
		}
		if err := w.handleJob(ctx, job); err != nil {
			log.Error("handle job failed", "job_id", job.JobID, "err", err)
		}
	}
}

func (w *Worker) snapshot() CapacitySnapshot {
	c := w.Pool.Counts()
	free := w.Capacity - c.Busy
	if free < 0 {
		free = 0
	}
	return CapacitySnapshot{
		MaxCapacity: w.Capacity,
		FreeSlots:   free,
		Warm:        c.Warm,
		Busy:        c.Busy,
	}
}

func (w *Worker) handleJob(ctx context.Context, job *api.JobAssignment) error {
	log := w.Log
	if log == nil {
		log = slog.Default()
	}
	jobIDStr := strconv.FormatInt(job.JobID, 10)
	payload := JobPayload{
		JobID:      jobIDStr,
		RunnerName: job.RunnerName,
		Labels:     append([]string(nil), job.Labels...),
		JITConfig:  job.EncodedJITConfig,
	}

	res, err := w.Pool.Bind(ctx, payload)
	// Clear secret from payload as soon as bind returns.
	payload.JITConfig = ""
	job.EncodedJITConfig = ""

	if err != nil {
		_ = w.Client.ReportFinished(ctx, job.JobID, "error", "", false, err.Error())
		return err
	}

	if err := w.Client.ReportStarted(ctx, job.JobID, string(res.VMID), res.WarmStart); err != nil {
		log.Error("report started failed", "job_id", job.JobID, "err", err)
	}

	log.Info("job running",
		"job_id", job.JobID,
		"vm_id", string(res.VMID),
		"warm_bind", res.WarmStart,
	)

	outcome, waitErr := w.waitForJob(ctx)
	if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		_ = w.Pool.JobFinished(context.Background(), res.VMID, "cancelled")
		_ = w.Client.ReportFinished(context.Background(), job.JobID, "cancelled", string(res.VMID), res.WarmStart, waitErr.Error())
		return waitErr
	}
	if outcome == "timeout" {
		log.Warn("job deadline exceeded; force destroy",
			"job_id", job.JobID,
			"vm_id", string(res.VMID),
			"deadline", w.JobDeadline.String(),
		)
		if err := w.Pool.JobFinished(ctx, res.VMID, "timeout"); err != nil {
			_ = w.Client.ReportFinished(ctx, job.JobID, "timeout", string(res.VMID), res.WarmStart, err.Error())
			return err
		}
		if err := w.Client.ReportFinished(ctx, job.JobID, "timeout", string(res.VMID), res.WarmStart, "job deadline exceeded"); err != nil {
			return err
		}
		return nil
	}

	if err := w.Pool.JobFinished(ctx, res.VMID, "success"); err != nil {
		_ = w.Client.ReportFinished(ctx, job.JobID, "error", string(res.VMID), res.WarmStart, err.Error())
		return err
	}
	if err := w.Client.ReportFinished(ctx, job.JobID, "success", string(res.VMID), res.WarmStart, ""); err != nil {
		return err
	}
	log.Info("job complete",
		"job_id", job.JobID,
		"vm_id", string(res.VMID),
		"warm_bind", res.WarmStart,
	)
	return nil
}

// waitForJob blocks until JobSimulate elapses, JobDeadline hits, or ctx cancels.
// With JobSimulate==0 and no deadline, returns success immediately (test default).
func (w *Worker) waitForJob(ctx context.Context) (outcome string, err error) {
	sim := w.JobSimulate
	if sim < 0 {
		sim = 0
	}
	deadline := w.JobDeadline

	// Immediate finish path used by unit/e2e tests.
	if sim == 0 && deadline <= 0 {
		return "success", nil
	}

	var deadlineCh <-chan time.Time
	if deadline > 0 {
		t := time.NewTimer(deadline)
		defer t.Stop()
		deadlineCh = t.C
	}

	var simCh <-chan time.Time
	if sim > 0 {
		t := time.NewTimer(sim)
		defer t.Stop()
		simCh = t.C
	} else if deadline > 0 {
		// No simulate: wait only on deadline (or cancel) — used when runner exit not wired.
		select {
		case <-ctx.Done():
			return "cancelled", ctx.Err()
		case <-deadlineCh:
			return "timeout", context.DeadlineExceeded
		}
	}

	select {
	case <-ctx.Done():
		return "cancelled", ctx.Err()
	case <-deadlineCh:
		return "timeout", context.DeadlineExceeded
	case <-simCh:
		return "success", nil
	}
}
