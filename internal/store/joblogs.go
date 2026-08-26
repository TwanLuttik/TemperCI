package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const maxJobEvents = 200

// JobEvent is one control/agent timeline entry for a GitHub job.
type JobEvent struct {
	Time    time.Time `json:"time"`
	Source  string    `json:"source"`
	Level   string    `json:"level,omitempty"`
	Message string    `json:"message"`
}

// JobLog is persisted guest + control diagnostic material for one job.
type JobLog struct {
	JobID       int64      `json:"job_id"`
	RunnerLog   string     `json:"runner_log"`
	AgentLog    string     `json:"agent_log"`
	ConsoleLog  string     `json:"console_log"`
	WorkflowLog string     `json:"workflow_log"`
	Events      []JobEvent `json:"events"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// GetJobLog returns stored logs for jobID, or an empty record if none.
func (s *Store) GetJobLog(jobID int64) (*JobLog, error) {
	if jobID == 0 {
		return nil, fmt.Errorf("store: job_id required")
	}
	var runner, agent, console, workflow, eventsJSON, updated string
	err := s.db.QueryRow(`
SELECT runner_log, agent_log, console_log, workflow_log, events_json, updated_at
FROM job_logs WHERE job_id = ?`, jobID).Scan(&runner, &agent, &console, &workflow, &eventsJSON, &updated)
	if err == sql.ErrNoRows {
		return &JobLog{JobID: jobID, Events: []JobEvent{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get job log: %w", err)
	}
	out := &JobLog{
		JobID:       jobID,
		RunnerLog:   runner,
		AgentLog:    agent,
		ConsoleLog:  console,
		WorkflowLog: workflow,
	}
	if updated != "" {
		out.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	}
	if strings.TrimSpace(eventsJSON) != "" && eventsJSON != "[]" {
		_ = json.Unmarshal([]byte(eventsJSON), &out.Events)
	}
	if out.Events == nil {
		out.Events = []JobEvent{}
	}
	return out, nil
}

// MergeJobLogs upserts guest log bodies. Empty incoming fields leave existing text.
func (s *Store) MergeJobLogs(jobID int64, runnerLog, agentLog, consoleLog string, workflowLog ...string) error {
	if jobID == 0 {
		return fmt.Errorf("store: job_id required")
	}
	cur, err := s.GetJobLog(jobID)
	if err != nil {
		return err
	}
	if runnerLog != "" {
		cur.RunnerLog = runnerLog
	}
	if agentLog != "" {
		cur.AgentLog = agentLog
	}
	if consoleLog != "" {
		cur.ConsoleLog = consoleLog
	}
	if len(workflowLog) > 0 && workflowLog[0] != "" && AcceptWorkflowLog(workflowLog[0]) {
		cur.WorkflowLog = workflowLog[0]
	}
	return s.writeJobLog(cur)
}

// ApplyWorkflowAppend returns the updated log, or ok=false if the chunk is a gap/duplicate.
func ApplyWorkflowAppend(cur string, offset int, chunk string) (string, bool) {
	if chunk == "" || offset < 0 {
		return cur, false
	}
	if cur == "" {
		if offset != 0 || !AcceptWorkflowLog(chunk) {
			return cur, false
		}
		return chunk, true
	}
	if offset == len(cur) {
		return cur + chunk, true
	}
	if offset < len(cur) && offset+len(chunk) > len(cur) {
		return cur[:offset] + chunk, true
	}
	return cur, false
}

// AppendWorkflowLog writes new bytes at offset. Gaps are ignored (inject heals).
func (s *Store) AppendWorkflowLog(jobID int64, offset int, chunk string) error {
	if jobID == 0 {
		return fmt.Errorf("store: job_id required")
	}
	cur, err := s.GetJobLog(jobID)
	if err != nil {
		return err
	}
	next, ok := ApplyWorkflowAppend(cur.WorkflowLog, offset, chunk)
	if !ok {
		return nil
	}
	cur.WorkflowLog = next
	return s.writeJobLog(cur)
}

// SetWorkflowLog stores the official GitHub Actions job log (step output).
func (s *Store) SetWorkflowLog(jobID int64, text string) error {
	if jobID == 0 {
		return fmt.Errorf("store: job_id required")
	}
	cur, err := s.GetJobLog(jobID)
	if err != nil {
		return err
	}
	if !AcceptWorkflowLog(text) {
		return nil
	}
	cur.WorkflowLog = text
	return s.writeJobLog(cur)
}

// AcceptWorkflowLog reports whether text is GitHub Actions step output
// (##[group] / ##[command]) rather than runner _diag (JobServerQueue, HostContext).
func AcceptWorkflowLog(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	if looksLikeActionsStepLog(text) {
		return true
	}
	return !looksLikeRunnerDiag(text)
}

func looksLikeActionsStepLog(s string) bool {
	return strings.Contains(s, "##[group]") ||
		strings.Contains(s, "##[section]") ||
		strings.Contains(s, "##[command]") ||
		strings.Contains(s, "##[error]")
}

func looksLikeRunnerDiag(s string) bool {
	return strings.Contains(s, "INFO JobServerQueue]") ||
		strings.Contains(s, "INFO HostContext]") ||
		strings.Contains(s, "INFO JobRunner]") ||
		strings.Contains(s, "INFO StepsRunner]")
}

// AppendJobEvent appends a timeline event (oldest dropped after maxJobEvents).
func (s *Store) AppendJobEvent(jobID int64, ev JobEvent) error {
	if jobID == 0 {
		return fmt.Errorf("store: job_id required")
	}
	if ev.Time.IsZero() {
		ev.Time = time.Now().UTC()
	}
	if ev.Level == "" {
		ev.Level = "info"
	}
	cur, err := s.GetJobLog(jobID)
	if err != nil {
		return err
	}
	cur.Events = append(cur.Events, ev)
	if len(cur.Events) > maxJobEvents {
		cur.Events = cur.Events[len(cur.Events)-maxJobEvents:]
	}
	return s.writeJobLog(cur)
}

func (s *Store) writeJobLog(l *JobLog) error {
	if l.Events == nil {
		l.Events = []JobEvent{}
	}
	raw, err := json.Marshal(l.Events)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	l.UpdatedAt = now
	_, err = s.db.Exec(`
INSERT INTO job_logs (job_id, runner_log, agent_log, console_log, workflow_log, events_json, updated_at)
VALUES (?,?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  runner_log = excluded.runner_log,
  agent_log = excluded.agent_log,
  console_log = excluded.console_log,
  workflow_log = excluded.workflow_log,
  events_json = excluded.events_json,
  updated_at = excluded.updated_at
`, l.JobID, l.RunnerLog, l.AgentLog, l.ConsoleLog, l.WorkflowLog, string(raw), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: write job log: %w", err)
	}
	return nil
}
