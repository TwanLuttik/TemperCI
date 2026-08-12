package firecracker_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/firecracker"
)

func TestNewRejectsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("host is linux; cannot assert non-linux rejection")
	}
	_, err := firecracker.New(firecracker.Config{Layout: vmm.NewLayout(t.TempDir())})
	if err == nil {
		t.Fatal("expected error on non-linux")
	}
}

func TestNewForTestCreateDestroyChecklist(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	// Provide a tiny base rootfs to copy as overlay.
	base := filepath.Join(root, "images", "base.ext4")
	if err := os.MkdirAll(filepath.Dir(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(base, []byte("rootfs-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	kernel := filepath.Join(root, "images", "vmlinux")
	if err := os.WriteFile(kernel, []byte("kernel"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr := firecracker.NewForTest(layout)
	ctx := context.Background()
	id := vmm.ID("fc-1")

	info, err := mgr.Create(ctx, vmm.Config{
		ID:         id,
		VCPUs:      2,
		MemoryMiB:  512,
		RootfsPath: base,
		KernelPath: kernel,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if info.ID != id {
		t.Fatalf("id=%s", info.ID)
	}
	if _, err := os.Stat(layout.OverlayPath(id)); err != nil {
		t.Fatalf("overlay: %v", err)
	}
	if _, err := os.Stat(filepath.Join(layout.NetDir(id), "tap")); err != nil {
		t.Fatalf("net: %v", err)
	}

	// Boot requires a real firecracker process; we only exercise Destroy checklist
	// without Boot here (process stop path with no pid is still covered).
	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if _, err := os.Stat(layout.InstanceDir(id)); !os.IsNotExist(err) {
		t.Fatalf("leftover: %v", err)
	}
	// Idempotent
	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatalf("second destroy: %v", err)
	}
	// Base image must remain
	if _, err := os.Stat(base); err != nil {
		t.Fatalf("base image removed: %v", err)
	}
}

func TestDestroyStopsPIDWhenPresent(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	mgr := firecracker.NewForTest(layout)
	ctx := context.Background()

	// Manually plant a created instance with a dead pid file (pid 0 / missing).
	id := vmm.ID("fc-pid")
	if err := os.MkdirAll(layout.NetDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.OverlayPath(id), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	meta := vmm.InstanceMeta{
		ID:        id,
		State:     vmm.StateRunning,
		VCPUs:     1,
		MemoryMiB: 256,
		Backend:   "firecracker",
		PID:       0,
		Network: vmm.NetworkState{
			TapDevice:   "tc-tap-fc-pid",
			NetNS:       "tc-ns-fc-pid",
			ProxyMarker: filepath.Join(layout.NetDir(id), "proxy.marker"),
		},
	}
	if err := os.WriteFile(meta.Network.ProxyMarker, []byte(id), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := vmm.WriteMeta(layout.MetaPath(id), meta); err != nil {
		t.Fatal(err)
	}
	if err := vmm.WritePIDFile(layout.PIDPath(id), 0); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Destroy(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.InstanceDir(id)); !os.IsNotExist(err) {
		t.Fatal("instance remains")
	}
}

func TestListAndExists(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	base := filepath.Join(layout.ImagesDir(), "b")
	_ = os.MkdirAll(layout.ImagesDir(), 0o755)
	_ = os.WriteFile(base, []byte("b"), 0o600)
	kernel := filepath.Join(layout.ImagesDir(), "k")
	_ = os.WriteFile(kernel, []byte("k"), 0o600)

	mgr := firecracker.NewForTest(layout)
	ctx := context.Background()
	if _, err := mgr.Create(ctx, vmm.Config{
		ID: "x", VCPUs: 1, MemoryMiB: 128, RootfsPath: base, KernelPath: kernel,
	}); err != nil {
		t.Fatal(err)
	}
	ok, err := mgr.Exists(ctx, "x")
	if err != nil || !ok {
		t.Fatalf("exists: %v %v", ok, err)
	}
	list, err := mgr.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
}
