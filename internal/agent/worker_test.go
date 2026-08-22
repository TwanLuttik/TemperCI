package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/control"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

func TestWorker_JobDeadlineForceDestroy(t *testing.T) {
	store := control.NewAssignmentStore()
	srv := control.NewServer(control.ServerConfig{
		Store:      store,
		AgentToken: "tok",
		Logger:     slog.Default(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady:          1,
		MaxReady:          1,
		ImagePath:         img,
		VCPUs:             1,
		MemoryMiB:         256,
		ReconcileInterval: 20 * time.Millisecond,
		BindWait:          time.Second,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })

	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 1 })

	store.Put(&control.Assignment{
		JobID:            77,
		EncodedJITConfig: "jit-secret-deadline",
		Status:           control.AssignmentMinted,
		Org:              "acme",
	})

	client := agent.NewControlClient(ts.URL, "deadline-agent", "tok", ts.Client())
	worker := &agent.Worker{
		Client:       client,
		Pool:         pool,
		Log:          slog.Default(),
		PollInterval: 20 * time.Millisecond,
		JobSimulate:  5 * time.Second, // would be long
		JobDeadline:  40 * time.Millisecond,
		Capacity:     1,
	}
	go func() { _ = worker.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		a := store.Get(77)
		return a != nil && a.Status == control.AssignmentFinished
	})
	a := store.Get(77)
	if a.Outcome != "timeout" {
		t.Fatalf("outcome = %q want timeout", a.Outcome)
	}
	if a.EncodedJITConfig != "" {
		t.Fatal("jit should be cleared")
	}
	if a.VMID == "" {
		t.Fatal("expected vm id on timeout finish")
	}
	if _, err := os.Stat(layout.InstanceDir(vmm.ID(a.VMID))); !os.IsNotExist(err) {
		t.Fatalf("instance should be destroyed after timeout: %v", err)
	}
}

func TestWorker_NoCapacityDoesNotClaim(t *testing.T) {
	// Control tracks claims; agent with Capacity 0 never receives work.
	var mu sync.Mutex
	var claimBodies []api.ClaimRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			_ = json.NewEncoder(w).Encode(api.RegisterResponse{OK: true, AgentID: "c0"})
		case "/v1/agent/jobs/claim":
			var req api.ClaimRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			claimBodies = append(claimBodies, req)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ClaimResponse{OK: true, Job: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 0, MaxReady: 0, MaxTotalVMs: 1, ImagePath: img, VCPUs: 1, MemoryMiB: 128,
		ReconcileInterval: time.Hour,
	}, agent.PoolDeps{VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout}, Runner: &agent.StubRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	// Force busy count via direct path: Capacity 0 free slots always.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Shutdown(context.Background()) }()

	client := agent.NewControlClient(ts.URL, "c0", "t", ts.Client())
	// Capacity 0 → free slots 0 always.
	w := &agent.Worker{
		Client: client, Pool: pool, Capacity: 0,
		PollInterval: 20 * time.Millisecond, JobSimulate: 0,
	}
	// Capacity validates to 1 in Run if <=0 — set Capacity 1 and make busy=1 instead.
	// Bind a fake job to fill busy.
	waitFor(t, 2*time.Second, func() bool { return true })
	// Use Capacity 1 with a busy VM: bind job first.
	pool2, err := agent.NewPool(agent.PoolConfig{
		MinReady: 1, MaxReady: 1, ImagePath: img, VCPUs: 1, MemoryMiB: 128,
		ReconcileInterval: 20 * time.Millisecond, BindWait: time.Second,
	}, agent.PoolDeps{VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout}, Runner: &agent.StubRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	_ = pool.Shutdown(context.Background())
	if err := pool2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool2.Shutdown(context.Background()) }()
	waitFor(t, 2*time.Second, func() bool { return pool2.Counts().Warm >= 1 })
	if _, err := pool2.Bind(ctx, agent.JobPayload{JobID: "hold", JITConfig: "x"}); err != nil {
		t.Fatal(err)
	}
	// busy=1 capacity=1 → free=0; worker must not POST /claim.
	w = &agent.Worker{Client: client, Pool: pool2, Capacity: 1, PollInterval: 15 * time.Millisecond}
	runCtx, runCancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer runCancel()
	_ = w.Run(runCtx)

	mu.Lock()
	defer mu.Unlock()
	if len(claimBodies) != 0 {
		t.Fatalf("expected no claims when free_slots=0, got %d: %+v", len(claimBodies), claimBodies)
	}
}

