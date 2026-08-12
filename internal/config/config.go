// Package config loads and validates operator configuration for the control
// plane and host agent (typically TOML under /etc/temperci/).
package config

import (
	"fmt"
	"os"
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
}

// LoadControlFile reads and validates a control-plane TOML config file.
func LoadControlFile(path string) (*ControlConfig, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
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
func (c *ControlConfig) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		c.ListenAddr = "0.0.0.0:8080"
	}
	if c.GitHubAppID == 0 {
		return fmt.Errorf("config: github_app_id is required")
	}
	if strings.TrimSpace(c.GitHubAppPrivateKeyPath) == "" {
		return fmt.Errorf("config: github_app_private_key_path is required")
	}
	if strings.TrimSpace(c.GitHubWebhookSecret) == "" {
		return fmt.Errorf("config: github_webhook_secret is required")
	}
	if strings.TrimSpace(c.GitHubOrg) == "" {
		return fmt.Errorf("config: github_org is required")
	}
	if c.LabelPrefix == "" {
		c.LabelPrefix = "temperci-"
	}
	if c.RunnerGroupID == 0 {
		c.RunnerGroupID = 1
	}
	if strings.TrimSpace(c.AgentToken) == "" {
		return fmt.Errorf("config: agent_token is required")
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
	// VCPU is default vCPU count for pool VMs.
	VCPU int `toml:"vcpu"`
	// MemoryMiB is default guest RAM for pool VMs.
	MemoryMiB int `toml:"memory_mib"`
	// IdleRecycleSeconds recycles warm VMs older than this (0 = disabled).
	IdleRecycleSeconds int `toml:"idle_recycle_seconds"`
	// VMMBackend selects the microVM backend: "fake" (default on non-Linux) or "firecracker".
	VMMBackend string `toml:"vmm_backend"`
	// ReconcileIntervalSeconds is how often the pool reconciler runs (default 1).
	ReconcileIntervalSeconds int `toml:"reconcile_interval_seconds"`
	// MaxTotalVMs hard-caps tracked instances to avoid unbounded growth if destroy fails.
	// Default: max_ready + 32.
	MaxTotalVMs int `toml:"max_total_vms"`

	// MetricsListenAddr binds the agent local metrics/admin HTTP server (empty = disabled).
	// Prefer 127.0.0.1 for admin endpoints.
	MetricsListenAddr string `toml:"metrics_listen_addr"`

	// Optional TLS when control_url is https://
	TLSCAFile          string `toml:"tls_ca_file"`
	TLSCertFile        string `toml:"tls_cert_file"`
	TLSKeyFile         string `toml:"tls_key_file"`
	TLSInsecureSkipVerify bool `toml:"tls_insecure_skip_verify"`
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
	if c.MinReady <= 0 {
		c.MinReady = 1
	}
	if c.MaxReady <= 0 {
		c.MaxReady = c.MinReady
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
	if c.ReconcileIntervalSeconds <= 0 {
		c.ReconcileIntervalSeconds = 1
	}
	if c.MaxTotalVMs <= 0 {
		c.MaxTotalVMs = c.MaxReady + 32
	}
	if c.MaxTotalVMs < c.MaxReady {
		return fmt.Errorf("config: max_total_vms (%d) must be >= max_ready (%d)", c.MaxTotalVMs, c.MaxReady)
	}
	c.MetricsListenAddr = strings.TrimSpace(c.MetricsListenAddr)
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
