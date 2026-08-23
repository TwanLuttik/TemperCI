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

func TestSetupStatus_ReportsInstalledSteps(t *testing.T) {
	dir := t.TempDir()
	pem := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(pem, []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"tok\"\ncache_listen_addr = \"127.0.0.1:8743\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerConfig{
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:                "open",
				SetupCompleted:          true,
				GitHubOrg:               "acme",
				GitHubAppID:             42,
				GitHubWebhookSecret:     "whsec",
				GitHubAppPrivateKeyPath: pem,
				AgentToken:              "tok",
				ListenAddr:              "0.0.0.0:8080",
			},
			ConfigPath:      filepath.Join(dir, "control.toml"),
			AgentConfigPath: agentPath,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		NeedsSetup     bool `json:"needs_setup"`
		SetupCompleted bool `json:"setup_completed"`
		Steps          []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"steps"`
		Values struct {
			GitHubOrg   string `json:"github_org"`
			WebhookSet  bool   `json:"webhook_set"`
			PemSet      bool   `json:"pem_set"`
			AgentToken  bool   `json:"agent_token_set"`
			CacheListen string `json:"cache_listen_addr"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.NeedsSetup || !body.SetupCompleted {
		t.Fatalf("setup flags = %+v", body)
	}
	if body.Values.GitHubOrg != "acme" || !body.Values.WebhookSet || !body.Values.PemSet || !body.Values.AgentToken {
		t.Fatalf("values = %+v", body.Values)
	}
	if body.Values.CacheListen != "127.0.0.1:8743" {
		t.Fatalf("cache = %q", body.Values.CacheListen)
	}
	byID := map[string]string{}
	for _, st := range body.Steps {
		byID[st.ID] = st.Status
	}
	if byID["access"] != "ok" || byID["github"] != "ok" || byID["agent"] != "ok" {
		t.Fatalf("steps = %+v", body.Steps)
	}
}

func TestSetupApply_DraftWritesGitHubWithoutCompleting(t *testing.T) {
	dir := t.TempDir()
	controlPath := filepath.Join(dir, "control.toml")
	agentPath := filepath.Join(dir, "agent.toml")
	pem := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"tok\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(controlPath, []byte("setup_completed = false\nagent_token = \"tok\"\nauth_mode = \"open\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerConfig{
		AgentToken: "tok",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:                "open",
				SetupCompleted:          false,
				AgentToken:              "tok",
				GitHubAppPrivateKeyPath: pem,
				ListenAddr:              "0.0.0.0:8080",
			},
			ConfigPath:      controlPath,
			AgentConfigPath: agentPath,
		},
	})
	body := `{
		"draft": true,
		"github_org": "coatcheckapp",
		"github_app_id": 4575087,
		"github_webhook_secret": "whsec",
		"github_app_private_key_pem": "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----"
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	got, err := config.LoadControlFile(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.GitHubOrg != "coatcheckapp" || got.GitHubAppID != 4575087 || got.GitHubWebhookSecret != "whsec" {
		t.Fatalf("github not saved: %+v", got)
	}
	if got.SetupCompleted {
		t.Fatal("draft must not complete setup")
	}
	raw, err := os.ReadFile(pem)
	if err != nil || !strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatalf("pem: %s err=%v", raw, err)
	}
}

func TestSetupApply_ReentryKeepsSecrets(t *testing.T) {
	dir := t.TempDir()
	pem := filepath.Join(dir, "github-app.pem")
	if err := os.WriteFile(pem, []byte("-----BEGIN RSA PRIVATE KEY-----\nOLD\n-----END RSA PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controlPath := filepath.Join(dir, "control.toml")
	agentPath := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(agentPath, []byte("agent_token = \"keep-me\"\ncache_listen_addr = \"127.0.0.1:8743\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(ServerConfig{
		AgentToken:    "keep-me",
		WebhookSecret: "original-secret",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:                "open",
				SetupCompleted:          true,
				GitHubOrg:               "acme",
				GitHubAppID:             7,
				GitHubWebhookSecret:     "original-secret",
				GitHubAppPrivateKeyPath: pem,
				AgentToken:              "keep-me",
				ListenAddr:              "0.0.0.0:8080",
			},
			ConfigPath:      controlPath,
			AgentConfigPath: agentPath,
		},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", strings.NewReader(`{
		"auth_mode":"open",
		"github_org":"acme",
		"listen_addr":"0.0.0.0:8080",
		"cache_listen_addr":"127.0.0.1:8743",
		"restart":false
	}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	got, err := config.LoadControlFile(controlPath)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentToken != "keep-me" || got.GitHubWebhookSecret != "original-secret" || got.GitHubAppID != 7 {
		t.Fatalf("config reset: %+v", got)
	}
	raw, _ := os.ReadFile(pem)
	if !strings.Contains(string(raw), "OLD") {
		t.Fatalf("pem rewritten: %s", raw)
	}
}
