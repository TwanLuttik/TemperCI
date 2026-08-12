package agent_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

func TestPool_DrainWarmAndReloadImage(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img1 := filepath.Join(layout.ImagesDir(), "v1")
	img2 := filepath.Join(layout.ImagesDir(), "v2")
	if err := os.WriteFile(img1, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(img2, []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady:          2,
		MaxReady:          2,
		ImagePath:         img1,
		VCPUs:             1,
		MemoryMiB:         256,
		ReconcileInterval: 20 * time.Millisecond,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner:  &agent.StubRunner{},
		Log:     slog.Default(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })

	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 2 })
	oldWarm := pool.Counts().Warm

	n, err := pool.ReloadImage(ctx, img2, true)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("drained = %d oldWarm=%d", n, oldWarm)
	}
	if pool.ImagePath() != img2 {
		t.Fatalf("image = %s", pool.ImagePath())
	}
	// Pool should refill warm with new image path.
	waitFor(t, 3*time.Second, func() bool { return pool.Counts().Warm >= 2 })
}

func TestLocalServer_MetricsAndDrain(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 1, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 128,
		ReconcileInterval: 20 * time.Millisecond,
	}, agent.PoolDeps{VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout}, Runner: &agent.StubRunner{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Shutdown(context.Background()) })
	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 1 })

	local := agent.NewLocalServer(pool, "agent-1", "admin-tok", slog.Default())
	ts := httptest.NewServer(local.Handler())
	t.Cleanup(ts.Close)

	// Metrics unauthenticated.
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m api.AgentMetrics
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.AgentID != "agent-1" || m.Warm < 1 {
		t.Fatalf("metrics = %+v", m)
	}

	// Drain requires auth.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/pool/drain", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth drain status = %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPost, ts.URL+"/v1/admin/pool/drain", nil)
	req.Header.Set(api.AgentAuthHeader, api.AgentBearerPrefix+"admin-tok")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var dr api.PoolReloadResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		t.Fatal(err)
	}
	if !dr.OK || dr.DrainedWarm < 1 {
		t.Fatalf("drain resp = %+v", dr)
	}
}
