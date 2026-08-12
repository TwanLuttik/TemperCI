// Package vmm defines the microVM lifecycle interface (Create, Boot, Destroy,
// Exists) and shared types. Hypervisor details stay behind backend packages.
package vmm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrNotFound is returned when a VM id is unknown on this host.
var ErrNotFound = errors.New("vmm: not found")

// ErrExists is returned when Create is called for an id that already exists.
var ErrExists = errors.New("vmm: already exists")

// ErrNotCreated is returned when Boot is called before Create.
var ErrNotCreated = errors.New("vmm: not created")

// ErrAlreadyRunning is returned when Boot is called on a running VM.
var ErrAlreadyRunning = errors.New("vmm: already running")

// ID uniquely identifies a microVM instance on a host.
type ID string

// Validate returns an error if id is empty or contains path-unsafe characters.
func (id ID) Validate() error {
	if id == "" {
		return fmt.Errorf("vmm: empty id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("vmm: invalid id %q", id)
	}
	return nil
}

// State is the coarse lifecycle state of a microVM.
type State string

const (
	// StateCreated means instance files exist but the guest is not running.
	StateCreated State = "created"
	// StateRunning means the VMM process is up (or simulated as up).
	StateRunning State = "running"
	// StateStopped means the guest was stopped but scratch may still exist.
	StateStopped State = "stopped"
)

// Config describes how to create a microVM.
type Config struct {
	ID         ID
	VCPUs      int
	MemoryMiB  int
	// KernelPath is the guest kernel (Firecracker). Optional for fake backend.
	KernelPath string
	// RootfsPath is the shared base rootfs image; backends create a per-VM COW/overlay.
	RootfsPath string
	// Metadata is optional operator/agent tags stored with the instance.
	Metadata map[string]string
}

// Validate checks required create fields.
func (c Config) Validate() error {
	if err := c.ID.Validate(); err != nil {
		return err
	}
	if c.VCPUs <= 0 {
		return fmt.Errorf("vmm: vcpus must be > 0")
	}
	if c.MemoryMiB <= 0 {
		return fmt.Errorf("vmm: memory_mib must be > 0")
	}
	return nil
}

// Info is identity and metadata for a microVM instance.
type Info struct {
	ID         ID
	State      State
	VCPUs      int
	MemoryMiB  int
	RootfsPath string
	KernelPath string
	CreatedAt  time.Time
	Metadata   map[string]string
}

// Manager is the microVM lifecycle interface used by the host agent.
//
// Destroy is idempotent: calling Destroy on an unknown or already-removed id
// returns nil after ensuring no host leftovers for that id remain.
type Manager interface {
	Create(ctx context.Context, cfg Config) (*Info, error)
	Boot(ctx context.Context, id ID) error
	Destroy(ctx context.Context, id ID) error
	Exists(ctx context.Context, id ID) (bool, error)
	Info(ctx context.Context, id ID) (*Info, error)
	// List returns all instances known under this manager's host layout.
	List(ctx context.Context) ([]Info, error)
}
