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
	"github.com/TwanLuttik/TemperCI/internal/ocicache"
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
	// Capacity is max concurrent jobs (usually MaxReady).
	// FreeSlots is min(Capacity - max(Busy, inflight), Warm + RemainingCreates - pendingBind).
	Capacity int
	// WaitRealRunner when true (default for firecracker) waits on GuestExec.WaitRunner.
	// Set false only for unit tests that do not implement WaitRunner beyond stubs.
	WaitRealRunner bool
	// Cache is the optional host-local Actions cache gateway.
	Cache *ghacache.Gateway
	// OCI is the optional host-local registry / build-cache gateway.
	OCI *ocicache.Gateway
	// BeforeBind, if set, runs after claim and before Pool.Bind (tests).
	BeforeBind func()

	// inflight counts claimed jobs whose handleJob goroutine has not returned.
	// Used so FreeSlots drops immediately on claim, before Pool.Busy updates.
	inflightMu sync.Mutex
	inflight   int

	invMu    sync.Mutex
	invAt    time.Time
	invRepos []string
	invCache *api.CacheUsage
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
			return w.drainReturn(err)
		}
		cap := w.snapshot()
		if cap.FreeSlots <= 0 {
			select {
			case <-ctx.Done():
				return w.drainReturn(ctx.Err())
			case <-time.After(poll):
			}
			continue
		}
		job, err := w.Client.Claim(ctx, cap)
		if err != nil {
			log.Error("claim failed", "err", err)
			select {
			case <-ctx.Done():
				return w.drainReturn(ctx.Err())
			case <-time.After(poll):
			}
			continue
		}
		if job == nil {
			// Claim already long-polled; loop immediately.
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

func (w *Worker) drainReturn(err error) error {
	if n := w.inFlight(); n > 0 && w.Log != nil {
		w.Log.Info("worker stopping; draining in-flight jobs", "n", n)
	}
	return err
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
	inflight := w.inFlight()
	used := c.Busy
	if inflight > used {
		// Claimed but Bind has not yet incremented Busy (or JobFinished already ran).
		used = inflight
	}
	free := w.Capacity - used
	if free < 0 {
		free = 0
	}
	pendingBind := inflight - c.Busy
	if pendingBind < 0 {
		pendingBind = 0
	}
	bindBudget := c.Warm + w.Pool.RemainingCreates() - pendingBind
	if bindBudget < free {
		free = bindBudget
	}
	if free < 0 {
		free = 0
	}
	repos, cache := w.cachedInventory()
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

func (w *Worker) cachedInventory() ([]string, *api.CacheUsage) {
	w.invMu.Lock()
	defer w.invMu.Unlock()
	if time.Since(w.invAt) < 10*time.Second && w.invCache != nil {
		return append([]string(nil), w.invRepos...), w.invCache
	}
	var repos []string
	var ghaStore *ghacache.Store
	var ociStore *ocicache.Store
	if w.Cache != nil {
		ghaStore = w.Cache.Store
	}
	if w.OCI != nil {
		ociStore = w.OCI.Store
	}
	if ghaStore != nil {
		repos = append(repos, ghaStore.Repos()...)
	}
	if ociStore != nil {
		repos = append(repos, ociStore.Repos()...)
	}
	repos = uniqueSorted(repos)
	cache := CacheUsageFromStores(ghaStore, ociStore)
	w.invAt = time.Now()
	w.invRepos = repos
	w.invCache = cache
	return repos, cache
}

func (w *Worker) register(ctx context.Context) error {
	ops, cmds, err := w.Client.Register(ctx, w.snapshot())
	if err != nil {
		return err
	}
	if n := ApplyAgentCmds(ctx, w.Pool, cmds); n > 0 && w.Log != nil {
		w.Log.Info("applied agent commands", "n", n)
	}
	var ghaStore *ghacache.Store
	var ociStore *ocicache.Store
	if w.Cache != nil {
		ghaStore = w.Cache.Store
	}
	if w.OCI != nil {
		ociStore = w.OCI.Store
	}
	if len(ops) == 0 || (ghaStore == nil && ociStore == nil) {
		return nil
	}
	n, err := ApplyCacheOps(ghaStore, ociStore, ops)
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
	// Agent SIGTERM must not yank a running GitHub job. Detach this job from the
	// worker cancel so drain can wait for runner.exit / simulate to finish.
	jobCtx := context.WithoutCancel(ctx)
	jobIDStr := strconv.FormatInt(job.JobID, 10)
	payload := JobPayload{
		JobID:      jobIDStr,
		RunnerName: job.RunnerName,
		Labels:     append([]string(nil), job.Labels...),
		JITConfig:  job.EncodedJITConfig,
	}

	if w.BeforeBind != nil {
		w.BeforeBind()
	}
	res, err := w.Pool.Bind(jobCtx, payload)
	// Clear secret from payload as soon as bind returns.
	payload.JITConfig = ""
	job.EncodedJITConfig = ""

	if err != nil {
		_ = w.finish(jobCtx, job.JobID, job.RepoFullName, "error", "", false, err.Error(), JobLogs{})
		return err
	}
	if job.RepoFullName != "" && (w.Cache != nil || w.OCI != nil) {
		guestIP := w.Pool.GuestIP(res.VMID)
		if guestIP == "" {
			guestIP = "127.0.0.1"
		}
		if w.Cache != nil {
			w.Cache.BindRemote(guestIP, job.RepoFullName)
			defer w.Cache.UnbindRemote(guestIP)
		}
		if w.OCI != nil {
			w.OCI.BindRemote(guestIP, job.RepoFullName)
			defer w.OCI.UnbindRemote(guestIP)
		}
	}

	if err := w.Client.ReportStarted(jobCtx, job.JobID, string(res.VMID), res.WarmStart); err != nil {
		log.Error("report started failed", "job_id", job.JobID, "err", err)
	}

	log.Info("job running",
		"job_id", job.JobID,
		"vm_id", string(res.VMID),
		"warm_bind", res.WarmStart,
	)

	outcome, waitErr := w.waitForJob(jobCtx, res.VMID, job.JobID)
	logs := w.collectLogs(res.VMID)
	if waitErr != nil && !errors.Is(waitErr, context.DeadlineExceeded) {
		_ = w.Pool.JobFinished(jobCtx, res.VMID, "cancelled")
		_ = w.finish(jobCtx, job.JobID, job.RepoFullName, "cancelled", string(res.VMID), res.WarmStart, waitErr.Error(), logs)
		return waitErr
	}
	if outcome == "timeout" {
		log.Warn("job deadline exceeded; force destroy",
			"job_id", job.JobID,
			"vm_id", string(res.VMID),
			"deadline", w.JobDeadline.String(),
		)
		if err := w.Pool.JobFinished(jobCtx, res.VMID, "timeout"); err != nil {
			_ = w.finish(jobCtx, job.JobID, job.RepoFullName, "timeout", string(res.VMID), res.WarmStart, err.Error(), logs)
			return err
		}
		if err := w.finish(jobCtx, job.JobID, job.RepoFullName, "timeout", string(res.VMID), res.WarmStart, "job deadline exceeded", logs); err != nil {
			return err
		}
		return nil
	}

	// outcome success | failure from runner exit code
	if err := w.Pool.JobFinished(jobCtx, res.VMID, outcome); err != nil && !errors.Is(err, ErrNotBusy) {
		_ = w.finish(jobCtx, job.JobID, job.RepoFullName, "error", string(res.VMID), res.WarmStart, err.Error(), logs)
		return err
	}
	if err := w.finish(jobCtx, job.JobID, job.RepoFullName, outcome, string(res.VMID), res.WarmStart, "", logs); err != nil {
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
func (w *Worker) waitForJob(ctx context.Context, vmID vmm.ID, jobID int64) (outcome string, err error) {
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
		streamCtx, stopStream := context.WithCancel(waitCtx)
		defer stopStream()
		if jobID != 0 && w.Client != nil {
			go w.streamLogs(streamCtx, jobID, vmID)
		}
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

func (w *Worker) streamLogs(ctx context.Context, jobID int64, vmID vmm.ID) {
	if w.Client == nil || jobID == 0 {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			logs := w.collectLogs(vmID)
			if logs.RunnerLog == "" && logs.AgentLog == "" && logs.ConsoleLog == "" && logs.WorkflowLog == "" {
				continue
			}
			sig := logs.RunnerLog + "\x00" + logs.AgentLog + "\x00" + logs.ConsoleLog + "\x00" + logs.WorkflowLog
			if sig == last {
				continue
			}
			last = sig
			if err := w.Client.ReportLogs(ctx, jobID, logs); err != nil && w.Log != nil {
				w.Log.Info("live log upload failed", "job_id", jobID, "err", err)
			}
		}
	}
}
