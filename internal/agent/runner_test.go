package agent_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestInjectRunner_WritesJITAndStarts(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	id := vmm.ID("vm-test1")
	if err := os.MkdirAll(layout.InstanceDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	guest := &agent.FileGuestExec{Layout: layout}
	r := &agent.InjectRunner{Guest: guest}

	secret := "ENCODED_JIT_SUPER_SECRET"
	err := r.StartRunner(context.Background(), id, agent.JobPayload{
		JobID:      "99",
		RunnerName: "temperci-job-99",
		JITConfig:  secret,
	})
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(layout.JITConfigPath(id))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != secret {
		t.Fatalf("jit file = %q", raw)
	}
	// Marker exists
	if _, err := os.Stat(layout.RunnerStartMarkerPath(id)); err != nil {
		t.Fatalf("runner marker: %v", err)
	}
	execLog, err := os.ReadFile(filepath.Join(layout.GuestDir(id), "exec.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(execLog), "run.sh") || !strings.Contains(string(execLog), "--jitconfig") {
		t.Fatalf("exec.log = %s", execLog)
	}
	if strings.Contains(string(execLog), "/mnt/temperci/") {
		t.Fatal("exec must pass --jitconfig as the encoded string, not a file path")
	}
	// Ensure we didn't write secret into exec.log
	if strings.Contains(string(execLog), secret) {
		t.Fatal("JIT secret leaked into exec.log")
	}
	cmdb, err := os.ReadFile(filepath.Join(layout.GuestDir(id), "runner.cmd"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cmdb), "/mnt/temperci/") {
		t.Fatal("runner.cmd must not imply --jitconfig is a file path")
	}
	if !strings.Contains(string(cmdb), "--jitconfig") {
		t.Fatalf("runner.cmd = %s", cmdb)
	}
}

func TestInjectRunner_RequiresJIT(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	r := &agent.InjectRunner{Guest: &agent.FileGuestExec{Layout: layout}}
	err := r.StartRunner(context.Background(), "vm-x", agent.JobPayload{JobID: "1"})
	if err == nil {
		t.Fatal("expected error")
	}
}
