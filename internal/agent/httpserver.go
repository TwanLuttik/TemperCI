package agent

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// LocalServer exposes agent metrics and admin pool controls (bind to localhost in production).
type LocalServer struct {
	Pool      *Pool
	AgentID   string
	// AdminToken authenticates admin routes (Bearer). Empty rejects admin writes.
	AdminToken string
	Log        *slog.Logger
	mux        *http.ServeMux
}

// NewLocalServer builds the agent-local HTTP handler.
func NewLocalServer(pool *Pool, agentID, adminToken string, log *slog.Logger) *LocalServer {
	if log == nil {
		log = slog.Default()
	}
	s := &LocalServer{
		Pool:       pool,
		AgentID:    agentID,
		AdminToken: adminToken,
		Log:        log,
		mux:        http.NewServeMux(),
	}
	s.mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("POST /v1/admin/pool/drain", s.withAdminAuth(s.handleDrain))
	s.mux.HandleFunc("POST /v1/admin/pool/reload", s.withAdminAuth(s.handleReload))
	return s
}

// Handler returns the root HTTP handler.
func (s *LocalServer) Handler() http.Handler {
	return s.mux
}

func (s *LocalServer) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *LocalServer) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	if s.Pool == nil {
		http.Error(w, "pool not configured", http.StatusServiceUnavailable)
		return
	}
	m := s.Pool.Metrics()
	body := api.AgentMetrics{
		AgentID:     s.AgentID,
		Warm:        m.Counts.Warm,
		Busy:        m.Counts.Busy,
		PoolBoot:    m.Counts.PoolBoot,
		Destroying:  m.Counts.Destroying,
		WarmBinds:   m.WarmBinds,
		ColdStarts:  m.ColdStarts,
		DestroysOK:  m.DestroysOK,
		DestroyFail: m.DestroyFail,
		Recycles:    m.Recycles,
		Orphans:     m.Orphans,
		ImagePath:   s.Pool.ImagePath(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func (s *LocalServer) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.AdminToken == "" {
			http.Error(w, `{"ok":false,"error":"admin auth not configured"}`, http.StatusServiceUnavailable)
			return
		}
		auth := r.Header.Get(api.AgentAuthHeader)
		token := auth
		if strings.HasPrefix(auth, api.AgentBearerPrefix) {
			token = strings.TrimPrefix(auth, api.AgentBearerPrefix)
		}
		if token == "" || token != s.AdminToken {
			http.Error(w, `{"ok":false,"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *LocalServer) handleDrain(w http.ResponseWriter, r *http.Request) {
	if s.Pool == nil {
		writeAgentErr(w, http.StatusServiceUnavailable, "pool not configured")
		return
	}
	n, err := s.Pool.DrainWarm(r.Context())
	resp := api.PoolReloadResponse{OK: err == nil, DrainedWarm: n, ImagePath: s.Pool.ImagePath()}
	if err != nil {
		resp.Error = err.Error()
		writeJSONStatus(w, http.StatusOK, resp) // partial drain still reported
		return
	}
	s.Log.Info("warm pool drained", "drained", n)
	writeJSONStatus(w, http.StatusOK, resp)
}

func (s *LocalServer) handleReload(w http.ResponseWriter, r *http.Request) {
	if s.Pool == nil {
		writeAgentErr(w, http.StatusServiceUnavailable, "pool not configured")
		return
	}
	var req api.PoolReloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && r.ContentLength != 0 {
		writeAgentErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	// Default drain on reload when image changes.
	drain := req.DrainWarm
	if req.ImagePath != "" && !req.DrainWarm {
		// Explicit false only if caller sent drain_warm:false; default true when image set.
		// Decode zero-value is false; treat image update as drain unless drain_warm explicitly present is hard without custom decode.
		// MVP: drain when ImagePath non-empty OR DrainWarm true.
		drain = true
	}
	n, err := s.Pool.ReloadImage(r.Context(), req.ImagePath, drain || req.DrainWarm)
	resp := api.PoolReloadResponse{
		OK:          err == nil,
		DrainedWarm: n,
		ImagePath:   s.Pool.ImagePath(),
	}
	if err != nil {
		resp.Error = err.Error()
	} else {
		s.Log.Info("pool reloaded", "image_path", resp.ImagePath, "drained", n)
	}
	writeJSONStatus(w, http.StatusOK, resp)
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAgentErr(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, api.ErrorBody{OK: false, Error: msg})
}
