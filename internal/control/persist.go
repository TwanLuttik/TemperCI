package control

import (
	"encoding/json"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/store"
)

// storePersister maps store.Store onto AssignmentPersister.
type storePersister struct {
	db *store.Store
}

// NewStorePersister adapts a SQLite store for assignment durability.
// Returns nil when db is nil so callers can pass it to NewAssignmentStoreWithPersister.
func NewStorePersister(db *store.Store) AssignmentPersister {
	if db == nil {
		return nil
	}
	return &storePersister{db: db}
}

func (p *storePersister) Persist(a *Assignment) error {
	if a == nil {
		return nil
	}
	return p.db.UpsertAssignment(assignmentToRow(a))
}

func (p *storePersister) LoadAll() ([]*Assignment, error) {
	rows, err := p.db.ListAssignments()
	if err != nil {
		return nil, err
	}
	out := make([]*Assignment, 0, len(rows))
	for i := range rows {
		out = append(out, rowToAssignment(&rows[i]))
	}
	return out, nil
}

func (p *storePersister) PruneFinished(olderThan time.Duration) error {
	_, err := p.db.PruneFinished(olderThan)
	return err
}

func (p *storePersister) Delete(jobID int64) error {
	return p.db.DeleteAssignment(jobID)
}

func assignmentToRow(a *Assignment) store.AssignmentRow {
	labels := "[]"
	if len(a.Labels) > 0 {
		if b, err := json.Marshal(a.Labels); err == nil {
			labels = string(b)
		}
	}
	return store.AssignmentRow{
		JobID:            a.JobID,
		RunID:            a.RunID,
		Org:              a.Org,
		RepoFullName:     a.RepoFullName,
		LabelsJSON:       labels,
		InstallationID:   a.InstallationID,
		RunnerName:       a.RunnerName,
		RunnerID:         a.RunnerID,
		EncodedJITConfig: a.EncodedJITConfig,
		Status:           string(a.Status),
		CreatedAt:        a.CreatedAt,
		AssignedAt:       a.AssignedAt,
		StartedAt:        a.StartedAt,
		FinishedAt:       a.FinishedAt,
		AssignedAgentID:  a.AssignedAgentID,
		VMID:             a.VMID,
		WarmBind:         a.WarmBind,
		Outcome:          a.Outcome,
		Error:            a.Error,
	}
}

func rowToAssignment(r *store.AssignmentRow) *Assignment {
	var labels []string
	if r.LabelsJSON != "" && r.LabelsJSON != "[]" {
		_ = json.Unmarshal([]byte(r.LabelsJSON), &labels)
	}
	return &Assignment{
		JobID:            r.JobID,
		RunID:            r.RunID,
		Org:              r.Org,
		RepoFullName:     r.RepoFullName,
		Labels:           labels,
		InstallationID:   r.InstallationID,
		RunnerName:       r.RunnerName,
		RunnerID:         r.RunnerID,
		EncodedJITConfig: r.EncodedJITConfig,
		Status:           AssignmentStatus(r.Status),
		CreatedAt:        r.CreatedAt,
		AssignedAt:       r.AssignedAt,
		StartedAt:        r.StartedAt,
		FinishedAt:       r.FinishedAt,
		AssignedAgentID:  r.AssignedAgentID,
		VMID:             r.VMID,
		WarmBind:         r.WarmBind,
		Outcome:          r.Outcome,
		Error:            r.Error,
	}
}
