package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/github"
	"github.com/TwanLuttik/TemperCI/internal/store"
	"github.com/TwanLuttik/TemperCI/internal/webui"
)

const sessionCookie = "temperci_session"

// DashboardConfig wires the operator UI into the control server.
type DashboardConfig struct {
	// Config is the live control config (may be updated after setup apply).
	Config *config.ControlConfig
	// ConfigPath is where setup apply writes TOML.
	ConfigPath string
	// AgentConfigPath is the host agent.toml (empty = sibling of ConfigPath).
	AgentConfigPath string
	Store           *store.Store
	// Ready is false until GitHub client + full agent APIs are available.
	FleetReady bool
	// Hub optional; if nil, server creates one.
	Hub *Hub
}

func (d *DashboardConfig) agentConfigPath() string {
	if d == nil {
		return "/etc/temperci/agent.toml"
	}
	if v := strings.TrimSpace(d.AgentConfigPath); v != "" {
		return v
	}
	return config.AgentPathBeside(d.ConfigPath)
}

func validateCacheListenAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return fmt.Errorf("cache_listen_addr must be host:port or empty to disable")
	}
	return nil
}

func (s *Server) writeAgentCacheListenAddr(addr string) error {
	if s.dash == nil {
		return fmt.Errorf("dashboard not configured")
	}
	path := s.dash.agentConfigPath()
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return config.PatchAgentTOMLString(path, "cache_listen_addr", strings.TrimSpace(addr))
}

func (s *Server) mountDashboard(d DashboardConfig) {
	if d.Config == nil {
		return
	}
	s.dash = &d
	s.mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)
	s.mux.HandleFunc("POST /api/v1/setup/apply", s.handleSetupApply)
	s.mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	s.mux.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	s.mux.HandleFunc("GET /api/v1/me", s.withUIAuth(s.handleMe, false))
	s.mux.HandleFunc("GET /api/v1/overview", s.withUIAuth(s.handleOverview, false))
	s.mux.HandleFunc("GET /api/v1/settings/config", s.withUIAuth(s.handleSettingsConfig, false))
	s.mux.HandleFunc("POST /api/v1/settings/config", s.withUIAuth(s.handleSettingsConfigSave, true))
	s.mux.HandleFunc("GET /api/v1/settings/shapes", s.withUIAuth(s.handleSettingsShapes, false))
	s.mux.HandleFunc("POST /api/v1/settings/shapes", s.withUIAuth(s.handleSettingsShapesSave, true))
	s.mux.HandleFunc("GET /api/v1/hosts", s.withUIAuth(s.handleHosts, false))
	s.mux.HandleFunc("GET /api/v1/jobs", s.withUIAuth(s.handleJobs, false))
	s.mux.HandleFunc("GET /api/v1/jobs/{id}", s.withUIAuth(s.handleJobDetail, false))
	s.mux.HandleFunc("GET /api/v1/users", s.withUIAuth(s.handleListUsers, true))
	s.mux.HandleFunc("POST /api/v1/users", s.withUIAuth(s.handleCreateUser, true))
	s.mux.HandleFunc("GET /api/v1/system/status", s.withUIAuth(s.handleSystemStatus, false))
	s.mux.HandleFunc("POST /api/v1/system/restart", s.withUIAuth(s.handleSystemRestart, true))
	s.mux.HandleFunc("POST /api/v1/system/install", s.withUIAuth(s.handleSystemInstall, true))
	s.mux.HandleFunc("GET /api/v1/ws", s.handleDashboardWS)
	s.mux.HandleFunc("GET /api/v1/vms", s.withUIAuth(s.handleVMs, false))
	s.mux.HandleFunc("GET /api/v1/cache", s.withUIAuth(s.handleCache, false))
	s.mux.HandleFunc("POST /api/v1/cache/clear", s.withUIAuth(s.handleCacheClear, true))
	// Vite SPA (embedded dist/). More specific /api and /v1 routes take precedence.
	s.mux.Handle("/", webui.SPAHandler())
}

func (s *Server) handleDashboardWS(w http.ResponseWriter, r *http.Request) {
	// Same auth as UI: open mode or valid session cookie.
	if _, err := s.resolvePrincipal(r); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.hub == nil {
		http.Error(w, "websocket unavailable", http.StatusServiceUnavailable)
		return
	}
	snap := s.BuildSnapshot()
	if snap.Overview != nil {
		snap.Overview["ws_clients"] = s.hub.ClientCount() + 1
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		raw = []byte(`{"type":"hello"}`)
	}
	s.hub.ServeWS(w, r, raw)
}

func (s *Server) handleVMs(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	snap := s.BuildSnapshot()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":  true,
		"vms": snap.VMs,
	})
}

type uiPrincipal struct {
	User  *store.User
	Admin bool
	Open  bool
}

func (s *Server) withUIAuth(next func(http.ResponseWriter, *http.Request, *uiPrincipal), adminOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := s.resolvePrincipal(r)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if p == nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if adminOnly && !p.Admin {
			writeAPIError(w, http.StatusForbidden, "admin required")
			return
		}
		next(w, r, p)
	}
}