func TestWorker_ConcurrentJobsUpToCapacity(t *testing.T) {
	store := control.NewAssignmentStore()
	srv := control.NewServer(control.ServerConfig{
		Store:      store,
		AgentToken: "tok",
		Logger:     slog.Default(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}

	var startMu sync.Mutex
	started := 0
	maxStarted := 0
	release := make(chan struct{})
	runner := &agent.StubRunner{
		StartFunc: func(ctx context.Context, id vmm.ID, job agent.JobPayload) error {
			startMu.Lock()
			started++
			if started > maxStarted {
				maxStarted = started
			}
			startMu.Unlock()
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		},
	}

	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady:          2,
		MaxReady:          2,
		ImagePath:         img,
		VCPUs:             1,
		MemoryMiB:         256,
		ReconcileInterval: 20 * time.Millisecond,
		BindWait:          time.Second,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner:  runner,
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })
	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 2 })

	for _, id := range []int64{101, 102, 103} {
		store.Put(&control.Assignment{
			JobID:            id,
			EncodedJITConfig: fmt.Sprintf("jit-%d", id),
			Status:           control.AssignmentMinted,
			Org:              "acme",
		})
	}

	client := agent.NewControlClient(ts.URL, "conc-agent", "tok", ts.Client())
	worker := &agent.Worker{
		Client:       client,
		Pool:         pool,
		Log:          slog.Default(),
		PollInterval: 15 * time.Millisecond,
		Capacity:     2,
	}
	go func() { _ = worker.Run(ctx) }()

	// Two jobs must be in StartRunner at once (Capacity=2).
	waitFor(t, 3*time.Second, func() bool {
		startMu.Lock()
		defer startMu.Unlock()
		return started >= 2
	})
	time.Sleep(80 * time.Millisecond)
	startMu.Lock()
	if started != 2 {
		startMu.Unlock()
		t.Fatalf("in-flight starts = %d want 2 (capacity)", started)
	}
	if maxStarted != 2 {
		startMu.Unlock()
		t.Fatalf("max in-flight = %d want 2", maxStarted)
	}
	startMu.Unlock()

	active := 0
	for _, id := range []int64{101, 102, 103} {
		a := store.Get(id)
		if a == nil {
			continue
		}
		switch a.Status {
		case control.AssignmentAssigned, control.AssignmentStarted, control.AssignmentFinished:
			active++
		}
	}
	if active != 2 {
		t.Fatalf("claimed/started jobs = %d want 2 while first pair blocked", active)
	}

	close(release)
	waitFor(t, 4*time.Second, func() bool {
		for _, id := range []int64{101, 102, 103} {
			a := store.Get(id)
			if a == nil || a.Status != control.AssignmentFinished {
				return false
			}
		}
		return true
	})
}

func TestWorker_ResourceFullDoesNotClaim(t *testing.T) {
	var mu sync.Mutex
	var claims int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			_ = json.NewEncoder(w).Encode(api.RegisterResponse{OK: true, AgentID: "rf"})
		case "/v1/agent/jobs/claim":
			mu.Lock()
			claims++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ClaimResponse{OK: true, Job: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 0, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 4096,
		DiskPerVMMiB: 256, ReconcileInterval: time.Hour, BindWait: 50 * time.Millisecond,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{}, Inventory: inv,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Shutdown(context.Background()) }()

	w := &agent.Worker{
		Client: agent.NewControlClient(ts.URL, "rf", "t", ts.Client()),
		Pool:   pool, Capacity: 2, PollInterval: 15 * time.Millisecond,
	}
	go func() { _ = w.Run(ctx) }()
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	n := claims
	mu.Unlock()
	if n != 0 {
		t.Fatalf("claimed %d times; leftover RAM cannot create and warm=0", n)
	}
}

func TestWorker_WarmStillClaimsWhenCreateBlocked(t *testing.T) {
	var mu sync.Mutex
	var lastFree int
	var claims int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			_ = json.NewEncoder(w).Encode(api.RegisterResponse{OK: true, AgentID: "rw"})
		case "/v1/agent/jobs/claim":
			var req api.ClaimRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			claims++
			lastFree = req.FreeSlots
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ClaimResponse{OK: true, Job: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 1, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 4096,
		DiskPerVMMiB: 256, ReconcileInterval: 20 * time.Millisecond, BindWait: time.Second,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{}, Inventory: cur,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Shutdown(context.Background()) }()
	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 1 })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000})

	w := &agent.Worker{
		Client: agent.NewControlClient(ts.URL, "rw", "t", ts.Client()),
		Pool:   pool, Capacity: 2, PollInterval: 15 * time.Millisecond,
	}
	go func() { _ = w.Run(ctx) }()
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return claims > 0 && lastFree == 1
	})
}

func TestWorker_LeftoverFullClaimsOnlyOneWarmJob(t *testing.T) {
	store := control.NewAssignmentStore()
	srv := control.NewServer(control.ServerConfig{
		Store:      store,
		AgentToken: "tok",
		Logger:     slog.Default(),
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 1, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 4096,
		DiskPerVMMiB: 256, ReconcileInterval: time.Hour, BindWait: 50 * time.Millisecond,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{}, Inventory: cur, Log: slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })
	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 1 })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000})

	for _, id := range []int64{201, 202} {
		store.Put(&control.Assignment{
			JobID:            id,
			EncodedJITConfig: fmt.Sprintf("jit-%d", id),
			Status:           control.AssignmentMinted,
			Org:              "acme",
		})
	}

	gate := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})

	client := agent.NewControlClient(ts.URL, "leftover-agent", "tok", ts.Client())
	worker := &agent.Worker{
		Client:       client,
		Pool:         pool,
		Log:          slog.Default(),
		PollInterval: 15 * time.Millisecond,
		Capacity:     2,
		JobSimulate:  time.Hour,
		BeforeBind:   func() { <-gate },
	}
	go func() { _ = worker.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		a := store.Get(201)
		b := store.Get(202)
		if a == nil || b == nil {
			return false
		}
		claimed := 0
		if a.Status != control.AssignmentMinted {
			claimed++
		}
		if b.Status != control.AssignmentMinted {
			claimed++
		}
		return claimed >= 1
	})
	time.Sleep(120 * time.Millisecond)

	claimed, pending := 0, 0
	errored := 0
	for _, id := range []int64{201, 202} {
		a := store.Get(id)
		if a == nil {
			t.Fatalf("missing job %d", id)
		}
		switch a.Status {
		case control.AssignmentMinted:
			pending++
		case control.AssignmentAssigned, control.AssignmentStarted:
			claimed++
		case control.AssignmentFinished, control.AssignmentFailed:
			claimed++
			errored++
		}
	}
	if claimed != 1 || pending != 1 {
		t.Fatalf("claimed=%d pending=%d errored=%d want 1 claimed / 1 pending", claimed, pending, errored)
	}
}
