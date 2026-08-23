package agent

import (
	"path/filepath"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// ListUsage returns per-VM usage samples for the dashboard (host-side process stats).
func (p *Pool) ListUsage() []api.VMUsage {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sampler == nil {
		p.sampler = newProcSampler()
	}
	now := p.now()
	out := make([]api.VMUsage, 0, len(p.vms))
	var layout vmm.Layout
	if p.cleaner != nil {
		layout = p.cleaner.Layout
	}
	for id, pv := range p.vms {
		u := api.VMUsage{
			ID:        string(id),
			State:     string(pv.state),
			JobID:     pv.jobID,
			VCPUs:     pv.vcpus,
			MemoryMiB: pv.memoryMiB,
			CreatedAt: pv.createdAt,
			SampledAt: now.UTC(),
		}
		if u.VCPUs <= 0 {
			u.VCPUs = p.cfg.VCPUs
		}
		if u.MemoryMiB <= 0 {
			u.MemoryMiB = p.cfg.MemoryMiB
		}
		// Load instance meta for PID + actual resource config when available.
		if layout.Root != "" {
			metaPath := filepath.Join(layout.InstanceDir(id), "meta.json")
			if m, err := vmm.ReadMeta(metaPath); err == nil {
				if m.VCPUs > 0 {
					u.VCPUs = m.VCPUs
				}
				if m.MemoryMiB > 0 {
					u.MemoryMiB = m.MemoryMiB
				}
				u.PID = m.PID
				if !m.CreatedAt.IsZero() {
					u.CreatedAt = m.CreatedAt
				}
				if m.PID > 0 {
					cpu, rss := p.sampler.sample(m.PID)
					u.CPUPercent = cpu
					u.RSSMiB = rss
				}
				u.DiskMiB = dirSizeMiB(layout.InstanceDir(id))
			}
		}
		out = append(out, u)
	}
	api.SortVMUsage(out)
	return out
}
