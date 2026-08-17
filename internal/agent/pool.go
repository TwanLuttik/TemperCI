package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// ErrNoCapacity is returned when Bind cannot obtain a VM (hard cap / no warm and cold create blocked).
var ErrNoCapacity = errors.New("agent: no capacity")

// ErrNotBusy is returned when JobFinished is called for an unknown or non-busy VM.
var ErrNotBusy = errors.New("agent: vm not busy")

// ErrPoolStopped is returned when the pool is not running.
var ErrPoolStopped = errors.New("agent: pool stopped")

type poolVM struct {
	id           vmm.ID
	state        VMState
	jobID        string
	warmSince    time.Time
	busySince    time.Time
	destroyAfter time.Time // next destroy retry not before this
	destroyTries int
	lastErr      string
}

// Pool is an in-memory warm microVM pool.
type Pool struct {
	cfg     PoolConfig
	vmm     vmm.Manager
	cleaner *cleanup.Cleaner
	runner  RunnerStarter
	log     *slog.Logger
	now     func() time.Time
	newID   func() (vmm.ID, error)

	mu             sync.Mutex
	vms            map[vmm.ID]*poolVM
	started        bool
	stopping       bool
	lastDestroyErr string
	metrics        Metrics
	sampler        *procSampler

	// createInFlight counts cold-bind provisions not yet registered in vms.
	createInFlight int

	reconcileMu sync.Mutex

	cancel context.CancelFunc
	wg     sync.WaitGroup
	wake   chan struct{}
}

// PoolDeps wires infrastructure for a Pool.
type PoolDeps struct {
	VMM     vmm.Manager
	Cleaner *cleanup.Cleaner
	Runner  RunnerStarter
	Log     *slog.Logger
	// Now optional clock for tests.
	Now func() time.Time
	// NewID optional id generator for tests.
	NewID func() (vmm.ID, error)
}

// NewPool builds a pool. Call Start to sweep orphans and run the reconciler.
func NewPool(cfg PoolConfig, deps PoolDeps) (*Pool, error) {
	if deps.VMM == nil {
		return nil, fmt.Errorf("agent: VMM is nil")
	}
	if deps.Cleaner == nil {
		return nil, fmt.Errorf("agent: Cleaner is nil")
	}
	// MinReady may be 0 (cold-only). Negative values are treated as 1.
	if cfg.MinReady < 0 {
		cfg.MinReady = 1
	}
	if cfg.MaxReady < cfg.MinReady {
		cfg.MaxReady = cfg.MinReady
	}
	if cfg.MaxReady <= 0 && cfg.MinReady == 0 {
		cfg.MaxReady = 1
	}
	if cfg.MaxTotalVMs <= 0 {
		cfg.MaxTotalVMs = cfg.MaxReady + 32
	}
	if cfg.VCPUs <= 0 {
		cfg.VCPUs = 2
	}
	if cfg.MemoryMiB <= 0 {
		cfg.MemoryMiB = 2048
	}
	if cfg.ImagePath == "" {
		return nil, fmt.Errorf("agent: ImagePath is required")
	}
	if cfg.ReconcileInterval <= 0 {
		cfg.ReconcileInterval = time.Second
	}
	if cfg.DestroyRetryBase <= 0 {
		cfg.DestroyRetryBase = 100 * time.Millisecond
	}
	if cfg.DestroyRetryMax <= 0 {
		cfg.DestroyRetryMax = 5 * time.Second
	}
	if cfg.BindWait <= 0 {
		cfg.BindWait = 2 * time.Second
	}

	log := deps.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
	}
	runner := deps.Runner
	if runner == nil {
		runner = &StubRunner{Log: log}
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	newID := deps.NewID
	if newID == nil {
		newID = randomVMID
	}

	return &Pool{
		cfg:     cfg,
		vmm:     deps.VMM,
		cleaner: deps.Cleaner,
		runner:  runner,
		log:     log,
		now:     now,
		newID:   newID,
		vms:     make(map[vmm.ID]*poolVM),
		wake:    make(chan struct{}, 1),
	}, nil
}

