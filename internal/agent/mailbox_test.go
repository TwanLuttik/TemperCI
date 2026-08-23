package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestApplyMailboxMessage_ReadyAndExit(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("vm-1")
	ApplyMailboxMessage(layout, id, "ready 2026-01-01")
	b, err := os.ReadFile(filepath.Join(layout.GuestDir(id), "agent.ready"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) == "" {
		t.Fatal("ready file empty")
	}
	ApplyMailboxMessage(layout, id, "exit 7")
	b, err = os.ReadFile(filepath.Join(layout.GuestDir(id), "runner.exit"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "7\n" {
		t.Fatalf("exit=%q", b)
	}
}

func TestMailboxHub_ListenAndSignal(t *testing.T) {
	h := &MailboxHub{Layout: vmm.NewLayout(t.TempDir())}
	id := vmm.ID("vm-udp")
	if err := h.Listen(id, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	defer h.Close(id)
	c, err := net.Dial("udp", "127.0.0.1:9876")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ready")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(h.Layout.GuestDir(id), "agent.ready")); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("ready file not written")
}
