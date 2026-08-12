package github

import (
	"encoding/json"
	"fmt"
)

// WorkflowJobEvent is the subset of a workflow_job webhook we need for JIT minting.
type WorkflowJobEvent struct {
	Action       string       `json:"action"`
	WorkflowJob  WorkflowJob  `json:"workflow_job"`
	Repository   Repository   `json:"repository"`
	Organization Organization `json:"organization"`
	Installation Installation `json:"installation"`
}

// WorkflowJob is the job payload inside a workflow_job event.
type WorkflowJob struct {
	ID         int64    `json:"id"`
	RunID      int64    `json:"run_id"`
	RunAttempt int      `json:"run_attempt"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Labels     []string `json:"labels"`
	HTMLURL    string   `json:"html_url"`
}

// Repository identifies the repository that owns the workflow.
type Repository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Private  bool   `json:"private"`
}

// Organization identifies the org on org-level webhooks.
type Organization struct {
	Login string `json:"login"`
	ID    int64  `json:"id"`
}

// Installation identifies the GitHub App installation that delivered the event.
type Installation struct {
	ID int64 `json:"id"`
}

// ParseWorkflowJobEvent unmarshals a workflow_job webhook body.
func ParseWorkflowJobEvent(body []byte) (*WorkflowJobEvent, error) {
	var ev WorkflowJobEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, fmt.Errorf("github: parse workflow_job event: %w", err)
	}
	return &ev, nil
}
