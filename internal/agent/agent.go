// Package agent implements host-agent domain logic: warm pool state machine,
// job bind, and post-job teardown coordination.
package agent

import (
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// VMState is the agent-level lifecycle state for a pool member.
// Distinct from vmm.State (hypervisor process state).
type VMState string

const (
	// StatePoolBoot: creating/booting; not yet eligible for jobs.
	StatePoolBoot VMState = "pool_boot"
	// StateWarm: booted, idle, no JIT/secrets, ready to bind.
	StateWarm VMState = "warm"
	// StateBusy: bound to a job; runner running (or stubbed).
	StateBusy VMState = "busy"
	// StateDestroying: teardown in progress; must not accept new work.
	StateDestroying VMState = "destroying"
)

// PoolConfig configures the warm pool.
type PoolConfig struct {
	MinReady    int
	MaxReady    int
	MaxTotalVMs int
	VCPUs       int
	MemoryMiB   int
	// DiskPerVMMiB is the overlay estimate used by admission (0 = derive from image).
	DiskPerVMMiB int
	// ReserveRAMMiB / ReserveDiskMiB are host headroom. 0 is a valid "no extra reserve".
	ReserveRAMMiB     int
	ReserveDiskMiB    int
	ImagePath         string
	KernelPath        string
	IdleRecycle       time.Duration
	ReconcileInterval time.Duration
	DestroyRetryBase  time.Duration
	DestroyRetryMax   time.Duration
	// BindWait is how long Bind waits for a warm VM before cold-booting.
	BindWait time.Duration
}

// PoolConfigFromAgent maps validated agent config into pool settings.
func PoolConfigFromAgent(cfg *config.AgentConfig) PoolConfig {
	pc := PoolConfig{
		MinReady:          cfg.MinReady,
		MaxReady:          cfg.MaxReady,
		MaxTotalVMs:       cfg.MaxTotalVMs,
		VCPUs:             cfg.VCPU,
		MemoryMiB:         cfg.MemoryMiB,
		ReserveRAMMiB:     cfg.HostReserveMemoryMiB,
		ReserveDiskMiB:    cfg.HostReserveDiskMiB,
		DiskPerVMMiB:      OverlayEstimateMiB(cfg.ImagePath),
		ImagePath:         cfg.ImagePath,
		KernelPath:        cfg.KernelPath,
		IdleRecycle:       time.Duration(cfg.IdleRecycleSeconds) * time.Second,
		ReconcileInterval: time.Duration(cfg.ReconcileIntervalSeconds) * time.Second,
		DestroyRetryBase:  100 * time.Millisecond,
		DestroyRetryMax:   5 * time.Second,
		BindWait:          2 * time.Second,
	}
	if pc.ReconcileInterval <= 0 {
		pc.ReconcileInterval = time.Second
	}
	return pc
}

// JobPayload is the assignment handed to Bind (JIT is secret — never full-log).
type JobPayload struct {
	// JobID is an opaque job identifier for logs/metrics (not secret).
	JobID string
	// RunnerName is an optional display name for the runner registration.
	RunnerName string
	// Labels are the runs-on labels for diagnostics (not secret).
	Labels []string
	// JITConfig is the single-use GitHub JIT runner config. Treat as secret.
	JITConfig string
}

// BindResult is returned after a successful bind.
type BindResult struct {
	VMID      vmm.ID
	WarmStart bool // true if bound from warm pool; false if cold-booted
	JobID     string
}

// Counts is a snapshot of pool membership by state.
type Counts struct {
	PoolBoot   int
	Warm       int
	Busy       int
	Destroying int
}

// Total returns the number of tracked VMs.
func (c Counts) Total() int {
	return c.PoolBoot + c.Warm + c.Busy + c.Destroying
}
