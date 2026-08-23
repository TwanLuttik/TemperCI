package agent

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// ListUsage returns per-VM usage samples for the dashboard (host-side process stats).
func (p *Pool) ListUsage() []api.VMUsage {
	p.mu.Lock()
	if p.sampler == nil {
		p.sampler = newProcSampler()
	}
	now := p.now()
	type snap struct {
		id        vmm.ID
		state     VMState
		jobID     string
		vcpus     int
		memoryMiB int
		createdAt time.Time
	}
	snaps := make([]snap, 0, len(p.vms))
	defCPU, defMem := p.cfg.VCPUs, p.cfg.MemoryMiB
	var layout vmm.Layout
	if p.cleaner != nil {
		layout = p.cleaner.Layout
	}
	for id, pv := range p.vms {
		snaps = append(snaps, snap{
			id: id, state: pv.state, jobID: pv.jobID,
			vcpus: pv.vcpus, memoryMiB: pv.memoryMiB, createdAt: pv.createdAt,
		})
	}
	sampler := p.sampler
	p.mu.Unlock()

	out := make([]api.VMUsage, 0, len(snaps))
	for _, pv := range snaps {
		u := api.VMUsage{
			ID:        string(pv.id),
			State:     string(pv.state),
			JobID:     pv.jobID,
			VCPUs:     pv.vcpus,
			MemoryMiB: pv.memoryMiB,
			CreatedAt: pv.createdAt,
			SampledAt: now.UTC(),
		}
		if u.VCPUs <= 0 {
			u.VCPUs = defCPU
		}
		if u.MemoryMiB <= 0 {
			u.MemoryMiB = defMem
		}
		if layout.Root != "" {
			if m, err := vmm.ReadMeta(layout.MetaPath(pv.id)); err == nil {
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
				if m.PID > 0 && sampler != nil {
					cpu, rss := sampler.sample(m.PID)
					u.CPUPercent = cpu
					u.RSSMiB = rss
				}
				if m.Metadata != nil {
					u.Shape = m.Metadata["shape"]
				}
			}
			u.DiskMiB = dirSizeMiB(layout.InstanceDir(pv.id))
			u.GuestIP = readTrimFile(filepath.Join(layout.NetDir(pv.id), "guest_ip"))
			u.HostIP = readTrimFile(filepath.Join(layout.NetDir(pv.id), "host_ip"))
			u.TapDevice = readTrimFile(filepath.Join(layout.NetDir(pv.id), "tap"))
			u.ConsoleTail = TailFile(filepath.Join(layout.LogDir(pv.id), "console.log"), maxVMConsoleTail)
			u.AgentTail = TailFile(filepath.Join(layout.GuestDir(pv.id), "agent.log"), maxVMConsoleTail)
		}
		out = append(out, u)
	}
	api.SortVMUsage(out)
	return out
}

func readTrimFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
