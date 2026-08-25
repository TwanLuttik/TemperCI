package control

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/github"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

// Server is the control-plane HTTP API (webhooks + agent + health + metrics + dashboard).
type Server struct {
	handler       *Handler
	store         *AssignmentStore
	agents        *AgentRegistry
	webhookSecret []byte
	agentToken    string
	log           *slog.Logger
	mux           *http.ServeMux
	dash          *DashboardConfig
	hub           *Hub
	cacheq        *cacheQueue
	cmdq          *cmdQueue
	runnerDelete  RunnerDeleter
	jobLogs       JobLogDownloader
	wfFetchMu     sync.Mutex
	wfFetchAt     map[int64]time.Time
	jobMetaMu     sync.Mutex
	jobMeta       map[int64]jobMetaCache
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Handler       *Handler
	Store         *AssignmentStore
	Agents        *AgentRegistry
	WebhookSecret string
	// AgentToken is the shared secret for agent API auth (Bearer). Required for agent routes.
	AgentToken string
	Logger     *slog.Logger
	// Dashboard enables the operator UI and /api/v1 routes when non-nil Config is set.
	Dashboard *DashboardConfig
	// Hub is the optional WebSocket broadcast hub for realtime dashboard updates.
	Hub *Hub
	// JobLogs downloads official GitHub Actions job logs for the dashboard.
	JobLogs JobLogDownloader
	// RunnerDelete removes a JIT self-hosted runner after operator cancel.
	RunnerDelete RunnerDeleter
}

