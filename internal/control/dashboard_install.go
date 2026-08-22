package control

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

const (
	defaultAgentBinary = "/usr/local/bin/temperci-agent"
	defaultAgentUnitA  = "/etc/systemd/system/temperci-agent.service"
	defaultAgentUnitB  = "/lib/systemd/system/temperci-agent.service"
	defaultAgentUnitC  = "/usr/lib/systemd/system/temperci-agent.service"
	stagedAgentBinary  = "/var/lib/temperci/bin/temperci-agent"
)

type agentInstallProbe struct {
	Installed   bool
	Installable bool
	BinaryOK    bool
	UnitOK      bool
	Src         string
	Hint        string
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

func (s *Server) probeAgentInstall() agentInstallProbe {
	p := agentInstallProbe{}
	p.BinaryOK = fileExists(defaultAgentBinary)
	p.UnitOK = fileExists(defaultAgentUnitA) || fileExists(defaultAgentUnitB) || fileExists(defaultAgentUnitC)
	registered := s != nil && s.agents != nil && s.agents.Len() > 0
	p.Installed = (p.BinaryOK && p.UnitOK) || registered
	p.Src = s.resolveAgentSrc()
	p.Installable = s.hostctlAvailable() && p.Src != ""
	switch {
	case p.Installed:
		p.Hint = ""
	case !s.hostctlAvailable():
		p.Hint = "Install temperci-hostctl to install the agent from the dashboard, or run: sudo temperci-hostctl install agent"
	case p.Src == "":
		p.Hint = "Place temperci-agent next to temperci-control (or in /usr/local/bin or /var/lib/temperci/bin), then click Install."
	default:
		p.Hint = "Install the host agent unit and start temperci-agent.service."
	}
	return p
}

func (s *Server) resolveAgentSrc() string {
	cands := []string{defaultAgentBinary, stagedAgentBinary}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), "temperci-agent"))
	}
	if s != nil {
		cands = append(cands, filepath.Join(filepath.Dir(s.hostctlPath()), "temperci-agent"))
	}
	seen := map[string]bool{}
	for _, c := range cands {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		if fileExists(c) {
			return c
		}
	}
	return ""
}

func controlURLFromListen(addr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil || port == "" {
		return "http://127.0.0.1:8080"
	}
	_ = host
	return "http://127.0.0.1:" + port
}

func (s *Server) ensureAgentConfig() error {
	if s.dash == nil || s.dash.Config == nil {
		return fmt.Errorf("dashboard not configured")
	}
	path := s.dash.agentConfigPath()
	token := strings.TrimSpace(s.dash.Config.AgentToken)
	dataDir := strings.TrimSpace(s.dash.Config.DataDir)
	_, err := config.EnsureAgentTOML(path, controlURLFromListen(s.dash.Config.ListenAddr), token, dataDir)
	return err
}

func applyAgentInstall(agent map[string]any, p agentInstallProbe) {
	agent["installed"] = p.Installed
	agent["installable"] = p.Installable
	agent["install_hint"] = p.Hint
	agent["binary"] = p.BinaryOK
	if !p.Installed {
		agent["status"] = "not_installed"
		agent["ready"] = false
		if detail, _ := agent["detail"].(string); detail == "" || strings.Contains(detail, "no agent registered") {
			agent["detail"] = "not installed"
			if p.Hint != "" {
				agent["detail"] = "not installed · " + p.Hint
			}
		}
	}
}

func (s *Server) handleSystemInstall(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	var req struct {
		Target string `json:"target"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req)
	if req.Target == "" {
		req.Target = "agent"
	}
	if req.Target != "agent" {
		writeAPIError(w, http.StatusBadRequest, "target must be agent")
		return
	}
	probe := s.probeAgentInstall()
	if probe.Installed {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"already":   true,
			"installed": true,
			"note":      "agent is already installed",
		})
		return
	}
	if !s.hostctlAvailable() {
		writeAPIError(w, http.StatusServiceUnavailable, probe.Hint)
		return
	}
	if probe.Src == "" {
		writeAPIError(w, http.StatusConflict, probe.Hint)
		return
	}
	if err := s.ensureAgentConfig(); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "write agent.toml: "+err.Error())
		return
	}
	if err := s.runHostctl("install", "agent", probe.Src); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "install agent: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":        true,
		"installed": true,
		"src":       probe.Src,
	})
}