// Start runs orphan sweep (desired empty for process-local pool) then starts
// the background reconciler. Safe to call once.
func (p *Pool) Start(ctx context.Context) error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return fmt.Errorf("agent: pool already started")
	}
	p.started = true
	p.stopping = false
	runCtx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.mu.Unlock()

	// On (re)start: destroy anything on the host; process-local state is empty.
	destroyed, err := p.cleaner.SweepOrphans(ctx, nil)
	if err != nil {
		cancel()
		p.mu.Lock()
		p.started = false
		p.mu.Unlock()
		return fmt.Errorf("agent: orphan sweep: %w", err)
	}
	if n := len(destroyed); n > 0 {
		p.metrics.Orphans.Add(uint64(n))
		p.log.Info("orphan sweep complete", "destroyed", n)
	}

	p.wg.Add(1)
	go p.reconcileLoop(runCtx)

	p.kick()
	return nil
}

// Stop cancels the reconciler and waits for background work. Does not destroy VMs;
// call Shutdown for full teardown.
func (p *Pool) Stop() {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return
	}
	p.stopping = true
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.wg.Wait()
	p.mu.Lock()
	p.started = false
	p.mu.Unlock()
}

// Shutdown stops the reconciler and destroys all tracked VMs.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.Stop()
	p.mu.Lock()
	ids := make([]vmm.ID, 0, len(p.vms))
	for id := range p.vms {
		ids = append(ids, id)
	}
	p.mu.Unlock()
	var first error
	for _, id := range ids {
		if err := p.destroyNow(ctx, id); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Bind selects a warm VM (or cold-boots), transitions warm→busy, and starts the runner.
// On runner start failure the VM is destroyed and not returned to warm.
func (p *Pool) Bind(ctx context.Context, job JobPayload) (*BindResult, error) {
	if job.JobID == "" {
		return nil, fmt.Errorf("agent: job id required")
	}

	deadline := p.now().Add(p.cfg.BindWait)
	var selected vmm.ID
	var warmStart bool

	for {
		p.mu.Lock()
		if p.stopping || !p.started {
			p.mu.Unlock()
			return nil, ErrPoolStopped
		}
		if id, ok := p.pickWarmLocked(); ok {
			selected = id
			warmStart = true
			vm := p.vms[id]
			vm.state = StateBusy
			vm.jobID = job.JobID
			vm.busySince = p.now()
			vm.warmSince = time.Time{}
			p.mu.Unlock()
			break
		}
		// No warm: try cold boot path if capacity allows.
		if p.canCreateLocked() {
			p.createInFlight++
			p.mu.Unlock()
			id, err := p.createAndBoot(ctx)
			p.mu.Lock()
			p.createInFlight--
			if err != nil {
				p.mu.Unlock()
				return nil, fmt.Errorf("agent: cold boot: %w", err)
			}
			// Register as busy immediately (never enter warm with secrets).
			if p.stopping {
				p.mu.Unlock()
				_ = p.cleaner.Destroy(context.Background(), id)
				return nil, ErrPoolStopped
			}
			p.vms[id] = &poolVM{
				id:        id,
				state:     StateBusy,
				jobID:     job.JobID,
				busySince: p.now(),
			}
			selected = id
			warmStart = false
			p.mu.Unlock()
			break
		}
		p.mu.Unlock()

		if p.now().After(deadline) {
			return nil, fmt.Errorf("%w: no warm VM and cannot cold create", ErrNoCapacity)
		}
		// Wait briefly for reconciler to finish a pool_boot.
		p.kick()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}

	// Attach JIT / start runner (stub OK). Never log full JIT.
	if err := p.runner.StartRunner(ctx, selected, job); err != nil {
		p.log.Error("runner start failed; destroying VM",
			"vm_id", string(selected),
			"job_id", job.JobID,
			"err", err,
		)
		// Do not re-warm: destroy this instance.
		_ = p.beginDestroy(ctx, selected, "bind_runner_failed")
		return nil, fmt.Errorf("agent: start runner: %w", err)
	}

	if warmStart {
		p.metrics.WarmBinds.Add(1)
	} else {
		p.metrics.ColdStarts.Add(1)
	}
	p.log.Info("job bound",
		"vm_id", string(selected),
		"job_id", job.JobID,
		"warm_bind", warmStart,
	)
	p.kick() // replenish min_ready
	return &BindResult{VMID: selected, WarmStart: warmStart, JobID: job.JobID}, nil
}

// RunnerWaiter returns the pool's runner starter for WaitRunner (may be nil).
func (p *Pool) RunnerWaiter() RunnerStarter {
	return p.runner
}

// JobFinished signals a terminal job outcome: busy → destroying → cleanup → replenish.
func (p *Pool) JobFinished(ctx context.Context, id vmm.ID, reason string) error {
	p.mu.Lock()
	vm, ok := p.vms[id]
	if !ok || vm.state != StateBusy {
		p.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrNotBusy, id)
	}
	vm.state = StateDestroying
	vm.jobID = ""
	vm.destroyAfter = time.Time{}
	p.mu.Unlock()

	p.log.Info("job finished; destroying", "vm_id", string(id), "reason", reason)
	err := p.destroyNow(ctx, id)
	p.kick()
	return err
}

