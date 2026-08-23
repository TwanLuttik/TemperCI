package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestCollectJobLogs(t *testing.T) {
	root := t.TempDir()
	layout := vmm.NewLayout(root)
	id := vmm.ID("vm-test1")
	arch := filepath.Join(root, "job-logs", string(id))
	if err := os.MkdirAll(arch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(layout.LogDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "runner.log"), []byte("runner ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "agent.log"), []byte("agent ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(arch, "workflow.log"), []byte("##[group]Run checkout"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout.LogDir(id), "console.log"), []byte("serial boot"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := CollectJobLogs(layout, id)
	if got.RunnerLog != "runner ok" || got.AgentLog != "agent ok" || got.ConsoleLog != "serial boot" || got.WorkflowLog != "##[group]Run checkout" {
		t.Fatalf("got %+v", got)
	}

	ArchiveConsole(layout, id)
	if _, err := os.Stat(filepath.Join(arch, "console.log")); err != nil {
		t.Fatalf("archived console: %v", err)
	}
}

func TestClipLogKeepsTail(t *testing.T) {
	s := strings.Repeat("a", 200) + "END"
	got := clipLog(s, 10)
	if !strings.HasSuffix(got, "END") {
		t.Fatalf("tail = %q", got)
	}
}

func TestTailFileLastBytes(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "console.log")
	body := strings.Repeat("head\n", 200) + "LIVE LINE\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got := TailFile(p, 32)
	if !strings.Contains(got, "LIVE LINE") {
		t.Fatalf("got %q", got)
	}
	if strings.HasPrefix(got, "head") && len(got) < len(body) {
		// truncated from the start — expected
	}
	if TailFile(filepath.Join(dir, "missing"), 32) != "" {
		t.Fatal("missing file should be empty")
	}
}
