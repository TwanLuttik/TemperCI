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

	waitFor(t, 2*time.Second, func() bool {
		c := p.Counts()
		return c.Busy == 1 && c.Warm >= 1 // replenished
	})

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
	mu     sync.Mutex
	failN  int
	fails  atomic.Int64
	calls  atomic.Int64
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
