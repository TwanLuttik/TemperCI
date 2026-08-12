package agent_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

// TestOrphanSweep_AfterAgentKillMidJob proves design §8 item 6:
// kill agent mid-job (busy VM left on disk) → restart → orphan sweep destroys leftovers.
func TestOrphanSweep_AfterAgentKillMidJob(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base")
	if err := os.WriteFile(img, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout, Log: slog.Default()}

	// --- process 1: start pool, bind a job (busy), then "kill" without destroy ---
	pool1, err := agent.NewPool(agent.PoolConfig{
		MinReady:          1,
		MaxReady:          1,
		ImagePath:         img,
		VCPUs:             1,
		MemoryMiB:         256,
		ReconcileInterval: 20 * time.Millisecond,
		BindWait:          time.Second,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: cleaner,
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := pool1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return pool1.Counts().Warm >= 1 })

	res, err := pool1.Bind(ctx, agent.JobPayload{
		JobID:     "mid-job-1",
		JITConfig: "jit-should-not-survive-restart",
	})
	if err != nil {
		t.Fatal(err)
	}
	busyID := res.VMID
	if pool1.Counts().Busy != 1 {
		t.Fatalf("busy = %d", pool1.Counts().Busy)
	}
	// Instance dir must exist (mid-job state on host).
	if _, err := os.Stat(layout.InstanceDir(busyID)); err != nil {
		t.Fatalf("busy instance missing: %v", err)
	}

	// Kill: stop reconciler only — do NOT call Shutdown (no graceful destroy).
	// This leaves the busy VM's host artifacts behind, like a crash/kill -9.
	pool1.Stop()

	// Confirm leftover still present after "kill".
	ok, err := mgr.Exists(ctx, busyID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected leftover busy VM after kill without destroy")
	}
	if _, err := os.Stat(layout.InstanceDir(busyID)); err != nil {
		t.Fatalf("expected leftover instance dir: %v", err)
	}

	// --- process 2: new pool on same data_dir — Start sweeps orphans ---
	pool2, err := agent.NewPool(agent.PoolConfig{
		MinReady:          1,
		MaxReady:          1,
		ImagePath:         img,
		VCPUs:             1,
		MemoryMiB:         256,
		ReconcileInterval: 20 * time.Millisecond,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: cleaner,
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pool2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool2.Shutdown(context.Background()) })

	// Leftover mid-job instance must be gone.
	ok, err = mgr.Exists(ctx, busyID)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("orphan busy VM %s still exists after restart sweep", busyID)
	}
	if _, err := os.Stat(layout.InstanceDir(busyID)); !os.IsNotExist(err) {
		t.Fatalf("orphan instance dir still present: %v", err)
	}
	if pool2.Metrics().Orphans < 1 {
		t.Fatalf("Orphans metric = %d want >=1", pool2.Metrics().Orphans)
	}

	// Pool rebuilt to min_ready.
	waitFor(t, 2*time.Second, func() bool { return pool2.Counts().Warm >= 1 })
	// New warm VMs must not carry prior JIT.
	for id := range pool2.DesiredIDs() {
		jitPath := layout.JITConfigPath(id)
		if b, err := os.ReadFile(jitPath); err == nil && len(b) > 0 {
			t.Fatalf("warm vm %s has leftover guest files", id)
		}
	}
}
