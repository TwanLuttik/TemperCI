package agent_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

func testPool(t *testing.T, cfg agent.PoolConfig, opts ...func(*agent.PoolDeps, *fake.Manager)) *agent.Pool {
	t.Helper()
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	// Touch a dummy image path reference (fake does not read it).
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	if cfg.ImagePath == "" {
		cfg.ImagePath = img
	}
	if cfg.MinReady == 0 {
		cfg.MinReady = 1
	}
	if cfg.MaxReady == 0 {
		cfg.MaxReady = 2
	}
	if cfg.VCPUs == 0 {
		cfg.VCPUs = 1
	}
	if cfg.MemoryMiB == 0 {
		cfg.MemoryMiB = 512
	}
	if cfg.ReconcileInterval == 0 {
		cfg.ReconcileInterval = 20 * time.Millisecond
	}
	if cfg.BindWait == 0 {
		cfg.BindWait = 500 * time.Millisecond
	}
	if cfg.DestroyRetryBase == 0 {
		cfg.DestroyRetryBase = 10 * time.Millisecond
	}
	if cfg.DestroyRetryMax == 0 {
		cfg.DestroyRetryMax = 50 * time.Millisecond
	}

	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout, Log: slog.Default()}
	deps := agent.PoolDeps{
		VMM:     mgr,
		Cleaner: cleaner,
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	}
	for _, o := range opts {
		o(&deps, mgr)
	}
	p, err := agent.NewPool(cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = p.Shutdown(context.Background())
	})
	return p
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestPoolMaintainsMinReady(t *testing.T) {
	p := testPool(t, agent.PoolConfig{MinReady: 2, MaxReady: 3})
	waitFor(t, 2*time.Second, func() bool {
		return p.Counts().Warm >= 2
	})
	c := p.Counts()
	if c.Warm < 2 {
		t.Fatalf("warm=%d want >= 2", c.Warm)
	}
	if c.Busy != 0 || c.Destroying != 0 {
		t.Fatalf("unexpected counts %+v", c)
	}
}

func TestBindWarmThenJobFinishedReplenish(t *testing.T) {
	p := testPool(t, agent.PoolConfig{MinReady: 1, MaxReady: 2})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })

	ctx := context.Background()
	res, err := p.Bind(ctx, agent.JobPayload{JobID: "job-1", JITConfig: "SECRET_JIT_TOKEN"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.WarmStart {
		t.Fatal("expected warm bind")
	}
	if res.VMID == "" {
		t.Fatal("empty vm id")
	}

	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		c := p.Counts()
		if c.Busy != 1 {
			t.Fatalf("busy=%d want 1: %+v", c.Busy, c)
		}
		if c.Warm+c.PoolBoot != 0 {
			t.Fatalf("replenished while busy: %+v", c)
		}
		time.Sleep(20 * time.Millisecond)
	}

	snap := p.Metrics()
	if snap.WarmBinds != 1 {
		t.Fatalf("WarmBinds=%d", snap.WarmBinds)
	}
	if snap.ColdStarts != 0 {
		t.Fatalf("ColdStarts=%d", snap.ColdStarts)
	}

	if err := p.JobFinished(ctx, res.VMID, "success"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		c := p.Counts()
		return c.Busy == 0 && c.Destroying == 0 && c.Warm >= 1
	})

	// Instance dir gone.
	// Bound VM must not reappear as warm with same id after destroy — new VMs for replenish.
	desired := p.DesiredIDs()
	if _, ok := desired[res.VMID]; ok {
		t.Fatalf("destroyed vm %s still tracked", res.VMID)
	}
}

