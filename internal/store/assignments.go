package store

import (
	"database/sql"
	"fmt"
	"time"
)

// AssignmentRow is a durable job assignment. EncodedJITConfig is secret — never log it.
type AssignmentRow struct {
	JobID            int64
	RunID            int64
	Org              string
	RepoFullName     string
	LabelsJSON       string
	InstallationID   int64
	RunnerName       string
	RunnerID         int64
	EncodedJITConfig string
	Status           string
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
	JobName          string
	WorkflowName     string
}

const assignmentColumns = `job_id, run_id, org, repo_full_name, labels_json, installation_id,
  runner_name, runner_id, encoded_jit_config, status, created_at,
  assigned_at, started_at, finished_at, assigned_agent_id, vm_id,
  warm_bind, outcome, error, cache_hits, cache_misses, cache_bytes_in, cache_bytes_out,
  job_name, workflow_name`

// UpsertAssignment inserts or replaces an assignment by job_id.
// encoded_jit_config is cleared when status is finished or failed.
func (s *Store) UpsertAssignment(a AssignmentRow) error {
	if a.JobID == 0 {
		return fmt.Errorf("store: assignment job_id required")
	}
	if a.Status == "finished" || a.Status == "failed" {
		a.EncodedJITConfig = ""
	}
	if a.LabelsJSON == "" {
		a.LabelsJSON = "[]"
	}
	warm := 0
	if a.WarmBind {
		warm = 1
	}
	_, err := s.db.Exec(`
INSERT INTO assignments (
  job_id, run_id, org, repo_full_name, labels_json, installation_id,
  runner_name, runner_id, encoded_jit_config, status, created_at,
  assigned_at, started_at, finished_at, assigned_agent_id, vm_id,
  warm_bind, outcome, error, cache_hits, cache_misses, cache_bytes_in, cache_bytes_out,
  job_name, workflow_name
) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  run_id = excluded.run_id,
  org = excluded.org,
  repo_full_name = excluded.repo_full_name,
  labels_json = excluded.labels_json,
  installation_id = excluded.installation_id,
  runner_name = excluded.runner_name,
  runner_id = excluded.runner_id,
  encoded_jit_config = excluded.encoded_jit_config,
  status = excluded.status,
  created_at = excluded.created_at,
  assigned_at = excluded.assigned_at,
  started_at = excluded.started_at,
  finished_at = excluded.finished_at,
  assigned_agent_id = excluded.assigned_agent_id,
  vm_id = excluded.vm_id,
  warm_bind = excluded.warm_bind,
  outcome = excluded.outcome,
  error = excluded.error,
  cache_hits = excluded.cache_hits,
  cache_misses = excluded.cache_misses,
  cache_bytes_in = excluded.cache_bytes_in,
  cache_bytes_out = excluded.cache_bytes_out,
  job_name = excluded.job_name,
  workflow_name = excluded.workflow_name
`,
		a.JobID, a.RunID, a.Org, a.RepoFullName, a.LabelsJSON, a.InstallationID,
		a.RunnerName, a.RunnerID, a.EncodedJITConfig, a.Status, formatStoredTime(a.CreatedAt),
		formatStoredTime(a.AssignedAt), formatStoredTime(a.StartedAt), formatStoredTime(a.FinishedAt),
		a.AssignedAgentID, a.VMID, warm, a.Outcome, a.Error,
		a.CacheHits, a.CacheMisses, a.CacheBytesIn, a.CacheBytesOut,
		a.JobName, a.WorkflowName,
	)
	if err != nil {
		return fmt.Errorf("store: upsert assignment: %w", err)
	}
	return nil
}

// GetAssignment loads one assignment, or nil if missing.
func (s *Store) GetAssignment(jobID int64) (*AssignmentRow, error) {
	row := s.db.QueryRow(`SELECT `+assignmentColumns+` FROM assignments WHERE job_id = ?`, jobID)
	a, err := scanAssignment(row)
	if err != nil {
		return nil, fmt.Errorf("store: get assignment: %w", err)
	}
	return a, nil
}

// ListAssignments returns all assignments ordered by created_at ascending.
func (s *Store) ListAssignments() ([]AssignmentRow, error) {
	rows, err := s.db.Query(`SELECT ` + assignmentColumns + ` FROM assignments ORDER BY created_at ASC, job_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("store: list assignments: %w", err)
	}
	defer rows.Close()
	var out []AssignmentRow
	for rows.Next() {
		a, err := scanAssignment(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list assignments: %w", err)
		}
		if a != nil {
			out = append(out, *a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list assignments: %w", err)
	}
	return out, nil
}

// DeleteAssignment removes an assignment by job_id.
func (s *Store) DeleteAssignment(jobID int64) error {
	_, err := s.db.Exec(`DELETE FROM assignments WHERE job_id = ?`, jobID)
	if err != nil {
		return fmt.Errorf("store: delete assignment: %w", err)
	}
	return nil
}

// PruneFinished deletes finished/failed rows older than olderThan.
// Minted, assigned, and started rows are never deleted.
func (s *Store) PruneFinished(olderThan time.Duration) (int, error) {
	if olderThan <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
DELETE FROM assignments
WHERE status IN ('finished', 'failed')
  AND (
    (finished_at != '' AND finished_at < ?)
    OR (finished_at = '' AND created_at != '' AND created_at < ?)
  )`, cutoff, cutoff)
	if err != nil {
		return 0, fmt.Errorf("store: prune finished: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanAssignment(row scannable) (*AssignmentRow, error) {
	var a AssignmentRow
	var created, assigned, started, finished string
	var warm int
	err := row.Scan(
		&a.JobID, &a.RunID, &a.Org, &a.RepoFullName, &a.LabelsJSON, &a.InstallationID,
		&a.RunnerName, &a.RunnerID, &a.EncodedJITConfig, &a.Status, &created,
		&assigned, &started, &finished, &a.AssignedAgentID, &a.VMID,
		&warm, &a.Outcome, &a.Error,
		&a.CacheHits, &a.CacheMisses, &a.CacheBytesIn, &a.CacheBytesOut,
		&a.JobName, &a.WorkflowName,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = parseStoredTime(created)
	a.AssignedAt = parseStoredTime(assigned)
	a.StartedAt = parseStoredTime(started)
	a.FinishedAt = parseStoredTime(finished)
	a.WarmBind = warm != 0
	return &a, nil
}

func formatStoredTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func parseStoredTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	t, _ := time.Parse(time.RFC3339, s)
	return t
}
