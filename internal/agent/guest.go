package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/firecracker"
)

// GuestExec injects files and runs commands inside (or as if inside) a guest VM.
//
// Fake/dev path: host filesystem under instances/<id>/guest/.
// Firecracker path: stage on host under guest/, sync to inject.ext4 (/dev/vdb);
// the guest boot agent starts run.sh when jitconfig appears.
type GuestExec interface {
	// WriteFile writes content into the guest workspace (or host stage for inject).
	// path is relative to the guest workspace root (e.g. "jitconfig").
	WriteFile(ctx context.Context, id vmm.ID, relPath string, data []byte, mode os.FileMode) error
	// Exec runs a command in the guest. On fake backends this records the command
	// and returns success without a real process.
	// On Firecracker inject path, Exec records intent; the guest agent performs the run.
	Exec(ctx context.Context, id vmm.ID, name string, args ...string) error
	// WaitRunner blocks until the guest reports runner completion (runner.exit),
	// or until ctx is cancelled. Fake backends return success immediately.
	WaitRunner(ctx context.Context, id vmm.ID) (exitCode int, err error)
}

// FileGuestExec implements GuestExec using the host instance guest/ directory.
// Suitable for fake VMM and as the host-side staging layer for Firecracker inject.
type FileGuestExec struct {
	Layout vmm.Layout
	// RunnerBinary is the path of actions/runner (or wrapper) invoked on start.
	// On fake: recorded only. On real guest: absolute path inside the guest image.
	RunnerBinary string
	// AfterExec optional hook (tests).
	AfterExec func(id vmm.ID, name string, args []string) error
}

// WriteFile creates parent dirs under guest/ and writes the file (mode default 0600).
func (g *FileGuestExec) WriteFile(ctx context.Context, id vmm.ID, relPath string, data []byte, mode os.FileMode) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}
	relPath = filepath.Clean(relPath)
	if relPath == "." || relPath == ".." || strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
		return fmt.Errorf("agent: invalid guest path %q", relPath)
	}
	if mode == 0 {
		mode = 0o600
	}
	dir := g.Layout.GuestDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("agent: guest dir: %w", err)
	}
	full := filepath.Join(dir, relPath)
	// Ensure still under guest dir.
	if !strings.HasPrefix(full, dir+string(os.PathSeparator)) && full != dir {
		return fmt.Errorf("agent: path escapes guest dir")
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		return err
	}
	return os.WriteFile(full, data, mode)
}

// Exec records the command under guest/exec.log and creates runner.started for runner launches.
func (g *FileGuestExec) Exec(ctx context.Context, id vmm.ID, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(g.Layout.GuestDir(id), 0o700); err != nil {
		return err
	}
	line := name
	if len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	line += "\n"
	logPath := filepath.Join(g.Layout.GuestDir(id), "exec.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.WriteString(line)
	_ = f.Close()
	if err != nil {
		return err
	}
	// Marker used by operators/tests to observe runner start without reading JIT.
	marker := fmt.Sprintf("name=%s\ntime=%s\n", name, time.Now().UTC().Format(time.RFC3339Nano))
	if err := os.WriteFile(g.Layout.RunnerStartMarkerPath(id), []byte(marker), 0o600); err != nil {
		return err
	}
	if g.AfterExec != nil {
		return g.AfterExec(id, name, args)
	}
	return nil
}

// WaitRunner for FileGuestExec: tests write runner.exit under guest/ for fake completion.
// If no exit file appears and ctx is not cancelled, returns success after a short poll
// when runner.started exists (unit tests / fake VMM).
func (g *FileGuestExec) WaitRunner(ctx context.Context, id vmm.ID) (int, error) {
	exitPath := filepath.Join(g.Layout.GuestDir(id), "runner.exit")
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	// For pure unit tests without exit file: if started marker exists, treat as done quickly.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b, err := os.ReadFile(exitPath); err == nil {
			code := 0
			fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &code)
			return code, nil
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				// Fake/dev: no real guest agent — complete successfully so e2e keeps working.
				if _, err := os.Stat(g.Layout.RunnerStartMarkerPath(id)); err == nil {
					return 0, nil
				}
				deadline = time.Now().Add(30 * time.Second) // keep waiting if production-like
			}
		}
	}
}

// FirecrackerGuestExec stages inject files on the host and syncs them to inject.ext4
// so the guest boot agent can start the official actions/runner.
type FirecrackerGuestExec struct {
	// Inner stages files on the host under instances/<id>/guest/.
	Inner *FileGuestExec
	// Layout for inject drive path (defaults to Inner.Layout).
	Layout vmm.Layout
}

func (f *FirecrackerGuestExec) layout() vmm.Layout {
	if f.Layout.Root != "" {
		return f.Layout
	}
	if f.Inner != nil {
		return f.Inner.Layout
	}
	return vmm.Layout{}
}

// WriteFile stages content for guest delivery.
func (f *FirecrackerGuestExec) WriteFile(ctx context.Context, id vmm.ID, relPath string, data []byte, mode os.FileMode) error {
	if f.Inner == nil {
		return fmt.Errorf("agent: FirecrackerGuestExec.Inner is nil")
	}
	return f.Inner.WriteFile(ctx, id, relPath, data, mode)
}

// Exec records the start request and syncs host guest/ → inject.ext4 for the guest agent.
func (f *FirecrackerGuestExec) Exec(ctx context.Context, id vmm.ID, name string, args ...string) error {
	if f.Inner == nil {
		return fmt.Errorf("agent: FirecrackerGuestExec.Inner is nil")
	}
	if err := f.Inner.Exec(ctx, id, name, args...); err != nil {
		return err
	}
	// Publish staged files into the second virtio disk the guest agent mounts.
	if err := firecracker.SyncGuestDirToInjectDrive(f.layout(), id); err != nil {
		return fmt.Errorf("agent: sync inject drive: %w", err)
	}
	return nil
}

// WaitRunner polls inject.ext4 for runner.exit written by the guest agent.
// Poll slowly: the host must not thrash loop-mounts of inject.ext4 while the
// guest agent is also mounting /dev/vdb (causes missed JIT / stuck jobs).
func (f *FirecrackerGuestExec) WaitRunner(ctx context.Context, id vmm.ID) (int, error) {
	layout := f.layout()
	// Brief quiet window so the guest can mount inject, copy JIT, and unmount.
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(layout.InjectDrivePath(id)); err != nil {
			return 1, nil
		}
		files, _ := firecracker.CopyInjectFiles(layout, id, layout.GuestDir(id),
			[]string{"runner.exit", "runner.log", "agent.log", "workflow.log"})
		if layout.Root != "" && len(files) > 0 {
			arch := filepath.Join(layout.Root, "job-logs", string(id))
			_ = os.MkdirAll(arch, 0o755)
			for name, body := range files {
				_ = os.WriteFile(filepath.Join(arch, name), body, 0o600)
			}
			if _, ok := files["console.log"]; !ok {
				ArchiveConsole(layout, id)
			}
		}
		if b, ok := files["runner.exit"]; ok {
			text := strings.TrimSpace(string(b))
			if text != "" {
				code := 0
				if _, err := fmt.Sscanf(text, "%d", &code); err != nil {
					code = 1
				}
				if code != 0 {
					slog.Default().Warn("guest runner failed",
						"vm_id", string(id),
						"exit_code", code,
						"runner_log", truncateForLog(string(files["runner.log"]), 1500),
						"agent_log", truncateForLog(string(files["agent.log"]), 800),
					)
				}
				return code, nil
			}
		}
		select {
		case <-ctx.Done():
			return -1, ctx.Err()
		case <-ticker.C:
		}
	}
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
