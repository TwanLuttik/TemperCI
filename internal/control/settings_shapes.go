package control

import (
	"net/http"
	"os"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

func (s *Server) handleSettingsShapes(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "dashboard not configured")
		return
	}
	path := s.dash.agentConfigPath()
	shapes := defaultUIShapes()
	if cfg, err := config.LoadAgentFile(path); err == nil {
		shapes = cfg.EffectiveShapes()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"agent_path": path,
		"shapes":     shapes,
	})
}

type settingsShapesSaveRequest struct {
	Shapes  []config.VMShapeConfig `json:"shapes"`
	Restart bool                   `json:"restart"`
}

func (s *Server) handleSettingsShapesSave(w http.ResponseWriter, r *http.Request, _ *uiPrincipal) {
	if s.dash == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "dashboard not configured")
		return
	}
	var req settingsShapesSaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid json")
		return
	}
	path := s.dash.agentConfigPath()
	if _, err := os.Stat(path); err != nil {
		writeAPIError(w, http.StatusNotFound, "agent.toml not found at "+path)
		return
	}
	if err := config.WriteAgentShapes(path, req.Shapes); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp := map[string]any{
		"ok":         true,
		"agent_path": path,
		"restart":    req.Restart,
		"note":       "Shapes written to agent.toml. Restart the agent to apply the warm pool (empty list = no warm VMs).",
	}
	if req.Restart {
		resp["reconnect"] = true
		writeJSON(w, http.StatusAccepted, resp)
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = s.runHostctl("restart", "agent")
		}()
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func defaultUIShapes() []config.VMShapeConfig {
	return []config.VMShapeConfig{{
		Label:     "temperci-4vcpu-ubuntu-2404",
		VCPU:      4,
		MemoryMiB: 8192,
		MinReady:  1,
	}}
}