// Counts returns current membership by state.
func (p *Pool) Counts() Counts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.countsLocked()
}

// Metrics returns counters plus current counts.
func (p *Pool) Metrics() Snapshot {
	s := p.metrics.Snapshot()
	s.Counts = p.Counts()
	return s
}

// LastDestroyError returns the most recent destroy failure message (empty if none).
func (p *Pool) LastDestroyError() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastDestroyErr
}

// DesiredIDs returns warm+busy+pool_boot+destroying ids (for external sweeps).
func (p *Pool) DesiredIDs() map[vmm.ID]struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[vmm.ID]struct{}, len(p.vms))
	for id := range p.vms {
		out[id] = struct{}{}
	}
	return out
}

// ImagePath returns the current base image path used for new boots.
func (p *Pool) ImagePath() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg.ImagePath
}

// HostLayout is the on-disk layout used for instance scratch and job-logs.
func (p *Pool) HostLayout() vmm.Layout {
	if p == nil || p.cleaner == nil {
		return vmm.Layout{}
	}
	return p.cleaner.Layout
}

// GuestIP returns the guest address recorded at VM create, or empty.
func (p *Pool) GuestIP(id vmm.ID) string {
	layout := p.HostLayout()
	if layout.Root == "" || id == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(layout.NetDir(id), "guest_ip"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// DrainWarm destroys all warm (idle) VMs so the pool can refill (e.g. after image update).
// Busy VMs are left running until their jobs finish.
// Returns the number of warm VMs marked for destroy.
func (p *Pool) DrainWarm(ctx context.Context) (int, error) {
	p.mu.Lock()
	var warm []vmm.ID
	for id, vm := range p.vms {
		if vm.state == StateWarm {
			warm = append(warm, id)
		}
	}
	p.mu.Unlock()

	var first error
	n := 0
	for _, id := range warm {
		p.log.Info("draining warm VM", "vm_id", string(id))
		if err := p.beginDestroy(ctx, id, "image_drain"); err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		n++
		p.metrics.Recycles.Add(1)
	}
	p.kick()
	return n, first
}

// ReloadImage updates the base image path for subsequent boots and optionally drains warm VMs.
func (p *Pool) ReloadImage(ctx context.Context, imagePath string, drain bool) (drained int, err error) {
	if imagePath != "" {
		p.mu.Lock()
		p.cfg.ImagePath = imagePath
		p.mu.Unlock()
		p.log.Info("pool image path updated", "image_path", imagePath)
	}
	if drain {
		return p.DrainWarm(ctx)
	}
	return 0, nil
}

func (p *Pool) reconcileLoop(ctx context.Context) {
	defer p.wg.Done()
	t := time.NewTicker(p.cfg.ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.reconcile(ctx)
		case <-p.wake:
			p.reconcile(ctx)
		}
	}
}

func (p *Pool) kick() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Pool) reconcile(ctx context.Context) {
	// Single-flight: avoid overlapping create/destroy from ticker + kick.
	if !p.reconcileMu.TryLock() {
		return
	}
	defer p.reconcileMu.Unlock()

	if err := ctx.Err(); err != nil {
		return
	}
	p.retryDestroys(ctx)
	p.recycleIdle(ctx)
	p.replenish(ctx)
	c := p.Counts()
	p.log.Debug("pool reconcile",
		"warm", c.Warm,
		"busy", c.Busy,
		"pool_boot", c.PoolBoot,
		"destroying", c.Destroying,
		"warm_binds", p.metrics.WarmBinds.Load(),
		"cold_starts", p.metrics.ColdStarts.Load(),
	)
}

