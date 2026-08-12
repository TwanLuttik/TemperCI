package control

import (
	"fmt"
	"sync"
	"time"
)

// AssignmentStatus is the lifecycle of a mint/assignment record.
type AssignmentStatus string

const (
	AssignmentMinted   AssignmentStatus = "minted"
	AssignmentAssigned AssignmentStatus = "assigned"
	AssignmentStarted  AssignmentStatus = "started"
	AssignmentFinished AssignmentStatus = "finished"
	AssignmentFailed   AssignmentStatus = "failed"
)

// Assignment is minimal in-memory state for a queued job that TemperCI accepted.
type Assignment struct {
	JobID          int64
	RunID          int64
	Org            string
	RepoFullName   string
	Labels         []string
	InstallationID int64
	RunnerName     string
	RunnerID       int64
	// EncodedJITConfig is secret material; do not log.
	EncodedJITConfig string
	Status           AssignmentStatus
	CreatedAt        time.Time
	AssignedAt       time.Time
	StartedAt        time.Time
	FinishedAt       time.Time
	AssignedAgentID  string
	VMID             string
	WarmBind         bool
	Outcome          string
	Error            string
}

// AssignmentStore is a concurrency-safe in-memory store (multi-host MVP).
type AssignmentStore struct {
	mu   sync.RWMutex
	byID map[int64]*Assignment
	// pending is FIFO job ids with status minted (claim order).
	pending []int64
}

// StatusCounts is a snapshot of assignment statuses for metrics.
type StatusCounts struct {
	Minted   int
	Assigned int
	Started  int
	Finished int
	Failed   int
	Total    int
}

// NewAssignmentStore creates an empty store.
func NewAssignmentStore() *AssignmentStore {
	return &AssignmentStore{byID: make(map[int64]*Assignment)}
}

// Put inserts or replaces an assignment keyed by GitHub job id.
// When status is minted, the job is enqueued for agent claim (if not already pending).
func (s *AssignmentStore) Put(a *Assignment) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now().UTC()
	}
	prev, existed := s.byID[a.JobID]
	s.byID[a.JobID] = &cp
	if cp.Status == AssignmentMinted {
		if !existed || prev.Status != AssignmentMinted {
			s.enqueuePendingLocked(a.JobID)
		}
	}
}

// Get returns a copy of the assignment for jobID, or nil.
func (s *AssignmentStore) Get(jobID int64) *Assignment {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.byID[jobID]
	if !ok {
		return nil
	}
	cp := *a
	return &cp
}

// Len returns the number of stored assignments.
func (s *AssignmentStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// PendingLen returns how many minted jobs await claim.
func (s *AssignmentStore) PendingLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.pending)
}

// ClaimNext assigns the oldest minted job to agentID.
// Returns a copy of the assignment, or nil when the queue is empty.
func (s *AssignmentStore) ClaimNext(agentID string) *Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agentID == "" {
		return nil
	}
	for len(s.pending) > 0 {
		jobID := s.pending[0]
		s.pending = s.pending[1:]
		a, ok := s.byID[jobID]
		if !ok || a.Status != AssignmentMinted {
			continue
		}
		now := time.Now().UTC()
		a.Status = AssignmentAssigned
		a.AssignedAgentID = agentID
		a.AssignedAt = now
		cp := *a
		return &cp
	}
	return nil
}

// MarkStarted transitions assigned → started for the agent that owns the job.
func (s *AssignmentStore) MarkStarted(jobID int64, agentID, vmID string, warmBind bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return fmt.Errorf("control: unknown job %d", jobID)
	}
	if a.AssignedAgentID != "" && a.AssignedAgentID != agentID {
		return fmt.Errorf("control: job %d assigned to other agent", jobID)
	}
	switch a.Status {
	case AssignmentAssigned, AssignmentStarted:
		// Idempotent re-start allowed while assigned/started.
	default:
		return fmt.Errorf("control: job %d not in assigned state (%s)", jobID, a.Status)
	}
	a.Status = AssignmentStarted
	a.AssignedAgentID = agentID
	a.VMID = vmID
	a.WarmBind = warmBind
	if a.StartedAt.IsZero() {
		a.StartedAt = time.Now().UTC()
	}
	return nil
}

