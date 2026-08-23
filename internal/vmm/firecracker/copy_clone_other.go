//go:build !linux && !darwin

package firecracker

func cloneFile(src, dst string) error {
	return errNoClone
}