func (s *Server) resolvePrincipal(r *http.Request) (*uiPrincipal, error) {
	if s.dash == nil || s.dash.Config == nil {
		return nil, fmt.Errorf("no dashboard")
	}
	cfg := s.dash.Config
	if cfg.NeedsSetup() {
		// Setup APIs handle their own auth; for fleet APIs during setup allow open for status.
		return &uiPrincipal{Open: true, Admin: true}, nil
	}
	if cfg.AuthMode == "open" {
		return &uiPrincipal{Open: true, Admin: true}, nil
	}
	if s.dash.Store == nil {
		return nil, fmt.Errorf("no store")
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, fmt.Errorf("no session")
	}
	u, err := s.dash.Store.SessionUser(c.Value)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, fmt.Errorf("invalid session")
	}
	return &uiPrincipal{User: u, Admin: u.Role == store.RoleAdmin}, nil
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.setupSnapshot())
}

type setupApplyRequest struct {
	AuthMode            string `json:"auth_mode"`
	AdminEmail          string `json:"admin_email"`
	AdminPassword       string `json:"admin_password"`
	GitHubAppID         int64  `json:"github_app_id"`
	GitHubOrg           string `json:"github_org"`
	GitHubWebhookSecret string `json:"github_webhook_secret"`
	// GitHubAppPrivateKeyPEM is the raw PEM body (not path).
	GitHubAppPrivateKeyPEM string `json:"github_app_private_key_pem"`
	AgentToken             string `json:"agent_token"`
	ListenAddr             string `json:"listen_addr"`
	CacheListenAddr        string `json:"cache_listen_addr"`
	Restart                bool   `json:"restart"`
	// Draft writes GitHub/auth fields without completing setup or restarting.
	// Used when the operator clicks Continue mid-wizard.
	Draft bool `json:"draft"`
}

// handleSetupDraft persists wizard fields without flipping setup_completed or restarting.
func (s *Server) handleSetupDraft(w http.ResponseWriter, req setupApplyRequest) {
	cur := *s.dash.Config
	if req.GitHubAppID != 0 {
		cur.GitHubAppID = req.GitHubAppID
	}
	if org := strings.TrimSpace(req.GitHubOrg); org != "" {
		cur.GitHubOrg = org
	}
	if sec := strings.TrimSpace(req.GitHubWebhookSecret); sec != "" {
		cur.GitHubWebhookSecret = sec
	}
	if mode := strings.ToLower(strings.TrimSpace(req.AuthMode)); mode == "open" || mode == "password" {
		cur.AuthMode = mode
	}
	if listen := strings.TrimSpace(req.ListenAddr); listen != "" {
		cur.ListenAddr = listen
	}
	if tok := strings.TrimSpace(req.AgentToken); tok != "" {
		cur.AgentToken = tok
		s.agentToken = tok
	}
	pemPath := cur.GitHubAppPrivateKeyPath
	if pemPath == "" {
		pemPath = "/etc/temperci/github-app.pem"
	}
	if pem := strings.TrimSpace(req.GitHubAppPrivateKeyPEM); pem != "" {
		if !strings.Contains(pem, "PRIVATE KEY") {
			writeAPIError(w, http.StatusBadRequest, "invalid private key pem")
			return
		}
		if err := os.MkdirAll(filepath.Dir(pemPath), 0o755); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "mkdir pem: "+err.Error())
			return
		}
		if err := os.WriteFile(pemPath, []byte(pem+"\n"), 0o600); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "write pem: "+err.Error())
			return
		}
		cur.GitHubAppPrivateKeyPath = pemPath
	}
	cfgPath := s.dash.ConfigPath
	if cfgPath == "" {
		cfgPath = "/etc/temperci/control.toml"
	}
	cur.SetupCompleted = false
	if err := cur.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.WriteControlFile(cfgPath, &cur); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "write config: "+err.Error())
		return
	}
	if strings.TrimSpace(req.CacheListenAddr) != "" {
		if err := s.writeAgentCacheListenAddr(req.CacheListenAddr); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "write agent cache_listen_addr: "+err.Error())
			return
		}
	}
	*s.dash.Config = cur
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"draft": true,
	})
}

