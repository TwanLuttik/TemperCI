package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/store"
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

func TestSetupStatus_ReportsWebhookDelivery(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := NewServer(ServerConfig{
		WebhookSecret: "whsec",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: false,
				ListenAddr:     "0.0.0.0:8080",
			},
			Store: st,
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil)
	req.Host = "127.0.0.1:8080"
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	var before struct {
		Webhook struct {
			Received bool `json:"received"`
		} `json:"webhook"`
		Values struct {
			WebhookReceived bool `json:"webhook_received"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before.Webhook.Received || before.Values.WebhookReceived {
		t.Fatalf("expected no delivery: %+v", before)
	}

	ping := []byte(`{"zen":"ok"}`)
	preq := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(string(ping)))
	preq.Header.Set("Content-Type", "application/json")
	preq.Header.Set("X-Hub-Signature-256", sign("whsec", ping))
	preq.Header.Set("X-GitHub-Event", "ping")
	prr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(prr, preq)
	if prr.Code != http.StatusOK {
		t.Fatalf("ping=%d %s", prr.Code, prr.Body.String())
	}

	rr2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	var after struct {
		Webhook struct {
			Received  bool   `json:"received"`
			LastEvent string `json:"last_event"`
		} `json:"webhook"`
		Values struct {
			WebhookReceived bool `json:"webhook_received"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if !after.Webhook.Received || !after.Values.WebhookReceived || after.Webhook.LastEvent != "ping" {
		t.Fatalf("after ping = %+v", after)
	}
}

func TestSetupStatus_JobAssignmentMarksWebhookReceived(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "control.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	as := NewAssignmentStore()
	as.Put(&Assignment{
		JobID:     42,
		Status:    AssignmentMinted,
		CreatedAt: time.Now().UTC(),
	})
	srv := NewServer(ServerConfig{
		Store:         as,
		WebhookSecret: "whsec",
		Dashboard: &DashboardConfig{
			Config: &config.ControlConfig{
				AuthMode:       "open",
				SetupCompleted: false,
				ListenAddr:     "0.0.0.0:8080",
			},
			Store: st,
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Webhook struct {
			Received  bool   `json:"received"`
			LastEvent string `json:"last_event"`
		} `json:"webhook"`
		Values struct {
			WebhookReceived bool `json:"webhook_received"`
		} `json:"values"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Webhook.Received || !body.Values.WebhookReceived {
		t.Fatalf("job should prove webhook is set up: %+v", body)
	}
	if body.Webhook.LastEvent != "workflow_job" {
		t.Fatalf("last_event=%q want workflow_job", body.Webhook.LastEvent)
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

func TestSetupApply_MarksFleetReadyWithoutRestart(t *testing.T) {
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
	dash := &DashboardConfig{
		Config: &config.ControlConfig{
			AuthMode:                "open",
			SetupCompleted:          false,
			AgentToken:              "tok",
			GitHubAppPrivateKeyPath: pem,
			ListenAddr:              "0.0.0.0:8080",
		},
		ConfigPath:      controlPath,
		AgentConfigPath: agentPath,
		FleetReady:      false,
	}
	srv := NewServer(ServerConfig{
		AgentToken: "tok",
		Dashboard:  dash,
	})
	body := `{
		"auth_mode": "open",
		"github_org": "coatcheckapp",
		"github_app_id": 4575087,
		"github_webhook_secret": "whsec",
		"github_app_private_key_pem": "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY-----",
		"restart": false
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup/apply", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d %s", rr.Code, rr.Body.String())
	}
	st := httptest.NewRecorder()
	srv.Handler().ServeHTTP(st, httptest.NewRequest(http.MethodGet, "/api/v1/setup/status", nil))
	if st.Code != http.StatusOK {
		t.Fatalf("status=%d %s", st.Code, st.Body.String())
	}
	var snap struct {
		FleetReady     bool `json:"fleet_ready"`
		NeedsSetup     bool `json:"needs_setup"`
		SetupCompleted bool `json:"setup_completed"`
	}
	if err := json.Unmarshal(st.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if !snap.FleetReady || snap.NeedsSetup || !snap.SetupCompleted {
		t.Fatalf("setup snapshot after apply = %+v", snap)
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
