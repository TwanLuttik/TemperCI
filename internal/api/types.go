// Package api defines shared wire types for the control plane ↔ host agent
// protocol (job assignment, capacity, lifecycle signals).
package api

import "time"

// Auth scheme for agent→control calls (shared bearer token; optional TLS/mTLS).
const AgentAuthHeader = "Authorization"

// AgentBearerPrefix is the Authorization value prefix, e.g. "Bearer <token>".
const AgentBearerPrefix = "Bearer "

// Assignment statuses mirrored for agent-facing APIs.
const (
	JobStatusMinted   = "minted"
	JobStatusAssigned = "assigned"
	JobStatusStarted  = "started"
	JobStatusFinished = "finished"
	JobStatusFailed   = "failed"
)

// VMUsage is a point-in-time view of one microVM on an agent host.
type VMUsage struct {
	// ID is the TemperCI microVM id.
	ID string `json:"id"`
	// State is agent pool state: warm, busy, pool_boot, destroying, etc.
	State string `json:"state"`
	// JobID when the VM is bound to a job (busy).
	JobID string `json:"job_id,omitempty"`
	// VCPUs configured for the guest.
	VCPUs int `json:"vcpus"`
	// MemoryMiB configured guest RAM.
	MemoryMiB int `json:"memory_mib"`
	// PID of the Firecracker (or VMM) process when known.
	PID int `json:"pid,omitempty"`
	// CPUPercent is host-side process CPU usage estimate (0–100 * nCPU scale optional).
	CPUPercent float64 `json:"cpu_percent"`
	// RSSMiB is resident set size of the VMM process in mebibytes.
	RSSMiB float64 `json:"rss_mib"`
	// DiskMiB is host instance directory size when available.
	DiskMiB float64 `json:"disk_mib,omitempty"`
	// CreatedAt is when the microVM instance was created (boot/create time).
	CreatedAt time.Time `json:"created_at,omitempty"`
	// SampledAt is when this sample was taken (UTC).
	SampledAt time.Time `json:"sampled_at,omitempty"`
	// GuestIP is the TAP address inside the microVM.
	GuestIP string `json:"guest_ip,omitempty"`
	// HostIP is the TAP address on the agent host.
	HostIP string `json:"host_ip,omitempty"`
	// TapDevice is the host tap name.
	TapDevice string `json:"tap,omitempty"`
	// Shape is the pool shape label when known.
	Shape string `json:"shape,omitempty"`
	// ConsoleTail is the last bytes of the Firecracker serial console.
	ConsoleTail string `json:"console_tail,omitempty"`
	// AgentTail is the last bytes of the guest-agent log.
	AgentTail string `json:"agent_tail,omitempty"`
}

// HostResources is the agent's view of leftover host compute and the clamped slot cap.
type HostResources struct {
	RAMTotalMiB        int    `json:"ram_total_mib"`
	RAMAvailMiB        int    `json:"ram_avail_mib"`
	AllocatedRAMMiB    int    `json:"allocated_ram_mib"`
	ReserveRAMMiB      int    `json:"reserve_ram_mib"`
	DiskTotalMiB       int    `json:"disk_total_mib"`
	DiskFreeMiB        int    `json:"disk_free_mib"`
	NumCPU             int    `json:"num_cpu"`
	ConfiguredMaxReady int    `json:"configured_max_ready"`
	EffectiveMaxReady  int    `json:"effective_max_ready"`
	ClampReason        string `json:"clamp_reason,omitempty"`
	LastAdmitReason    string `json:"last_admit_reason,omitempty"`
	// ExclusiveBusy is unused (packing is RAM-based). Kept so old UIs still parse.
	ExclusiveBusy bool `json:"exclusive_busy,omitempty"`
}

// RegisterRequest is sent by an agent on startup (or heartbeat re-register).
type RegisterRequest struct {
	// AgentID is a stable host identity (hostname or configured id).
	AgentID string `json:"agent_id"`
	// MaxCapacity is the max concurrent jobs this agent will accept.
	MaxCapacity int `json:"max_capacity,omitempty"`
	// Capacity is free job slots currently available (capacity-aware scheduling).
	// Legacy field; FreeSlots is preferred on claim.
	Capacity int `json:"capacity,omitempty"`
	// Warm is the number of idle warm VMs (observability / scheduling hints).
	Warm int `json:"warm,omitempty"`
	// Busy is the number of VMs currently running jobs.
	Busy int `json:"busy,omitempty"`
	// Labels optional host capability labels (reserved).
	Labels []string `json:"labels,omitempty"`
	// VMs is the live microVM list. Must not be omitempty: an empty
	// slice has to reach control so a finished job does not leave a
	// stale "busy" guest on the dashboard.
	VMs []VMUsage `json:"vms"`
	// CachedRepos is org/repo namespaces present in this agent's local cache.
	CachedRepos []string `json:"cached_repos,omitempty"`
	// Cache is the latest host-local Actions cache inventory.
	Cache     *CacheUsage    `json:"cache,omitempty"`
	Resources *HostResources `json:"resources,omitempty"`
}