func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request) {
	if s.dash == nil || s.dash.Config == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "dashboard not configured")
		return
	}
	// Only allow apply when setup not completed, or already admin in password/open mode.
	if !s.dash.Config.NeedsSetup() {
		p, err := s.resolvePrincipal(r)
		if err != nil || p == nil || !p.Admin {
			writeAPIError(w, http.StatusForbidden, "setup already completed")
			return
		}
	}

	var req setupApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if req.Draft {
		s.handleSetupDraft(w, req)
		return
	}
	authMode := strings.ToLower(strings.TrimSpace(req.AuthMode))
	if authMode == "" {
		authMode = "password"
	}
	if authMode != "open" && authMode != "password" {
		writeAPIError(w, http.StatusBadRequest, "auth_mode must be open or password")
		return
	}

	cur := *s.dash.Config
	reentry := !cur.NeedsSetup()
	if req.GitHubAppID == 0 {
		req.GitHubAppID = cur.GitHubAppID
	}
	if strings.TrimSpace(req.GitHubOrg) == "" {
		req.GitHubOrg = cur.GitHubOrg
	}
	if strings.TrimSpace(req.GitHubWebhookSecret) == "" {
		req.GitHubWebhookSecret = cur.GitHubWebhookSecret
	}
	if !reentry {
		if req.GitHubAppID == 0 || strings.TrimSpace(req.GitHubOrg) == "" || strings.TrimSpace(req.GitHubWebhookSecret) == "" {
			writeAPIError(w, http.StatusBadRequest, "github_app_id, github_org, github_webhook_secret required")
			return
		}
		if strings.TrimSpace(req.GitHubAppPrivateKeyPEM) == "" {
			writeAPIError(w, http.StatusBadRequest, "github_app_private_key_pem required")
			return
		}
	}
	if pem := strings.TrimSpace(req.GitHubAppPrivateKeyPEM); pem != "" && !strings.Contains(pem, "PRIVATE KEY") {
		writeAPIError(w, http.StatusBadRequest, "invalid private key pem")
		return
	}
	agentToken := strings.TrimSpace(req.AgentToken)
	if agentToken == "" {
		agentToken = strings.TrimSpace(cur.AgentToken)
	}
	if agentToken == "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		agentToken = hex.EncodeToString(b)
	}
	listen := strings.TrimSpace(req.ListenAddr)
	if listen == "" {
		listen = cur.ListenAddr
	}
	if listen == "" {
		listen = "0.0.0.0:8080"
	}

	cfgPath := s.dash.ConfigPath
	if cfgPath == "" {
		cfgPath = "/etc/temperci/control.toml"
	}
	pemPath := cur.GitHubAppPrivateKeyPath
	if pemPath == "" {
		pemPath = "/etc/temperci/github-app.pem"
	}
	if pem := strings.TrimSpace(req.GitHubAppPrivateKeyPEM); pem != "" {
		if err := os.MkdirAll(filepath.Dir(pemPath), 0o755); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "mkdir pem: "+err.Error())
			return
		}
		if err := os.WriteFile(pemPath, []byte(pem+"\n"), 0o600); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "write pem: "+err.Error())
			return
		}
	}

	newCfg := cur
	newCfg.ListenAddr = listen
	newCfg.GitHubAppID = req.GitHubAppID
	newCfg.GitHubOrg = strings.TrimSpace(req.GitHubOrg)
	newCfg.GitHubWebhookSecret = req.GitHubWebhookSecret
	newCfg.GitHubAppPrivateKeyPath = pemPath
	newCfg.AgentToken = agentToken
	newCfg.AuthMode = authMode
	newCfg.SetupCompleted = true
	if newCfg.SQLitePath == "" {
		newCfg.SQLitePath = filepath.Join(newCfg.DataDir, "control.db")
	}
	if err := newCfg.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateCacheListenAddr(req.CacheListenAddr); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.WriteControlFile(cfgPath, &newCfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "write config: "+err.Error())
		return
	}
	if err := s.writeAgentCacheListenAddr(req.CacheListenAddr); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "write agent cache_listen_addr: "+err.Error())
		return
	}

	if s.dash.Store != nil {
		if authMode == "password" {
			n, _ := s.dash.Store.UserCount()
			if n == 0 {
				if strings.TrimSpace(req.AdminEmail) == "" || strings.TrimSpace(req.AdminPassword) == "" {
					writeAPIError(w, http.StatusBadRequest, "admin_email and admin_password required for password mode")
					return
				}
				if _, err := s.dash.Store.CreateUser(req.AdminEmail, req.AdminPassword, store.RoleAdmin, false); err != nil {
					writeAPIError(w, http.StatusInternalServerError, "create admin: "+err.Error())
					return
				}
			}
		}
		_ = s.dash.Store.SetSetupCompleted(true)
	}

	*s.dash.Config = newCfg
	s.agentToken = newCfg.AgentToken

	resp := map[string]any{
		"ok":          true,
		"agent_token": agentToken,
		"config_path": cfgPath,
		"reconnect":   false,
		"restart":     false,
	}
	if req.Restart {
		resp["reconnect"] = true
		resp["restart"] = true
		writeJSON(w, http.StatusAccepted, resp)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = s.runHostctl("restart", "all")
		}()
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.dash == nil || s.dash.Config.AuthMode != "password" || s.dash.Store == nil {
		writeAPIError(w, http.StatusBadRequest, "password auth not enabled")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := s.dash.Store.Authenticate(req.Email, req.Password)
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	tok, exp, err := s.dash.Store.CreateSession(u.ID, 24*time.Hour)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  exp,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"email": u.Email,
		"role":  u.Role,
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && s.dash != nil && s.dash.Store != nil {
		_ = s.dash.Store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, p *uiPrincipal) {
	out := map[string]any{
		"ok":        true,
		"auth_mode": s.dash.Config.AuthMode,
		"admin":     p.Admin,
		"open":      p.Open,
	}
	if p.User != nil {
		out["email"] = p.User.Email
		out["role"] = p.User.Role
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	counts := s.store.CountByStatus()
	agents := s.agents.List()
	var warm, busy int
	for _, a := range agents {
		warm += a.Warm
		busy += a.Busy
	}
	recent := s.store.ListRecent(100)
	p50, p95 := recentRunPercentiles(recent)
	cacheHits, cacheMisses, _, _ := recentCacheTotals(recent)
	var cacheBytes, cacheMax int64
	for _, a := range agents {
		if a.Cache != nil {
			cacheBytes += a.Cache.Bytes
			cacheMax += a.Cache.MaxBytes
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                 true,
		"fleet_ready":        s.dash != nil && s.dash.FleetReady,
		"setup_completed":    s.dash != nil && s.dash.Config != nil && s.dash.Config.SetupCompleted,
		"org":                s.dash.Config.GitHubOrg,
		"agents_registered":  len(agents),
		"warm":               warm,
		"busy":               busy,
		"jobs_pending":       s.store.PendingLen(),
		"jobs_minted":        counts.Minted,
		"jobs_assigned":      counts.Assigned,
		"jobs_started":       counts.Started,
		"jobs_finished":      counts.Finished,
		"jobs_failed":        counts.Failed,
		"hostctl_configured": s.hostctlAvailable(),
		"run_p50_ms":         p50,
		"run_p95_ms":         p95,
		"cache_hits":         cacheHits,
		"cache_misses":       cacheMisses,
		"cache_bytes":        cacheBytes,
		"cache_max_bytes":    cacheMax,
	})
}

// handleSettingsConfig returns a safe, redacted view of control-plane config
// so operators can verify GitHub App / webhook / paths are set without leaking secrets.
func (s *Server) handleSettingsConfig(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash == nil || s.dash.Config == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "dashboard not configured")
		return
	}
	cfg := s.dash.Config
	pemPath := cfg.GitHubAppPrivateKeyPath
	pemExists := false
	pemLooksKey := false
	if pemPath != "" {
		if raw, err := os.ReadFile(pemPath); err == nil {
			pemExists = true
			pemLooksKey = strings.Contains(string(raw), "PRIVATE KEY")
		}
	}
	hostctlPath := cfg.HostctlPath
	if hostctlPath == "" {
		hostctlPath = "/usr/local/bin/temperci-hostctl"
	}
	hostctlExists := false
	if _, err := os.Stat(hostctlPath); err == nil {
		hostctlExists = true
	} else if p, err := exec.LookPath("temperci-hostctl"); err == nil {
		hostctlPath = p
		hostctlExists = true
	}

	type field struct {
		Key         string   `json:"key"`
		Label       string   `json:"label"`
		Group       string   `json:"group"`
		Value       string   `json:"value,omitempty"`
		Configured  bool     `json:"configured"`
		Secret      bool     `json:"secret"`
		Editable    bool     `json:"editable"`
		InputType   string   `json:"input_type,omitempty"` // text | number | password | select | textarea | readonly
		Options     []string `json:"options,omitempty"`
		Status      string   `json:"status"` // ok | missing | warn
		Description string   `json:"description,omitempty"`
	}

	secretStatus := func(set bool) (configured bool, status string) {
		if set {
			return true, "ok"
		}
		return false, "missing"
	}
	valueStatus := func(set bool) string {
		if set {
			return "ok"
		}
		return "missing"
	}

	whSet, whSt := secretStatus(strings.TrimSpace(cfg.GitHubWebhookSecret) != "")
	tokSet, tokSt := secretStatus(strings.TrimSpace(cfg.AgentToken) != "")
	appIDSet := cfg.GitHubAppID != 0
	orgSet := strings.TrimSpace(cfg.GitHubOrg) != ""

	cacheAddr, cacheOK := config.ReadAgentTOMLString(s.dash.agentConfigPath(), "cache_listen_addr")
	cacheStatus := "ok"
	cacheDesc := "Agent cache gateway bind, typically 127.0.0.1:8743. Empty disables intercept. Enabling this NAT-redirects all guest HTTPS; non-cache hosts are spliced to GitHub. Restart the agent (and drain warm VMs) after changing."
	if !cacheOK {
		cacheStatus = "warn"
		cacheDesc = "Could not read " + s.dash.agentConfigPath() + ". Saving this field writes cache_listen_addr there."
	}

	pemStatus := "missing"
	if pemExists && pemLooksKey {
		pemStatus = "ok"
	} else if pemExists {
		pemStatus = "warn"
	}

	fields := []field{
		{
			Key: "listen_addr", Label: "Listen address", Group: "Network",
			Value: cfg.ListenAddr, Configured: cfg.ListenAddr != "", Status: valueStatus(cfg.ListenAddr != ""),
			Editable: true, InputType: "text",
			Description: "HTTP bind for webhooks, agent API, and dashboard",
		},
		{
			Key: "github_app_id", Label: "GitHub App ID", Group: "GitHub App",
			Value: fmt.Sprintf("%d", cfg.GitHubAppID), Configured: appIDSet, Status: valueStatus(appIDSet),
			Editable: true, InputType: "number",
			Description: "Numeric App ID from the GitHub App settings page",
		},
		{
			Key: "github_org", Label: "GitHub organization", Group: "GitHub App",
			Value: cfg.GitHubOrg, Configured: orgSet, Status: valueStatus(orgSet),
			Editable: true, InputType: "text",
			Description: "Org where the App is installed and JIT runners are minted",
		},
		{
			Key: "github_webhook_secret", Label: "Webhook secret", Group: "GitHub App",
			Secret: true, Configured: whSet, Status: whSt,
			Value:    secretHint(cfg.GitHubWebhookSecret),
			Editable: true, InputType: "password",
			Description: "Leave blank to keep current. Must match the GitHub App webhook secret.",
		},
		{
			Key: "github_app_private_key_path", Label: "App private key path", Group: "GitHub App",
			Value: pemPath, Configured: pemExists && pemLooksKey, Status: pemStatus,
			Editable: true, InputType: "text",
			Description: "Path to the .pem on this host",
		},
		{
			Key: "github_app_private_key_pem", Label: "App private key (PEM)", Group: "GitHub App",
			Secret: true, Configured: pemExists && pemLooksKey, Status: pemStatus,
			Value:    map[bool]string{true: "present on disk", false: "missing or invalid"}[pemExists && pemLooksKey],
			Editable: true, InputType: "textarea",
			Description: "Paste a new PEM to replace the key file. Leave blank to keep the current file.",
		},
		{
			Key: "label_prefix", Label: "Label prefix", Group: "Scheduling",
			Value: cfg.LabelPrefix, Configured: cfg.LabelPrefix != "", Status: "ok",
			Editable: true, InputType: "text",
			Description: "Only jobs with runs-on labels under this prefix are handled",
		},
		{
			Key: "runner_group_id", Label: "Runner group ID", Group: "Scheduling",
			Value: fmt.Sprintf("%d", cfg.RunnerGroupID), Configured: true, Status: "ok",
			Editable: true, InputType: "number",
			Description: "GitHub org runner group for JIT registration (Default is usually 1)",
		},
		{
			Key: "agent_token", Label: "Agent token", Group: "Agents",
			Secret: true, Configured: tokSet, Status: tokSt,
			Value:    secretHint(cfg.AgentToken),
			Editable: true, InputType: "password",
			Description: "Leave blank to keep current. Must match agent.toml after change.",
		},
		{
			Key: "cache_listen_addr", Label: "Cache listen address", Group: "Cache",
			Value: cacheAddr, Configured: cacheOK, Status: cacheStatus,
			Editable: true, InputType: "text",
			Description: cacheDesc,
		},
		{
			Key: "auth_mode", Label: "Dashboard auth mode", Group: "Dashboard",
			Value: cfg.AuthMode, Configured: cfg.AuthMode != "", Status: "ok",
			Editable: true, InputType: "select", Options: []string{"open", "password"},
			Description: "open (no login) or password (local users)",
		},
		{
			Key: "setup_completed", Label: "Setup completed", Group: "Dashboard",
			Value: fmt.Sprintf("%v", cfg.SetupCompleted || !cfg.NeedsSetup()), Configured: !cfg.NeedsSetup(), Status: valueStatus(!cfg.NeedsSetup()),
			Editable: true, InputType: "select", Options: []string{"true", "false"},
			Description: "Mark first-run setup finished",
		},
		{
			Key: "config_path", Label: "Config file", Group: "Paths",
			Value: s.dash.ConfigPath, Configured: s.dash.ConfigPath != "", Status: valueStatus(s.dash.ConfigPath != ""),
			Editable: false, InputType: "readonly",
		},
		{
			Key: "sqlite_path", Label: "SQLite path", Group: "Paths",
			Value: cfg.SQLitePath, Configured: cfg.SQLitePath != "", Status: "ok",
			Editable: true, InputType: "text",
		},
		{
			Key: "data_dir", Label: "Data directory", Group: "Paths",
			Value: cfg.DataDir, Configured: cfg.DataDir != "", Status: "ok",
			Editable: true, InputType: "text",
		},
		{
			Key: "hostctl_path", Label: "hostctl binary", Group: "Paths",
			Value: hostctlPath, Configured: hostctlExists, Status: valueStatus(hostctlExists),
			Editable: true, InputType: "text",
			Description: "Used for restart from the dashboard",
		},
		{
			Key: "tls_enabled", Label: "TLS on control listener", Group: "Network",
			Value:      fmt.Sprintf("%v", cfg.TLSCertFile != "" && cfg.TLSKeyFile != ""),
			Configured: true, Status: "ok", Editable: false, InputType: "readonly",
			Description: "Optional HTTPS — edit cert paths in control.toml on the host",
		},
	}

	missing := 0
	for _, f := range fields {
		if f.Status == "missing" {
			missing++
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":             true,
		"config_path":    s.dash.ConfigPath,
		"fleet_ready":    s.dash.FleetReady,
		"setup_required": cfg.NeedsSetup(),
		"missing_count":  missing,
		"fields":         fields,
	})
}