// NewServer builds an HTTP handler serving health, GitHub webhooks, agent APIs, and metrics.
func NewServer(cfg ServerConfig) *Server {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	store := cfg.Store
	if store == nil && cfg.Handler != nil {
		store = cfg.Handler.Store()
	}
	if store == nil {
		store = NewAssignmentStore()
	}
	agents := cfg.Agents
	if agents == nil {
		agents = NewAgentRegistry()
	}
	hub := cfg.Hub
	if hub == nil {
		hub = NewHub(log)
	}
	s := &Server{
		handler:       cfg.Handler,
		store:         store,
		agents:        agents,
		webhookSecret: []byte(cfg.WebhookSecret),
		agentToken:    cfg.AgentToken,
		log:           log,
		mux:           http.NewServeMux(),
		hub:           hub,
		cacheq:        newCacheQueue(),
		cmdq:          newCmdQueue(),
		runnerDelete:  cfg.RunnerDelete,
		jobLogs:       cfg.JobLogs,
		wfFetchAt:     make(map[int64]time.Time),
		jobMeta:       make(map[int64]jobMetaCache),
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("POST /webhooks/github", s.handleGitHubWebhook)
	// Also accept the common singular path.
	s.mux.HandleFunc("POST /webhook/github", s.handleGitHubWebhook)

	// Agent API (shared token auth; optional TLS/mTLS at listener layer).
	s.mux.HandleFunc("POST /v1/agent/register", s.withAgentAuth(s.handleAgentRegister))
	s.mux.HandleFunc("POST /v1/agent/jobs/claim", s.withAgentAuth(s.handleJobClaim))
	s.mux.HandleFunc("POST /v1/agent/jobs/started", s.withAgentAuth(s.handleJobStarted))
	s.mux.HandleFunc("POST /v1/agent/jobs/finished", s.withAgentAuth(s.handleJobFinished))
	s.mux.HandleFunc("POST /v1/agent/jobs/logs", s.withAgentAuth(s.handleJobLogs))

	if cfg.Dashboard != nil && cfg.Dashboard.Config != nil {
		s.mountDashboard(*cfg.Dashboard)
	}
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Store returns the assignment store (for tests).
func (s *Server) Store() *AssignmentStore {
	return s.store
}

// Agents returns the agent registry (for tests).
func (s *Server) Agents() *AgentRegistry {
	return s.agents
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	counts := s.store.CountByStatus()
	m := api.ControlMetrics{
		AgentsRegistered: s.agents.Len(),
		JobsPending:      s.store.PendingLen(),
		JobsMinted:       counts.Minted,
		JobsAssigned:     counts.Assigned,
		JobsStarted:      counts.Started,
		JobsFinished:     counts.Finished,
		JobsFailed:       counts.Failed,
		Agents:           s.agents.List(),
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	const maxBody = 1 << 20 // 1 MiB
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if len(body) > maxBody {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}

	sig := r.Header.Get("X-Hub-Signature-256")
	if err := github.VerifyWebhookSignature(s.webhookSecret, body, sig); err != nil {
		s.log.Warn("webhook signature rejected", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event := r.Header.Get("X-GitHub-Event")
	s.recordWebhookDelivery(event, r.Header.Get("X-GitHub-Delivery"))
	if event != "" && event != "workflow_job" && event != "ping" {
		// Accept other events with 200 so GitHub does not retry noisily.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"ignored":true,"reason":"event_type"}`))
		return
	}
	if event == "ping" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}

	if s.handler == nil {
		http.Error(w, "handler not configured", http.StatusInternalServerError)
		return
	}

	// ACK before GitHub's ~10s budget. Ignore-only events stay on this
	// goroutine (no GitHub API). Owned queued jobs mint in the background.
	ev, err := github.ParseWorkflowJobEvent(body)
	if err != nil {
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}
	if ev.Action == "completed" || ev.Action == "cancelled" {
		result := s.finishFromGitHub(r.Context(), ev)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if result != nil && result.Ignored {
			_, _ = w.Write([]byte(`{"ok":true,"ignored":true,"reason":"` + result.Reason + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
		return
	}

	if ev.Action == "in_progress" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go func() {
			result := s.recoverStolenRunner(context.Background(), ev)
			if result != nil && !result.Ignored && result.Assignment != nil {
				s.log.Info("recovered stolen runner",
					"minted_job_id", result.Assignment.JobID,
					"taken_by", ev.WorkflowJob.ID,
				)
			}
		}()
		return
	}

	if ev.Action != "queued" || !IsOwned(ev.WorkflowJob.Labels, s.handler.cfg.LabelPrefix) {
		result, herr := s.handler.HandleWorkflowJob(r.Context(), body)
		if herr != nil {
			s.log.Error("webhook handling failed", "err", herr)
			http.Error(w, "handler error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if result != nil && result.Ignored {
			_, _ = w.Write([]byte(`{"ok":true,"ignored":true,"reason":"` + result.Reason + `"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true,"accepted":true}`))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	bodyCopy := append([]byte(nil), body...)
	go func() {
		result, herr := s.handler.HandleWorkflowJob(context.Background(), bodyCopy)
		if herr != nil {
			s.log.Error("webhook handling failed", "err", herr)
			if ev, pErr := github.ParseWorkflowJobEvent(bodyCopy); pErr == nil {
				s.recordJobEvent(ev.WorkflowJob.ID, "control", "error", "mint JIT failed: "+herr.Error())
			}
			return
		}
		if result != nil && !result.Ignored && result.Assignment != nil {
			s.recordJobEvent(result.Assignment.JobID, "control", "info",
				"minted JIT config repo="+result.Assignment.RepoFullName+" runner="+result.Assignment.RunnerName)
			s.PublishSnapshot()
		}
	}()
}

func (s *Server) recordWebhookDelivery(event, delivery string) {
	if s.dash == nil || s.dash.Store == nil {
		return
	}
	event = strings.TrimSpace(event)
	if event == "" {
		event = "unknown"
	}
	_ = s.dash.Store.RecordWebhookDelivery(store.WebhookDelivery{
		Event:    event,
		Delivery: strings.TrimSpace(delivery),
	})
}

type agentHandler func(w http.ResponseWriter, r *http.Request)

func (s *Server) withAgentAuth(next agentHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.agentToken == "" {
			s.log.Error("agent API called but agent_token not configured")
			writeAPIError(w, http.StatusServiceUnavailable, "agent auth not configured")
			return
		}
		auth := r.Header.Get(api.AgentAuthHeader)
		if auth == "" {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		token := auth
		if strings.HasPrefix(auth, api.AgentBearerPrefix) {
			token = strings.TrimPrefix(auth, api.AgentBearerPrefix)
		}
		if !tokenEqual(token, s.agentToken) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (s *Server) handleAgentRegister(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeAPIError(w, http.StatusBadRequest, "agent_id required")
		return
	}
	info := s.agents.Register(req)
	s.log.Info("agent registered",
		"agent_id", info.AgentID,
		"capacity", info.Capacity,
		"max_capacity", info.MaxCapacity,
		"warm", info.Warm,
		"busy", info.Busy,
		"vms", len(info.VMs),
	)
	s.PublishSnapshot()
	writeJSON(w, http.StatusOK, api.RegisterResponse{
		OK:       true,
		AgentID:  info.AgentID,
		CacheOps: s.cacheq.take(info.AgentID),
		Commands: s.cmdq.take(info.AgentID),
	})
}

func (s *Server) handleJobClaim(w http.ResponseWriter, r *http.Request) {
	var req api.ClaimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		writeAPIError(w, http.StatusBadRequest, "agent_id required")
		return
	}
	// Capacity-aware gate: no free slots → no assignment (FIFO job stays pending).
	if req.FreeSlots <= 0 {
		s.agents.UpdateCapacity(req.AgentID, 0, req.Warm, req.Busy)
		s.agents.Touch(req.AgentID)
		writeJSON(w, http.StatusOK, api.ClaimResponse{OK: true, Job: nil})
		return
	}
	s.agents.UpdateCapacity(req.AgentID, req.FreeSlots, req.Warm, req.Busy)
	s.agents.Touch(req.AgentID)
	wait := time.Duration(req.WaitMS) * time.Millisecond
	if wait > 30*time.Second {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	var a *Assignment
	for {
		a = s.store.ClaimNext(req.AgentID, req.CachedRepos)
		if a != nil || wait <= 0 || !time.Now().Before(deadline) {
			break
		}
		s.store.WaitMinted(r.Context(), time.Until(deadline))
	}
	if a == nil {
		writeJSON(w, http.StatusOK, api.ClaimResponse{OK: true, Job: nil})
		return
	}
	// Reflect one less free slot after successful claim (best-effort registry view).
	s.agents.UpdateCapacity(req.AgentID, req.FreeSlots-1, req.Warm, req.Busy+1)
	s.log.Info("job claimed",
		"job_id", a.JobID,
		"agent_id", req.AgentID,
		"runner_id", a.RunnerID,
		// intentionally omit EncodedJITConfig
	)
	s.recordJobEvent(a.JobID, "control", "info", "claimed by agent "+req.AgentID)
	s.PublishSnapshot()
	writeJSON(w, http.StatusOK, api.ClaimResponse{
		OK: true,
		Job: &api.JobAssignment{
			JobID:            a.JobID,
			RunID:            a.RunID,
			Org:              a.Org,
			RepoFullName:     a.RepoFullName,
			Name:             a.Name,
			Labels:           append([]string(nil), a.Labels...),
			RunnerName:       a.RunnerName,
			RunnerID:         a.RunnerID,
			EncodedJITConfig: a.EncodedJITConfig,
			Status:           string(a.Status),
			AssignedAgentID:  a.AssignedAgentID,
		},
	})
}

func (s *Server) handleJobStarted(w http.ResponseWriter, r *http.Request) {
	var req api.JobStartedRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AgentID == "" || req.JobID == 0 {
		writeAPIError(w, http.StatusBadRequest, "agent_id and job_id required")
		return
	}
	if err := s.store.MarkStarted(req.JobID, req.AgentID, req.VMID, req.WarmBind); err != nil {
		writeAPIError(w, http.StatusConflict, err.Error())
		return
	}
	s.agents.Touch(req.AgentID)
	s.log.Info("job started",
		"job_id", req.JobID,
		"agent_id", req.AgentID,
		"vm_id", req.VMID,
		"warm_bind", req.WarmBind,
	)
	msg := "started on " + req.VMID
	if req.WarmBind {
		msg += " (warm bind)"
	}
	s.recordJobEvent(req.JobID, "agent", "info", msg)
	s.PublishSnapshot()
	writeJSON(w, http.StatusOK, api.JobStartedResponse{OK: true})
}

func (s *Server) handleJobFinished(w http.ResponseWriter, r *http.Request) {
	var req api.JobFinishedRequest
	if err := decodeJSONLimit(r, &req, 4<<20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AgentID == "" || req.JobID == 0 {
		writeAPIError(w, http.StatusBadRequest, "agent_id and job_id required")
		return
	}
	outcome := req.Outcome
	if outcome == "" {
		outcome = "unknown"
	}
	if a := s.store.Get(req.JobID); a != nil && a.Name != "" &&
		agent.RefineOutcomeForJob("success", req.RunnerLog, a.Name) == "error" {
		reason := "runner accepted different GitHub job"
		if started := agent.RunningJobName(req.RunnerLog); started != "" {
			reason += ": " + started
		}
		if s.handler != nil {
			if _, rerr := s.handler.Remint(r.Context(), req.JobID, reason); rerr != nil {
				s.log.Error("remint after wrong job", "job_id", req.JobID, "err", rerr)
			} else {
				s.mergeJobLogs(req.JobID, req.RunnerLog, req.AgentLog, req.ConsoleLog, req.WorkflowLog)
				s.agents.Touch(req.AgentID)
				s.recordJobEvent(req.JobID, "control", "warn", reason+"; reminted JIT")
				s.PublishSnapshot()
				writeJSON(w, http.StatusOK, api.JobFinishedResponse{OK: true})
				return
			}
		}
		outcome = "error"
		if req.Error == "" {
			req.Error = reason
		}
	}
	if err := s.store.MarkFinished(req.JobID, req.AgentID, outcome, req.VMID, req.WarmBind, req.Error); err != nil {
		writeAPIError(w, http.StatusConflict, err.Error())
		return
	}
	if req.CacheHits != 0 || req.CacheMisses != 0 || req.CacheBytesIn != 0 || req.CacheBytesOut != 0 {
		_ = s.store.SetCacheStats(req.JobID, req.CacheHits, req.CacheMisses, req.CacheBytesIn, req.CacheBytesOut)
	}
	s.mergeJobLogs(req.JobID, req.RunnerLog, req.AgentLog, req.ConsoleLog, req.WorkflowLog)
	s.agents.Touch(req.AgentID)
	s.log.Info("job finished",
		"job_id", req.JobID,
		"agent_id", req.AgentID,
		"outcome", outcome,
		"vm_id", req.VMID,
		"warm_bind", req.WarmBind,
	)
	msg := "finished outcome=" + outcome
	if req.Error != "" {
		msg += " err=" + req.Error
	}
	level := "info"
	if outcome != "success" {
		level = "warn"
	}
	s.recordJobEvent(req.JobID, "agent", level, msg)
	s.PublishSnapshot()
	writeJSON(w, http.StatusOK, api.JobFinishedResponse{OK: true})
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	var req api.JobLogsRequest
	if err := decodeJSONLimit(r, &req, 4<<20); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.AgentID == "" || req.JobID == 0 {
		writeAPIError(w, http.StatusBadRequest, "agent_id and job_id required")
		return
	}
	s.mergeJobLogs(req.JobID, req.RunnerLog, req.AgentLog, req.ConsoleLog, req.WorkflowLog)
	s.agents.Touch(req.AgentID)
	writeJSON(w, http.StatusOK, api.JobLogsResponse{OK: true})
}

func (s *Server) jobDB() *store.Store {
	if s.dash != nil && s.dash.Store != nil {
		return s.dash.Store
	}
	return nil
}

func (s *Server) recordJobEvent(jobID int64, source, level, message string) {
	if jobID == 0 || message == "" {
		return
	}
	db := s.jobDB()
	if db == nil {
		return
	}
	_ = db.AppendJobEvent(jobID, store.JobEvent{
		Source:  source,
		Level:   level,
		Message: message,
	})
}

func (s *Server) mergeJobLogs(jobID int64, runner, agent, console string, workflow ...string) {
	wf := ""
	if len(workflow) > 0 {
		wf = workflow[0]
	}
	if jobID == 0 || (runner == "" && agent == "" && console == "" && wf == "") {
		return
	}
	db := s.jobDB()
	if db == nil {
		return
	}
	_ = db.MergeJobLogs(jobID, runner, agent, console, wf)
}

func decodeJSON(r *http.Request, dst any) error {
	return decodeJSONLimit(r, dst, 1<<20)
}

func decodeJSONLimit(r *http.Request, dst any, maxBody int64) error {
	defer r.Body.Close()
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	return dec.Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, api.ErrorBody{OK: false, Error: msg})
}
