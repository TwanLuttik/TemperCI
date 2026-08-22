package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlURLFromListen(t *testing.T) {
	if got := controlURLFromListen("0.0.0.0:9090"); got != "http://127.0.0.1:9090" {
		t.Fatalf("got %q", got)
	}
	if got := controlURLFromListen("bad"); got != "http://127.0.0.1:8080" {
		t.Fatalf("got %q", got)
	}
}

func TestHandleSystemInstall_RequiresHostctl(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)
	srv.dash.Config.HostctlPath = filepath.Join(dir, "missing-hostctl")
	srv.dash.Config.AgentToken = "tok"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/install", strings.NewReader(`{"target":"agent"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleSystemStatus_IncludesInstallFields(t *testing.T) {
	dir := t.TempDir()
	srv := settingsTestServer(t, dir)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Agent struct {
			Installed   *bool  `json:"installed"`
			Installable bool   `json:"installable"`
			InstallHint string `json:"install_hint"`
			Binary      bool   `json:"binary"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Agent.Installed == nil {
		t.Fatal("installed field missing")
	}
}

func TestApplyAgentInstall_NotInstalled(t *testing.T) {
	agent := map[string]any{"detail": "no agent registered", "status": "unknown"}
	applyAgentInstall(agent, agentInstallProbe{
		Installed:   false,
		Installable: true,
		Hint:        "Install the host agent unit and start temperci-agent.service.",
	})
	if agent["status"] != svcNotInstalled {
		t.Fatalf("status=%v", agent["status"])
	}
	if agent["installed"] != false {
		t.Fatalf("installed=%v", agent["installed"])
	}
	detail, _ := agent["detail"].(string)
	if !strings.Contains(detail, "not installed") {
		t.Fatalf("detail=%q", detail)
	}
}

func TestHandleSystemInstall_WritesConfigAndRunsHostctl(t *testing.T) {
	dir := t.TempDir()
	hostctl := filepath.Join(dir, "temperci-hostctl")
	script := "#!/bin/sh\n" +
		"echo install \"$1\" \"$2\" \"$3\" > " + filepath.Join(dir, "hostctl.out") + "\n" +
		"exit 0\n"
	if err := os.WriteFile(hostctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := settingsTestServer(t, dir)
	srv.dash.Config.HostctlPath = hostctl
	srv.dash.Config.AgentToken = "tok"
	srv.dash.Config.ListenAddr = "127.0.0.1:8080"
	srv.dash.Config.DataDir = dir
	// Stage a source binary next to hostctl so probe finds it.
	if err := os.WriteFile(filepath.Join(dir, "temperci-agent"), []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/install", strings.NewReader(`{"target":"agent"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	agentPath := filepath.Join(dir, "agent.toml")
	raw, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `agent_token = "tok"`) {
		t.Fatalf("agent.toml:\n%s", raw)
	}
	out, err := os.ReadFile(filepath.Join(dir, "hostctl.out"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "install") || !strings.Contains(string(out), "agent") {
		t.Fatalf("hostctl.out=%q", out)
	}
}