// secretHint returns a safe display string for a secret (set + length only).
func secretHint(secret string) string {
	s := strings.TrimSpace(secret)
	if s == "" {
		return "not set"
	}
	return fmt.Sprintf("set (%d chars)", len(s))
}

// settingsConfigSaveRequest is the body for POST /api/v1/settings/config.
// Empty secret fields mean "leave unchanged".
type settingsConfigSaveRequest struct {
	ListenAddr              string  `json:"listen_addr"`
	GitHubAppID             *int64  `json:"github_app_id"`
	GitHubOrg               string  `json:"github_org"`
	GitHubWebhookSecret     string  `json:"github_webhook_secret"`
	GitHubAppPrivateKeyPath string  `json:"github_app_private_key_path"`
	GitHubAppPrivateKeyPEM  string  `json:"github_app_private_key_pem"`
	LabelPrefix             string  `json:"label_prefix"`
	RunnerGroupID           *int64  `json:"runner_group_id"`
	AgentToken              string  `json:"agent_token"`
	AuthMode                string  `json:"auth_mode"`
	SetupCompleted          *bool   `json:"setup_completed"`
	SQLitePath              string  `json:"sqlite_path"`
	DataDir                 string  `json:"data_dir"`
	HostctlPath             string  `json:"hostctl_path"`
	CacheListenAddr         *string `json:"cache_listen_addr"`
	Restart                 bool    `json:"restart"`
}

