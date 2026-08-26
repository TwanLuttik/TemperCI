// Package config loads and validates operator configuration for the control
// plane and host agent (typically TOML under /etc/temperci/).
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ControlConfig is the control-plane runtime configuration.
type ControlConfig struct {
	ListenAddr              string `toml:"listen_addr"`
	GitHubAppID             int64  `toml:"github_app_id"`
	GitHubAppPrivateKeyPath string `toml:"github_app_private_key_path"`
	GitHubWebhookSecret     string `toml:"github_webhook_secret"`
	GitHubOrg               string `toml:"github_org"`
	LabelPrefix             string `toml:"label_prefix"`
	RunnerGroupID           int64  `toml:"runner_group_id"`
	// AgentToken is the shared bearer secret for control↔agent APIs.
	// Prefer combining with TLS (and optional mTLS) in production.
	AgentToken string `toml:"agent_token"`
	// MCPToken is the shared bearer secret for the read-only Streamable HTTP
	// MCP endpoint at /mcp. Empty disables the endpoint (404).
	MCPToken string `toml:"mcp_token"`

	// Optional HTTPS for the control-plane listener.
	TLSCertFile string `toml:"tls_cert_file"`
	TLSKeyFile  string `toml:"tls_key_file"`
	// TLSClientCAFile enables mTLS: agents must present a client cert signed by this CA.
	TLSClientCAFile string `toml:"tls_client_ca_file"`

	// AssignmentStuckSeconds marks assigned/started jobs stuck after this many seconds (0 = 3600 default; negative disables).
	AssignmentStuckSeconds int `toml:"assignment_stuck_seconds"`
	// StaleMintedSeconds fails unclaimed minted jobs after this many seconds (0 = 7200 default; negative disables).
	StaleMintedSeconds int `toml:"stale_minted_seconds"`
	// ReconcileIntervalSeconds is how often stuck-assignment reconciliation runs (default 30).
	ReconcileIntervalSeconds int `toml:"reconcile_interval_seconds"`

	// Dashboard / setup (operator UI).
	// AuthMode is "open" (no login) or "password" (local users in SQLite).
	AuthMode string `toml:"auth_mode"`
	// SQLitePath is the dashboard database (users/sessions). Default: /var/lib/temperci/control.db
	SQLitePath string `toml:"sqlite_path"`
	// SetupCompleted is true after the first-run wizard (or manual install).
	// When false, GitHub fields may be omitted and setup mode is enabled.
	SetupCompleted bool `toml:"setup_completed"`
	// HostctlPath is the optional temperci-hostctl binary for systemctl restarts.
	HostctlPath string `toml:"hostctl_path"`
	// DataDir is used for default sqlite path parent and PEM writes during setup.
	DataDir string `toml:"data_dir"`
}

// LoadControlFile reads and validates a control-plane TOML config file.
func LoadControlFile(path string) (*ControlConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		// Preserve os.ErrNotExist for callers that bootstrap missing configs.
		if os.IsNotExist(err) {
			return nil, err
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg ControlConfig
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks required control-plane fields.
// When SetupCompleted is false, GitHub credentials and agent_token are optional (setup wizard mode).
func (c *ControlConfig) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = "0.0.0.0:8080"
	}
	if strings.TrimSpace(c.DataDir) == "" {
		c.DataDir = "/var/lib/temperci"
	}
	if strings.TrimSpace(c.SQLitePath) == "" {
		c.SQLitePath = filepath.Join(c.DataDir, "control.db")
	}
	if strings.TrimSpace(c.AuthMode) == "" {
		c.AuthMode = "open"
	}
	c.AuthMode = strings.ToLower(strings.TrimSpace(c.AuthMode))
	if c.AuthMode != "open" && c.AuthMode != "password" {
		return fmt.Errorf("config: auth_mode must be open or password")
	}
	if strings.TrimSpace(c.HostctlPath) == "" {
		c.HostctlPath = "/usr/local/bin/temperci-hostctl"
	}
	if c.LabelPrefix == "" {
		c.LabelPrefix = "temperci-"
	}
	if c.RunnerGroupID == 0 {
		c.RunnerGroupID = 1
	}
	if strings.TrimSpace(c.GitHubAppPrivateKeyPath) == "" {
		c.GitHubAppPrivateKeyPath = "/etc/temperci/github-app.pem"
	}

	// Full GitHub validation when setup is done, or when a legacy config already
	// has credentials (missing setup_completed flag).
	if c.SetupCompleted || c.looksFullyConfigured() {
		if c.GitHubAppID == 0 {
			return fmt.Errorf("config: github_app_id is required")
		}
		if strings.TrimSpace(c.GitHubWebhookSecret) == "" {
			return fmt.Errorf("config: github_webhook_secret is required")
		}
		if strings.TrimSpace(c.GitHubOrg) == "" {
			return fmt.Errorf("config: github_org is required")
		}
		if strings.TrimSpace(c.AgentToken) == "" {
			return fmt.Errorf("config: agent_token is required")
		}
	}

	cert := strings.TrimSpace(c.TLSCertFile)
	key := strings.TrimSpace(c.TLSKeyFile)
	c.TLSCertFile = cert
	c.TLSKeyFile = key
	c.TLSClientCAFile = strings.TrimSpace(c.TLSClientCAFile)
	if (cert == "") != (key == "") {
		return fmt.Errorf("config: tls_cert_file and tls_key_file must both be set or both empty")
	}
	if c.TLSClientCAFile != "" && cert == "" {
		return fmt.Errorf("config: tls_client_ca_file requires tls_cert_file and tls_key_file")
	}
	if c.AssignmentStuckSeconds == 0 {
		c.AssignmentStuckSeconds = 3600
	}
	if c.StaleMintedSeconds == 0 {
		c.StaleMintedSeconds = 7200
	}
	if c.ReconcileIntervalSeconds <= 0 {
		c.ReconcileIntervalSeconds = 30
	}
	return nil
}

