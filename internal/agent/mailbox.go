package agent

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// MailboxPort is the UDP port on each TAP host IP that the guest uses to
// signal ready/exit without loop-mounting inject.ext4.
const MailboxPort = 9876

// MailboxHub listens per-VM on the TAP host address and writes host-visible
// guest/ markers (agent.ready, runner.exit).
type MailboxHub struct {
	Layout vmm.Layout

	mu    sync.Mutex
	conns map[vmm.ID]net.PacketConn
}

// Listen binds UDP on hostIP:MailboxPort for one VM. Idempotent.
func (h *MailboxHub) Listen(id vmm.ID, hostIP string) error {
	if h == nil || hostIP == "" || id == "" {
		return nil
	}
	addr := net.JoinHostPort(hostIP, fmt.Sprintf("%d", MailboxPort))
	h.mu.Lock()
	if h.conns == nil {
		h.conns = make(map[vmm.ID]net.PacketConn)
	}
	if _, ok := h.conns[id]; ok {
		h.mu.Unlock()
		return nil
	}
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.conns[id] = c
	h.mu.Unlock()
	go h.readLoop(id, c)
	return nil
}

// Close stops the listener for id.
func (h *MailboxHub) Close(id vmm.ID) {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.conns[id]
	delete(h.conns, id)
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// CloseAll stops every listener.
func (h *MailboxHub) CloseAll() {
	if h == nil {
		return
	}
	h.mu.Lock()
	conns := h.conns
	h.conns = nil
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

func (h *MailboxHub) readLoop(id vmm.ID, c net.PacketConn) {
	buf := make([]byte, 256)
	for {
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			return
		}
		ApplyMailboxMessage(h.Layout, id, string(buf[:n]))
	}
}

// ApplyMailboxMessage writes host-side guest markers from a UDP payload.
// Messages: "ready" or "exit <code>".
func ApplyMailboxMessage(layout vmm.Layout, id vmm.ID, raw string) {
	if layout.Root == "" || id == "" {
		return
	}
	msg := strings.TrimSpace(raw)
	if msg == "" {
		return
	}
	dir := layout.GuestDir(id)
	_ = os.MkdirAll(dir, 0o700)
	switch {
	case msg == "ready" || strings.HasPrefix(msg, "ready"):
		_ = os.WriteFile(filepath.Join(dir, "agent.ready"), []byte(msg+"\n"), 0o600)
	case strings.HasPrefix(msg, "exit"):
		code := strings.TrimSpace(strings.TrimPrefix(msg, "exit"))
		if code == "" {
			code = "0"
		}
		_ = os.WriteFile(filepath.Join(dir, "runner.exit"), []byte(code+"\n"), 0o600)
	}
}
