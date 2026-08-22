package vmm

import (
	"fmt"
	"path/filepath"
)

// Layout describes the host directory layout for images and per-VM instances.
//
//	<root>/
//	  images/                 # shared base images (not deleted on destroy)
//	  instances/<id>/         # per-VM scratch (deleted on destroy)
type Layout struct {
	// Root is the temperci data root (e.g. /var/lib/temperci).
	Root string
}

// NewLayout returns a Layout rooted at root.
func NewLayout(root string) Layout {
	return Layout{Root: root}
}

// ImagesDir is the shared base-image directory.
func (l Layout) ImagesDir() string {
	return filepath.Join(l.Root, "images")
}

// CacheDir is host-local Actions cache storage (not deleted per job).
func (l Layout) CacheDir() string {
	return filepath.Join(l.Root, "cache")
}

// OCICacheDir is host-local OCI pull-through / build cache (not deleted per job).
func (l Layout) OCICacheDir() string {
	return filepath.Join(l.Root, "ocicache")
}

// InstancesDir is the parent of all per-VM instance directories.
func (l Layout) InstancesDir() string {
	return filepath.Join(l.Root, "instances")
}

// InstanceDir is the scratch directory for one VM id.
func (l Layout) InstanceDir(id ID) string {
	return filepath.Join(l.InstancesDir(), string(id))
}

// MetaPath is the per-instance metadata file.
func (l Layout) MetaPath(id ID) string {
	return filepath.Join(l.InstanceDir(id), "meta.json")
}

// OverlayPath is the per-instance COW/overlay disk.
func (l Layout) OverlayPath(id ID) string {
	return filepath.Join(l.InstanceDir(id), "rootfs.overlay")
}

// APISockPath is the Firecracker API Unix socket path.
func (l Layout) APISockPath(id ID) string {
	return filepath.Join(l.InstanceDir(id), "api.sock")
}

// PIDPath records the VMM process id for an instance.
func (l Layout) PIDPath(id ID) string {
	return filepath.Join(l.InstanceDir(id), "firecracker.pid")
}

// NetDir holds per-VM network state (taps, netns, proxy markers).
func (l Layout) NetDir(id ID) string {
	return filepath.Join(l.InstanceDir(id), "net")
}

// LogDir holds optional per-instance logs.
func (l Layout) LogDir(id ID) string {
	return filepath.Join(l.InstanceDir(id), "logs")
}

// GuestDir is the host-side inject channel for a VM (JIT config, runner start markers).
// On fake/dev backends this is a real directory under the instance scratch.
// On Firecracker, content is synced into InjectDrivePath (second virtio disk) for the guest agent.
func (l Layout) GuestDir(id ID) string {
	return filepath.Join(l.InstanceDir(id), "guest")
}

// InjectDrivePath is a small ext4 disk attached as /dev/vdb for host↔guest job inject.
func (l Layout) InjectDrivePath(id ID) string {
	return filepath.Join(l.InstanceDir(id), "inject.ext4")
}

// JITConfigPath is where the agent writes the encoded JIT config for the runner.
func (l Layout) JITConfigPath(id ID) string {
	return filepath.Join(l.GuestDir(id), "jitconfig")
}

// RunnerStartMarkerPath records that the guest runner was requested to start.
func (l Layout) RunnerStartMarkerPath(id ID) string {
	return filepath.Join(l.GuestDir(id), "runner.started")
}

// Validate ensures Root is set.
func (l Layout) Validate() error {
	if l.Root == "" {
		return fmt.Errorf("vmm: layout root is empty")
	}
	return nil
}