// looksFullyConfigured reports whether GitHub + agent credentials are present
// (used for legacy installs without setup_completed = true).
func (c *ControlConfig) looksFullyConfigured() bool {
	return c.GitHubAppID != 0 &&
		strings.TrimSpace(c.GitHubOrg) != "" &&
		strings.TrimSpace(c.AgentToken) != "" &&
		strings.TrimSpace(c.GitHubWebhookSecret) != ""
}

// NeedsSetup reports whether the control plane should run the first-run wizard.
func (c *ControlConfig) NeedsSetup() bool {
	if c.SetupCompleted || c.looksFullyConfigured() {
		return false
	}
	return true
}

// AgentConfig is the host-agent runtime configuration.
type AgentConfig struct {
	ControlURL string `toml:"control_url"`
	// AgentID is this host's stable identity (default: hostname).
	AgentID string `toml:"agent_id"`
	// AgentToken must match control plane agent_token (shared secret).
	AgentToken string `toml:"agent_token"`
	// PollIntervalSeconds is how often the worker claims jobs when idle.
	PollIntervalSeconds int `toml:"poll_interval_seconds"`
	// JobSimulateSeconds waits after bind before destroy (fake/dev without runner wait).
	// 0 finishes immediately after bind+start. Real guests should wait on runner exit.
	JobSimulateSeconds int `toml:"job_simulate_seconds"`
	// JobDeadlineSeconds force-destroys a busy job after this many seconds (0 = disabled).
	JobDeadlineSeconds int `toml:"job_deadline_seconds"`
	// MinReady is the target number of warm (idle) VMs.
	MinReady int `toml:"min_ready"`
	// MaxReady is the soft cap on warm + pool_boot VMs.
	MaxReady int `toml:"max_ready"`
	// ImagePath is the shared base rootfs used for pool members.
	ImagePath string `toml:"image_path"`
	// KernelPath is the guest kernel (required for firecracker; optional for fake).
	KernelPath string `toml:"kernel_path"`
	// DataDir is the host data root (<data_dir>/images, <data_dir>/instances).
	// If empty, derived from ScratchDir's parent when ScratchDir ends with "instances".
	DataDir string `toml:"data_dir"`
	// ScratchDir is optional; historically the instances directory. Prefer DataDir.
	ScratchDir string `toml:"scratch_dir"`
	// VCPU is default vCPU count for pool VMs (used when shapes is empty).
	VCPU int `toml:"vcpu"`
	// MemoryMiB is default guest RAM for pool VMs (used when shapes is empty).
	MemoryMiB int `toml:"memory_mib"`
	// Shapes is the warm-pool catalog. Empty means a single shape from vcpu/memory_mib/min_ready.
	Shapes []VMShapeConfig `toml:"shapes"`
	// IdleRecycleSeconds recycles warm VMs older than this (0 = disabled).
	IdleRecycleSeconds int `toml:"idle_recycle_seconds"`
	// VMMBackend selects the microVM backend: "fake" (default on non-Linux) or "firecracker".
	VMMBackend string `toml:"vmm_backend"`
	// ReconcileIntervalSeconds is how often the pool reconciler runs (default 1).
	ReconcileIntervalSeconds int `toml:"reconcile_interval_seconds"`
	// MaxTotalVMs hard-caps tracked instances to avoid unbounded growth if destroy fails.
	// Default: max_ready + 32.
	MaxTotalVMs int `toml:"max_total_vms"`
	// HostReserveMemoryMiB is RAM kept for the host OS (0 = default 2048).
	HostReserveMemoryMiB int `toml:"host_reserve_memory_mib"`
	// HostReserveDiskMiB is disk kept free on data_dir (0 = default 5120).
	HostReserveDiskMiB int `toml:"host_reserve_disk_mib"`

	// MetricsListenAddr binds the agent local metrics/admin HTTP server (empty = disabled).
	// Prefer 127.0.0.1 for admin endpoints.
	MetricsListenAddr string `toml:"metrics_listen_addr"`

	// CacheMaxBytes is the LRU cap for host-local Actions cache (default 50 GiB).
	CacheMaxBytes int64 `toml:"cache_max_bytes"`
	// CacheListenAddr binds the Actions cache gateway (empty = disabled).
	CacheListenAddr string `toml:"cache_listen_addr"`
	// OCICacheMaxBytes is the LRU cap for host-local OCI/build cache (default 100 GiB).
	OCICacheMaxBytes int64 `toml:"oci_cache_max_bytes"`

	// Optional TLS when control_url is https://
	TLSCAFile             string `toml:"tls_ca_file"`
	TLSCertFile           string `toml:"tls_cert_file"`
	TLSKeyFile            string `toml:"tls_key_file"`
	TLSInsecureSkipVerify bool   `toml:"tls_insecure_skip_verify"`
}

