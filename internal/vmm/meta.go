package vmm

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// InstanceMeta is the on-disk metadata for a VM instance.
type InstanceMeta struct {
	ID         ID                `json:"id"`
	State      State             `json:"state"`
	VCPUs      int               `json:"vcpus"`
	MemoryMiB  int               `json:"memory_mib"`
	RootfsPath string            `json:"rootfs_path,omitempty"`
	KernelPath string            `json:"kernel_path,omitempty"`
	CreatedAt  time.Time         `json:"created_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	// Backend is "fake" or "firecracker".
	Backend string `json:"backend"`
	// PID is the VMM process id when running (0 if none / fake).
	PID int `json:"pid,omitempty"`
	// Network records host-side net artifacts for destroy.
	Network NetworkState `json:"network,omitempty"`
}

// NetworkState tracks per-VM host network resources for cleanup.
type NetworkState struct {
	// TapDevice is the host tap name, if any (e.g. tc-tap-<id>).
	TapDevice string `json:"tap_device,omitempty"`
	// NetNS is the network namespace name, if any.
	NetNS string `json:"netns,omitempty"`
	// ProxyMarker is a path or token for host proxy/forward state.
	ProxyMarker string `json:"proxy_marker,omitempty"`
}

// ToInfo converts stored metadata to public Info.
func (m InstanceMeta) ToInfo() Info {
	return Info{
		ID:         m.ID,
		State:      m.State,
		VCPUs:      m.VCPUs,
		MemoryMiB:  m.MemoryMiB,
		RootfsPath: m.RootfsPath,
		KernelPath: m.KernelPath,
		CreatedAt:  m.CreatedAt,
		Metadata:   m.Metadata,
	}
}

// WriteMeta atomically writes instance metadata to path.
func WriteMeta(path string, m InstanceMeta) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("vmm: marshal meta: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("vmm: write meta: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("vmm: rename meta: %w", err)
	}
	return nil
}

// ReadMeta loads instance metadata from path.
func ReadMeta(path string) (InstanceMeta, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return InstanceMeta{}, err
	}
	var m InstanceMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return InstanceMeta{}, fmt.Errorf("vmm: parse meta: %w", err)
	}
	return m, nil
}
