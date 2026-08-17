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
	if err := os.WriteFile(filepath.Join(layout.LogDir(id), "console.log"), []byte("serial boot"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := CollectJobLogs(layout, id)
	if got.RunnerLog != "runner ok" || got.AgentLog != "agent ok" || got.ConsoleLog != "serial boot" {
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
