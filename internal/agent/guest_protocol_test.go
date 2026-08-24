package agent_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// TestFileGuestExec_JITInRunnerExitOut covers the host-side inject protocol
// used by fake VMM / FileGuestExec (jitconfig written, runner.exit read).
func TestFileGuestExec_JITInRunnerExitOut(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("proto-host")
	g := &agent.FileGuestExec{Layout: layout}
	ctx := context.Background()

	if err := g.WriteFile(ctx, id, "jitconfig", []byte("encoded-jit-not-secret-enough"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.JITConfigPath(id)); err != nil {
		t.Fatalf("jitconfig: %v", err)
	}
	if err := os.WriteFile(filepath.Join(layout.GuestDir(id), "runner.exit"), []byte("0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, err := g.WaitRunner(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("WaitRunner exit=%d want 0", code)
	}
}

func TestFileGuestExec_WaitRunnerCancelledUnblocks(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("stop-me")
	g := &agent.FileGuestExec{Layout: layout}
	if err := os.MkdirAll(layout.GuestDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.GuestDir(id), "runner.exit"), []byte("cancelled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	code, err := g.WaitRunner(ctx, id)
	if !errors.Is(err, agent.ErrRunnerStopped) {
		t.Fatalf("WaitRunner = %d, %v want ErrRunnerStopped", code, err)
	}
}

func TestFileGuestExec_WaitRunnerInstanceGoneUnblocks(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("gone-vm")
	g := &agent.FileGuestExec{Layout: layout}
	if err := os.MkdirAll(layout.GuestDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := g.WaitRunner(ctx, id)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := os.RemoveAll(layout.InstanceDir(id)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, agent.ErrRunnerStopped) {
			t.Fatalf("got %v want ErrRunnerStopped", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitRunner still blocked after instance dir removed")
	}
}

func TestFirecrackerGuestExec_WaitRunnerCancelledUnblocks(t *testing.T) {
	layout := vmm.NewLayout(t.TempDir())
	id := vmm.ID("fc-stop")
	g := &agent.FirecrackerGuestExec{Inner: &agent.FileGuestExec{Layout: layout}, Layout: layout}
	if err := os.MkdirAll(layout.GuestDir(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.GuestDir(id), "runner.exit"), []byte("cancelled\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	code, err := g.WaitRunner(ctx, id)
	if !errors.Is(err, agent.ErrRunnerStopped) {
		t.Fatalf("WaitRunner = %d, %v want ErrRunnerStopped", code, err)
	}
}

// TestGuestAgentScript_Protocol execs the bash guest agent against a fake
// inject mount (deploy/ubuntu/guest-agent/protocol_test.sh).
func TestGuestAgentScript_Protocol(t *testing.T) {
	script := filepath.Join(repoRoot(t), "deploy", "ubuntu", "guest-agent", "protocol_test.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("protocol_test.sh missing: %v", err)
	}
	cmd := exec.Command("bash", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protocol_test.sh: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}

func TestGuestAgentScript_OOMRemap(t *testing.T) {
	script := filepath.Join(repoRoot(t), "deploy", "ubuntu", "guest-agent", "remap_exit_test.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("remap_exit_test.sh missing: %v", err)
	}
	cmd := exec.Command("bash", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("remap_exit_test.sh: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