// RegisterResponse acknowledges registration.
type RegisterResponse struct {
	OK      bool   `json:"ok"`
	AgentID string `json:"agent_id"`
	// CacheOps are pending operator cache commands for this agent to apply.
	CacheOps []CacheOp `json:"cache_ops,omitempty"`
	// Commands are pending operator actions (kill VM, etc.).
	Commands []AgentCmd `json:"commands,omitempty"`
}

const AgentCmdKillVM = "kill_vm"

// AgentCmd is a pending operator command the agent should apply on heartbeat.
type AgentCmd struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	VMID   string `json:"vm_id,omitempty"`
	JobID  int64  `json:"job_id,omitempty"`
}

// Cache usage / operator command types.

const (
	CacheOpPurgeAll  = "purge_all"
	CacheOpPurgeRepo = "purge_repo"
)

// CacheEntryUsage is one finalized cache key inside a repo namespace.
type CacheEntryUsage struct {
	Key        string    `json:"key"`
	Version    string    `json:"version,omitempty"`
	Bytes      int64     `json:"bytes"`
	Created    time.Time `json:"created,omitempty"`
	LastAccess time.Time `json:"last_access,omitempty"`
}

// CacheRepoUsage is one org/repo on an agent.
type CacheRepoUsage struct {
	Repo       string            `json:"repo"`
	Bytes      int64             `json:"bytes"`
	Entries    int               `json:"entries"`
	LastAccess time.Time         `json:"last_access,omitempty"`
	Keys       []CacheEntryUsage `json:"keys,omitempty"`
}

// CacheUsage is host-local Actions cache inventory.
type CacheUsage struct {
	Bytes    int64            `json:"bytes"`
	MaxBytes int64            `json:"max_bytes"`
	Entries  int              `json:"entries"`
	Repos    []CacheRepoUsage `json:"repos,omitempty"`
}

// CacheOp is a pending purge the agent should apply.
type CacheOp struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Repo   string `json:"repo,omitempty"`
}

// CacheHost is dashboard view of one agent's cache.
type CacheHost struct {
	AgentID    string    `json:"agent_id"`
	LastSeenAt time.Time `json:"last_seen_at,omitempty"`
	CacheUsage
}

// CacheClearRequest is the dashboard purge request.
type CacheClearRequest struct {
	AgentID string `json:"agent_id,omitempty"`
	Repo    string `json:"repo,omitempty"`
}

// CacheClearResponse reports how many agents were queued.
type CacheClearResponse struct {
	OK     bool   `json:"ok"`
	Queued int    `json:"queued"`
	Error  string `json:"error,omitempty"`
}

// ClaimRequest asks the control plane for the next pending job.
// Agents with FreeSlots <= 0 are not assigned work (multi-host capacity gate).
type ClaimRequest struct {
	AgentID string `json:"agent_id"`
	// FreeSlots is how many additional jobs this agent can take right now.
	// Required for capacity-aware assignment; 0 means no claim (agent full).
	FreeSlots int `json:"free_slots"`
	// Warm/Busy optional snapshot updated on claim (heartbeat).
	Warm int `json:"warm,omitempty"`
	Busy int `json:"busy,omitempty"`
	// CachedRepos is org/repo namespaces this agent already has on disk (sticky claim).
	CachedRepos []string `json:"cached_repos,omitempty"`
	// WaitMS, when > 0, long-polls up to that many milliseconds for a minted job.
	WaitMS int `json:"wait_ms,omitempty"`
}

// JobAssignment is a claimed job payload delivered to an agent.
// EncodedJITConfig is secret material — never full-log.
type JobAssignment struct {
	JobID            int64    `json:"job_id"`
	RunID            int64    `json:"run_id"`
	Org              string   `json:"org"`
	RepoFullName     string   `json:"repo_full_name"`
	Name             string   `json:"name,omitempty"`
	Labels           []string `json:"labels"`
	RunnerName       string   `json:"runner_name"`
	RunnerID         int64    `json:"runner_id"`
	EncodedJITConfig string   `json:"encoded_jit_config"`
	Status           string   `json:"status"`
	AssignedAgentID  string   `json:"assigned_agent_id,omitempty"`
}

// ClaimResponse is returned from POST /v1/agent/jobs/claim.
// Job is nil when no work is available (or agent has no free capacity).
type ClaimResponse struct {
	OK  bool           `json:"ok"`
	Job *JobAssignment `json:"job,omitempty"`
}

// JobStartedRequest reports that the agent bound a VM and started the runner.
type JobStartedRequest struct {
	AgentID  string `json:"agent_id"`
	JobID    int64  `json:"job_id"`
	VMID     string `json:"vm_id"`
	WarmBind bool   `json:"warm_bind"`
}

// JobStartedResponse acknowledges job started.
type JobStartedResponse struct {
	OK bool `json:"ok"`
}

