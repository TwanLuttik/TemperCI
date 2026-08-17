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
	JobID      int64      `json:"job_id"`
	RunnerLog  string     `json:"runner_log"`
	AgentLog   string     `json:"agent_log"`
	ConsoleLog string     `json:"console_log"`
	Events     []JobEvent `json:"events"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// GetJobLog returns stored logs for jobID, or an empty record if none.
func (s *Store) GetJobLog(jobID int64) (*JobLog, error) {
	if jobID == 0 {
		return nil, fmt.Errorf("store: job_id required")
	}
	var runner, agent, console, eventsJSON, updated string
	err := s.db.QueryRow(`
SELECT runner_log, agent_log, console_log, events_json, updated_at
FROM job_logs WHERE job_id = ?`, jobID).Scan(&runner, &agent, &console, &eventsJSON, &updated)
	if err == sql.ErrNoRows {
		return &JobLog{JobID: jobID, Events: []JobEvent{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get job log: %w", err)
	}
	out := &JobLog{
		JobID:      jobID,
		RunnerLog:  runner,
		AgentLog:   agent,
		ConsoleLog: console,
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
func (s *Store) MergeJobLogs(jobID int64, runnerLog, agentLog, consoleLog string) error {
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
	return s.writeJobLog(cur)
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
INSERT INTO job_logs (job_id, runner_log, agent_log, console_log, events_json, updated_at)
VALUES (?,?,?,?,?,?)
ON CONFLICT(job_id) DO UPDATE SET
  runner_log = excluded.runner_log,
  agent_log = excluded.agent_log,
  console_log = excluded.console_log,
  events_json = excluded.events_json,
  updated_at = excluded.updated_at
`, l.JobID, l.RunnerLog, l.AgentLog, l.ConsoleLog, string(raw), now.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: write job log: %w", err)
	}
	return nil
}
