package firecracker

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func allocatedBytes(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat_t unavailable for %s", path)
	}
	return st.Blocks * 512
}

func TestWalkDataExtents(t *testing.T) {
	// Simulated 8MiB file: 4KiB data at 0, 4KiB data at 1MiB, rest holes.
	const size = int64(8 << 20)
	seek := func(off int64, whence int) (int64, error) {
		switch whence {
		case unix.SEEK_DATA:
			if off < 4096 {
				return 0, nil
			}
			if off < 1<<20+4096 {
				if off < 1<<20 {
					return 1 << 20, nil
				}
				return off, nil
			}
			return 0, unix.ENXIO
		case unix.SEEK_HOLE:
			if off < 4096 {
				return 4096, nil
			}
			if off < 1<<20+4096 {
				return 1<<20 + 4096, nil
			}
			return size, nil
		default:
			return 0, errors.New("bad whence")
		}
	}
	got, err := walkDataExtents(size, seek)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int64{{0, 4096}, {1 << 20, 1<<20 + 4096}}
	if len(got) != len(want) {
		t.Fatalf("extents=%v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extent[%d]=%v want %v", i, got[i], want[i])
		}
	}
}

func TestCopyFilePreservesHoles(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "base.ext4")
	dst := filepath.Join(dir, "overlay")

	const size = int64(8 << 20) // 8 MiB logical, almost all holes
	f, err := os.OpenFile(src, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("HEAD"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("TAIL"), size-4); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	srcAlloc := allocatedBytes(t, src)
	if srcAlloc > size/2 {
		t.Skipf("filesystem does not keep holes (allocated %d of %d); extent walker is covered by TestWalkDataExtents", srcAlloc, size)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(got)) != size {
		t.Fatalf("logical size=%d want %d", len(got), size)
	}
	if string(got[:4]) != "HEAD" || string(got[size-4:]) != "TAIL" {
		t.Fatal("data extents not copied")
	}

	dstAlloc := allocatedBytes(t, dst)
	if dstAlloc > 1<<20 {
		t.Fatalf("overlay materialized holes: allocated %d (src %d); want sparse copy", dstAlloc, srcAlloc)
	}
}

func TestCopyFileDenseSmallFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	want := []byte("rootfs-bytes")
	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("got %q", got)
	}
}
