// Package cleanup deletes host scratch for a VM id and reconciles orphaned
// instances left after crashes or agent restarts.
package cleanup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// DesiredFunc returns the set of VM ids the agent still wants to keep
// (typically warm + busy). Anything else on disk is an orphan.
type DesiredFunc func(ctx context.Context) (map[vmm.ID]struct{}, error)

// Cleaner destroys VM instances and sweeps host leftovers.
type Cleaner struct {
	// VMM performs hypervisor destroy (process + backend-specific resources).
	VMM vmm.Manager
	// Layout is the host data root (must match the VMM backend).
	Layout vmm.Layout
	// Log is optional; defaults to discarding if nil.
	Log *slog.Logger
}

// Destroy runs the full teardown checklist for id via the VMM backend.
// It is idempotent: a second call is safe and returns nil when nothing remains.
func (c *Cleaner) Destroy(ctx context.Context, id vmm.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if c.VMM == nil {
		return fmt.Errorf("cleanup: VMM is nil")
	}
	if err := c.VMM.Destroy(ctx, id); err != nil {
		return fmt.Errorf("cleanup: destroy %s: %w", id, err)
	}
	// Belt-and-suspenders: ensure instance dir is gone even if a backend left it.
	dir := c.instanceDir(id)
	if dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("cleanup: remove instance dir %s: %w", id, err)
		}
	}
	return nil
}

// SweepOrphans compares desired VM ids to host instance directories and
// destroys anything unknown. desired may be nil (treat as empty).
//
// Also removes unreadable/stray directories under instances/ that are not in
// the desired set.
func (c *Cleaner) SweepOrphans(ctx context.Context, desired map[vmm.ID]struct{}) ([]vmm.ID, error) {
	if c.VMM == nil {
		return nil, fmt.Errorf("cleanup: VMM is nil")
	}
	if desired == nil {
		desired = map[vmm.ID]struct{}{}
	}

	infos, err := c.VMM.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("cleanup: list: %w", err)
	}

	// Also scan the filesystem for dirs the backend list might miss.
	onDisk, err := c.listInstanceDirs()
	if err != nil {
		return nil, err
	}

	seen := map[vmm.ID]struct{}{}
	var orphans []vmm.ID
	for _, info := range infos {
		seen[info.ID] = struct{}{}
		if _, keep := desired[info.ID]; keep {
			continue
		}
		orphans = append(orphans, info.ID)
	}
	for _, id := range onDisk {
		if _, ok := seen[id]; ok {
			continue
		}
		if _, keep := desired[id]; keep {
			continue
		}
		orphans = append(orphans, id)
	}

	var destroyed []vmm.ID
	for _, id := range orphans {
		if err := ctx.Err(); err != nil {
			return destroyed, err
		}
		c.log().Info("orphan sweep destroying", "vm_id", string(id))
		if err := c.Destroy(ctx, id); err != nil {
			return destroyed, fmt.Errorf("cleanup: sweep %s: %w", id, err)
		}
		destroyed = append(destroyed, id)
	}
	return destroyed, nil
}

// SweepOrphansFunc is SweepOrphans with a dynamic desired-state callback.
func (c *Cleaner) SweepOrphansFunc(ctx context.Context, desired DesiredFunc) ([]vmm.ID, error) {
	var set map[vmm.ID]struct{}
	if desired != nil {
		var err error
		set, err = desired(ctx)
		if err != nil {
			return nil, fmt.Errorf("cleanup: desired: %w", err)
		}
	}
	return c.SweepOrphans(ctx, set)
}

func (c *Cleaner) instanceDir(id vmm.ID) string {
	if c.Layout.Root != "" {
		return c.Layout.InstanceDir(id)
	}
	return ""
}

func (c *Cleaner) listInstanceDirs() ([]vmm.ID, error) {
	root := c.Layout.InstancesDir()
	if c.Layout.Root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cleanup: readdir %s: %w", root, err)
	}
	var ids []vmm.ID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip hidden/temp
		if e.Name() == "" || e.Name()[0] == '.' {
			continue
		}
		id := vmm.ID(e.Name())
		if err := id.Validate(); err != nil {
			// Non-conforming dir names are still removed as orphans if not desired;
			// callers only pass validated desired ids, so treat invalid names as orphans
			// by attempting destroy only when they look like path segments.
			// We skip invalid ids to avoid path traversal; operators can rm manually.
			c.log().Warn("skipping invalid instance dir name", "name", e.Name())
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (c *Cleaner) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// InstanceRoot returns the instances parent path for diagnostics.
func (c *Cleaner) InstanceRoot() string {
	if c.Layout.Root == "" {
		return ""
	}
	return c.Layout.InstancesDir()
}

// EnsureLayout creates images/ and instances/ under the data root.
func EnsureLayout(layout vmm.Layout) error {
	if err := layout.Validate(); err != nil {
		return err
	}
	for _, d := range []string{layout.ImagesDir(), layout.InstancesDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("cleanup: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// Path helpers re-exported for tests and agent wiring.
func InstanceDir(layout vmm.Layout, id vmm.ID) string {
	return layout.InstanceDir(id)
}

// RemoveEmptyParents is a no-op helper reserved for future nested layouts.
func RemoveEmptyParents(path string) error {
	_ = filepath.Clean(path)
	return nil
}
