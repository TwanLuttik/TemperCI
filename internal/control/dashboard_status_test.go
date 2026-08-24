package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

func TestSystemdDisplayStatus(t *testing.T) {
	cases := []struct {
		unit      string
		processUp bool
		want      string
	}{
		{"active", true, svcRunning},
		{"active", false, svcStarting},
		{"activating", false, svcStarting},
		{"reloading", true, svcStarting},
		{"deactivating", true, svcStopping},
		{"failed", true, svcFailed},
		{"inactive", false, svcStopped},
		{"unknown", true, svcRunning},
		{"unknown", false, svcUnknown},
		{"", false, svcUnknown},
	}
	for _, tc := range cases {
		if got := systemdDisplayStatus(tc.unit, tc.processUp); got != tc.want {
			t.Errorf("systemdDisplayStatus(%q, %v)=%q want %q", tc.unit, tc.processUp, got, tc.want)
		}
	}
}

func TestOverallServiceStatus(t *testing.T) {
	if got := overallServiceStatus(svcRunning, svcRunning); got != svcRunning {
		t.Fatalf("got %q", got)
	}
	if got := overallServiceStatus(svcRunning, svcFailed); got != svcFailed {
		t.Fatalf("got %q", got)
	}
	if got := overallServiceStatus(svcStarting, svcStopped); got != svcStopped {
		t.Fatalf("got %q", got)
	}
}

func TestBuildSystemStatus_NoHostctl(t *testing.T) {
	out := buildSystemStatus(false, "unknown", "unknown", nil, "")
	ctrl := out["control"].(map[string]any)
	agent := out["agent"].(map[string]any)
	if ctrl["status"] != svcRunning {
		t.Fatalf("control status=%v", ctrl["status"])
	}
	if agent["status"] != svcUnknown {
		t.Fatalf("agent status=%v", agent["status"])
	}
	if agent["ready"] != false {
		t.Fatalf("agent ready=%v", agent["ready"])
	}
	if out["overall"] != svcUnknown {
		t.Fatalf("overall=%v", out["overall"])
	}
	if out["hostctl"] != false {
		t.Fatalf("hostctl=%v", out["hostctl"])
	}
}

func TestBuildSystemStatus_ActiveRegistered(t *testing.T) {
	out := buildSystemStatus(true, "active", "active", []string{"host-1"}, "2026-08-16T12:00:00Z")
	ctrl := out["control"].(map[string]any)
	agent := out["agent"].(map[string]any)
	if ctrl["status"] != svcRunning || ctrl["name"] != "temperci-control.service" {
		t.Fatalf("control=%v", ctrl)
	}
	if agent["status"] != svcRunning || agent["ready"] != true {
		t.Fatalf("agent=%v", agent)
	}
	if out["overall"] != svcRunning {
		t.Fatalf("overall=%v", out["overall"])
	}
	if detail, _ := agent["detail"].(string); !strings.Contains(detail, "host-1") || !strings.Contains(detail, "active") {
		t.Fatalf("agent detail=%q", detail)
	}
}

func TestHandleSystemStatus_WithFakeHostctl(t *testing.T) {
	dir := t.TempDir()
	hostctl := writeFakeHostctl(t, dir, "active", "failed")
	srv := settingsTestServer(t, dir)
	srv.dash.Config.HostctlPath = hostctl
	srv.agents.Register(api.RegisterRequest{AgentID: "box-1", Capacity: 1})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		OK      bool   `json:"ok"`
		Hostctl bool   `json:"hostctl"`
		Overall string `json:"overall"`
		Control struct {
			Status string `json:"status"`
			Unit   string `json:"unit"`
			Name   string `json:"name"`
		} `json:"control"`
		Agent struct {
			Status     string   `json:"status"`
			Unit       string   `json:"unit"`
			Ready      bool     `json:"ready"`
			Registered bool     `json:"registered"`
			IDs        []string `json:"registered_ids"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Hostctl {
		t.Fatal("expected hostctl true")
	}
	if resp.Control.Status != svcRunning || resp.Control.Unit != "active" {
		t.Fatalf("control=%+v", resp.Control)
	}
	if resp.Control.Name != "temperci-control.service" {
		t.Fatalf("name=%q", resp.Control.Name)
	}
	if resp.Agent.Status != svcFailed || resp.Agent.Unit != "failed" || resp.Agent.Ready {
		t.Fatalf("agent=%+v", resp.Agent)
	}
	if !resp.Agent.Registered || len(resp.Agent.IDs) != 1 || resp.Agent.IDs[0] != "box-1" {
		t.Fatalf("registered=%v ids=%v", resp.Agent.Registered, resp.Agent.IDs)
	}
	if resp.Overall != svcFailed {
		t.Fatalf("overall=%q", resp.Overall)
	}
}

func TestHandleSystemStatus_SudoErrorIsNotStopped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "temperci-hostctl")
	script := "#!/bin/sh\n" +
		"echo 'sudo: The \"no new privileges\" flag is set, which prevents sudo from running as root.' >&2\n" +
		"echo 'sudo: If sudo is running in a container, you may need to adjust the container configuration to disable the flag.' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := settingsTestServer(t, dir)
	srv.dash.Config.HostctlPath = path
	srv.agents.Register(api.RegisterRequest{AgentID: "pve", Capacity: 1})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/status", nil)
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Overall string `json:"overall"`
		Control struct {
			Status string `json:"status"`
			Unit   string `json:"unit"`
		} `json:"control"`
		Agent struct {
			Status     string `json:"status"`
			Unit       string `json:"unit"`
			Registered bool   `json:"registered"`
			Ready      bool   `json:"ready"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Control.Unit == "inactive" || resp.Control.Status == svcStopped {
		t.Fatalf("control sudo failure must not look stopped: %+v", resp.Control)
	}
	if resp.Control.Unit != "unknown" || resp.Control.Status != svcRunning {
		t.Fatalf("control=%+v want unit=unknown status=running", resp.Control)
	}
	if resp.Agent.Unit == "inactive" || resp.Agent.Status == svcStopped {
		t.Fatalf("agent sudo failure must not look stopped: %+v", resp.Agent)
	}
	if resp.Agent.Unit != "unknown" || resp.Agent.Status != svcRunning || !resp.Agent.Registered {
		t.Fatalf("agent=%+v want unit=unknown status=running registered", resp.Agent)
	}
	if resp.Overall != svcRunning {
		t.Fatalf("overall=%q want running", resp.Overall)
	}
}

func writeFakeHostctl(t *testing.T, dir, controlState, agentState string) string {
	t.Helper()
	path := filepath.Join(dir, "temperci-hostctl")
	script := "#!/bin/sh\n" +
		"action=\"$1\"\n" +
		"target=\"$2\"\n" +
		"if [ \"$action\" != \"status\" ]; then echo ok; exit 0; fi\n" +
		"case \"$target\" in\n" +
		"  control) echo " + controlState + " ;;\n" +
		"  agent) echo " + agentState + " ;;\n" +
		"  *) echo unknown; exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
