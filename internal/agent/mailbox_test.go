package agent

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"os/exec"
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

func TestApplyMailboxMessage_WorkflowChunk(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("vm-wf")
	first := []byte("##[group]Run checkout\n")
	ApplyMailboxMessage(layout, id, "wf 0 21 "+b64(first))
	path := filepath.Join(layout.GuestDir(id), "workflow.log")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(first) {
		t.Fatalf("first write = %q", got)
	}
	more := []byte("Synced\n")
	ApplyMailboxMessage(layout, id, fmt.Sprintf("wf %d %d %s", len(first), len(first)+len(more), b64(more)))
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := string(first) + string(more)
	if string(got) != want {
		t.Fatalf("append = %q want %q", got, want)
	}
}

func TestApplyMailboxMessage_WorkflowGapIgnored(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("vm-gap")
	ApplyMailboxMessage(layout, id, "wf 10 14 "+b64([]byte("nope")))
	if _, err := os.Stat(filepath.Join(layout.GuestDir(id), "workflow.log")); err == nil {
		t.Fatal("gap packet must not create a holey file")
	}
}

func TestApplyMailboxMessage_WorkflowFromBash(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "workflow.log")
	body := "##[group]Run x\nhello\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `
set -euo pipefail
src="$1"
off=0
sz=$(wc -c <"$src" | tr -d ' ')
chunk=$(tail -c +1 "$src" | head -c "$sz" | base64 | tr -d '\n')
printf 'wf %d %d %s\n' "$off" "$sz" "$chunk"
`
	out, err := exec.Command("bash", "-c", script, "bash", src).Output()
	if err != nil {
		t.Fatal(err)
	}
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("vm-bash")
	ApplyMailboxMessage(layout, id, string(out))
	got, err := os.ReadFile(filepath.Join(layout.GuestDir(id), "workflow.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("bash packet decoded to %q want %q (pkt=%q)", got, body, out)
	}
}

func TestMailboxHub_TCPLogStream(t *testing.T) {
	h := &MailboxHub{Layout: vmm.NewLayout(t.TempDir())}
	id := vmm.ID("vm-tcp")
	if err := h.Listen(id, "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	defer h.Close(id)
	c, err := net.Dial("tcp", "127.0.0.1:9877")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	first := []byte("##[group]Run checkout\n")
	if _, err := fmt.Fprintf(c, "wf 0 %d\n", len(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(first); err != nil {
		t.Fatal(err)
	}
	more := []byte("Synced\n")
	if _, err := fmt.Fprintf(c, "wf %d %d\n", len(first), len(more)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(more); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.Layout.GuestDir(id), "workflow.log")
	deadline := time.Now().Add(2 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		got, err = os.ReadFile(path)
		if err == nil && string(got) == string(first)+string(more) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("workflow.log = %q want %q", got, string(first)+string(more))
}

func TestReadLogStreamFrame(t *testing.T) {
	body := []byte("hello\nworld")
	var buf []byte
	buf = append(buf, fmt.Sprintf("wf 4 %d\n", len(body))...)
	buf = append(buf, body...)
	off, chunk, err := ReadLogStreamFrame(bufio.NewReader(bytes.NewReader(buf)))
	if err != nil {
		t.Fatal(err)
	}
	if off != 4 || string(chunk) != string(body) {
		t.Fatalf("off=%d chunk=%q", off, chunk)
	}
}

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
