package control

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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
	Name           string
	WorkflowName   string
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
	CacheHits        int
	CacheMisses      int
	CacheBytesIn     int64
	CacheBytesOut    int64
}

// AssignmentPersister is optional durability for AssignmentStore mutations.
// A nil persister keeps the store memory-only.
type AssignmentPersister interface {
	Persist(a *Assignment) error
	LoadAll() ([]*Assignment, error)
}

// AssignmentStore is a concurrency-safe in-memory store (multi-host MVP).
type AssignmentStore struct {
	mu   sync.RWMutex
	byID map[int64]*Assignment
	// pending is FIFO job ids with status minted (claim order).
	pending   []int64
	persister AssignmentPersister
	// minted is signaled when a job becomes claimable (long-poll wake).
	minted chan struct{}
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

// NewAssignmentStore creates an empty in-memory store (no persistence).
func NewAssignmentStore() *AssignmentStore {
	return &AssignmentStore{byID: make(map[int64]*Assignment), minted: make(chan struct{}, 1)}
}

// NewAssignmentStoreWithPersister creates a store and loads existing rows.
func NewAssignmentStoreWithPersister(p AssignmentPersister) (*AssignmentStore, error) {
	s := NewAssignmentStore()
	if err := s.SetPersister(p); err != nil {
		return nil, err
	}
	return s, nil
}

// SetPersister attaches durability and replaces in-memory state from LoadAll.
// A nil persister leaves current memory contents unchanged.
func (s *AssignmentStore) SetPersister(p AssignmentPersister) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.persister = p
	if p == nil {
		return nil
	}
	return s.loadLocked()
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
		s.signalMinted()
	}
	_ = s.persistLocked(&cp)
}

func (s *AssignmentStore) signalMinted() {
	if s.minted == nil {
		return
	}
	select {
	case s.minted <- struct{}{}:
	default:
	}
}

// WaitMinted blocks until a minted job is available or timeout/ctx fires.
func (s *AssignmentStore) WaitMinted(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	if s.PendingLen() > 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	ch := s.minted
	if ch == nil {
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		return
	}
	select {
	case <-ctx.Done():
	case <-timer.C:
	case <-ch:
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

// ClaimNext assigns a minted job to agentID.
// If cachedRepos is non-empty, the oldest pending job whose RepoFullName is in
// that list is preferred; otherwise FIFO. Returns nil when the queue is empty.
func (s *AssignmentStore) ClaimNext(agentID string, cachedRepos []string) *Assignment {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agentID == "" {
		return nil
	}
	idx := s.pickPendingLocked(cachedRepos)
	if idx < 0 {
		return nil
	}
	jobID := s.pending[idx]
	s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
	a, ok := s.byID[jobID]
	if !ok || a.Status != AssignmentMinted {
		return s.claimNextUnlocked(agentID, cachedRepos)
	}
	now := time.Now().UTC()
	a.Status = AssignmentAssigned
	a.AssignedAgentID = agentID
	a.AssignedAt = now
	cp := *a
	_ = s.persistLocked(a)
	return &cp
}

func (s *AssignmentStore) claimNextUnlocked(agentID string, cachedRepos []string) *Assignment {
	// pending already mutated; recurse without taking the lock again.
	idx := s.pickPendingLocked(cachedRepos)
	if idx < 0 {
		return nil
	}
	jobID := s.pending[idx]
	s.pending = append(s.pending[:idx], s.pending[idx+1:]...)
	a, ok := s.byID[jobID]
	if !ok || a.Status != AssignmentMinted {
		return s.claimNextUnlocked(agentID, cachedRepos)
	}
	now := time.Now().UTC()
	a.Status = AssignmentAssigned
	a.AssignedAgentID = agentID
	a.AssignedAt = now
	cp := *a
	_ = s.persistLocked(a)
	return &cp
}

func (s *AssignmentStore) pickPendingLocked(cachedRepos []string) int {
	want := map[string]struct{}{}
	for _, r := range cachedRepos {
		r = strings.ToLower(strings.TrimSpace(r))
		if r != "" {
			want[r] = struct{}{}
		}
	}
	if len(want) > 0 {
		for i, jobID := range s.pending {
			a, ok := s.byID[jobID]
			if !ok || a.Status != AssignmentMinted {
				continue
			}
			if _, hit := want[strings.ToLower(a.RepoFullName)]; hit {
				return i
			}
		}
	}
	for i, jobID := range s.pending {
		a, ok := s.byID[jobID]
		if ok && a.Status == AssignmentMinted {
			return i
		}
	}
	return -1
}

// SetIdentity records GitHub job/workflow titles when they become known.
func (s *AssignmentStore) SetIdentity(jobID int64, name, workflowName string) {
	if jobID == 0 || (name == "" && workflowName == "") {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return
	}
	changed := false
	if name != "" && a.Name != name {
		a.Name = name
		changed = true
	}
	if workflowName != "" && a.WorkflowName != workflowName {
		a.WorkflowName = workflowName
		changed = true
	}
	if changed {
		_ = s.persistLocked(a)
	}
}

// SetCacheStats records host-local actions/cache counters on a job.
func (s *AssignmentStore) SetCacheStats(jobID int64, hits, misses int, bytesIn, bytesOut int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return fmt.Errorf("control: unknown job %d", jobID)
	}
	a.CacheHits = hits
	a.CacheMisses = misses
	a.CacheBytesIn = bytesIn
	a.CacheBytesOut = bytesOut
	return s.persistLocked(a)
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
	return s.persistLocked(a)
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
	return s.persistLocked(a)
}

// Cancel marks a minted/assigned/started job finished with outcome cancelled.
// Already-cancelled jobs are a no-op. Other terminal outcomes are rejected.
func (s *AssignmentStore) Cancel(jobID int64, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[jobID]
	if !ok {
		return fmt.Errorf("control: unknown job %d", jobID)
	}
	if a.Status == AssignmentFinished && a.Outcome == "cancelled" {
		return nil
	}
	if a.Status == AssignmentFinished || a.Status == AssignmentFailed {
		return fmt.Errorf("control: job %d already %s", jobID, a.Status)
	}
	a.Status = AssignmentFinished
	a.Outcome = "cancelled"
	if reason != "" {
		a.Error = reason
	}
	if a.FinishedAt.IsZero() {
		a.FinishedAt = time.Now().UTC()
	}
	a.EncodedJITConfig = ""
	s.removePendingLocked(a.JobID)
	return s.persistLocked(a)
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
	_ = s.persistLocked(a)
}

// ListRecent returns up to limit assignments (most recently created first).
// EncodedJITConfig is cleared in copies.
func (s *AssignmentStore) ListRecent(limit int) []*Assignment {
	if limit <= 0 {
		limit = 50
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*Assignment, 0, len(s.byID))
	for _, a := range s.byID {
		cp := *a
		cp.EncodedJITConfig = ""
		all = append(all, &cp)
	}
	// Simple insertion order by CreatedAt desc.
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].CreatedAt.After(all[i].CreatedAt) {
				all[i], all[j] = all[j], all[i]
			}
		}
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all
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
	return s.persistLocked(a)
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