func TestPool_KillBusyVM(t *testing.T) {
	p := testPool(t, agent.PoolConfig{MinReady: 1, MaxReady: 1})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
	ctx := context.Background()
	res, err := p.Bind(ctx, agent.JobPayload{JobID: "kill-1", JITConfig: "jit"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.KillVM(ctx, res.VMID, "dashboard"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		c := p.Counts()
		return c.Busy == 0 && c.Destroying == 0
	})
	if _, ok := p.DesiredIDs()[res.VMID]; ok {
		t.Fatalf("killed vm %s still tracked", res.VMID)
	}
}

func TestBindFailsAfterSelect_DestroysNoRewarm(t *testing.T) {
	failRunner := &agent.StubRunner{
		StartFunc: func(ctx context.Context, id vmm.ID, job agent.JobPayload) error {
			return errors.New("inject failed")
		},
	}
	var boundID vmm.ID
	p := testPool(t, agent.PoolConfig{MinReady: 1, MaxReady: 2}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Runner = failRunner
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })

	// Capture current warm id before bind.
	// After failed bind it must not return to warm.
	ctx := context.Background()
	// We need the id that would be selected — bind will destroy it.
	res, err := p.Bind(ctx, agent.JobPayload{JobID: "job-fail", JITConfig: "secret"})
	if err == nil {
		t.Fatal("expected bind error")
	}
	if res != nil {
		t.Fatalf("expected nil result, got %+v", res)
	}
	_ = boundID

	waitFor(t, 2*time.Second, func() bool {
		c := p.Counts()
		// Failed VM destroyed; pool replenished with a *new* warm VM.
		return c.Destroying == 0 && c.Busy == 0 && c.Warm >= 1
	})

	// No busy leftovers.
	c := p.Counts()
	if c.Busy != 0 {
		t.Fatalf("busy should be 0: %+v", c)
	}
}

func TestDestroyFails_RetryBackoffNoBlindReplenish(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	base, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}

	flaky := &flakyVMM{Manager: base, failN: 3}
	cleaner := &cleanup.Cleaner{VMM: flaky, Layout: layout}
	cfg := agent.PoolConfig{
		MinReady:          1,
		MaxReady:          1,
		MaxTotalVMs:       3, // tight cap
		VCPUs:             1,
		MemoryMiB:         512,
		ImagePath:         img,
		ReconcileInterval: 20 * time.Millisecond,
		DestroyRetryBase:  5 * time.Millisecond,
		DestroyRetryMax:   20 * time.Millisecond,
		BindWait:          500 * time.Millisecond,
	}
	p, err := agent.NewPool(cfg, agent.PoolDeps{
		VMM:     flaky,
		Cleaner: cleaner,
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })

	res, err := p.Bind(ctx, agent.JobPayload{JobID: "j1", JITConfig: "x"})
	if err != nil {
		t.Fatal(err)
	}

	// Job finished while destroy will fail a few times.
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.JobFinished(ctx, res.VMID, "done")
	}()

	// While destroy is failing, destroying count should be 1 and total should not explode.
	waitFor(t, 2*time.Second, func() bool {
		return p.Metrics().DestroyFail >= 1 || p.LastDestroyError() != "" || flaky.fails.Load() >= 1
	})

	// Allow retries to eventually succeed.
	select {
	case err := <-errCh:
		// First JobFinished may return error if destroy failed on first try.
		_ = err
	case <-time.After(2 * time.Second):
		// destroyNow may have returned error already
	}

	waitFor(t, 3*time.Second, func() bool {
		c := p.Counts()
		return c.Destroying == 0 && flaky.fails.Load() >= 1
	})

	if p.Metrics().DestroyFail < 1 {
		t.Fatalf("expected destroy failures recorded, metrics=%+v fails=%d", p.Metrics(), flaky.fails.Load())
	}
	// Eventually destroyed; last error cleared or destroys OK increased.
	waitFor(t, 2*time.Second, func() bool {
		return p.Metrics().DestroysOK >= 1
	})

	// Hard cap respected during failure window (total never > MaxTotalVMs).
	if c := p.Counts(); c.Total() > cfg.MaxTotalVMs {
		t.Fatalf("total %d exceeded max %d", c.Total(), cfg.MaxTotalVMs)
	}
}

