package cleanup_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
	"github.com/TwanLuttik/TemperCI/internal/vmm/firecracker"
)

// TestSmokeCreateDestroyLoop is invoked by scripts/vmm-smoke.sh via env vars.
// When TEMPERCI_SMOKE_ROOT is unset, the test runs a short loop under t.TempDir.
func TestSmokeCreateDestroyLoop(t *testing.T) {
	root := os.Getenv("TEMPERCI_SMOKE_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	n := 3
	if s := os.Getenv("TEMPERCI_SMOKE_N"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil || v < 1 {
			t.Fatalf("TEMPERCI_SMOKE_N=%q", s)
		}
		n = v
	}
	backend := os.Getenv("TEMPERCI_SMOKE_BACKEND")
	if backend == "" {
		backend = "fake"
	}

	layout := vmm.NewLayout(root)
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}

	var mgr vmm.Manager
	switch backend {
	case "fake":
		m, err := fake.New(layout)
		if err != nil {
			t.Fatal(err)
		}
		mgr = m
	case "firecracker":
		// Prefer real New; fall back to layout-only manager for destroy-path smoke.
		m, err := firecracker.New(firecracker.Config{Layout: layout})
		if err != nil {
			t.Logf("firecracker.New unavailable (%v); using NewForTest create/destroy only", err)
			mgr = firecracker.NewForTest(layout)
		} else {
			mgr = m
		}
		// Ensure a tiny base image exists for overlay copy.
		base := filepath.Join(layout.ImagesDir(), "smoke-rootfs")
		kernel := filepath.Join(layout.ImagesDir(), "smoke-vmlinux")
		_ = os.WriteFile(base, []byte("smoke-rootfs"), 0o600)
		_ = os.WriteFile(kernel, []byte("smoke-kernel"), 0o600)
	default:
		t.Fatalf("unknown backend %q", backend)
	}

	c := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	ctx := context.Background()

	for i := 0; i < n; i++ {
		id := vmm.ID(fmt.Sprintf("smoke-%d", i))
		cfg := vmm.Config{
			ID:         id,
			VCPUs:      1,
			MemoryMiB:  256,
			RootfsPath: filepath.Join(layout.ImagesDir(), "smoke-rootfs"),
			KernelPath: filepath.Join(layout.ImagesDir(), "smoke-vmlinux"),
		}
		if backend == "fake" {
			cfg.RootfsPath = "base"
			cfg.KernelPath = "vmlinux"
		} else {
			// firecracker create needs real files
			_ = os.WriteFile(cfg.RootfsPath, []byte("r"), 0o600)
			_ = os.WriteFile(cfg.KernelPath, []byte("k"), 0o600)
		}
		if _, err := mgr.Create(ctx, cfg); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		// Boot only on fake (no hypervisor). Real Boot needs firecracker process + assets.
		if backend == "fake" {
			if err := mgr.Boot(ctx, id); err != nil {
				t.Fatalf("boot %s: %v", id, err)
			}
		}
		if err := c.Destroy(ctx, id); err != nil {
			t.Fatalf("destroy %s: %v", id, err)
		}
		if _, err := os.Stat(layout.InstanceDir(id)); !os.IsNotExist(err) {
			t.Fatalf("leftover after destroy %s", id)
		}
	}

	// Orphan plant + sweep
	orphan := vmm.ID("smoke-orphan")
	cfg := vmm.Config{ID: orphan, VCPUs: 1, MemoryMiB: 256, RootfsPath: "b", KernelPath: "k"}
	if backend == "firecracker" {
		cfg.RootfsPath = filepath.Join(layout.ImagesDir(), "smoke-rootfs")
		cfg.KernelPath = filepath.Join(layout.ImagesDir(), "smoke-vmlinux")
	}
	if _, err := mgr.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	destroyed, err := c.SweepOrphans(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(destroyed) != 1 || destroyed[0] != orphan {
		t.Fatalf("sweep destroyed=%v", destroyed)
	}

	entries, err := os.ReadDir(layout.InstancesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("instances not empty: %v", names(entries))
	}
}

func names(entries []os.DirEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
