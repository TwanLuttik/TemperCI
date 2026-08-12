package control

import (
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// AgentRegistry tracks registered host agents (in-memory multi-host MVP).
type AgentRegistry struct {
	mu   sync.RWMutex
	byID map[string]*api.AgentInfo
}

// NewAgentRegistry creates an empty registry.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{byID: make(map[string]*api.AgentInfo)}
}

// Register inserts or refreshes an agent record with capacity snapshot.
func (r *AgentRegistry) Register(req api.RegisterRequest) api.AgentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	info, ok := r.byID[req.AgentID]
	if !ok {
		info = &api.AgentInfo{
			AgentID:      req.AgentID,
			RegisteredAt: now,
		}
		r.byID[req.AgentID] = info
	}
	if req.MaxCapacity > 0 {
		info.MaxCapacity = req.MaxCapacity
	}
	// Capacity = free slots last reported by the agent.
	info.Capacity = req.Capacity
	info.Warm = req.Warm
	info.Busy = req.Busy
	if len(req.Labels) > 0 {
		info.Labels = append([]string(nil), req.Labels...)
	}
	info.LastSeenAt = now
	cp := *info
	return cp
}

// UpdateCapacity refreshes free slots and pool snapshot for an agent.
func (r *AgentRegistry) UpdateCapacity(agentID string, freeSlots, warm, busy int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.byID[agentID]
	if !ok {
		return
	}
	info.Capacity = freeSlots
	info.Warm = warm
	info.Busy = busy
	info.LastSeenAt = time.Now().UTC()
}

// HasCapacity reports whether the agent is registered and reports freeSlots > 0.
// Unknown agents are allowed if freeSlots > 0 (claim still works after first register).
func (r *AgentRegistry) HasCapacity(agentID string, freeSlots int) bool {
	if freeSlots <= 0 {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Registered agents: freeSlots from request is authoritative.
	// Unregistered: still allow claim so first-poll paths work after crash;
	// production agents should Register first.
	_ = agentID
	return true
}

// Get returns a copy of the agent record, or nil.
func (r *AgentRegistry) Get(agentID string) *api.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.byID[agentID]
	if !ok {
		return nil
	}
	cp := *info
	return &cp
}

// Touch updates last-seen for an agent if registered.
func (r *AgentRegistry) Touch(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, ok := r.byID[agentID]; ok {
		info.LastSeenAt = time.Now().UTC()
	}
}

// List returns copies of all registered agents.
func (r *AgentRegistry) List() []api.AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]api.AgentInfo, 0, len(r.byID))
	for _, info := range r.byID {
		out = append(out, *info)
	}
	return out
}

// Len returns registered agent count.
func (r *AgentRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