func TestAgentRestart_OrphanSweepThenRebuild(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Simulate leftover instances from a previous agent process.
	for _, id := range []vmm.ID{"orphan-old-1", "orphan-old-2"} {
		if _, err := mgr.Create(ctx, vmm.Config{ID: id, VCPUs: 1, MemoryMiB: 512, RootfsPath: img}); err != nil {
			t.Fatal(err)
		}
		if err := mgr.Boot(ctx, id); err != nil {
			t.Fatal(err)
		}
	}

	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	cfg := agent.PoolConfig{
		MinReady:          1,
		MaxReady:          2,
		VCPUs:             1,
		MemoryMiB:         512,
		ImagePath:         img,
		ReconcileInterval: 20 * time.Millisecond,
	}
	p, err := agent.NewPool(cfg, agent.PoolDeps{VMM: mgr, Cleaner: cleaner, Runner: &agent.StubRunner{}, Log: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	// Orphans gone.
	for _, id := range []vmm.ID{"orphan-old-1", "orphan-old-2"} {
		ok, err := mgr.Exists(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("orphan %s still exists", id)
		}
	}
	if p.Metrics().Orphans < 2 {
		t.Fatalf("Orphans metric = %d", p.Metrics().Orphans)
	}

	// Pool rebuilt.
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
}

func TestReplenishSkipsWhileBusy(t *testing.T) {
	p := testPool(t, agent.PoolConfig{
		MinReady: 1,
		MaxReady: 4,
		Shapes: []agent.VMShape{
			{Label: "temperci-4vcpu-6g-ubuntu-2404", VCPUs: 4, MemoryMiB: 6144, MinReady: 1},
			{Label: "temperci-4vcpu-ubuntu-2404", VCPUs: 4, MemoryMiB: 8192, MinReady: 1},
		},
	})
	waitFor(t, 3*time.Second, func() bool { return p.Counts().Warm >= 2 })
	if n := p.Counts().Warm; n != 2 {
		t.Fatalf("warm=%d want 2 before bind", n)
	}

	ctx := context.Background()
	res, err := p.Bind(ctx, agent.JobPayload{
		JobID:     "e2e-8g",
		JITConfig: "jit",
		Labels:    []string{"temperci-4vcpu-8g-ubuntu-2404"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bound8g := false
	for _, u := range p.ListUsage() {
		if u.ID == string(res.VMID) && u.MemoryMiB == 8192 && u.State == string(agent.StateBusy) {
			bound8g = true
		}
	}
	if !bound8g {
		t.Fatalf("expected busy 8g VM %s in usage: %+v", res.VMID, p.ListUsage())
	}

	// Do not boot a replacement 8g (or extra 6g) while the job is running.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		c := p.Counts()
		if c.Busy != 1 {
			t.Fatalf("busy=%d want 1: %+v", c.Busy, c)
		}
		if c.Warm+c.PoolBoot > 1 {
			t.Fatalf("refilled while busy: %+v", c)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if c := p.Counts(); c.Warm != 1 || c.PoolBoot != 0 {
		t.Fatalf("while busy want warm=1 pool_boot=0: %+v", c)
	}

	if err := p.JobFinished(ctx, res.VMID, "success"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		c := p.Counts()
		return c.Busy == 0 && c.Destroying == 0 && c.Warm >= 2
	})
}

func TestBindPacks12gWith6gWhenRAMFits(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 32072, RAMAvailMiB: 28000, DiskTotalMiB: 96000, DiskFreeMiB: 66000, NumCPU: 24,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, BindWait: 200 * time.Millisecond,
		VCPUs: 4, MemoryMiB: 6144, ReserveRAMMiB: 2048, ReserveDiskMiB: 0,
		Shapes: []agent.VMShape{
			{Label: "temperci-4vcpu-6g-ubuntu-2404", VCPUs: 4, MemoryMiB: 6144, MinReady: 0},
		},
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})

	ctx := context.Background()
	if _, err := p.Bind(ctx, agent.JobPayload{
		JobID: "e2e-12g", JITConfig: "jit",
		Labels: []string{"temperci-4vcpu-12g-ubuntu-2404"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Bind(ctx, agent.JobPayload{
		JobID: "api-6g", JITConfig: "jit",
		Labels: []string{"temperci-4vcpu-6g-ubuntu-2404"},
	}); err != nil {
		t.Fatalf("6g should pack next to 12g when RAM fits: %v", err)
	}
	if c := p.Counts(); c.Busy != 2 {
		t.Fatalf("busy=%d want 2", c.Busy)
	}
}

func TestBindWaitsWhenCommittedRAMIsFull(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 16384, RAMAvailMiB: 15000, DiskTotalMiB: 96000, DiskFreeMiB: 66000, NumCPU: 8,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, BindWait: 80 * time.Millisecond,
		VCPUs: 4, MemoryMiB: 6144, ReserveRAMMiB: 2048, ReserveDiskMiB: 0,
		Shapes: []agent.VMShape{
			{Label: "temperci-4vcpu-6g-ubuntu-2404", VCPUs: 4, MemoryMiB: 6144, MinReady: 0},
		},
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})

	ctx := context.Background()
	e2e, err := p.Bind(ctx, agent.JobPayload{
		JobID: "e2e-12g", JITConfig: "jit",
		Labels: []string{"temperci-4vcpu-12g-ubuntu-2404"},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := p.Bind(ctx, agent.JobPayload{
			JobID: "api-6g", JITConfig: "jit",
			Labels: []string{"temperci-4vcpu-6g-ubuntu-2404"},
		})
		done <- err
	}()

	select {
	case err := <-done:
		t.Fatalf("6g bind returned while 12g still holds RAM: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := p.JobFinished(ctx, e2e.VMID, "success"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("6g bind after 12g freed RAM: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("6g bind did not proceed after RAM freed")
	}
}

func TestBindTwoSmallJobsStillPack(t *testing.T) {
	p := testPool(t, agent.PoolConfig{
		MinReady: 2,
		MaxReady: 2,
		BindWait: 500 * time.Millisecond,
		Shapes: []agent.VMShape{
			{Label: "temperci-4vcpu-6g-ubuntu-2404", VCPUs: 4, MemoryMiB: 6144, MinReady: 2},
		},
	})
	waitFor(t, 3*time.Second, func() bool { return p.Counts().Warm >= 2 })

	ctx := context.Background()
	if _, err := p.Bind(ctx, agent.JobPayload{
		JobID: "api-1", JITConfig: "jit",
		Labels: []string{"temperci-4vcpu-6g-ubuntu-2404"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Bind(ctx, agent.JobPayload{
		JobID: "api-2", JITConfig: "jit",
		Labels: []string{"temperci-4vcpu-6g-ubuntu-2404"},
	}); err != nil {
		t.Fatalf("second 6g bind: %v", err)
	}
	if c := p.Counts(); c.Busy != 2 {
		t.Fatalf("busy=%d want 2: %+v", c.Busy, c)
	}
}

func TestIdleRecycle(t *testing.T) {
	start := time.Now()
	var clock atomic.Int64
	clock.Store(start.UnixNano())

	p := testPool(t, agent.PoolConfig{
		MinReady:    1,
		MaxReady:    1,
		IdleRecycle: 100 * time.Millisecond,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Now = func() time.Time {
			return time.Unix(0, clock.Load())
		}
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })

	// Advance clock past idle recycle.
	clock.Store(start.Add(500 * time.Millisecond).UnixNano())

	waitFor(t, 2*time.Second, func() bool {
		return p.Metrics().Recycles >= 1
	})
	// Still maintain min_ready after recycle.
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
}

func TestBindWarmAndColdMix(t *testing.T) {
	p := testPool(t, agent.PoolConfig{
		MinReady: 1,
		MaxReady: 2,
		BindWait: 500 * time.Millisecond,
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
	ctx := context.Background()
	r1, err := p.Bind(ctx, agent.JobPayload{JobID: "a", JITConfig: "s"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := p.Bind(ctx, agent.JobPayload{JobID: "b", JITConfig: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if r1 == nil || r2 == nil {
		t.Fatal("nil bind result")
	}
	_ = p.JobFinished(ctx, r1.VMID, "done")
	_ = p.JobFinished(ctx, r2.VMID, "done")
	m := p.Metrics()
	if m.WarmBinds+m.ColdStarts < 2 {
		t.Fatalf("metrics %+v", m)
	}
}

func TestBindColdDirect(t *testing.T) {
	// MinReady=0: no warm pool; every bind cold-boots.
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	cfg := agent.PoolConfig{
		MinReady:          0,
		MaxReady:          1,
		MaxTotalVMs:       4,
		VCPUs:             1,
		MemoryMiB:         512,
		ImagePath:         img,
		ReconcileInterval: time.Hour,
		BindWait:          200 * time.Millisecond,
	}
	p, err := agent.NewPool(cfg, agent.PoolDeps{VMM: mgr, Cleaner: cleaner, Runner: &agent.StubRunner{}, Log: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	time.Sleep(50 * time.Millisecond)
	if p.Counts().Warm != 0 {
		t.Fatalf("expected no warm with min_ready=0: %+v", p.Counts())
	}

	r, err := p.Bind(ctx, agent.JobPayload{JobID: "cold-1", JITConfig: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if r.WarmStart {
		t.Fatal("expected cold bind")
	}
	if p.Metrics().ColdStarts < 1 {
		t.Fatalf("ColdStarts=%d", p.Metrics().ColdStarts)
	}
	if err := p.JobFinished(ctx, r.VMID, "ok"); err != nil {
		t.Fatal(err)
	}
}

// flakyVMM fails Destroy the first failN times globally, then succeeds.
type flakyVMM struct {
	vmm.Manager
	mu    sync.Mutex
	failN int
	fails atomic.Int64
	calls atomic.Int64
}

func (f *flakyVMM) Destroy(ctx context.Context, id vmm.ID) error {
	n := f.calls.Add(1)
	f.mu.Lock()
	limit := f.failN
	f.mu.Unlock()
	if int(n) <= limit {
		f.fails.Add(1)
		return fmt.Errorf("simulated destroy failure #%d", n)
	}
	return f.Manager.Destroy(ctx, id)
}

func TestPool_ClampsMinReadyToHostRAM(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 16384, RAMAvailMiB: 14000, DiskTotalMiB: 200000, DiskFreeMiB: 200000, NumCPU: 8,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 3, MaxReady: 3, MemoryMiB: 8192, DiskPerVMMiB: 256,
		ReserveRAMMiB: 2048, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm+p.Counts().PoolBoot >= 1 })
	time.Sleep(150 * time.Millisecond)
	c := p.Counts()
	if c.Warm+c.PoolBoot+c.Busy > 1 {
		t.Fatalf("clamped host must not boot >1 VM: %+v", c)
	}
	if p.EffectiveMaxReady() != 1 {
		t.Fatalf("EffectiveMaxReady=%d", p.EffectiveMaxReady())
	}
	if p.ConfiguredMaxReady() != 3 {
		t.Fatalf("ConfiguredMaxReady=%d", p.ConfiguredMaxReady())
	}
	if p.ClampReason() != agent.ReasonRAMFit {
		t.Fatalf("ClampReason=%q", p.ClampReason())
	}
}

func TestPool_HostResourcesReportsAllocatedAndExclusive(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 32072, RAMAvailMiB: 19439, DiskTotalMiB: 96000, DiskFreeMiB: 66000, NumCPU: 24,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 0, MaxReady: 2, VCPUs: 4, MemoryMiB: 6144, DiskPerVMMiB: 256,
		ReserveRAMMiB: 2048, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})
	res, err := p.Bind(context.Background(), agent.JobPayload{
		JobID: "e2e-12g", JITConfig: "x", Labels: []string{"temperci-4vcpu-12g-ubuntu-2404"},
	})
	if err != nil {
		t.Fatal(err)
	}
	hr := p.HostResources()
	if hr == nil {
		t.Fatal("HostResources nil")
	}
	if hr.AllocatedRAMMiB < 12*1024 {
		t.Fatalf("allocated=%d want >=12288", hr.AllocatedRAMMiB)
	}
	if hr.ReserveRAMMiB != 2048 {
		t.Fatalf("reserve=%d", hr.ReserveRAMMiB)
	}
	if hr.ExclusiveBusy {
		t.Fatal("packing is RAM-based; exclusive_busy must stay false")
	}
	if hr.RAMTotalMiB != 32072 || hr.RAMAvailMiB != 19439 {
		t.Fatalf("ram %+v", hr)
	}
	_ = p.JobFinished(context.Background(), res.VMID, "ok")
}

func TestPool_LiveRAMBlocksColdCreate(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 512, DiskTotalMiB: 200000, DiskFreeMiB: 200000, NumCPU: 8,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 0, MaxReady: 2, MemoryMiB: 4096, DiskPerVMMiB: 256,
		ReserveRAMMiB: 0, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})
	_, err := p.Bind(context.Background(), agent.JobPayload{JobID: "cold", JITConfig: "x"})
	if err == nil || !errors.Is(err, agent.ErrNoCapacity) {
		t.Fatalf("want ErrNoCapacity, got %v", err)
	}
	if p.LastAdmitReason() != agent.ReasonRAMAvail {
		t.Fatalf("LastAdmitReason=%q", p.LastAdmitReason())
	}
}

func TestPool_LastAdmitClearedWhenCreateSucceeds(t *testing.T) {
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 512, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, MemoryMiB: 4096, DiskPerVMMiB: 256,
		ReserveRAMMiB: 0, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = cur
	})
	waitFor(t, 2*time.Second, func() bool { return p.LastAdmitReason() == agent.ReasonRAMAvail })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
	if got := p.LastAdmitReason(); got != "" {
		t.Fatalf("LastAdmitReason=%q after successful create, want empty", got)
	}
}

func TestPool_ReloadImageRefreshesDiskPerVMMiB(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img1 := filepath.Join(layout.ImagesDir(), "small")
	img2 := filepath.Join(layout.ImagesDir(), "large")
	if err := os.WriteFile(img1, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img2, make([]byte, 5*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	p, err := agent.NewPool(agent.PoolConfig{
		MinReady: 0, MaxReady: 1, ImagePath: img1, VCPUs: 1, MemoryMiB: 256,
		ReconcileInterval: time.Hour,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSmall := agent.OverlayEstimateMiB(img1)
	if got := p.DiskPerVMMiB(); got != wantSmall {
		t.Fatalf("DiskPerVMMiB=%d want %d", got, wantSmall)
	}
	if _, err := p.ReloadImage(context.Background(), img2, false); err != nil {
		t.Fatal(err)
	}
	wantLarge := agent.OverlayEstimateMiB(img2)
	if wantLarge == wantSmall {
		t.Fatalf("test images must differ in overlay estimate: both %d", wantSmall)
	}
	if got := p.DiskPerVMMiB(); got != wantLarge {
		t.Fatalf("DiskPerVMMiB=%d want %d after larger rootfs", got, wantLarge)
	}
}

func TestPool_WarmBindWhenCreateBlocked(t *testing.T) {
	// Huge RAM at start so one warm VM boots; then flip live RAM down.
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, MemoryMiB: 4096, DiskPerVMMiB: 256,
		ReserveRAMMiB: 0, ReserveDiskMiB: 0, BindWait: 200 * time.Millisecond,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = cur
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000})
	res, err := p.Bind(context.Background(), agent.JobPayload{JobID: "warm", JITConfig: "x"})
	if err != nil {
		t.Fatalf("warm bind must succeed: %v", err)
	}
	if !res.WarmStart {
		t.Fatal("expected warm bind")
	}
}

func TestBind_MatchingWarmThenColdDifferentSize(t *testing.T) {
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 3, VCPUs: 4, MemoryMiB: 8192,
		Shapes: []agent.VMShape{
			{Label: "temperci-4vcpu-ubuntu-2404", VCPUs: 4, MemoryMiB: 8192, MinReady: 1},
			{Label: "temperci-2vcpu-4g-ubuntu-2404", VCPUs: 2, MemoryMiB: 4096, MinReady: 0},
		},
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })

	warm, err := p.Bind(context.Background(), agent.JobPayload{
		JobID: "big", Labels: []string{"temperci-4vcpu-ubuntu-2404"}, JITConfig: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !warm.WarmStart {
		t.Fatal("4vcpu job should take the warm VM")
	}

	cold, err := p.Bind(context.Background(), agent.JobPayload{
		JobID: "small", Labels: []string{"temperci-2vcpu-4g-ubuntu-2404"}, JITConfig: "x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cold.WarmStart {
		t.Fatal("2vcpu job must cold-boot when that size is not warm")
	}

	us := p.ListUsage()
	var saw2, saw4 bool
	for _, u := range us {
		if u.VCPUs == 2 && u.MemoryMiB == 4096 {
			saw2 = true
		}
		if u.VCPUs == 4 && u.MemoryMiB == 8192 {
			saw4 = true
		}
	}
	if !saw2 || !saw4 {
		t.Fatalf("usage missing sizes: %+v", us)
	}
}

type atomicInventory struct {
	mu  sync.Mutex
	inv agent.HostInventory
}

func (a *atomicInventory) Sample() (agent.HostInventory, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inv, nil
}

func (a *atomicInventory) set(inv agent.HostInventory) {
	a.mu.Lock()
	a.inv = inv
	a.mu.Unlock()
}

// delayedVMM wraps fake.Manager to slow Boot and count concurrent boots.
type delayedVMM struct {
	inner   *fake.Manager
	delay   time.Duration
	creates *atomic.Int32
	current *atomic.Int32
	max     *atomic.Int32
}

func (d *delayedVMM) Create(ctx context.Context, cfg vmm.Config) (*vmm.Info, error) {
	if d.creates != nil {
		d.creates.Add(1)
	}
	return d.inner.Create(ctx, cfg)
}

func (d *delayedVMM) Boot(ctx context.Context, id vmm.ID) error {
	if d.current != nil {
		n := d.current.Add(1)
		for {
			old := d.max.Load()
			if n <= old || d.max.CompareAndSwap(old, n) {
				break
			}
		}
		defer d.current.Add(-1)
	}
	if d.delay > 0 {
		time.Sleep(d.delay)
	}
	return d.inner.Boot(ctx, id)
}

func (d *delayedVMM) Destroy(ctx context.Context, id vmm.ID) error {
	return d.inner.Destroy(ctx, id)
}
func (d *delayedVMM) Exists(ctx context.Context, id vmm.ID) (bool, error) {
	return d.inner.Exists(ctx, id)
}
func (d *delayedVMM) Info(ctx context.Context, id vmm.ID) (*vmm.Info, error) {
	return d.inner.Info(ctx, id)
}
func (d *delayedVMM) List(ctx context.Context) ([]vmm.Info, error) {
	return d.inner.List(ctx)
}

func TestBindWaitsForInFlightPoolBoot(t *testing.T) {
	var creates atomic.Int32
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, BindWait: time.Second,
	}, func(deps *agent.PoolDeps, mgr *fake.Manager) {
		deps.VMM = &delayedVMM{inner: mgr, delay: 150 * time.Millisecond, creates: &creates}
	})
	waitFor(t, 2*time.Second, func() bool {
		c := p.Counts()
		return c.Warm+c.PoolBoot >= 1
	})
	res, err := p.Bind(context.Background(), agent.JobPayload{JobID: "wait-boot", JITConfig: "jit"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.WarmStart {
		t.Fatal("expected warm bind from in-flight pool_boot, not a second cold create")
	}
	if n := creates.Load(); n != 1 {
		t.Fatalf("creates=%d want 1 (replenish only)", n)
	}
}

func TestReplenishBootsInParallel(t *testing.T) {
	var current, max atomic.Int32
	p := testPool(t, agent.PoolConfig{
		MinReady: 2, MaxReady: 2,
	}, func(deps *agent.PoolDeps, mgr *fake.Manager) {
		deps.VMM = &delayedVMM{
			inner: mgr, delay: 120 * time.Millisecond,
			current: &current, max: &max,
		}
	})
	waitFor(t, 3*time.Second, func() bool { return p.Counts().Warm >= 2 })
	if max.Load() < 2 {
		t.Fatalf("replenish was serial: max concurrent boots=%d want 2", max.Load())
	}
}
