package control

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/github"
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
	s := &Server{
		handler:       cfg.Handler,
		store:         store,
		agents:        agents,
		webhookSecret: []byte(cfg.WebhookSecret),
		agentToken:    cfg.AgentToken,
		log:           log,
		mux:           http.NewServeMux(),
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
	result, err := s.handler.HandleWorkflowJob(r.Context(), body)
	if err != nil {
		s.log.Error("webhook handling failed", "err", err)
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if result.Ignored {
		_, _ = w.Write([]byte(`{"ok":true,"ignored":true,"reason":"` + result.Reason + `"}`))
		return
	}
	_, _ = w.Write([]byte(`{"ok":true,"minted":true}`))
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
	)
	writeJSON(w, http.StatusOK, api.RegisterResponse{OK: true, AgentID: info.AgentID})
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
	a := s.store.ClaimNext(req.AgentID)
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
	writeJSON(w, http.StatusOK, api.ClaimResponse{
		OK: true,
		Job: &api.JobAssignment{
			JobID:            a.JobID,
			RunID:            a.RunID,
			Org:              a.Org,
			RepoFullName:     a.RepoFullName,
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
	writeJSON(w, http.StatusOK, api.JobStartedResponse{OK: true})
}

func (s *Server) handleJobFinished(w http.ResponseWriter, r *http.Request) {
	var req api.JobFinishedRequest
	if err := decodeJSON(r, &req); err != nil {
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
	if err := s.store.MarkFinished(req.JobID, req.AgentID, outcome, req.VMID, req.WarmBind, req.Error); err != nil {
		writeAPIError(w, http.StatusConflict, err.Error())
		return
	}
	s.agents.Touch(req.AgentID)
	s.log.Info("job finished",
		"job_id", req.JobID,
		"agent_id", req.AgentID,
		"outcome", outcome,
		"vm_id", req.VMID,
		"warm_bind", req.WarmBind,
	)
	writeJSON(w, http.StatusOK, api.JobFinishedResponse{OK: true})
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	const maxBody = 1 << 20
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
