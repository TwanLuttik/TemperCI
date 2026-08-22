package control

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

func settingsTestServer(t *testing.T, dir string) *Server {
	t.Helper()
	controlPath := filepath.Join(dir, "control.toml")
	agentPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"tok\"\ncache_listen_addr = \"127.0.0.1:8743\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controlPath, []byte("listen_addr = \"127.0.0.1:8080\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{RunnerGroupID: 1})
	return NewServer(ServerConfig{
		Handler:       h,
		WebhookSecret: "super-secret",
		AgentToken:    "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:                "open",
				SetupCompleted:          true,
				GitHubOrg:               "acme",
				ListenAddr:              "127.0.0.1:8080",
				GitHubAppID:             1,
				GitHubWebhookSecret:     "whsec",
				GitHubAppPrivateKeyPath: filepath.Join(dir, "github-app.pem"),
				AgentToken:              "tok",
			},
			ConfigPath: controlPath,
		},
	})
}

func TestSettingsConfig_IncludesCacheListenAddr(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings/config", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK     bool `json:"ok"`
		Fields []struct {
			Key   string `json:"key"`
			Value string `json:"value"`
			Group string `json:"group"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var found *struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Group string `json:"group"`
	}
	for i := range resp.Fields {
		if resp.Fields[i].Key == "cache_listen_addr" {
			found = &resp.Fields[i]
			break
		}
	}
	if found == nil {
		t.Fatal("cache_listen_addr missing from settings fields")
	}
	if found.Value != "127.0.0.1:8743" {
		t.Fatalf("value=%q", found.Value)
	}
	if found.Group != "Cache" {
		t.Fatalf("group=%q", found.Group)
	}
}

func TestSettingsConfigSave_WritesAgentCacheListenAddr(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)
	agentPath := filepath.Join(dir, "agent.toml")

	body := `{"cache_listen_addr":"","setup_completed":true}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/settings/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	raw, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `cache_listen_addr = ""`) {
		t.Fatalf("agent.toml not updated:\n%s", raw)
	}
	if strings.Contains(string(raw), "127.0.0.1:8743") {
		t.Fatalf("old addr still present:\n%s", raw)
	}
}

func TestSetupApply_WritesAgentCacheListenAddr(t *testing.T) {
	dir := t.TempDir()
	controlPath := filepath.Join(dir, "control.toml")
	agentPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"old\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(&mockMinter{}, NewAssignmentStore(), HandlerConfig{RunnerGroupID: 1})
	srv := NewServer(ServerConfig{
		Handler:       h,
		WebhookSecret: "wh",
		AgentToken:    "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:                "open",
				SetupCompleted:          false,
				ListenAddr:              "127.0.0.1:8080",
				DataDir:                 dir,
				GitHubAppPrivateKeyPath: filepath.Join(dir, "github-app.pem"),
			},
			ConfigPath: controlPath,
		},
	})

	payload := map[string]any{
		"auth_mode":                  "open",
		"github_app_id":              99,
		"github_org":                 "acme",
		"github_webhook_secret":      "whsec",
		"github_app_private_key_pem": "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----",
		"agent_token":                "tok",
		"listen_addr":                "127.0.0.1:8080",
		"cache_listen_addr":          "",
		"restart":                    false,
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `cache_listen_addr = ""`) {
		t.Fatalf("setup did not write cache_listen_addr:\n%s", got)
	}
}
