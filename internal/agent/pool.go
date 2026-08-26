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

	"github.com/TwanLuttik/TemperCI/internal/api"
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
	vcpus        int
	memoryMiB    int
	createdAt    time.Time
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
	createInFlight    int
	createInFlightMem int

	inventory     InventorySource
	configuredMax int
	clampReason   string
	lastAdmit     string
	readyCheck    func(id vmm.ID) bool
	mailbox       *MailboxHub

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
	// Inventory optional host sample; nil keeps slot-only create checks.
	Inventory InventorySource
	// ReadyCheck, if set, reports whether the guest agent has signaled ready
	// (host-visible guest/agent.ready — written by the UDP mailbox).
	ReadyCheck func(id vmm.ID) bool
	// Mailbox optional UDP ready/exit channel (Firecracker).
	Mailbox *MailboxHub
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
	if len(cfg.Shapes) == 0 {
		cfg.Shapes = []VMShape{{
			Label:     ShapeLabel(cfg.VCPUs, cfg.MemoryMiB),
			VCPUs:     cfg.VCPUs,
			MemoryMiB: cfg.MemoryMiB,
			MinReady:  cfg.MinReady,
		}}
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
	if cfg.GuestReadyWait <= 0 {
		cfg.GuestReadyWait = 45 * time.Second
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

	if cfg.DiskPerVMMiB <= 0 {
		cfg.DiskPerVMMiB = OverlayEstimateMiB(cfg.ImagePath)
	}
	configuredMax := cfg.MaxReady
	clampReason := ""
	if deps.Inventory != nil {
		inv, err := deps.Inventory.Sample()
		if err != nil {
			log.Error("host inventory failed; refusing VMs", "err", err)
			cfg.MinReady = 0
			cfg.MaxReady = 0
			clampReason = "inventory_error"
		} else {
			var why string
			cfg, _, why = ClampPoolToHost(cfg, inv)
			if cfg.MaxReady < configuredMax {
				clampReason = why
				log.Info("host admission clamped max_ready",
					"configured", configuredMax,
					"effective", cfg.MaxReady,
					"reason", why,
					"ram_total_mib", inv.RAMTotalMiB,
					"disk_free_mib", inv.DiskFreeMiB,
				)
			}
		}
	}

	return &Pool{
		cfg:           cfg,
		vmm:           deps.VMM,
		cleaner:       deps.Cleaner,
		runner:        runner,
		log:           log,
		now:           now,
		newID:         newID,
		vms:           make(map[vmm.ID]*poolVM),
		wake:          make(chan struct{}, 1),
		inventory:     deps.Inventory,
		configuredMax: configuredMax,
		clampReason:   clampReason,
		readyCheck:    deps.ReadyCheck,
		mailbox:       deps.Mailbox,
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
	shape := ResolveJobShape(job.Labels, p.cfg.Shapes, p.cfg.VCPUs, p.cfg.MemoryMiB)

	started := p.now()
	bindDeadline := started.Add(p.cfg.BindWait)
	bootDeadline := started.Add(p.cfg.GuestReadyWait)
	var selected vmm.ID
	var warmStart bool

	for {
		p.mu.Lock()
		if p.stopping || !p.started {
			p.mu.Unlock()
			return nil, ErrPoolStopped
		}
		if id, ok := p.pickWarmLocked(shape.VCPUs, shape.MemoryMiB); ok {
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
		// A matching VM is already booting — wait for it instead of a second create.
		if p.matchingPoolBootLocked(shape.VCPUs, shape.MemoryMiB) && p.now().Before(bootDeadline) {
			p.mu.Unlock()
			p.kick()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		// No matching warm: try cold boot of the requested size if capacity allows.
		if p.canCreateLocked(shape.MemoryMiB) {
			p.createInFlight++
			p.createInFlightMem += shape.MemoryMiB
			p.mu.Unlock()
			id, err := p.createAndBoot(ctx, shape)
			p.mu.Lock()
			p.createInFlight--
			p.createInFlightMem -= shape.MemoryMiB
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
				vcpus:     shape.VCPUs,
				memoryMiB: shape.MemoryMiB,
				createdAt: p.now(),
				busySince: p.now(),
			}
			selected = id
			warmStart = false
			p.mu.Unlock()
			break
		}
		// Another job is holding RAM/slots that will free — wait instead of
		// failing the claimed GitHub job after BindWait.
		if p.reclaimableLocked() {
			p.mu.Unlock()
			p.kick()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		p.mu.Unlock()

		if p.now().After(bindDeadline) && p.now().After(bootDeadline) {
			return nil, fmt.Errorf("%w: no warm VM and cannot cold create", ErrNoCapacity)
		}
		if p.now().After(bindDeadline) {
			// No in-flight boot to wait for (or boot wait already expired above).
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
		"vcpu", shape.VCPUs,
		"memory_mib", shape.MemoryMiB,
		"shape", shape.Label,
	)
	p.kick() // replenish min_ready
	return &BindResult{VMID: selected, WarmStart: warmStart, JobID: job.JobID}, nil
}

// RunnerWaiter returns the pool's runner starter for WaitRunner (may be nil).
func (p *Pool) RunnerWaiter() RunnerStarter {
	return p.runner
}

// KillVM force-destroys a tracked VM (busy, warm, or booting).
func (p *Pool) KillVM(ctx context.Context, id vmm.ID, reason string) error {
	if reason == "" {
		reason = "killed"
	}
	p.mu.Lock()
	vm, ok := p.vms[id]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("agent: unknown vm %s", id)
	}
	busy := vm.state == StateBusy
	p.mu.Unlock()
	if busy {
		return p.JobFinished(ctx, id, reason)
	}
	return p.beginDestroy(ctx, id, reason)
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

// DiskPerVMMiB returns the overlay estimate used by admission.
func (p *Pool) DiskPerVMMiB() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg.DiskPerVMMiB
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
		p.cfg.DiskPerVMMiB = OverlayEstimateMiB(imagePath)
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
	for _, shape := range p.cfg.Shapes {
		if shape.MinReady <= 0 {
			continue
		}
		for {
			id, err := p.reservePoolBoot(shape)
			if err != nil {
				if errors.Is(err, ErrNoCapacity) || errors.Is(err, ErrPoolStopped) {
					break
				}
				p.log.Error("pool boot reserve failed", "err", err, "shape", shape.Label)
				return
			}
			p.wg.Add(1)
			go func(id vmm.ID, shape VMShape) {
				defer p.wg.Done()
				if err := p.finishBootWarm(ctx, id, shape); err != nil {
					if !errors.Is(err, ErrPoolStopped) && ctx.Err() == nil {
						p.log.Error("pool boot failed", "err", err, "shape", shape.Label, "vm_id", string(id))
					}
					return
				}
				p.log.Info("warm VM ready", "vm_id", string(id), "shape", shape.Label, "warm", p.Counts().Warm)
			}(id, shape)
		}
	}
}

func (p *Pool) reservePoolBoot(shape VMShape) (vmm.ID, error) {
	id, err := p.newID()
	if err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopping {
		return "", ErrPoolStopped
	}
	c := p.countsLocked()
	// Warm refill is an idle-pool concern. Creating extra guests while a job
	// is running is what packed 28 GiB onto a 32 GiB host (busy 8g+6g plus
	// two replacement warms). Existing warm VMs still bind.
	if c.Busy > 0 {
		return "", ErrNoCapacity
	}
	have := p.countShapeLocked(shape.VCPUs, shape.MemoryMiB, StateWarm, StatePoolBoot)
	if have >= shape.MinReady || !p.canCreateLocked(shape.MemoryMiB) {
		return "", ErrNoCapacity
	}
	if c.Warm+c.PoolBoot >= p.cfg.MaxReady {
		return "", ErrNoCapacity
	}
	p.vms[id] = &poolVM{id: id, state: StatePoolBoot, vcpus: shape.VCPUs, memoryMiB: shape.MemoryMiB, createdAt: p.now()}
	return id, nil
}

// bootIntoWarm creates+boots a VM and registers it as warm.
func (p *Pool) bootIntoWarm(ctx context.Context, shape VMShape) (vmm.ID, error) {
	id, err := p.reservePoolBoot(shape)
	if err != nil {
		return "", err
	}
	if err := p.finishBootWarm(ctx, id, shape); err != nil {
		return "", err
	}
	return id, nil
}

func (p *Pool) finishBootWarm(ctx context.Context, id vmm.ID, shape VMShape) error {
	if err := p.provision(ctx, id, shape); err != nil {
		p.mu.Lock()
		delete(p.vms, id)
		p.mu.Unlock()
		_ = p.cleaner.Destroy(context.Background(), id)
		return err
	}
	p.mu.Lock()
	vm, ok := p.vms[id]
	if !ok {
		p.mu.Unlock()
		_ = p.cleaner.Destroy(context.Background(), id)
		return ErrPoolStopped
	}
	vm.state = StateWarm
	vm.warmSince = p.now()
	p.mu.Unlock()
	return nil
}

// createAndBoot provisions a VM without registering warm (cold bind path).
// Caller holds responsibility for createInFlight accounting.
func (p *Pool) createAndBoot(ctx context.Context, shape VMShape) (vmm.ID, error) {
	id, err := p.newID()
	if err != nil {
		return "", err
	}
	if err := p.provision(ctx, id, shape); err != nil {
		_ = p.cleaner.Destroy(context.Background(), id)
		return "", err
	}
	return id, nil
}

func (p *Pool) provision(ctx context.Context, id vmm.ID, shape VMShape) error {
	if shape.VCPUs <= 0 {
		shape.VCPUs = p.cfg.VCPUs
	}
	if shape.MemoryMiB <= 0 {
		shape.MemoryMiB = p.cfg.MemoryMiB
	}
	cfg := vmm.Config{
		ID:         id,
		VCPUs:      shape.VCPUs,
		MemoryMiB:  shape.MemoryMiB,
		RootfsPath: p.cfg.ImagePath,
		KernelPath: p.cfg.KernelPath,
		Metadata: map[string]string{
			"role":   "pool",
			"shape":  shape.Label,
			"vcpu":   fmt.Sprintf("%d", shape.VCPUs),
			"memory": fmt.Sprintf("%d", shape.MemoryMiB),
		},
	}
	if _, err := p.vmm.Create(ctx, cfg); err != nil {
		return fmt.Errorf("create: %w", err)
	}
	p.listenMailbox(id)
	if err := p.vmm.Boot(ctx, id); err != nil {
		_ = p.cleaner.Destroy(context.Background(), id)
		return fmt.Errorf("boot: %w", err)
	}
	if err := p.waitGuestReady(ctx, id); err != nil {
		_ = p.cleaner.Destroy(context.Background(), id)
		return fmt.Errorf("guest ready: %w", err)
	}
	return nil
}

func (p *Pool) listenMailbox(id vmm.ID) {
	if p.mailbox == nil {
		return
	}
	hostIP := p.HostIP(id)
	if hostIP == "" {
		return
	}
	_ = p.mailbox.Listen(id, hostIP)
}

// HostIP returns the TAP host address recorded at VM create, or empty.
func (p *Pool) HostIP(id vmm.ID) string {
	layout := p.HostLayout()
	if layout.Root == "" || id == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(layout.NetDir(id), "host_ip"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func (p *Pool) waitGuestReady(ctx context.Context, id vmm.ID) error {
	deadline := p.now().Add(p.cfg.GuestReadyWait)
	readyPath := filepath.Join(p.cleaner.Layout.GuestDir(id), "agent.ready")
	for {
		if p.readyCheck != nil && p.readyCheck(id) {
			return nil
		}
		if _, err := os.Stat(readyPath); err == nil {
			return nil
		}
		if p.now().After(deadline) {
			return fmt.Errorf("timeout after %s", p.cfg.GuestReadyWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (p *Pool) matchingPoolBootLocked(vcpus, memoryMiB int) bool {
	for _, vm := range p.vms {
		if vm == nil || vm.state != StatePoolBoot {
			continue
		}
		if vm.vcpus == vcpus && vm.memoryMiB == memoryMiB {
			return true
		}
	}
	return false
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
	if p.mailbox != nil {
		p.mailbox.Close(id)
	}
	if p.cleaner != nil {
		SignalRunnerStopped(p.cleaner.Layout, id)
	}
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

func (p *Pool) pickWarmLocked(vcpus, memoryMiB int) (vmm.ID, bool) {
	// Prefer oldest matching warm (FIFO).
	var best vmm.ID
	var bestTime time.Time
	found := false
	for id, vm := range p.vms {
		if vm.state != StateWarm {
			continue
		}
		if vm.vcpus != vcpus || vm.memoryMiB != memoryMiB {
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

func (p *Pool) countShapeLocked(vcpus, memoryMiB int, states ...VMState) int {
	n := 0
	for _, vm := range p.vms {
		if vm.vcpus != vcpus || vm.memoryMiB != memoryMiB {
			continue
		}
		for _, st := range states {
			if vm.state == st {
				n++
				break
			}
		}
	}
	return n
}

func (p *Pool) allocatedRAMLocked() int {
	n := p.createInFlightMem
	for _, vm := range p.vms {
		if vm.memoryMiB > 0 {
			n += vm.memoryMiB
			continue
		}
		n += p.cfg.MemoryMiB
	}
	return n
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

// ExclusiveBusy is kept for older dashboards. Packing is RAM-based now.
func (p *Pool) ExclusiveBusy() bool {
	return false
}

func (p *Pool) reclaimableLocked() bool {
	if p.createInFlight > 0 {
		return true
	}
	for _, vm := range p.vms {
		switch vm.state {
		case StateBusy, StatePoolBoot, StateDestroying:
			return true
		}
	}
	return false
}

// canCreateLocked reports whether a new instance may be provisioned.
// Blocks blind replenish when at hard cap (e.g. destroy failures piling up).
func (p *Pool) canCreateLocked(nextMemoryMiB int) bool {
	c := p.countsLocked()
	total := c.Total() + p.createInFlight
	if total >= p.cfg.MaxTotalVMs {
		p.noteAdmitRefuse("max_total_vms", "refusing create: at max_total_vms",
			"total", total,
			"max_total_vms", p.cfg.MaxTotalVMs,
			"destroying", c.Destroying,
			"last_destroy_err", p.lastDestroyErr,
		)
		return false
	}
	if p.inventory == nil {
		p.lastAdmit = ""
		return true
	}
	inv, err := p.inventory.Sample()
	if err != nil {
		p.noteAdmitRefuse("inventory_error", "refusing create: inventory sample failed", "err", err)
		return false
	}
	if nextMemoryMiB <= 0 {
		nextMemoryMiB = p.cfg.MemoryMiB
	}
	dec := p.admission().CanCreateMemory(inv, p.allocatedRAMLocked(), nextMemoryMiB)
	if !dec.OK {
		p.noteAdmitRefuse(dec.Reason, "refusing create: host resources",
			"reason", dec.Reason,
			"allocated_ram_mib", p.allocatedRAMLocked(),
			"memory_mib", nextMemoryMiB,
			"ram_avail_mib", inv.RAMAvailMiB,
			"disk_free_mib", inv.DiskFreeMiB,
		)
		return false
	}
	p.lastAdmit = ""
	return true
}

// noteAdmitRefuse records a refuse reason. Warns on a new reason; Debug on repeats
// so leftover-full Bind/replenish retries do not spam.
func (p *Pool) noteAdmitRefuse(reason, msg string, args ...any) {
	repeat := p.lastAdmit == reason
	p.lastAdmit = reason
	if repeat {
		p.log.Debug(msg, args...)
		return
	}
	p.log.Warn(msg, args...)
}

func (p *Pool) admission() Admission {
	return Admission{
		MemoryMiB:      p.cfg.MemoryMiB,
		DiskMiB:        p.cfg.DiskPerVMMiB,
		ReserveRAMMiB:  p.cfg.ReserveRAMMiB,
		ReserveDiskMiB: p.cfg.ReserveDiskMiB,
	}
}

func (p *Pool) remainingCreatesLocked() int {
	if p.inventory == nil {
		return 1 << 20
	}
	inv, err := p.inventory.Sample()
	if err != nil {
		p.lastAdmit = "inventory_error"
		return 0
	}
	total := p.countsLocked().Total() + p.createInFlight
	// Slot estimate uses the default shape; Bind still checks the requested size.
	return p.admission().Remaining(inv, total)
}

func (p *Pool) EffectiveMaxReady() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg.MaxReady
}

func (p *Pool) ConfiguredMaxReady() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.configuredMax > 0 {
		return p.configuredMax
	}
	return p.cfg.MaxReady
}

func (p *Pool) RemainingCreates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remainingCreatesLocked()
}

func (p *Pool) LastAdmitReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAdmit
}

func (p *Pool) ClampReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clampReason
}

func (p *Pool) InventorySample() (HostInventory, error) {
	if p == nil || p.inventory == nil {
		return HostInventory{}, nil
	}
	return p.inventory.Sample()
}

func (p *Pool) HostResources() *api.HostResources {
	if p == nil {
		return nil
	}
	inv, err := p.InventorySample()
	if err != nil {
		return &api.HostResources{
			ConfiguredMaxReady: p.ConfiguredMaxReady(),
			EffectiveMaxReady:  p.EffectiveMaxReady(),
			ClampReason:        p.ClampReason(),
			LastAdmitReason:    "inventory_error",
		}
	}
	if p.inventory == nil {
		return nil
	}
	p.mu.Lock()
	allocated := p.allocatedRAMLocked()
	reserve := p.cfg.ReserveRAMMiB
	p.mu.Unlock()
	return &api.HostResources{
		RAMTotalMiB:        inv.RAMTotalMiB,
		RAMAvailMiB:        inv.RAMAvailMiB,
		AllocatedRAMMiB:    allocated,
		ReserveRAMMiB:      reserve,
		DiskTotalMiB:       inv.DiskTotalMiB,
		DiskFreeMiB:        inv.DiskFreeMiB,
		NumCPU:             inv.NumCPU,
		ConfiguredMaxReady: p.ConfiguredMaxReady(),
		EffectiveMaxReady:  p.EffectiveMaxReady(),
		ClampReason:        p.ClampReason(),
		LastAdmitReason:    p.LastAdmitReason(),
	}
}

func randomVMID() (vmm.ID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return vmm.ID("vm-" + hex.EncodeToString(b[:])), nil
}