// VMShapeConfig is one guest size that can be kept warm and/or cold-booted.
type VMShapeConfig struct {
	Label     string `toml:"label" json:"label"`
	VCPU      int    `toml:"vcpu" json:"vcpu"`
	MemoryMiB int    `toml:"memory_mib" json:"memory_mib"`
	MinReady  int    `toml:"min_ready" json:"min_ready"`
}

// LoadAgentFile reads and validates a host-agent TOML config file.
func LoadAgentFile(path string) (*AgentConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var cfg AgentConfig
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate checks and defaults agent fields.
func (c *AgentConfig) Validate() error {
	if strings.TrimSpace(c.ControlURL) == "" {
		c.ControlURL = "http://127.0.0.1:8080"
	}
	if strings.TrimSpace(c.AgentToken) == "" {
		return fmt.Errorf("config: agent_token is required")
	}
	if strings.TrimSpace(c.AgentID) == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			c.AgentID = h
		} else {
			c.AgentID = "temperci-agent"
		}
	}
	if c.PollIntervalSeconds <= 0 {
		c.PollIntervalSeconds = 1
	}
	if c.JobSimulateSeconds < 0 {
		return fmt.Errorf("config: job_simulate_seconds must be >= 0")
	}
	if c.JobDeadlineSeconds < 0 {
		return fmt.Errorf("config: job_deadline_seconds must be >= 0")
	}
	if c.MinReady < 0 {
		return fmt.Errorf("config: min_ready must be >= 0")
	}
	if c.MaxReady <= 0 {
		if c.MinReady > 0 {
			c.MaxReady = c.MinReady
		} else {
			// No warm pool still needs job slots so cold-boots can be claimed.
			c.MaxReady = 2
		}
	}
	if c.MaxReady < c.MinReady {
		return fmt.Errorf("config: max_ready (%d) must be >= min_ready (%d)", c.MaxReady, c.MinReady)
	}
	if strings.TrimSpace(c.ImagePath) == "" {
		return fmt.Errorf("config: image_path is required")
	}
	if c.VCPU <= 0 {
		c.VCPU = 2
	}
	if c.MemoryMiB <= 0 {
		c.MemoryMiB = 2048
	}
	for i := range c.Shapes {
		if err := normalizeShape(&c.Shapes[i], c.VCPU, c.MemoryMiB); err != nil {
			return err
		}
	}
	if sum := sumShapeMinReady(c.Shapes); sum > c.MaxReady {
		c.MaxReady = sum
	}
	if c.IdleRecycleSeconds < 0 {
		return fmt.Errorf("config: idle_recycle_seconds must be >= 0")
	}
	if strings.TrimSpace(c.VMMBackend) == "" {
		c.VMMBackend = "fake"
	}
	switch strings.ToLower(c.VMMBackend) {
	case "fake", "firecracker":
		c.VMMBackend = strings.ToLower(c.VMMBackend)
	default:
		return fmt.Errorf("config: vmm_backend must be fake or firecracker, got %q", c.VMMBackend)
	}
	if c.VMMBackend == "firecracker" && strings.TrimSpace(c.KernelPath) == "" {
		return fmt.Errorf("config: kernel_path is required when vmm_backend is firecracker")
	}
	if c.ReconcileIntervalSeconds <= 0 {
		c.ReconcileIntervalSeconds = 1
	}
	if c.MaxTotalVMs <= 0 {
		c.MaxTotalVMs = c.MaxReady + 32
	}
	if c.MaxTotalVMs < c.MaxReady {
		return fmt.Errorf("config: max_total_vms (%d) must be >= max_ready (%d)", c.MaxTotalVMs, c.MaxReady)
	}
	if c.HostReserveMemoryMiB < 0 {
		return fmt.Errorf("config: host_reserve_memory_mib must be >= 0")
	}
	if c.HostReserveMemoryMiB == 0 {
		c.HostReserveMemoryMiB = 2048
	}
	if c.HostReserveDiskMiB < 0 {
		return fmt.Errorf("config: host_reserve_disk_mib must be >= 0")
	}
	if c.HostReserveDiskMiB == 0 {
		c.HostReserveDiskMiB = 5120
	}
	c.MetricsListenAddr = strings.TrimSpace(c.MetricsListenAddr)
	c.CacheListenAddr = strings.TrimSpace(c.CacheListenAddr)
	if c.CacheMaxBytes < 0 {
		return fmt.Errorf("config: cache_max_bytes must be >= 0")
	}
	if c.CacheMaxBytes == 0 {
		c.CacheMaxBytes = 50 << 30
	}
	if c.OCICacheMaxBytes < 0 {
		return fmt.Errorf("config: oci_cache_max_bytes must be >= 0")
	}
	if c.OCICacheMaxBytes == 0 {
		c.OCICacheMaxBytes = 100 << 30
	}
	c.TLSCAFile = strings.TrimSpace(c.TLSCAFile)
	c.TLSCertFile = strings.TrimSpace(c.TLSCertFile)
	c.TLSKeyFile = strings.TrimSpace(c.TLSKeyFile)
	if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
		return fmt.Errorf("config: tls_cert_file and tls_key_file must both be set or both empty")
	}

	// Resolve data root.
	c.DataDir = strings.TrimSpace(c.DataDir)
	c.ScratchDir = strings.TrimSpace(c.ScratchDir)
	if c.DataDir == "" {
		if c.ScratchDir == "" {
			c.DataDir = "/var/lib/temperci"
		} else {
			// scratch_dir often points at .../instances; parent is the data root.
			base := strings.TrimRight(c.ScratchDir, "/")
			if strings.HasSuffix(base, "/instances") || strings.HasSuffix(base, "instances") {
				c.DataDir = strings.TrimSuffix(base, "instances")
				c.DataDir = strings.TrimRight(c.DataDir, "/")
				if c.DataDir == "" {
					c.DataDir = "/"
				}
			} else {
				c.DataDir = base
			}
		}
	}
	return nil
}

