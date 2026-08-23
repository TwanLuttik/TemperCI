package firecracker

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

var errNoClone = errors.New("firecracker: clone not supported")

// copyFile copies src to dst, preferring a filesystem clone (reflink/clonefile)
// and falling back to a hole-preserving copy so sparse guest images stay sparse.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	_ = os.Remove(dst)
	if err := cloneFile(src, dst); err == nil {
		return nil
	}
	return copySparse(src, dst)
}

func copySparse(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	size := st.Size()
	if err := out.Truncate(size); err != nil {
		return err
	}

	seek := func(off int64, whence int) (int64, error) {
		return unix.Seek(int(in.Fd()), off, whence)
	}
	extents, err := walkDataExtents(size, seek)
	if err != nil {
		// SEEK_HOLE/SEEK_DATA unsupported — dense copy.
		if _, err := in.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err := out.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			return err
		}
		return out.Close()
	}
	buf := make([]byte, 256<<10)
	for _, e := range extents {
		if _, err := in.Seek(e[0], io.SeekStart); err != nil {
			return err
		}
		if _, err := out.Seek(e[0], io.SeekStart); err != nil {
			return err
		}
		n := e[1] - e[0]
		if _, err := io.CopyBuffer(out, io.LimitReader(in, n), buf); err != nil {
			return err
		}
	}
	return out.Close()
}

// walkDataExtents returns [start,end) data ranges using SEEK_DATA / SEEK_HOLE.
func walkDataExtents(size int64, seek func(off int64, whence int) (int64, error)) ([][2]int64, error) {
	if size <= 0 {
		return nil, nil
	}
	var extents [][2]int64
	off := int64(0)
	for off < size {
		data, err := seek(off, unix.SEEK_DATA)
		if err != nil {
			if errors.Is(err, unix.ENXIO) {
				break
			}
			return nil, err
		}
		if data >= size {
			break
		}
		if data < off {
			data = off
		}
		hole, err := seek(data, unix.SEEK_HOLE)
		if err != nil {
			return nil, err
		}
		if hole <= data {
			hole = size
		}
		if hole > size {
			hole = size
		}
		extents = append(extents, [2]int64{data, hole})
		off = hole
	}
	return extents, nil
}
