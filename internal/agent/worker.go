package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// Worker polls the control plane for jobs, binds warm VMs, and reports lifecycle.
type Worker struct {
	Client *ControlClient
	Pool   *Pool
	Log    *slog.Logger
	// PollInterval between empty claims (default 500ms).
	PollInterval time.Duration
	// JobSimulate holds how long to wait after bind before treating the job as done.
	// When > 0, skips real runner wait (dev/demo). When 0, waits for guest runner.exit.
	JobSimulate time.Duration
	// JobDeadline is the max time from bind to force destroy + report timeout.
	// 0 means default 6h when waiting on a real runner; still 0 = no deadline for simulate path.
	JobDeadline time.Duration
	// Capacity is max concurrent jobs (usually MaxReady). Free slots = Capacity - Busy.
	Capacity int
	// WaitRealRunner when true (default for firecracker) waits on GuestExec.WaitRunner.
	// Set false only for unit tests that do not implement WaitRunner beyond stubs.
	WaitRealRunner bool
	// Cache is the optional host-local Actions cache gateway.
	Cache *ghacache.Gateway

	// inflight counts claimed jobs whose handleJob goroutine has not returned.
	// Used so FreeSlots drops immediately on claim, before Pool.Busy updates.
	inflightMu sync.Mutex
	inflight   int
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
	if w.Capacity < 0 {
		w.Capacity = 0
	}

	if err := w.register(ctx); err != nil {
		return fmt.Errorf("agent: register: %w", err)
	}
	log.Info("agent worker registered",
		"agent_id", w.Client.AgentID,
		"capacity", w.Capacity,
		"free_slots", w.snapshot().FreeSlots,
	)

	// Heartbeat re-registers with microVM usage for the realtime dashboard.
	go w.heartbeat(ctx, log)

	var jobsWG sync.WaitGroup
	defer jobsWG.Wait()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cap := w.snapshot()
		if cap.FreeSlots <= 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(poll):
			}
			continue
		}
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
		jobsWG.Add(1)
		w.addInFlight(1)
		go func(job *api.JobAssignment) {
			defer jobsWG.Done()
			defer w.addInFlight(-1)
			if err := w.handleJob(ctx, job); err != nil {
				log.Error("handle job failed", "job_id", job.JobID, "err", err)
			}
		}(job)
	}
}

func (w *Worker) heartbeat(ctx context.Context, log *slog.Logger) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.register(ctx); err != nil {
				log.Error("heartbeat register failed", "err", err)
			}
		}
	}
}

func (w *Worker) addInFlight(delta int) {
	w.inflightMu.Lock()
	w.inflight += delta
	if w.inflight < 0 {
		w.inflight = 0
	}
	w.inflightMu.Unlock()
}

func (w *Worker) inFlight() int {
	w.inflightMu.Lock()
	defer w.inflightMu.Unlock()
	return w.inflight
}

func (w *Worker) snapshot() CapacitySnapshot {
	c := w.Pool.Counts()
	used := c.Busy
	if n := w.inFlight(); n > used {
		// Claimed but Bind has not yet incremented Busy (or JobFinished already ran).
		used = n
	}
	free := w.Capacity - used
	if free < 0 {
		free = 0
	}
	bindBudget := c.Warm + w.Pool.RemainingCreates()
	if bindBudget < free {
		free = bindBudget
	}
	if free < 0 {
		free = 0
	}
	var repos []string
	var cache *api.CacheUsage
	if w.Cache != nil && w.Cache.Store != nil {
		repos = w.Cache.Store.Repos()
		cache = CacheUsageFromStore(w.Cache.Store)
	}
	return CapacitySnapshot{
		MaxCapacity: w.Capacity,
		FreeSlots:   free,
		Warm:        c.Warm,
		Busy:        c.Busy,
		VMs:         w.Pool.ListUsage(),
		CachedRepos: repos,
		Cache:       cache,
		Resources:   w.Pool.HostResources(),
	}
}