func (s *AssignmentStore) loadLocked() error {
	all, err := s.persister.LoadAll()
	if err != nil {
		return err
	}
	s.byID = make(map[int64]*Assignment, len(all))
	s.pending = nil
	var minted []*Assignment
	for _, a := range all {
		if a == nil || a.JobID == 0 {
			continue
		}
		cp := *a
		s.byID[cp.JobID] = &cp
		if cp.Status == AssignmentMinted {
			minted = append(minted, &cp)
		}
	}
	sort.Slice(minted, func(i, j int) bool {
		if minted[i].CreatedAt.Equal(minted[j].CreatedAt) {
			return minted[i].JobID < minted[j].JobID
		}
		return minted[i].CreatedAt.Before(minted[j].CreatedAt)
	})
	for _, a := range minted {
		s.enqueuePendingLocked(a.JobID)
	}
	return nil
}

// persistLocked writes a copy. Never log EncodedJITConfig.
func (s *AssignmentStore) persistLocked(a *Assignment) error {
	if s.persister == nil || a == nil {
		return nil
	}
	cp := *a
	if err := s.persister.Persist(&cp); err != nil {
		slog.Warn("persist assignment failed", "job_id", a.JobID, "status", string(a.Status), "err", err)
		return err
	}
	return nil
}

type assignmentPruner interface {
	PruneFinished(olderThan time.Duration) error
}

// PruneFinished drops finished/failed assignments older than olderThan.
// Minted, assigned, and started jobs are never pruned.
func (s *AssignmentStore) PruneFinished(olderThan time.Duration) int {
	if olderThan <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	n := 0
	for id, a := range s.byID {
		if a.Status != AssignmentFinished && a.Status != AssignmentFailed {
			continue
		}
		ref := a.FinishedAt
		if ref.IsZero() {
			ref = a.CreatedAt
		}
		if ref.IsZero() || now.Sub(ref) < olderThan {
			continue
		}
		delete(s.byID, id)
		n++
	}
	if p, ok := s.persister.(assignmentPruner); ok {
		if err := p.PruneFinished(olderThan); err != nil {
			slog.Warn("prune finished assignments failed", "err", err)
		}
	}
	return n
}
