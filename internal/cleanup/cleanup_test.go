package cleanup_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

func TestDestroyRemovesScratch(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	c := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	ctx := context.Background()
	id := vmm.ID("job-vm-1")

	if _, err := mgr.Create(ctx, vmm.Config{ID: id, VCPUs: 2, MemoryMiB: 1024, RootfsPath: "base"}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Boot(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := c.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(layout.InstanceDir(id)); !os.IsNotExist(err) {
		t.Fatalf("leftover instance: %v", err)
	}
	// Second destroy is safe.
	if err := c.Destroy(ctx, id); err != nil {
		t.Fatalf("idempotent destroy: %v", err)
	}
}

func TestOrphanSweepRemovesUnknowns(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	c := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	ctx := context.Background()

	// Desired: keep warm-1. Orphans: orphan-a, orphan-b.
	for _, id := range []vmm.ID{"warm-1", "orphan-a", "orphan-b"} {
		if _, err := mgr.Create(ctx, vmm.Config{ID: id, VCPUs: 1, MemoryMiB: 512}); err != nil {
			t.Fatal(err)
		}
	}
	// Stray dir with meta-less content should also be swept if name is valid.
	stray := layout.InstanceDir("orphan-stray")
	if err := os.MkdirAll(filepath.Join(stray, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stray, "rootfs.overlay"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	desired := map[vmm.ID]struct{}{
		"warm-1": {},
	}
	destroyed, err := c.SweepOrphans(ctx, desired)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(destroyed) < 3 {
		t.Fatalf("destroyed=%v want at least 3 orphans", destroyed)
	}

	// warm-1 remains
	ok, err := mgr.Exists(ctx, "warm-1")
	if err != nil || !ok {
		t.Fatalf("warm-1 should remain: ok=%v err=%v", ok, err)
	}
	for _, id := range []vmm.ID{"orphan-a", "orphan-b", "orphan-stray"} {
		if _, err := os.Stat(layout.InstanceDir(id)); !os.IsNotExist(err) {
			t.Fatalf("orphan %s still present", id)
		}
	}

	// Empty desired destroys everything remaining.
	destroyed, err = c.SweepOrphans(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(destroyed) != 1 || destroyed[0] != "warm-1" {
		t.Fatalf("expected warm-1 destroyed, got %v", destroyed)
	}
	entries, err := os.ReadDir(layout.InstancesDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("instances not empty: %v", entries)
	}
}

func TestOrphanSweepIdempotentWhenClean(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	c := &cleanup.Cleaner{VMM: mgr, Layout: layout}
	ctx := context.Background()

	if _, err := mgr.Create(ctx, vmm.Config{ID: "keep", VCPUs: 1, MemoryMiB: 256}); err != nil {
		t.Fatal(err)
	}
	desired := map[vmm.ID]struct{}{"keep": {}}
	destroyed, err := c.SweepOrphans(ctx, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(destroyed) != 0 {
		t.Fatalf("unexpected destroyed: %v", destroyed)
	}
}

func TestEnsureLayout(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(filepath.Join(root, "data"))
	if err := cleanup.EnsureLayout(layout); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{layout.ImagesDir(), layout.InstancesDir()} {
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: %v", d, err)
		}
	}
}
