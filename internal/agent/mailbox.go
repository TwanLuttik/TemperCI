package agent

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// MailboxPort is the UDP port on each TAP host IP that the guest uses to
// signal ready/exit without loop-mounting inject.ext4.
const MailboxPort = 9876

// LogStreamPort is a TCP pipe on the same TAP host IP. The guest writes
// new workflow.log bytes here (no inject remount, no base64).
const LogStreamPort = 9877

const maxLogStreamFrame = 1 << 20

// MailboxHub listens per-VM on the TAP host address and writes host-visible
// guest/ markers (agent.ready, runner.exit) plus a TCP workflow stream.
type MailboxHub struct {
	Layout vmm.Layout

	mu    sync.Mutex
	conns map[vmm.ID]net.PacketConn
	logs  map[vmm.ID]net.Listener
}

// Listen binds UDP ready/exit and TCP workflow stream for one VM. Idempotent.
func (h *MailboxHub) Listen(id vmm.ID, hostIP string) error {
	if h == nil || hostIP == "" || id == "" {
		return nil
	}
	addr := net.JoinHostPort(hostIP, fmt.Sprintf("%d", MailboxPort))
	h.mu.Lock()
	if h.conns == nil {
		h.conns = make(map[vmm.ID]net.PacketConn)
	}
	if h.logs == nil {
		h.logs = make(map[vmm.ID]net.Listener)
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
	logAddr := net.JoinHostPort(hostIP, fmt.Sprintf("%d", LogStreamPort))
	ln, lerr := net.Listen("tcp", logAddr)
	if lerr == nil {
		h.logs[id] = ln
	}
	h.mu.Unlock()
	go h.readLoop(id, c)
	if ln != nil {
		go h.acceptLogs(id, ln)
	}
	return nil
}

// Close stops the listener for id.
func (h *MailboxHub) Close(id vmm.ID) {
	if h == nil {
		return
	}
	h.mu.Lock()
	c := h.conns[id]
	ln := h.logs[id]
	delete(h.conns, id)
	delete(h.logs, id)
	h.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	if ln != nil {
		_ = ln.Close()
	}
}

// CloseAll stops every listener.
func (h *MailboxHub) CloseAll() {
	if h == nil {
		return
	}
	h.mu.Lock()
	conns := h.conns
	logs := h.logs
	h.conns = nil
	h.logs = nil
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
	for _, ln := range logs {
		_ = ln.Close()
	}
}

// mailboxReadSize is one UDP datagram. Workflow chunks are base64 and
// capped at 32KiB raw (~43KiB on the wire).
const mailboxReadSize = 65535

func (h *MailboxHub) readLoop(id vmm.ID, c net.PacketConn) {
	buf := make([]byte, mailboxReadSize)
	for {
		n, _, err := c.ReadFrom(buf)
		if err != nil {
			return
		}
		ApplyMailboxMessage(h.Layout, id, string(buf[:n]))
	}
}

func (h *MailboxHub) acceptLogs(id vmm.ID, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go h.readLogStream(id, c)
	}
}

func (h *MailboxHub) readLogStream(id vmm.ID, c net.Conn) {
	defer c.Close()
	br := bufio.NewReaderSize(c, 64*1024)
	for {
		off, chunk, err := ReadLogStreamFrame(br)
		if err != nil {
			return
		}
		if len(chunk) == 0 {
			continue
		}
		applyWorkflowChunk(h.Layout, id, off, chunk)
	}
}

// ReadLogStreamFrame reads one "wf <offset> <n>\n" + n raw bytes frame.
func ReadLogStreamFrame(r *bufio.Reader) (offset int, chunk []byte, err error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return 0, nil, err
	}
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 || fields[0] != "wf" {
		return 0, nil, fmt.Errorf("agent: bad log stream header %q", strings.TrimSpace(line))
	}
	off, err1 := strconv.Atoi(fields[1])
	n, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || off < 0 || n < 0 || n > maxLogStreamFrame {
		return 0, nil, fmt.Errorf("agent: bad log stream sizes")
	}
	if n == 0 {
		return off, nil, nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	return off, buf, nil
}

// ApplyMailboxMessage writes host-side guest markers from a UDP payload.
// Messages: "ready", "exit <code>", or "wf <offset> <total> <base64>".
func ApplyMailboxMessage(layout vmm.Layout, id vmm.ID, raw string) {
	if layout.Root == "" || id == "" {
		return
	}
	if strings.HasPrefix(raw, "wf ") {
		applyWorkflowMailbox(layout, id, raw)
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

// applyWorkflowMailbox applies one "wf <offset> <total> <base64>" datagram.
// The guest sends new bytes only; offset is the start in workflow.log.
func applyWorkflowMailbox(layout vmm.Layout, id vmm.ID, raw string) {
	line := raw
	if i := strings.IndexByte(raw, '\n'); i >= 0 {
		line = raw[:i]
	}
	fields := strings.SplitN(strings.TrimSpace(line), " ", 4)
	if len(fields) != 4 || fields[0] != "wf" {
		return
	}
	off, err1 := strconv.Atoi(fields[1])
	total, err2 := strconv.Atoi(fields[2])
	if err1 != nil || err2 != nil || off < 0 || total < 0 {
		return
	}
	chunk, err := decodeMailboxB64(fields[3])
	if err != nil || len(chunk) == 0 {
		return
	}
	applyWorkflowChunk(layout, id, off, chunk)
}

func decodeMailboxB64(s string) ([]byte, error) {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func applyWorkflowChunk(layout vmm.Layout, id vmm.ID, offset int, chunk []byte) {
	dir := layout.GuestDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dir, "workflow.log")
	st, err := os.Stat(path)
	size := 0
	if err == nil {
		size = int(st.Size())
	} else if !os.IsNotExist(err) {
		return
	} else if offset != 0 {
		// Gap: do not create a holey file. Inject copy heals this.
		return
	}
	if offset > size {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
		return
	}
	_, _ = f.Write(chunk)
}