func (w *Worker) register(ctx context.Context) error {
	ops, err := w.Client.Register(ctx, w.snapshot())
	if err != nil {
		return err
	}
	if len(ops) == 0 || w.Cache == nil || w.Cache.Store == nil {
		return nil
	}
	n, err := ApplyCacheOps(w.Cache.Store, ops)
	if err != nil {
		if w.Log != nil {
			w.Log.Error("apply cache ops", "err", err, "applied", n)
		}
		return err
	}
	if w.Log != nil && n > 0 {
		w.Log.Info("applied cache ops", "n", n)
	}
	return nil
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
		_ = w.finish(ctx, job.JobID, job.RepoFullName, "error", "", false, err.Error(), JobLogs{})
		return err
	}
	if w.Cache != nil && job.RepoFullName != "" {
		guestIP := w.Pool.GuestIP(res.VMID)
		if guestIP == "" {
			guestIP = "127.0.0.1"
		}
		w.Cache.BindRemote(guestIP, job.RepoFullName)
		defer w.Cache.UnbindRemote(guestIP)
	}

	if err := w.Client.ReportStarted(ctx, job.JobID, string(res.VMID), res.WarmStart); err != nil {
		log.Error("report started failed", "job_id", job.JobID, "err", err)
	}

	log.Info("job running",
		"job_id", job.JobID,
		"vm_id", string(res.VMID),
		"warm_bind", res.WarmStart,
	)

	outcome, waitErr := w.waitForJob(ctx, res.VMID)
	logs := w.collectLogs(res.VMID)
	if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		_ = w.Pool.JobFinished(context.Background(), res.VMID, "cancelled")
		_ = w.finish(context.Background(), job.JobID, job.RepoFullName, "cancelled", string(res.VMID), res.WarmStart, waitErr.Error(), logs)
		return waitErr
	}
	if outcome == "timeout" {
		log.Warn("job deadline exceeded; force destroy",
			"job_id", job.JobID,
			"vm_id", string(res.VMID),
			"deadline", w.JobDeadline.String(),
		)
		if err := w.Pool.JobFinished(ctx, res.VMID, "timeout"); err != nil {
			_ = w.finish(ctx, job.JobID, job.RepoFullName, "timeout", string(res.VMID), res.WarmStart, err.Error(), logs)
			return err
		}
		if err := w.finish(ctx, job.JobID, job.RepoFullName, "timeout", string(res.VMID), res.WarmStart, "job deadline exceeded", logs); err != nil {
			return err
		}
		return nil
	}

	// outcome success | failure from runner exit code
	if err := w.Pool.JobFinished(ctx, res.VMID, outcome); err != nil {
		_ = w.finish(ctx, job.JobID, job.RepoFullName, "error", string(res.VMID), res.WarmStart, err.Error(), logs)
		return err
	}
	if err := w.finish(ctx, job.JobID, job.RepoFullName, outcome, string(res.VMID), res.WarmStart, "", logs); err != nil {
		return err
	}
	log.Info("job complete",
		"job_id", job.JobID,
		"vm_id", string(res.VMID),
		"warm_bind", res.WarmStart,
		"outcome", outcome,
	)
	return nil
}

func (w *Worker) collectLogs(vmID vmm.ID) JobLogs {
	if w.Pool == nil || vmID == "" {
		return JobLogs{}
	}
	ArchiveConsole(w.Pool.HostLayout(), vmID)
	return CollectJobLogs(w.Pool.HostLayout(), vmID)
}

func (w *Worker) finish(ctx context.Context, jobID int64, repo, outcome, vmID string, warmBind bool, errMsg string, logs JobLogs) error {
	if w.Client == nil {
		return nil
	}
	if w.Cache != nil && repo != "" {
		st := w.Cache.TakeStats(repo)
		logs.CacheHits = st.Hits
		logs.CacheMisses = st.Misses
		logs.CacheBytesIn = st.BytesIn
		logs.CacheBytesOut = st.BytesOut
	}
	return w.Client.ReportFinishedLogs(ctx, jobID, outcome, vmID, warmBind, errMsg, logs)
}

// waitForJob blocks until the guest runner finishes, JobSimulate elapses, deadline hits, or ctx cancels.
func (w *Worker) waitForJob(ctx context.Context, vmID vmm.ID) (outcome string, err error) {
	sim := w.JobSimulate
	if sim < 0 {
		sim = 0
	}
	deadline := w.JobDeadline

	// Dev/demo: simulate wall-clock job duration.
	if sim > 0 {
		var deadlineCh <-chan time.Time
		if deadline > 0 {
			t := time.NewTimer(deadline)
			defer t.Stop()
			deadlineCh = t.C
		}
		t := time.NewTimer(sim)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return "cancelled", ctx.Err()
		case <-deadlineCh:
			return "timeout", context.DeadlineExceeded
		case <-t.C:
			return "success", nil
		}
	}

	// Production: wait for guest agent runner.exit (unless tests disable).
	if w.WaitRealRunner && w.Pool != nil && w.Pool.RunnerWaiter() != nil {
		log := w.Log
		if log == nil {
			log = slog.Default()
		}
		if deadline <= 0 {
			deadline = 6 * time.Hour
		}
		waitCtx, cancel := context.WithTimeout(ctx, deadline)
		defer cancel()
		log.Info("waiting for guest runner.exit",
			"vm_id", string(vmID),
			"deadline", deadline.String(),
		)
		code, werr := w.Pool.RunnerWaiter().WaitRunner(waitCtx, vmID)
		if werr != nil {
			if errors.Is(werr, context.DeadlineExceeded) || errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return "timeout", context.DeadlineExceeded
			}
			return "cancelled", werr
		}
		log.Info("guest runner exited", "vm_id", string(vmID), "exit_code", code)
		if code != 0 {
			return "failure", nil
		}
		return "success", nil
	}

	// Unit/e2e without WaitRealRunner: finish immediately (legacy test default).
	if w.Log != nil {
		w.Log.Warn("WaitRealRunner disabled; finishing job without guest runner wait")
	}
	return "success", nil
}
