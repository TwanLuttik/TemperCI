package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadControlFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.toml")
	content := `
listen_addr = "127.0.0.1:9090"
github_app_id = 42
github_app_private_key_path = "/tmp/key.pem"
github_webhook_secret = "s3cret"
github_org = "acme"
label_prefix = "temperci-"
runner_group_id = 2
agent_token = "shared-secret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadControlFile(path)
	if err != nil {
		t.Fatalf("LoadControlFile: %v", err)
	}
	if cfg.ListenAddr != "127.0.0.1:9090" {
		t.Errorf("listen = %q", cfg.ListenAddr)
	}
	if cfg.GitHubAppID != 42 || cfg.GitHubOrg != "acme" || cfg.RunnerGroupID != 2 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestLoadControlFile_MissingAppID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.toml")
	content := `
setup_completed = true
github_app_private_key_path = "/tmp/key.pem"
github_webhook_secret = "s3cret"
github_org = "acme"
agent_token = "shared-secret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadControlFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestLoadControlFile_SetupModeOptionalGitHub(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.toml")
	content := `
setup_completed = false
listen_addr = "127.0.0.1:8080"
auth_mode = "open"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadControlFile(path)
	if err != nil {
		t.Fatalf("LoadControlFile: %v", err)
	}
	if !cfg.NeedsSetup() {
		t.Fatal("expected NeedsSetup")
	}
}

func TestLoadAgentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
control_url = "http://127.0.0.1:8080"
agent_id = "host-1"
agent_token = "shared-secret"
min_ready = 2
max_ready = 4
image_path = "/var/lib/temperci/images/base.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
data_dir = "/var/lib/temperci"
vcpu = 4
memory_mib = 8192
idle_recycle_seconds = 3600
vmm_backend = "fake"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatalf("LoadAgentFile: %v", err)
	}
	if cfg.MinReady != 2 || cfg.MaxReady != 4 || cfg.VCPU != 4 || cfg.MemoryMiB != 8192 {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.DataDir != "/var/lib/temperci" || cfg.ImagePath == "" {
		t.Errorf("paths = %+v", cfg)
	}
	if cfg.MaxTotalVMs != 4+32 {
		t.Errorf("MaxTotalVMs default = %d", cfg.MaxTotalVMs)
	}
}

func TestLoadAgentFile_ScratchDirDerivesDataDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
image_path = "/img/base"
scratch_dir = "/var/lib/temperci/instances"
agent_token = "shared-secret"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatalf("LoadAgentFile: %v", err)
	}
	if cfg.DataDir != "/var/lib/temperci" {
		t.Errorf("DataDir = %q", cfg.DataDir)
	}
}

func TestLoadAgentFile_MissingImage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	if err := os.WriteFile(path, []byte(`min_ready = 1`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadAgentFile(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestAgentConfig_MaxReadyBelowMin(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", MinReady: 3, MaxReady: 1, AgentToken: "t"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected max_ready < min_ready error")
	}
}

func TestLoadControlFile_MissingAgentToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "control.toml")
	content := `
setup_completed = true
github_app_id = 1
github_app_private_key_path = "/tmp/key.pem"
github_webhook_secret = "s3cret"
github_org = "acme"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadControlFile(path); err == nil {
		t.Fatal("expected agent_token required")
	}
}
