package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// GuestExec injects files and runs commands inside (or as if inside) a guest VM.
//
// Fake/dev path: host filesystem under instances/<id>/guest/.
// Firecracker path: stage on host then deliver via vsock/SSH-like channel
// (exec may be partially stubbed without KVM; inject still writes host stage files).
type GuestExec interface {
	// WriteFile writes content into the guest workspace (or host stage for inject).
	// path is relative to the guest workspace root (e.g. "jitconfig").
	WriteFile(ctx context.Context, id vmm.ID, relPath string, data []byte, mode os.FileMode) error
	// Exec runs a command in the guest. On fake backends this records the command
	// and returns success without a real process.
	Exec(ctx context.Context, id vmm.ID, name string, args ...string) error
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

// FirecrackerGuestExec stages inject files on the host (same layout as FileGuestExec)
// and documents the production guest-exec path. Without a live vsock/SSH channel,
// Exec records intent and returns nil after staging — operators enable real guest
// exec on Ubuntu+KVM (see deploy/ubuntu/guest-image.md).
type FirecrackerGuestExec struct {
	// Inner stages files on the host under instances/<id>/guest/.
	Inner *FileGuestExec
	// EnabledRealExec when true would dial guest vsock; MVP keeps this false on non-KVM.
	EnabledRealExec bool
}

// WriteFile stages content for guest delivery.
func (f *FirecrackerGuestExec) WriteFile(ctx context.Context, id vmm.ID, relPath string, data []byte, mode os.FileMode) error {
	if f.Inner == nil {
		return fmt.Errorf("agent: FirecrackerGuestExec.Inner is nil")
	}
	return f.Inner.WriteFile(ctx, id, relPath, data, mode)
}

// Exec stages a command request. Real vsock/SSH guest exec is operator-enabled on Linux+KVM.
func (f *FirecrackerGuestExec) Exec(ctx context.Context, id vmm.ID, name string, args ...string) error {
	if f.Inner == nil {
		return fmt.Errorf("agent: FirecrackerGuestExec.Inner is nil")
	}
	// Always record on host stage for observability and tests.
	if err := f.Inner.Exec(ctx, id, name, args...); err != nil {
		return err
	}
	if !f.EnabledRealExec {
		// Host-side stage only (no KVM guest channel in this process).
		return nil
	}
	// Placeholder for future vsock/SSH: return clear error if someone enables it early.
	return fmt.Errorf("agent: real firecracker guest exec not wired (use host stage inject + cloud-init/systemd in guest image)")
}
