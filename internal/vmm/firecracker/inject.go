package firecracker

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

const injectDriveSizeBytes = 64 * 1024 * 1024 // 64 MiB

func injectTemplatePath(layout vmm.Layout) string {
	return filepath.Join(layout.ImagesDir(), "inject-template.ext4")
}

func ensureInjectTemplate(path string) error {
	if path == "" {
		return fmt.Errorf("inject template path empty")
	}
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return nil
	}
	return createInjectDrive(path)
}

func cloneInjectDrive(template, dest string) error {
	if err := ensureInjectTemplate(template); err != nil {
		return createInjectDrive(dest)
	}
	if err := cloneFile(template, dest); err == nil {
		return nil
	}
	if _, err := copySparse(template, dest); err != nil {
		return createInjectDrive(dest)
	}
	return nil
}

// createInjectDrive creates an empty ext4 image for host→guest inject.
func createInjectDrive(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Truncate(injectDriveSizeBytes); err != nil {
		_ = f.Close()
		return err
	}
	_ = f.Close()
	// Best-effort format; production hosts have e2fsprogs.
	cmd := exec.Command("mkfs.ext4", "-F", "-q", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("mkfs.ext4 inject drive: %w (%s)", err, string(out))
	}
	return nil
}

// SyncGuestDirToInjectDrive copies files from the host guest/ staging dir into inject.ext4.
// Guest must not hold a write mount on the drive while this runs (agent protocol: guest
// only mounts after jitconfig appears; host writes then unmounts).
func SyncGuestDirToInjectDrive(layout vmm.Layout, id vmm.ID) error {
	drive := layout.InjectDrivePath(id)
	guestDir := layout.GuestDir(id)
	if _, err := os.Stat(drive); err != nil {
		return fmt.Errorf("inject drive: %w", err)
	}
	if err := os.MkdirAll(guestDir, 0o700); err != nil {
		return err
	}
	mnt, err := os.MkdirTemp("", "temperci-inject-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mnt)

	if out, err := exec.Command("mount", "-o", "loop", drive, mnt).CombinedOutput(); err != nil {
		return fmt.Errorf("mount inject: %w (%s)", err, string(out))
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()

	entries, err := os.ReadDir(guestDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(guestDir, e.Name())
		dst := filepath.Join(mnt, e.Name())
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info, err := e.Info(); err == nil {
			mode = info.Mode().Perm()
		}
		if err := os.WriteFile(dst, data, mode); err != nil {
			return err
		}
		if f, err := os.OpenFile(dst, os.O_RDWR, 0); err == nil {
			_ = f.Sync()
			_ = f.Close()
		}
	}
	return nil
}

// ReadInjectFile mounts inject.ext4 read-only and returns a file's contents.
func ReadInjectFile(layout vmm.Layout, id vmm.ID, name string) ([]byte, error) {
	drive := layout.InjectDrivePath(id)
	mnt, err := os.MkdirTemp("", "temperci-inject-ro-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(mnt)
	if out, err := exec.Command("mount", "-o", "loop,ro", drive, mnt).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mount inject ro: %w (%s)", err, string(out))
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()
	return os.ReadFile(filepath.Join(mnt, name))
}

// CopyInjectFiles mounts inject.ext4 once and copies named files into destDir.
// Missing names are skipped. Returns the bytes that were copied.
func CopyInjectFiles(layout vmm.Layout, id vmm.ID, destDir string, names []string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if destDir == "" || len(names) == 0 {
		return out, nil
	}
	drive := layout.InjectDrivePath(id)
	mnt, err := os.MkdirTemp("", "temperci-inject-ro-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(mnt)
	if outp, err := exec.Command("mount", "-o", "loop,ro", drive, mnt).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("mount inject ro: %w (%s)", err, string(outp))
	}
	defer func() { _ = exec.Command("umount", mnt).Run() }()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, err
	}
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.Contains(name, "/") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(mnt, name))
		if err != nil || len(b) == 0 {
			continue
		}
		if err := os.WriteFile(filepath.Join(destDir, name), b, 0o600); err != nil {
			continue
		}
		out[name] = b
	}
	return out, nil
}
