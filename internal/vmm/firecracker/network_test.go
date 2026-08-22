package firecracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootArgsIncludesRootAndRW(t *testing.T) {
	dir := t.TempDir()
	args := bootArgs("vm-1", dir)
	want := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw systemd.unified_cgroup_hierarchy=1 selinux=0"
	if args != want {
		t.Fatalf("bootArgs = %q want %q", args, want)
	}
}

func TestBootArgsIncludesIPWhenNetFilesPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "guest_ip"), []byte("10.231.0.6\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gateway"), []byte("10.231.0.5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prefix"), []byte("30\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := bootArgs("vm-2", dir)
	if !strings.HasPrefix(args, "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw systemd.unified_cgroup_hierarchy=1 selinux=0 ") {
		t.Fatalf("missing root cmdline prefix: %q", args)
	}
	if !strings.Contains(args, "ip=10.231.0.6::10.231.0.5:255.255.255.252:temperci:eth0:off") {
		t.Fatalf("missing ip= cmdline: %q", args)
	}
}
