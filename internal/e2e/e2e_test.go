// Package e2e_test exercises the single-node control↔agent job path over httptest.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/control"
	"github.com/TwanLuttik/TemperCI/internal/github"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

type mockMinter struct {
	n int
}

func (m *mockMinter) GenerateJITConfig(_ context.Context, req github.GenerateJITConfigRequest) (*github.GenerateJITConfigResponse, error) {
	m.n++
	return &github.GenerateJITConfigResponse{
		Runner:           github.RunnerInfo{ID: int64(1000 + m.n), Name: req.Name},
		EncodedJITConfig: fmt.Sprintf("jit-secret-for-%s", req.Name),
	}, nil
}

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func queuedJobBody(jobID, runID int64, labels []string) []byte {
	labelsJSON, _ := json.Marshal(labels)
	s := fmt.Sprintf(`{
  "action": "queued",
  "workflow_job": {
    "id": %d,
    "run_id": %d,
    "run_attempt": 1,
    "name": "build",
    "status": "queued",
    "labels": %s
  },
  "repository": {"id": 1, "name": "demo", "full_name": "acme/demo", "private": true},
  "organization": {"login": "acme", "id": 2},
  "installation": {"id": 99},
  "sender": {"login": "octocat", "id": 1}
}`, jobID, runID, string(labelsJSON))
	return []byte(s)
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

// TestE2E_WebhookMintAssignBindFinish verifies:
//
//	webhook → mint → claim → warm bind → job finished → destroy
//
// and a second job hits warm_bind=true with empty instance dir after each destroy.
func TestE2E_WebhookMintAssignBindFinish(t *testing.T) {
	const (
		webhookSecret = "whsec"
		agentToken    = "agent-tok"
		agentID       = "e2e-agent"
	)

	// --- control plane ---
	store := control.NewAssignmentStore()
	minter := &mockMinter{}
	handler := control.NewHandler(minter, store, control.HandlerConfig{
		Org:           "acme",
		RunnerGroupID: 1,
		Logger:        slog.Default(),
	})
	ctrl := control.NewServer(control.ServerConfig{
		Handler:       handler,
		Store:         store,
		WebhookSecret: webhookSecret,
		AgentToken:    agentToken,
		Logger:        slog.Default(),
	})
	ts := httptest.NewServer(ctrl.Handler())
	t.Cleanup(ts.Close)

	// --- agent pool (fake VMM) ---
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	img := filepath.Join(layout.ImagesDir(), "base.ext4")
	if err := os.WriteFile(img, []byte("base"), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout, Log: slog.Default()}
	guest := &agent.FileGuestExec{Layout: layout}
	runner := &agent.InjectRunner{Guest: guest, Log: slog.Default()}

	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady:          1,
		MaxReady:          2,
		VCPUs:             4,
		MemoryMiB:         8192,
		ImagePath:         img,
		ReconcileInterval: 20 * time.Millisecond,
		BindWait:          time.Second,
		DestroyRetryBase:  10 * time.Millisecond,
		DestroyRetryMax:   50 * time.Millisecond,
	}, agent.PoolDeps{
		VMM:     mgr,
		Cleaner: cleaner,
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

	waitFor(t, 3*time.Second, func() bool { return pool.Counts().Warm >= 1 })

	client := agent.NewControlClient(ts.URL, agentID, agentToken, ts.Client())
	worker := &agent.Worker{
		Client:       client,
		Pool:         pool,
		Log:          slog.Default(),
		PollInterval: 20 * time.Millisecond,
		JobSimulate:  0, // finish immediately after bind
		Capacity:     2,
	}
	go func() { _ = worker.Run(ctx) }()

	// Helper: post webhook
	postQueued := func(jobID, runID int64) {
		t.Helper()
		body := queuedJobBody(jobID, runID, []string{"temperci-4vcpu-ubuntu-2404"})
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/webhooks/github", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", sign(webhookSecret, body))
		req.Header.Set("X-GitHub-Event", "workflow_job")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("webhook status %d", resp.StatusCode)
		}
	}

	// --- job 1 ---
	postQueued(1001, 2001)
	waitFor(t, 5*time.Second, func() bool {
		a := store.Get(1001)
		return a != nil && a.Status == control.AssignmentFinished
	})
	a1 := store.Get(1001)
	if a1.AssignedAgentID != agentID {
		t.Fatalf("agent = %q", a1.AssignedAgentID)
	}
	if a1.Outcome != "success" {
		t.Fatalf("outcome = %q", a1.Outcome)
	}
	if a1.VMID == "" {
		t.Fatal("missing vm id")
	}
	// First job is typically warm if pool was ready.
	if !a1.WarmBind {
		t.Logf("job1 warm_bind=%v (cold ok under race; job2 must be warm)", a1.WarmBind)
	}
	// Instance for job1 must be gone.
	if _, err := os.Stat(layout.InstanceDir(vmm.ID(a1.VMID))); !os.IsNotExist(err) {
		t.Fatalf("job1 instance dir still present: %v", err)
	}

	// Wait for pool replenish so job2 can warm-bind.
	waitFor(t, 3*time.Second, func() bool { return pool.Counts().Warm >= 1 })

	// --- job 2 (must warm_bind) ---
	postQueued(1002, 2002)
	waitFor(t, 5*time.Second, func() bool {
		a := store.Get(1002)
		return a != nil && a.Status == control.AssignmentFinished
	})
	a2 := store.Get(1002)
	if !a2.WarmBind {
		t.Fatalf("job2 warm_bind=false; want true (metrics warm_binds=%d cold=%d)",
			pool.Metrics().WarmBinds, pool.Metrics().ColdStarts)
	}
	if a2.VMID == "" || a2.VMID == a1.VMID {
		// New VM for job2 after destroy of job1.
		if a2.VMID == a1.VMID {
			t.Fatalf("job2 reused vm id %s after destroy", a2.VMID)
		}
	}
	if _, err := os.Stat(layout.InstanceDir(vmm.ID(a2.VMID))); !os.IsNotExist(err) {
		t.Fatalf("job2 instance dir still present: %v", err)
	}

	// No leftover instance dirs except possibly warm pool members.
	entries, err := os.ReadDir(layout.InstancesDir())
	if err != nil {
		t.Fatal(err)
	}
	// Busy finished VMs destroyed; only warm/pool_boot may remain.
	desired := pool.DesiredIDs()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := vmm.ID(e.Name())
		if _, ok := desired[id]; !ok {
			t.Fatalf("orphan instance dir %s (not in pool)", e.Name())
		}
		// Warm VMs must not contain JIT leftovers from finished jobs.
		jit := layout.JITConfigPath(id)
		if b, err := os.ReadFile(jit); err == nil && strings.Contains(string(b), "jit-secret") {
			t.Fatalf("warm vm %s still has JIT material", id)
		}
	}

	m := pool.Metrics()
	if m.WarmBinds < 1 {
		t.Fatalf("WarmBinds=%d want >=1", m.WarmBinds)
	}
	if m.DestroysOK < 2 {
		t.Fatalf("DestroysOK=%d want >=2", m.DestroysOK)
	}
}