func (s *Server) handleSettingsConfigSave(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash == nil || s.dash.Config == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "dashboard not configured")
		return
	}
	var req settingsConfigSaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}

	cfgPath := s.dash.ConfigPath
	if cfgPath == "" {
		cfgPath = "/etc/temperci/control.toml"
	}

	newCfg := *s.dash.Config

	if v := strings.TrimSpace(req.ListenAddr); v != "" {
		newCfg.ListenAddr = v
	}
	if req.GitHubAppID != nil {
		newCfg.GitHubAppID = *req.GitHubAppID
	}
	if v := strings.TrimSpace(req.GitHubOrg); v != "" {
		newCfg.GitHubOrg = v
	}
	if v := strings.TrimSpace(req.GitHubWebhookSecret); v != "" {
		newCfg.GitHubWebhookSecret = v
	}
	if v := strings.TrimSpace(req.GitHubAppPrivateKeyPath); v != "" {
		newCfg.GitHubAppPrivateKeyPath = v
	}
	if v := strings.TrimSpace(req.LabelPrefix); v != "" {
		newCfg.LabelPrefix = v
	}
	if req.RunnerGroupID != nil {
		newCfg.RunnerGroupID = *req.RunnerGroupID
	}
	if v := strings.TrimSpace(req.AgentToken); v != "" {
		newCfg.AgentToken = v
	}
	if v := strings.ToLower(strings.TrimSpace(req.AuthMode)); v != "" {
		if v != "open" && v != "password" {
			writeAPIError(w, http.StatusBadRequest, "auth_mode must be open or password")
			return
		}
		newCfg.AuthMode = v
	}
	if req.SetupCompleted != nil {
		newCfg.SetupCompleted = *req.SetupCompleted
	}
	if v := strings.TrimSpace(req.SQLitePath); v != "" {
		newCfg.SQLitePath = v
	}
	if v := strings.TrimSpace(req.DataDir); v != "" {
		newCfg.DataDir = v
	}
	if v := strings.TrimSpace(req.HostctlPath); v != "" {
		newCfg.HostctlPath = v
	}
	if req.CacheListenAddr != nil {
		if err := validateCacheListenAddr(*req.CacheListenAddr); err != nil {
			writeAPIError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	pemBody := strings.TrimSpace(req.GitHubAppPrivateKeyPEM)
	if pemBody != "" {
		if !strings.Contains(pemBody, "PRIVATE KEY") {
			writeAPIError(w, http.StatusBadRequest, "invalid private key pem")
			return
		}
		pemPath := newCfg.GitHubAppPrivateKeyPath
		if pemPath == "" {
			pemPath = "/etc/temperci/github-app.pem"
			newCfg.GitHubAppPrivateKeyPath = pemPath
		}
		if err := os.MkdirAll(filepath.Dir(pemPath), 0o755); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "mkdir pem: "+err.Error())
			return
		}
		if err := os.WriteFile(pemPath, []byte(pemBody+"\n"), 0o600); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "write pem: "+err.Error())
			return
		}
	}

	// After editing real credentials, force setup completed.
	if newCfg.GitHubAppID != 0 && newCfg.GitHubOrg != "" && newCfg.AgentToken != "" && newCfg.GitHubWebhookSecret != "" {
		newCfg.SetupCompleted = true
	}

	if err := newCfg.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.WriteControlFile(cfgPath, &newCfg); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "write config: "+err.Error())
		return
	}
	if req.CacheListenAddr != nil {
		if err := config.PatchAgentTOMLString(s.dash.agentConfigPath(), "cache_listen_addr", strings.TrimSpace(*req.CacheListenAddr)); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "write agent cache_listen_addr: "+err.Error())
			return
		}
	}

	*s.dash.Config = newCfg
	s.agentToken = newCfg.AgentToken
	s.webhookSecret = []byte(newCfg.GitHubWebhookSecret)

	resp := map[string]any{
		"ok":          true,
		"config_path": cfgPath,
		"restart":     false,
		"reconnect":   false,
		"note":        "Config written. Restart control to fully reload the GitHub client.",
	}
	if req.Restart {
		resp["restart"] = true
		resp["reconnect"] = true
		writeJSON(w, http.StatusAccepted, resp)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = s.runHostctl("restart", "all")
		}()
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":    true,
		"hosts": s.agents.List(),
	})
}

