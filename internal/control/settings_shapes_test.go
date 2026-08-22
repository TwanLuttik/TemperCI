package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestSettingsShapes_GetDefaultAndSave(t *testing.T) {
	dir := t.TempDir()
	agentPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"tok\"\nimage_path = \"/img\"\nvmm_backend = \"fake\"\nvcpu = 4\nmemory_mib = 8192\nmin_ready = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerConfig{
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config:          &config.ControlConfig{AuthMode: "open", SetupCompleted: true, GitHubOrg: "acme"},
			ConfigPath:      filepath.Join(dir, "control.toml"),
			AgentConfigPath: agentPath,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/shapes", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Shapes []config.VMShapeConfig `json:"shapes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Shapes) != 1 || got.Shapes[0].VCPU != 4 || got.Shapes[0].MemoryMiB != 8192 {
		t.Fatalf("default shapes = %+v", got.Shapes)
	}

	body := `{"shapes":[{"vcpu":4,"memory_mib":8192,"min_ready":1},{"vcpu":2,"memory_mib":4096,"min_ready":0}]}`
	req = httptest.NewRequest(http.MethodPost, "/api/v1/settings/shapes", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST %d %s", rr.Code, rr.Body.String())
	}
	cfg, err := config.LoadAgentFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Shapes) != 2 || cfg.Shapes[1].VCPU != 2 {
		t.Fatalf("saved shapes = %+v", cfg.Shapes)
	}
}
