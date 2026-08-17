package agent

import (
	"os"
	"path/filepath"
	"unicode/utf8"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

const maxUploadedLogBytes = 128 * 1024

// JobLogs is guest diagnostic text uploaded to the control plane (no secrets).
type JobLogs struct {
	RunnerLog     string
	AgentLog      string
	ConsoleLog    string
	CacheHits     int
	CacheMisses   int
	CacheBytesIn  int64
	CacheBytesOut int64
}

// CollectJobLogs reads archived + still-on-disk guest logs for a VM.
// Safe to call before or after destroy; missing files are empty strings.
func CollectJobLogs(layout vmm.Layout, id vmm.ID) JobLogs {
	var out JobLogs
	if layout.Root == "" || id == "" {
		return out
	}
	arch := filepath.Join(layout.Root, "job-logs", string(id))
	out.RunnerLog = readLogFile(
		filepath.Join(arch, "runner.log"),
		filepath.Join(layout.GuestDir(id), "runner.log"),
	)
	out.AgentLog = readLogFile(
		filepath.Join(arch, "agent.log"),
		filepath.Join(layout.GuestDir(id), "agent.log"),
	)
	out.ConsoleLog = readLogFile(
		filepath.Join(arch, "console.log"),
		filepath.Join(layout.LogDir(id), "console.log"),
	)
	return out
}

// ArchiveConsole copies the Firecracker serial console into job-logs so destroy
// does not erase it.
func ArchiveConsole(layout vmm.Layout, id vmm.ID) {
	if layout.Root == "" || id == "" {
		return
	}
	src := filepath.Join(layout.LogDir(id), "console.log")
	b, err := os.ReadFile(src)
	if err != nil || len(b) == 0 {
		return
	}
	arch := filepath.Join(layout.Root, "job-logs", string(id))
	_ = os.MkdirAll(arch, 0o755)
	_ = os.WriteFile(filepath.Join(arch, "console.log"), b, 0o600)
}

func readLogFile(paths ...string) string {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		return clipLog(string(b), maxUploadedLogBytes)
	}
	return ""
}

func clipLog(s string, max int) string {
	if max <= 0 || len(s) <= max {
		if utf8.ValidString(s) {
			return s
		}
		return string([]rune(s))
	}
	// Keep the tail — runner failures are usually at the end.
	s = s[len(s)-max:]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[1:]
	}
	return s
}