func normalizeShape(s *VMShapeConfig, defaultVCPU, defaultMem int) error {
	if s.VCPU <= 0 {
		s.VCPU = defaultVCPU
	}
	if s.MemoryMiB <= 0 {
		s.MemoryMiB = defaultMem
	}
	if s.MinReady < 0 {
		return fmt.Errorf("config: shape min_ready must be >= 0")
	}
	if strings.TrimSpace(s.Label) == "" {
		s.Label = defaultShapeLabel(s.VCPU, s.MemoryMiB)
	}
	return nil
}

func sumShapeMinReady(shapes []VMShapeConfig) int {
	n := 0
	for _, s := range shapes {
		n += s.MinReady
	}
	return n
}

func defaultShapeLabel(vcpus, memoryMiB int) string {
	if vcpus == 4 && memoryMiB == 8192 {
		return "temperci-4vcpu-ubuntu-2404"
	}
	g := memoryMiB / 1024
	if g < 1 {
		g = 1
	}
	return fmt.Sprintf("temperci-%dvcpu-%dg-ubuntu-2404", vcpus, g)
}

// EffectiveShapes is the warm catalog. Empty [[shapes]] with min_ready>0
// becomes the legacy single size. Empty [[shapes]] with min_ready=0 means
// no warm pool; jobs still cold-boot from the workflow runs-on label.
func (c *AgentConfig) EffectiveShapes() []VMShapeConfig {
	if c == nil {
		return nil
	}
	if len(c.Shapes) > 0 {
		out := make([]VMShapeConfig, len(c.Shapes))
		copy(out, c.Shapes)
		return out
	}
	if c.MinReady <= 0 {
		return []VMShapeConfig{}
	}
	return []VMShapeConfig{{
		Label:     defaultShapeLabel(c.VCPU, c.MemoryMiB),
		VCPU:      c.VCPU,
		MemoryMiB: c.MemoryMiB,
		MinReady:  c.MinReady,
	}}
}
