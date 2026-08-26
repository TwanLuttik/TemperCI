package firecracker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestDestroyReapsTrackedWait(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	mgr := NewForTest(layout)
	id := vmm.ID("fc-reap")
	if err := os.MkdirAll(layout.InstanceDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := vmm.WriteMeta(layout.MetaPath(id), vmm.InstanceMeta{ID: id, State: vmm.StateRunning}); err != nil {
		t.Fatal(err)
	}

	waited := make(chan struct{})
	mgr.storeWait(id, func() error {
		close(waited)
		return nil
	})
	if err := mgr.Destroy(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("Destroy did not Wait the Firecracker child (zombie leak)")
	}
}
