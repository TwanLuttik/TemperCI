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
}

// RegisterResponse acknowledges registration.
type RegisterResponse struct {
	OK      bool   `json:"ok"`
	AgentID string `json:"agent_id"`
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
}

// JobAssignment is a claimed job payload delivered to an agent.
// EncodedJITConfig is secret material — never full-log.
type JobAssignment struct {
	JobID            int64    `json:"job_id"`
	RunID            int64    `json:"run_id"`
	Org              string   `json:"org"`
	RepoFullName     string   `json:"repo_full_name"`
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
}

// ControlMetrics is a scrapeable JSON metrics payload from temperci-control.
type ControlMetrics struct {
	AgentsRegistered int            `json:"agents_registered"`
	JobsPending      int            `json:"jobs_pending"`
	JobsMinted       int            `json:"jobs_minted"`
	JobsAssigned     int            `json:"jobs_assigned"`
	JobsStarted      int            `json:"jobs_started"`
	JobsFinished     int            `json:"jobs_finished"`
	JobsFailed       int            `json:"jobs_failed"`
	Agents           []AgentInfo    `json:"agents,omitempty"`
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
	OK           bool   `json:"ok"`
	DrainedWarm  int    `json:"drained_warm"`
	ImagePath    string `json:"image_path,omitempty"`
	Error        string `json:"error,omitempty"`
}