// MarkFinished transitions started/assigned → finished.
func (s *AssignmentStore) MarkFinished(jobID int64, agentID, outcome, vmID string, warmBind bool, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return fmt.Errorf("control: unknown job %d", jobID)
	}
	if a.AssignedAgentID != "" && a.AssignedAgentID != agentID {
		return fmt.Errorf("control: job %d assigned to other agent", jobID)
	}
	switch a.Status {
	case AssignmentAssigned, AssignmentStarted, AssignmentFinished:
		// Allow finish from assigned (failed before start) or idempotent finish.
	default:
		return fmt.Errorf("control: job %d cannot finish from status %s", jobID, a.Status)
	}
	a.Status = AssignmentFinished
	a.AssignedAgentID = agentID
	a.Outcome = outcome
	if errMsg != "" {
		a.Error = errMsg
	}
	if vmID != "" {
		a.VMID = vmID
	}
	a.WarmBind = warmBind || a.WarmBind
	if a.FinishedAt.IsZero() {
		a.FinishedAt = time.Now().UTC()
	}
	// Drop secret after finish so long-lived process memory holds less JIT material.
	a.EncodedJITConfig = ""
	return nil
}

// MarkFailed records a mint/assign failure (webhook path).
func (s *AssignmentStore) MarkFailed(jobID int64, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return
	}
	a.Status = AssignmentFailed
	a.Error = errMsg
	a.EncodedJITConfig = ""
	s.removePendingLocked(jobID)
}

// CountByStatus returns assignment counts by status.
func (s *AssignmentStore) CountByStatus() StatusCounts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var c StatusCounts
	for _, a := range s.byID {
		c.Total++
		switch a.Status {
		case AssignmentMinted:
			c.Minted++
		case AssignmentAssigned:
			c.Assigned++
		case AssignmentStarted:
			c.Started++
		case AssignmentFinished:
			c.Finished++
		case AssignmentFailed:
			c.Failed++
		}
	}
	return c
}

// ListStuck returns copies of assignments in assigned/started older than age
// (relative to AssignedAt/StartedAt, falling back to CreatedAt).
func (s *AssignmentStore) ListStuck(now time.Time, age time.Duration) []*Assignment {
	if age <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Assignment
	for _, a := range s.byID {
		switch a.Status {
		case AssignmentAssigned, AssignmentStarted:
		default:
			continue
		}
		ref := a.AssignedAt
		if a.Status == AssignmentStarted && !a.StartedAt.IsZero() {
			ref = a.StartedAt
		}
		if ref.IsZero() {
			ref = a.CreatedAt
		}
		if ref.IsZero() || now.Sub(ref) < age {
			continue
		}
		cp := *a
		out = append(out, &cp)
	}
	return out
}

// ListStaleMinted returns minted jobs older than age (never claimed).
func (s *AssignmentStore) ListStaleMinted(now time.Time, age time.Duration) []*Assignment {
	if age <= 0 {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Assignment
	for _, a := range s.byID {
		if a.Status != AssignmentMinted {
			continue
		}
		ref := a.CreatedAt
		if ref.IsZero() || now.Sub(ref) < age {
			continue
		}
		cp := *a
		out = append(out, &cp)
	}
	return out
}

// RequeueAssigned moves an assigned (not started) job back to minted for another agent.
// Used when an agent dies before start. Clears AssignedAgentID and re-enqueues FIFO.
func (s *AssignmentStore) RequeueAssigned(jobID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return fmt.Errorf("control: unknown job %d", jobID)
	}
	if a.Status != AssignmentAssigned {
		return fmt.Errorf("control: job %d not assigned (%s)", jobID, a.Status)
	}
	a.Status = AssignmentMinted
	a.AssignedAgentID = ""
	a.AssignedAt = time.Time{}
	a.VMID = ""
	s.enqueuePendingLocked(jobID)
	return nil
}

func (s *AssignmentStore) enqueuePendingLocked(jobID int64) {
	for _, id := range s.pending {
		if id == jobID {
			return
		}
	}
	s.pending = append(s.pending, jobID)
}

func (s *AssignmentStore) removePendingLocked(jobID int64) {
	out := s.pending[:0]
	for _, id := range s.pending {
		if id != jobID {
			out = append(out, id)
		}
	}
	s.pending = out
}
