package control

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/TwanLuttik/TemperCI/internal/github"
)

// JITMinter mints org-level JIT runner configs. Implemented by *github.Client.
type JITMinter interface {
	GenerateJITConfig(ctx context.Context, req github.GenerateJITConfigRequest) (*github.GenerateJITConfigResponse, error)
}

// HandlerConfig configures webhook handling for queued workflow jobs.
type HandlerConfig struct {
	// Org is the org used for generate-jitconfig when the event has no org login
	// (should be rare). Prefer event organization login when present.
	Org string
	// LabelPrefix defaults to temperci-.
	LabelPrefix string
	// RunnerGroupID is required by GitHub generate-jitconfig (Default group is often 1).
	RunnerGroupID int64
	Logger        *slog.Logger
}

// Handler processes verified workflow_job webhook payloads.
type Handler struct {
	minter JITMinter
	store  *AssignmentStore
	cfg    HandlerConfig
	log    *slog.Logger
}

// NewHandler constructs a Handler.
func NewHandler(minter JITMinter, store *AssignmentStore, cfg HandlerConfig) *Handler {
	if cfg.LabelPrefix == "" {
		cfg.LabelPrefix = DefaultLabelPrefix
	}
	if cfg.RunnerGroupID == 0 {
		cfg.RunnerGroupID = 1
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Handler{minter: minter, store: store, cfg: cfg, log: log}
}

// Store returns the assignment store.
func (h *Handler) Store() *AssignmentStore {
	return h.store
}

// HandleResult describes what the handler did with a webhook payload.
type HandleResult struct {
	// Ignored is true when the event was accepted but not acted on (wrong action,
	// non-TemperCI labels, etc.).
	Ignored bool
	// Reason is a short machine-readable ignore reason when Ignored.
	Reason     string
	Assignment *Assignment
}

// HandleWorkflowJob parses a workflow_job body and, for owned queued jobs, mints JIT.
// The caller is responsible for signature verification before invoking this.
func (h *Handler) HandleWorkflowJob(ctx context.Context, body []byte) (*HandleResult, error) {
	ev, err := github.ParseWorkflowJobEvent(body)
	if err != nil {
		return nil, err
	}

	if ev.Action != "queued" {
		return &HandleResult{Ignored: true, Reason: "action_not_queued"}, nil
	}

	owned := OwnedLabels(ev.WorkflowJob.Labels, h.cfg.LabelPrefix)
	if len(owned) == 0 {
		h.log.Info("ignoring non-temperci job",
			"job_id", ev.WorkflowJob.ID,
			"labels", ev.WorkflowJob.Labels,
		)
		return &HandleResult{Ignored: true, Reason: "labels_not_owned"}, nil
	}

	org := ev.Organization.Login
	if org == "" {
		org = h.cfg.Org
	}
	if org == "" {
		return nil, fmt.Errorf("control: cannot determine org for job %d", ev.WorkflowJob.ID)
	}

	runnerName := "temperci-job-" + strconv.FormatInt(ev.WorkflowJob.ID, 10)
	jit, err := h.minter.GenerateJITConfig(ctx, github.GenerateJITConfigRequest{
		Org:            org,
		Name:           runnerName,
		RunnerGroupID:  h.cfg.RunnerGroupID,
		Labels:         owned,
		InstallationID: ev.Installation.ID,
	})
	if err != nil {
		a := &Assignment{
			JobID:          ev.WorkflowJob.ID,
			RunID:          ev.WorkflowJob.RunID,
			Org:            org,
			RepoFullName:   ev.Repository.FullName,
			Name:           ev.WorkflowJob.Name,
			WorkflowName:   ev.EventWorkflowName(),
			Labels:         owned,
			InstallationID: ev.Installation.ID,
			RunnerName:     runnerName,
			Status:         AssignmentFailed,
			Error:          err.Error(),
		}
		h.store.Put(a)
		return nil, fmt.Errorf("control: mint JIT for job %d: %w", ev.WorkflowJob.ID, err)
	}

	a := &Assignment{
		JobID:            ev.WorkflowJob.ID,
		RunID:            ev.WorkflowJob.RunID,
		Org:              org,
		RepoFullName:     ev.Repository.FullName,
		Name:             ev.WorkflowJob.Name,
		WorkflowName:     ev.EventWorkflowName(),
		Labels:           append([]string(nil), owned...),
		InstallationID:   ev.Installation.ID,
		RunnerName:       runnerName,
		RunnerID:         jit.Runner.ID,
		EncodedJITConfig: jit.EncodedJITConfig,
		Status:           AssignmentMinted,
	}
	h.store.Put(a)

	h.log.Info("minted JIT config",
		"job_id", a.JobID,
		"run_id", a.RunID,
		"org", a.Org,
		"repo", a.RepoFullName,
		"labels", a.Labels,
		"runner_id", a.RunnerID,
		// intentionally omit EncodedJITConfig
	)

	return &HandleResult{Assignment: a}, nil
}