func (p *Pool) replenish(ctx context.Context) {
	for {
		p.mu.Lock()
		if p.stopping {
			p.mu.Unlock()
			return
		}
		c := p.countsLocked()
		need := p.cfg.MinReady - (c.Warm + c.PoolBoot)
		if need <= 0 || !p.canCreateLocked() {
			p.mu.Unlock()
			return
		}
		// Soft cap on idle capacity (warm + pool_boot).
		idle := c.Warm + c.PoolBoot
		if idle >= p.cfg.MaxReady {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		id, err := p.bootIntoWarm(ctx)
		if err != nil {
			if errors.Is(err, ErrNoCapacity) {
				return
			}
			p.log.Error("pool boot failed", "err", err)
			return
		}
		p.log.Info("warm VM ready", "vm_id", string(id), "warm", p.Counts().Warm)
	}
}

// bootIntoWarm creates+boots a VM and registers it as warm.
func (p *Pool) bootIntoWarm(ctx context.Context) (vmm.ID, error) {
	// Reserve pool_boot slot with a provisional id so DesiredIDs / counts work mid-boot.
	id, err := p.newID()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	if p.stopping {
		p.mu.Unlock()
		return "", ErrPoolStopped
	}
	c := p.countsLocked()
	if c.Warm+c.PoolBoot >= p.cfg.MaxReady || !p.canCreateLocked() {
		p.mu.Unlock()
		return "", ErrNoCapacity
	}
	p.vms[id] = &poolVM{id: id, state: StatePoolBoot}
	p.mu.Unlock()

	if err := p.provision(ctx, id); err != nil {
		p.mu.Lock()
		delete(p.vms, id)
		p.mu.Unlock()
		// Best-effort cleanup of partial create.
		_ = p.cleaner.Destroy(context.Background(), id)
		return "", err
	}

	p.mu.Lock()
	vm, ok := p.vms[id]
	if !ok {
		p.mu.Unlock()
		// Stopped mid-boot; destroy.
		_ = p.cleaner.Destroy(context.Background(), id)
		return "", ErrPoolStopped
	}
	vm.state = StateWarm
	vm.warmSince = p.now()
	p.mu.Unlock()
	return id, nil
}

// createAndBoot provisions a VM without registering warm (cold bind path).
// Caller holds responsibility for createInFlight accounting.
func (p *Pool) createAndBoot(ctx context.Context) (vmm.ID, error) {
	id, err := p.newID()
	if err != nil {
		return "", err
	}
	if err := p.provision(ctx, id); err != nil {
		_ = p.cleaner.Destroy(context.Background(), id)
		return "", err
	}
	return id, nil
}

func (p *Pool) provision(ctx context.Context, id vmm.ID) error {
	cfg := vmm.Config{
		ID:         id,
		VCPUs:      p.cfg.VCPUs,
		MemoryMiB:  p.cfg.MemoryMiB,
		RootfsPath: p.cfg.ImagePath,
		KernelPath: p.cfg.KernelPath,
		Metadata: map[string]string{
			"role": "pool",
		},
	}
	if _, err := p.vmm.Create(ctx, cfg); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	if err := p.vmm.Boot(ctx, id); err != nil {
		_ = p.cleaner.Destroy(context.Background(), id)
		return fmt.Errorf("boot: %w", err)
	}
	return nil
}

func (p *Pool) recycleIdle(ctx context.Context) {
	if p.cfg.IdleRecycle <= 0 {
		return
	}
	now := p.now()
	var expired []vmm.ID
	p.mu.Lock()
	for id, vm := range p.vms {
		if vm.state != StateWarm {
			continue
		}
		if vm.warmSince.IsZero() {
			continue
		}
		if now.Sub(vm.warmSince) >= p.cfg.IdleRecycle {
			expired = append(expired, id)
		}
	}
	p.mu.Unlock()

	for _, id := range expired {
		p.log.Info("recycling idle warm VM", "vm_id", string(id))
		if err := p.beginDestroy(ctx, id, "idle_recycle"); err != nil {
			p.log.Error("idle recycle destroy", "vm_id", string(id), "err", err)
			continue
		}
		p.metrics.Recycles.Add(1)
	}
}

func (p *Pool) retryDestroys(ctx context.Context) {
	now := p.now()
	var due []vmm.ID
	p.mu.Lock()
	for id, vm := range p.vms {
		if vm.state != StateDestroying {
			continue
		}
		if !vm.destroyAfter.IsZero() && now.Before(vm.destroyAfter) {
			continue
		}
		due = append(due, id)
	}
	p.mu.Unlock()

	for _, id := range due {
		_ = p.destroyNow(ctx, id)
	}
}

// beginDestroy marks destroying and runs destroy (async-safe).
func (p *Pool) beginDestroy(ctx context.Context, id vmm.ID, reason string) error {
	p.mu.Lock()
	vm, ok := p.vms[id]
	if !ok {
		p.mu.Unlock()
		// Still try host cleanup.
		return p.cleaner.Destroy(ctx, id)
	}
	vm.state = StateDestroying
	vm.jobID = ""
	vm.destroyAfter = time.Time{}
	p.mu.Unlock()
	p.log.Info("destroying VM", "vm_id", string(id), "reason", reason)
	return p.destroyNow(ctx, id)
}

func (p *Pool) destroyNow(ctx context.Context, id vmm.ID) error {
	err := p.cleaner.Destroy(ctx, id)
	p.mu.Lock()
	defer p.mu.Unlock()
	vm, ok := p.vms[id]
	if err != nil {
		p.metrics.DestroyFail.Add(1)
		p.lastDestroyErr = err.Error()
		if ok {
			vm.state = StateDestroying
			vm.destroyTries++
			vm.lastErr = err.Error()
			// Exponential backoff with cap.
			shift := vm.destroyTries - 1
			if shift > 6 {
				shift = 6
			}
			if shift < 0 {
				shift = 0
			}
			backoff := p.cfg.DestroyRetryBase << shift
			if backoff > p.cfg.DestroyRetryMax {
				backoff = p.cfg.DestroyRetryMax
			}
			vm.destroyAfter = p.now().Add(backoff)
		}
		tries := 0
		if ok {
			tries = vm.destroyTries
		}
		p.log.Error("destroy failed; will retry",
			"vm_id", string(id),
			"err", err,
			"tries", tries,
		)
		return err
	}
	p.metrics.DestroysOK.Add(1)
	p.lastDestroyErr = ""
	delete(p.vms, id)
	return nil
}

func (p *Pool) pickWarmLocked() (vmm.ID, bool) {
	// Prefer oldest warm (FIFO).
	var best vmm.ID
	var bestTime time.Time
	found := false
	for id, vm := range p.vms {
		if vm.state != StateWarm {
			continue
		}
		if !found || vm.warmSince.Before(bestTime) {
			best = id
			bestTime = vm.warmSince
			found = true
		}
	}
	return best, found
}

func (p *Pool) countsLocked() Counts {
	var c Counts
	for _, vm := range p.vms {
		switch vm.state {
		case StatePoolBoot:
			c.PoolBoot++
		case StateWarm:
			c.Warm++
		case StateBusy:
			c.Busy++
		case StateDestroying:
			c.Destroying++
		}
	}
	return c
}

// canCreateLocked reports whether a new instance may be provisioned.
// Blocks blind replenish when at hard cap (e.g. destroy failures piling up).
func (p *Pool) canCreateLocked() bool {
	c := p.countsLocked()
	total := c.Total() + p.createInFlight
	if total >= p.cfg.MaxTotalVMs {
		p.log.Error("refusing create: at max_total_vms",
			"total", total,
			"max_total_vms", p.cfg.MaxTotalVMs,
			"destroying", c.Destroying,
			"last_destroy_err", p.lastDestroyErr,
		)
		return false
	}
	return true
}

func randomVMID() (vmm.ID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return vmm.ID("vm-" + hex.EncodeToString(b[:])), nil
}