// JobFinishedRequest reports terminal job outcome from the agent.
type JobFinishedRequest struct {
	AgentID string `json:"agent_id"`
	JobID   int64  `json:"job_id"`
	// Outcome is a short reason: success, failure, cancelled, timeout, error, stuck.
	Outcome string `json:"outcome"`
	// WarmBind echoes the bind path used (observability).
	WarmBind bool   `json:"warm_bind,omitempty"`
	VMID     string `json:"vm_id,omitempty"`
	// Error is optional non-secret failure detail.
	Error string `json:"error,omitempty"`
	// Guest diagnostic logs (truncated by the agent). Never include JIT material.
	RunnerLog   string `json:"runner_log,omitempty"`
	AgentLog    string `json:"agent_log,omitempty"`
	ConsoleLog  string `json:"console_log,omitempty"`
	WorkflowLog string `json:"workflow_log,omitempty"`
	// Host-local actions/cache counters for this job (optional).
	CacheHits     int   `json:"cache_hits,omitempty"`
	CacheMisses   int   `json:"cache_misses,omitempty"`
	CacheBytesIn  int64 `json:"cache_bytes_in,omitempty"`
	CacheBytesOut int64 `json:"cache_bytes_out,omitempty"`
}

// JobLogsRequest is an incremental log upload while a job is still running.
type JobLogsRequest struct {
	AgentID     string `json:"agent_id"`
	JobID       int64  `json:"job_id"`
	RunnerLog   string `json:"runner_log,omitempty"`
	AgentLog    string `json:"agent_log,omitempty"`
	ConsoleLog  string `json:"console_log,omitempty"`
	WorkflowLog string `json:"workflow_log,omitempty"`
}

// JobLogsResponse acknowledges a log upload.
type JobLogsResponse struct {
	OK bool `json:"ok"`
}

// JobFinishedResponse acknowledges job finished.
type JobFinishedResponse struct {
	OK bool `json:"ok"`
}

// ErrorBody is a JSON error payload for agent API failures.
type ErrorBody struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

// AgentInfo is control-plane view of a registered agent (in-memory MVP).
type AgentInfo struct {
	AgentID      string    `json:"agent_id"`
	MaxCapacity  int       `json:"max_capacity"`
	Capacity     int       `json:"capacity"` // last reported free slots
	Warm         int       `json:"warm"`
	Busy         int       `json:"busy"`
	Labels       []string  `json:"labels,omitempty"`
	RegisteredAt time.Time `json:"registered_at"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	// VMs is the latest microVM usage sample from the agent (may be empty).
	VMs []VMUsage `json:"vms,omitempty"`
	// CachedRepos last reported by the agent.
	CachedRepos []string `json:"cached_repos,omitempty"`
	// Cache last reported inventory (may be nil if the agent has no gateway).
	Cache     *CacheUsage    `json:"cache,omitempty"`
	Resources *HostResources `json:"resources,omitempty"`
}

// ControlMetrics is a scrapeable JSON metrics payload from temperci-control.
type ControlMetrics struct {
	AgentsRegistered int         `json:"agents_registered"`
	JobsPending      int         `json:"jobs_pending"`
	JobsMinted       int         `json:"jobs_minted"`
	JobsAssigned     int         `json:"jobs_assigned"`
	JobsStarted      int         `json:"jobs_started"`
	JobsFinished     int         `json:"jobs_finished"`
	JobsFailed       int         `json:"jobs_failed"`
	Agents           []AgentInfo `json:"agents,omitempty"`
}

// AgentMetrics is a scrapeable JSON metrics payload from temperci-agent.
type AgentMetrics struct {
	AgentID     string `json:"agent_id"`
	Warm        int    `json:"warm"`
	Busy        int    `json:"busy"`
	PoolBoot    int    `json:"pool_boot"`
	Destroying  int    `json:"destroying"`
	WarmBinds   uint64 `json:"warm_binds"`
	ColdStarts  uint64 `json:"cold_starts"`
	DestroysOK  uint64 `json:"destroys_ok"`
	DestroyFail uint64 `json:"destroy_fail"`
	Recycles    uint64 `json:"recycles"`
	Orphans     uint64 `json:"orphans"`
	ImagePath   string `json:"image_path,omitempty"`
}

// PoolReloadRequest asks the agent to update the guest image and optionally drain warm VMs.
type PoolReloadRequest struct {
	// ImagePath is the new base rootfs path (required unless drain-only).
	ImagePath string `json:"image_path,omitempty"`
	// DrainWarm destroys idle warm VMs so they refill with the new image.
	DrainWarm bool `json:"drain_warm"`
}

// PoolReloadResponse acknowledges pool reload/drain.
type PoolReloadResponse struct {
	OK          bool   `json:"ok"`
	DrainedWarm int    `json:"drained_warm"`
	ImagePath   string `json:"image_path,omitempty"`
	Error       string `json:"error,omitempty"`
}
