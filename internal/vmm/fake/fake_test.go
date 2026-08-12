package fake_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
)

func TestCreateBootDestroyNoLeftovers(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr, err := fake.New(layout)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := vmm.ID("vm-test-1")

	info, err := mgr.Create(ctx, vmm.Config{
		ID:         id,
		VCPUs:      2,
		MemoryMiB:  1024,
		RootfsPath: "/images/base.ext4",
		KernelPath: "/images/vmlinux",
		Metadata:   map[string]string{"pool": "default"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.State != vmm.StateCreated {
		t.Fatalf("state=%s want created", info.State)
	}

	// Scratch must exist under instances/
	inst := layout.InstanceDir(id)
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("instance dir: %v", err)
	}
	if _, err := os.Stat(layout.OverlayPath(id)); err != nil {
		t.Fatalf("overlay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.NetDir(id), "tap")); err != nil {
		t.Fatalf("tap marker: %v", err)
	}

	if err := mgr.Boot(ctx, id); err != nil {
		t.Fatalf("boot: %v", err)
	}
	got, err := mgr.Info(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != vmm.StateRunning {
		t.Fatalf("state=%s want running", got.State)
	}

	ok, err := mgr.Exists(ctx, id)
	if err != nil || !ok {
		t.Fatalf("exists=%v err=%v", ok, err)
	}

	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// No leftover files under the temp root for this instance.
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Fatalf("instance dir still present: %v", err)
	}
	// Shared images dir may exist empty; instances dir should not contain id.
	entries, err := os.ReadDir(layout.InstancesDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == string(id) {
			t.Fatalf("leftover instance entry %s", e.Name())
		}
	}

	ok, err = mgr.Exists(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("exists true after destroy")
	}
}

func TestDestroyIdempotent(t *testing.T) {
	root := t.TempDir()
	mgr, err := fake.New(vmm.NewLayout(root))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := vmm.ID("vm-idem")

	if _, err := mgr.Create(ctx, vmm.Config{ID: id, VCPUs: 1, MemoryMiB: 512}); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatalf("first destroy: %v", err)
	}
	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
	// Destroy never-created id is also safe.
	if err := mgr.Destroy(ctx, vmm.ID("never-existed")); err != nil {
		t.Fatalf("destroy missing: %v", err)
	}
}

func TestCreateDuplicate(t *testing.T) {
	mgr, err := fake.New(vmm.NewLayout(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cfg := vmm.Config{ID: "dup", VCPUs: 1, MemoryMiB: 256}
	if _, err := mgr.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Create(ctx, cfg); err == nil {
		t.Fatal("expected ErrExists")
	}
}

func TestList(t *testing.T) {
	mgr, err := fake.New(vmm.NewLayout(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []vmm.ID{"a", "b"} {
		if _, err := mgr.Create(ctx, vmm.Config{ID: id, VCPUs: 1, MemoryMiB: 256}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := mgr.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("list len=%d want 2", len(list))
	}
}
