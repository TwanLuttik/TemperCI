package firecracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestInjectDriveRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("mkfs.ext4"); err != nil {
		t.Skip("mkfs.ext4 not found (install e2fsprogs); skipping inject round-trip")
	}

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	id := vmm.ID("inject-rt")
	if err := os.MkdirAll(layout.InstanceDir(id), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := createInjectDrive(layout.InjectDrivePath(id)); err != nil {
		t.Fatalf("createInjectDrive: %v", err)
	}

	guestDir := layout.GuestDir(id)
	if err := os.MkdirAll(guestDir, 0o700); err != nil {
		t.Fatal(err)
	}
	want := []byte("hello-from-host\n")
	if err := os.WriteFile(filepath.Join(guestDir, "probe.txt"), want, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SyncGuestDirToInjectDrive(layout, id); err != nil {
		if mountUnavailable(err) {
			t.Skipf("loop mount unavailable: %v", err)
		}
		t.Fatalf("SyncGuestDirToInjectDrive: %v", err)
	}

	got, err := ReadInjectFile(layout, id, "probe.txt")
	if err != nil {
		if mountUnavailable(err) {
			t.Skipf("loop mount unavailable: %v", err)
		}
		t.Fatalf("ReadInjectFile: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("ReadInjectFile = %q, want %q", got, want)
	}
}

func mountUnavailable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, needle := range []string{
		"operation not permitted",
		"permission denied",
		"not permitted",
		"must be superuser",
		"only root",
		"failed to setup loop",
		"no such device",
		"unknown filesystem type",
		"invalid argument",
	} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