func (s *Server) handleCache(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	writeJSON(w, http.StatusOK, s.cacheSnapshot())
}

func (s *Server) cacheSnapshot() map[string]any {
	agents := s.agents.List()
	hosts := make([]api.CacheHost, 0, len(agents))
	var bytes, maxBytes int64
	var entries int
	var repos int
	for _, a := range agents {
		h := api.CacheHost{AgentID: a.AgentID, LastSeenAt: a.LastSeenAt}
		if a.Cache != nil {
			h.CacheUsage = *a.Cache
			if h.Repos != nil {
				h.Repos = append([]api.CacheRepoUsage(nil), h.Repos...)
			}
		}
		hosts = append(hosts, h)
		bytes += h.Bytes
		maxBytes += h.MaxBytes
		entries += h.Entries
		repos += len(h.Repos)
	}
	return map[string]any{
		"ok":        true,
		"bytes":     bytes,
		"max_bytes": maxBytes,
		"entries":   entries,
		"repos":     repos,
		"hosts":     hosts,
	}
}

func (s *Server) handleCacheClear(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	var req api.CacheClearRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := validateCacheRepo(req.Repo); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := cacheAction(req.Repo)
	var ids []string
	if strings.TrimSpace(req.AgentID) != "" {
		if s.agents.Get(req.AgentID) == nil {
			writeAPIError(w, http.StatusNotFound, "agent not registered")
			return
		}
		ids = []string{req.AgentID}
	} else {
		for _, a := range s.agents.List() {
			ids = append(ids, a.AgentID)
		}
	}
	if len(ids) == 0 {
		writeAPIError(w, http.StatusBadRequest, "no agents registered")
		return
	}
	n := s.cacheq.enqueue(ids, action, strings.TrimSpace(req.Repo))
	s.log.Info("cache clear queued", "action", action, "repo", req.Repo, "agents", n)
	s.PublishSnapshot()
	writeJSON(w, http.StatusOK, api.CacheClearResponse{OK: true, Queued: n})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	list := s.store.ListRecent(100)
	type jobRow struct {
		JobID           int64     `json:"job_id"`
		RunID           int64     `json:"run_id"`
		Org             string    `json:"org"`
		RepoFullName    string    `json:"repo_full_name"`
		Name            string    `json:"name,omitempty"`
		WorkflowName    string    `json:"workflow_name,omitempty"`
		Labels          []string  `json:"labels"`
		Status          string    `json:"status"`
		AssignedAgentID string    `json:"assigned_agent_id,omitempty"`
		VMID            string    `json:"vm_id,omitempty"`
		WarmBind        bool      `json:"warm_bind,omitempty"`
		Outcome         string    `json:"outcome,omitempty"`
		Error           string    `json:"error,omitempty"`
		CreatedAt       time.Time `json:"created_at"`
		StartedAt       time.Time `json:"started_at,omitempty"`
		FinishedAt      time.Time `json:"finished_at,omitempty"`
		QueueMS         int64     `json:"queue_ms,omitempty"`
		BindMS          int64     `json:"bind_ms,omitempty"`
		RunMS           int64     `json:"run_ms,omitempty"`
		TotalMS         int64     `json:"total_ms,omitempty"`
		CacheHits       int       `json:"cache_hits,omitempty"`
		CacheMisses     int       `json:"cache_misses,omitempty"`
	}
	now := time.Now().UTC()
	rows := make([]jobRow, 0, len(list))
	for _, a := range list {
		tm := timingsFromAssignment(a, now)
		rows = append(rows, jobRow{
			JobID:           a.JobID,
			RunID:           a.RunID,
			Org:             a.Org,
			RepoFullName:    a.RepoFullName,
			Name:            a.Name,
			WorkflowName:    a.WorkflowName,
			Labels:          a.Labels,
			Status:          string(a.Status),
			AssignedAgentID: a.AssignedAgentID,
			VMID:            a.VMID,
			WarmBind:        a.WarmBind,
			Outcome:         a.Outcome,
			Error:           a.Error,
			CreatedAt:       a.CreatedAt,
			StartedAt:       a.StartedAt,
			FinishedAt:      a.FinishedAt,
			QueueMS:         tm.QueueMS,
			BindMS:          tm.BindMS,
			RunMS:           tm.RunMS,
			TotalMS:         tm.TotalMS,
			CacheHits:       a.CacheHits,
			CacheMisses:     a.CacheMisses,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "jobs": rows})
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	a := s.store.Get(id)
	if a == nil {
		writeAPIError(w, http.StatusNotFound, "job not found")
		return
	}
	var logs *store.JobLog
	if db := s.jobDB(); db != nil {
		logs, _ = db.GetJobLog(id)
	}
	if logs == nil {
		logs = &store.JobLog{JobID: id, Events: []store.JobEvent{}}
	}
	s.ensureWorkflowLog(r, a, logs)
	tm := timingsFromAssignment(a, time.Now().UTC())
	name := a.Name
	steps := []github.WorkflowJobStep{}
	if meta := s.ensureJobMeta(r, a); meta != nil {
		if meta.Name != "" {
			name = meta.Name
		}
		if meta.Steps != nil {
			steps = meta.Steps
		}
	}
	if a2 := s.store.Get(a.JobID); a2 != nil {
		if a2.WorkflowName != "" {
			a.WorkflowName = a2.WorkflowName
		}
		if a2.Name != "" && name == "" {
			name = a2.Name
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true,
		"job": map[string]any{
			"job_id":            a.JobID,
			"run_id":            a.RunID,
			"org":               a.Org,
			"repo_full_name":    a.RepoFullName,
			"labels":            a.Labels,
			"status":            string(a.Status),
			"assigned_agent_id": a.AssignedAgentID,
			"vm_id":             a.VMID,
			"warm_bind":         a.WarmBind,
			"outcome":           a.Outcome,
			"error":             a.Error,
			"created_at":        a.CreatedAt,
			"assigned_at":       a.AssignedAt,
			"started_at":        a.StartedAt,
			"finished_at":       a.FinishedAt,
			"runner_name":       a.RunnerName,
			"runner_id":         a.RunnerID,
			"queue_ms":          tm.QueueMS,
			"bind_ms":           tm.BindMS,
			"run_ms":            tm.RunMS,
			"total_ms":          tm.TotalMS,
			"cache_hits":        a.CacheHits,
			"cache_misses":      a.CacheMisses,
			"cache_bytes_in":    a.CacheBytesIn,
			"cache_bytes_out":   a.CacheBytesOut,
			"name":              name,
			"workflow_name":     a.WorkflowName,
			"steps":             steps,
		},
		"logs": logs,
	})
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash.Store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "store unavailable")
		return
	}
	users, err := s.dash.Store.ListUsers()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash.Config.AuthMode != "password" || s.dash.Store == nil {
		writeAPIError(w, http.StatusBadRequest, "password mode required")
		return
	}
	var req struct {
		Email              string `json:"email"`
		Password           string `json:"password"`
		Role               string `json:"role"`
		MustChangePassword bool   `json:"must_change_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	u, err := s.dash.Store.CreateUser(req.Email, req.Password, req.Role, req.MustChangePassword)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	u.PasswordHash = ""
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "user": u})
}

func (s *Server) handleSystemRestart(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	var req struct {
		Target string `json:"target"` // control | agent | all
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	if req.Target == "" {
		req.Target = "all"
	}
	if req.Target != "control" && req.Target != "agent" && req.Target != "all" {
		writeAPIError(w, http.StatusBadRequest, "target must be control, agent, or all")
		return
	}
	if !s.hostctlAvailable() {
		writeAPIError(w, http.StatusServiceUnavailable, "hostctl not available; run: systemctl restart temperci-control temperci-agent")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":        true,
		"reconnect": req.Target == "control" || req.Target == "all",
		"target":    req.Target,
	})
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = s.runHostctl("restart", req.Target)
	}()
}

// handleSystemStatus reports control + agent readiness for the dashboard restart UI.
func (s *Server) handleSystemStatus(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	hostctl := s.hostctlAvailable()
	controlUnit := "unknown"
	agentUnit := "unknown"
	if hostctl {
		controlUnit = s.hostctlUnitState("control")
		agentUnit = s.hostctlUnitState("agent")
	}

	agents := s.agents.List()
	var agentIDs []string
	var lastSeen string
	for _, a := range agents {
		agentIDs = append(agentIDs, a.AgentID)
		if !a.LastSeenAt.IsZero() {
			ts := a.LastSeenAt.UTC().Format(time.RFC3339)
			if ts > lastSeen {
				lastSeen = ts
			}
		}
	}

	out := buildSystemStatus(hostctl, controlUnit, agentUnit, agentIDs, lastSeen)
	out["time"] = time.Now().UTC().Format(time.RFC3339)
	if agent, ok := out["agent"].(map[string]any); ok {
		applyAgentInstall(agent, s.probeAgentInstall())
		if ctrl, ok := out["control"].(map[string]any); ok {
			cs, _ := ctrl["status"].(string)
			as, _ := agent["status"].(string)
			out["overall"] = overallServiceStatus(cs, as)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) hostctlAvailable() bool {
	if s.dash == nil || s.dash.Config == nil {
		return false
	}
	path := s.dash.Config.HostctlPath
	if path == "" {
		path = "temperci-hostctl"
	}
	if _, err := os.Stat(path); err == nil {
		return true
	}
	if p, err := exec.LookPath("temperci-hostctl"); err == nil {
		s.dash.Config.HostctlPath = p
		return true
	}
	return false
}

func (s *Server) hostctlPath() string {
	if s.dash != nil && s.dash.Config != nil && s.dash.Config.HostctlPath != "" {
		return s.dash.Config.HostctlPath
	}
	return "temperci-hostctl"
}

// hostctlUnitState runs `temperci-hostctl status <target>` and returns systemd is-active text.
func (s *Server) hostctlUnitState(target string) string {
	cmd := exec.Command(s.hostctlPath(), "status", target)
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	// systemctl is-active prints active/inactive/failed even on non-zero exit.
	if text == "" {
		if err != nil {
			return "unknown"
		}
		return "unknown"
	}
	// First line only (may be multi-unit for "all"; we call per target).
	line := strings.Split(text, "\n")[0]
	line = strings.TrimSpace(line)
	switch line {
	case "active", "inactive", "failed", "activating", "deactivating", "reloading":
		return line
	default:
		// Some hosts may include service name; take last token.
		parts := strings.Fields(line)
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			switch last {
			case "active", "inactive", "failed", "activating", "deactivating":
				return last
			}
		}
		if err != nil {
			return "inactive"
		}
		return line
	}
}

func (s *Server) runHostctl(action, target string, extra ...string) error {
	path := s.hostctlPath()
	args := append([]string{action, target}, extra...)
	cmd := exec.Command(path, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		s.log.Error("hostctl failed", "err", err, "out", string(out))
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	s.log.Info("hostctl ok", "action", action, "target", target, "out", strings.TrimSpace(string(out)))
	return nil
}
