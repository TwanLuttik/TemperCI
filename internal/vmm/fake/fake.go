// Package fake implements a filesystem-backed microVM manager for tests and
// local development without KVM or Firecracker.
package fake

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

const backendName = "fake"

// Manager is a fake VMM that records instances under a host layout root.
type Manager struct {
	layout vmm.Layout
	mu     sync.Mutex
}

// New returns a fake Manager rooted at layout.Root.
func New(layout vmm.Layout) (*Manager, error) {
	if err := layout.Validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(layout.ImagesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("fake vmm: images dir: %w", err)
	}
	if err := os.MkdirAll(layout.InstancesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("fake vmm: instances dir: %w", err)
	}
	return &Manager{layout: layout}, nil
}

// Layout returns the host layout used by this manager.
func (m *Manager) Layout() vmm.Layout {
	return m.layout
}

// Create provisions instance scratch (metadata, fake overlay, net markers).
func (m *Manager) Create(ctx context.Context, cfg vmm.Config) (*vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.layout.InstanceDir(cfg.ID)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%w: %s", vmm.ErrExists, cfg.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("fake vmm: mkdir: %w", err)
	}
	if err := os.MkdirAll(m.layout.NetDir(cfg.ID), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("fake vmm: net dir: %w", err)
	}
	if err := os.MkdirAll(m.layout.LogDir(cfg.ID), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("fake vmm: log dir: %w", err)
	}

	// Simulate COW overlay: write a small marker file referencing the base image.
	overlay := fmt.Sprintf("fake-overlay base=%s\n", cfg.RootfsPath)
	if err := os.WriteFile(m.layout.OverlayPath(cfg.ID), []byte(overlay), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("fake vmm: overlay: %w", err)
	}

	tap := TapName(cfg.ID)
	netns := NetNSName(cfg.ID)
	proxy := filepath.Join(m.layout.NetDir(cfg.ID), "proxy.marker")
	if err := os.WriteFile(proxy, []byte(string(cfg.ID)+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("fake vmm: proxy marker: %w", err)
	}
	// Marker files for tap/netns so destroy can remove them by path.
	if err := os.WriteFile(filepath.Join(m.layout.NetDir(cfg.ID), "tap"), []byte(tap+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(m.layout.NetDir(cfg.ID), "netns"), []byte(netns+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	meta := vmm.InstanceMeta{
		ID:         cfg.ID,
		State:      vmm.StateCreated,
		VCPUs:      cfg.VCPUs,
		MemoryMiB:  cfg.MemoryMiB,
		RootfsPath: cfg.RootfsPath,
		KernelPath: cfg.KernelPath,
		CreatedAt:  time.Now().UTC(),
		Metadata:   cfg.Metadata,
		Backend:    backendName,
		Network: vmm.NetworkState{
			TapDevice:   tap,
			NetNS:       netns,
			ProxyMarker: proxy,
		},
	}
	if err := vmm.WriteMeta(m.layout.MetaPath(cfg.ID), meta); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	info := meta.ToInfo()
	return &info, nil
}

// Boot marks the instance running (no real process).
func (m *Manager) Boot(ctx context.Context, id vmm.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", vmm.ErrNotFound, id)
		}
		return err
	}
	switch meta.State {
	case vmm.StateRunning:
		return fmt.Errorf("%w: %s", vmm.ErrAlreadyRunning, id)
	case vmm.StateCreated, vmm.StateStopped:
		// ok
	default:
		return fmt.Errorf("fake vmm: cannot boot from state %q", meta.State)
	}

	// Fake PID file so cleanup can exercise process teardown paths.
	if err := os.WriteFile(m.layout.PIDPath(id), []byte("0\n"), 0o600); err != nil {
		return err
	}
	meta.State = vmm.StateRunning
	meta.PID = 0
	return vmm.WriteMeta(m.layout.MetaPath(id), meta)
}

// Destroy removes all host leftovers for id. Idempotent.
func (m *Manager) Destroy(ctx context.Context, id vmm.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return destroyInstance(m.layout, id)
}

// Exists reports whether instance scratch for id is present.
func (m *Manager) Exists(ctx context.Context, id vmm.ID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := id.Validate(); err != nil {
		return false, err
	}
	_, err := os.Stat(m.layout.InstanceDir(id))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Info returns metadata for an instance.
func (m *Manager) Info(ctx context.Context, id vmm.ID) (*vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", vmm.ErrNotFound, id)
		}
		return nil, err
	}
	info := meta.ToInfo()
	return &info, nil
}

// List returns all instances under the layout root.
func (m *Manager) List(ctx context.Context) ([]vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.layout.InstancesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []vmm.Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := vmm.ID(e.Name())
		if err := id.Validate(); err != nil {
			continue
		}
		meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
		if err != nil {
			// Directory without readable meta still counts as present for sweep.
			out = append(out, vmm.Info{ID: id, State: vmm.StateCreated})
			continue
		}
		out = append(out, meta.ToInfo())
	}
	return out, nil
}

// TapName returns the synthetic host tap name for id.
func TapName(id vmm.ID) string {
	return "tc-tap-" + string(id)
}

// NetNSName returns the synthetic netns name for id.
func NetNSName(id vmm.ID) string {
	return "tc-ns-" + string(id)
}

// destroyInstance implements the destroy checklist without holding locks
// beyond the caller's mutex. Safe if the instance is already gone.
func destroyInstance(layout vmm.Layout, id vmm.ID) error {
	dir := layout.InstanceDir(id)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	// 1. Stop guest / VMM process (fake: remove pid file; no real process).
	_ = os.Remove(layout.PIDPath(id))

	// 2. Remove VM definition / jailer resources (none beyond instance dir).
	// 3. Delete COW/overlay + metadata — handled by RemoveAll below.
	// 4. Remove taps/netns/proxy state for id.
	meta, err := vmm.ReadMeta(layout.MetaPath(id))
	if err == nil {
		_ = clearNetwork(layout, id, meta.Network)
	} else {
		_ = clearNetwork(layout, id, vmm.NetworkState{
			TapDevice:   TapName(id),
			NetNS:       NetNSName(id),
			ProxyMarker: filepath.Join(layout.NetDir(id), "proxy.marker"),
		})
	}

	// Full instance dir removal (overlay, meta, socks, logs, net/).
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("fake vmm: remove instance %s: %w", id, err)
	}
	return nil
}

func clearNetwork(layout vmm.Layout, id vmm.ID, net vmm.NetworkState) error {
	// Fake backend only removes on-disk markers; real net link/netns cleanup
	// lives in the Firecracker backend / cleanup package host hooks.
	_ = id
	_ = layout
	if net.ProxyMarker != "" {
		_ = os.Remove(net.ProxyMarker)
	}
	return nil
}
