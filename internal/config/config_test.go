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
mcp_token = "mcp-secret"
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
	if cfg.MCPToken != "mcp-secret" {
		t.Errorf("mcp_token = %q", cfg.MCPToken)
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
	if cfg.CacheMaxBytes != 50<<30 {
		t.Errorf("CacheMaxBytes default = %d", cfg.CacheMaxBytes)
	}
	if cfg.OCICacheMaxBytes != 100<<30 {
		t.Errorf("OCICacheMaxBytes default = %d", cfg.OCICacheMaxBytes)
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

func TestAgentConfig_MinReadyZeroAllowed(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", MinReady: 0, MaxReady: 2, AgentToken: "t"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.MinReady != 0 {
		t.Fatalf("MinReady = %d want 0", cfg.MinReady)
	}
	if cfg.MaxReady != 2 {
		t.Fatalf("MaxReady = %d want 2 (job capacity stays)", cfg.MaxReady)
	}
	if got := cfg.EffectiveShapes(); len(got) != 0 {
		t.Fatalf("EffectiveShapes = %+v want empty (no warm catalog)", got)
	}
}

func TestEffectiveShapes_LegacyMinReadyStillSynthesizes(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", MinReady: 1, MaxReady: 2, VCPU: 4, MemoryMiB: 8192, AgentToken: "t"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := cfg.EffectiveShapes()
	if len(got) != 1 || got[0].MinReady != 1 || got[0].MemoryMiB != 8192 {
		t.Fatalf("EffectiveShapes = %+v", got)
	}
}

func TestLoadAgentFile_FirecrackerRequiresKernel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
image_path = "/img/base"
agent_token = "shared-secret"
vmm_backend = "firecracker"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadAgentFile(path); err == nil {
		t.Fatal("expected kernel_path required for firecracker")
	}
}

func TestLoadAgentFile_FirecrackerWithKernel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
image_path = "/img/base"
kernel_path = "/img/vmlinux"
agent_token = "shared-secret"
vmm_backend = "firecracker"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatalf("LoadAgentFile: %v", err)
	}
	if cfg.KernelPath != "/img/vmlinux" || cfg.VMMBackend != "firecracker" {
		t.Fatalf("cfg = %+v", cfg)
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

func TestAgentConfig_ReserveDefaults(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 2048 || cfg.HostReserveDiskMiB != 5120 {
		t.Fatalf("defaults = ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}

func TestAgentConfig_ReserveExplicitZeroBecomesDefault(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveMemoryMiB: 0, HostReserveDiskMiB: 0}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 2048 || cfg.HostReserveDiskMiB != 5120 {
		t.Fatalf("0 must default, got ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}

func TestAgentConfig_ReserveNegative(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveMemoryMiB: -1}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative ram reserve error")
	}
	cfg = AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveDiskMiB: -5}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative disk reserve error")
	}
}

func TestLoadAgentFile_ReserveKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
image_path = "/img/base"
agent_token = "shared-secret"
host_reserve_memory_mib = 1024
host_reserve_disk_mib = 2048
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 1024 || cfg.HostReserveDiskMiB != 2048 {
		t.Fatalf("got ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}
