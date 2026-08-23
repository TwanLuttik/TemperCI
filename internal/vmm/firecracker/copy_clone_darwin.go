//go:build darwin

package firecracker

import "golang.org/x/sys/unix"

func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}
