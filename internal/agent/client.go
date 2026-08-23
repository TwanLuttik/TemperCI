package agent

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// ControlClient talks to the control-plane agent API.
type ControlClient struct {
	BaseURL    string
	AgentID    string
	Token      string
	HTTPClient *http.Client
}

// ClientTLSConfig configures optional HTTPS verification and mTLS for the agent.
type ClientTLSConfig struct {
	// CAFile verifies the control-plane server certificate (recommended for HTTPS).
	CAFile string
	// CertFile + KeyFile present a client certificate (mTLS).
	CertFile string
	KeyFile  string
	// InsecureSkipVerify disables server cert verification (dev only).
	InsecureSkipVerify bool
}

// NewControlClient builds a client. httpClient may be nil (defaults with timeout).
func NewControlClient(baseURL, agentID, token string, httpClient *http.Client) *ControlClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &ControlClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		AgentID:    agentID,
		Token:      token,
		HTTPClient: httpClient,
	}
}

// NewHTTPClientTLS builds an HTTP client with optional TLS/mTLS settings.
func NewHTTPClientTLS(tlsCfg ClientTLSConfig) (*http.Client, error) {
	t := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: tlsCfg.InsecureSkipVerify} //nolint:gosec // explicit operator opt-in for lab TLS
	if tlsCfg.CAFile != "" {
		pem, err := os.ReadFile(tlsCfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("agent: read TLS CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("agent: no certificates in TLS CA file")
		}
		t.RootCAs = pool
	}
	if tlsCfg.CertFile != "" && tlsCfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(tlsCfg.CertFile, tlsCfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("agent: load client cert: %w", err)
		}
		t.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: t,
		},
	}, nil
}

// CapacitySnapshot is the agent pool view sent on register/claim.
type CapacitySnapshot struct {
	MaxCapacity int
	FreeSlots   int
	Warm        int
	Busy        int
	VMs         []api.VMUsage
	CachedRepos []string
	Cache       *api.CacheUsage
	Resources   *api.HostResources
}

// Register announces this agent to the control plane with capacity.
// Returned cache ops should be applied locally (may be empty).
func (c *ControlClient) Register(ctx context.Context, cap CapacitySnapshot) ([]api.CacheOp, []api.AgentCmd, error) {
	req := api.RegisterRequest{
		AgentID:     c.AgentID,
		MaxCapacity: cap.MaxCapacity,
		Capacity:    cap.FreeSlots,
		Warm:        cap.Warm,
		Busy:        cap.Busy,
		VMs:         cap.VMs,
		CachedRepos: cap.CachedRepos,
		Cache:       cap.Cache,
		Resources:   cap.Resources,
	}
	var resp api.RegisterResponse
	if err := c.post(ctx, "/v1/agent/register", req, &resp); err != nil {
		return nil, nil, err
	}
	if !resp.OK {
		return nil, nil, fmt.Errorf("agent: register not ok")
	}
	return resp.CacheOps, resp.Commands, nil
}

// Claim requests the next pending job when free slots remain. Returns nil,nil when no work.
func (c *ControlClient) Claim(ctx context.Context, cap CapacitySnapshot) (*api.JobAssignment, error) {
	req := api.ClaimRequest{
		AgentID:     c.AgentID,
		FreeSlots:   cap.FreeSlots,
		Warm:        cap.Warm,
		Busy:        cap.Busy,
		CachedRepos: cap.CachedRepos,
	}
	var resp api.ClaimResponse
	if err := c.post(ctx, "/v1/agent/jobs/claim", req, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, fmt.Errorf("agent: claim not ok")
	}
	return resp.Job, nil
}

// ReportStarted notifies control that the runner was bound/started.
func (c *ControlClient) ReportStarted(ctx context.Context, jobID int64, vmID string, warmBind bool) error {
	req := api.JobStartedRequest{
		AgentID:  c.AgentID,
		JobID:    jobID,
		VMID:     vmID,
		WarmBind: warmBind,
	}
	var resp api.JobStartedResponse
	return c.post(ctx, "/v1/agent/jobs/started", req, &resp)
}

// ReportFinished notifies control of a terminal job outcome.
func (c *ControlClient) ReportFinished(ctx context.Context, jobID int64, outcome, vmID string, warmBind bool, errMsg string) error {
	return c.ReportFinishedLogs(ctx, jobID, outcome, vmID, warmBind, errMsg, JobLogs{})
}

// ReportFinishedLogs is ReportFinished plus guest diagnostic logs.
func (c *ControlClient) ReportFinishedLogs(ctx context.Context, jobID int64, outcome, vmID string, warmBind bool, errMsg string, logs JobLogs) error {
	req := api.JobFinishedRequest{
		AgentID:       c.AgentID,
		JobID:         jobID,
		Outcome:       outcome,
		VMID:          vmID,
		WarmBind:      warmBind,
		Error:         errMsg,
		RunnerLog:     logs.RunnerLog,
		AgentLog:      logs.AgentLog,
		ConsoleLog:    logs.ConsoleLog,
		WorkflowLog:   logs.WorkflowLog,
		CacheHits:     logs.CacheHits,
		CacheMisses:   logs.CacheMisses,
		CacheBytesIn:  logs.CacheBytesIn,
		CacheBytesOut: logs.CacheBytesOut,
	}
	var resp api.JobFinishedResponse
	return c.post(ctx, "/v1/agent/jobs/finished", req, &resp)
}

// ReportLogs uploads incremental guest logs while a job is still running.
func (c *ControlClient) ReportLogs(ctx context.Context, jobID int64, logs JobLogs) error {
	req := api.JobLogsRequest{
		AgentID:     c.AgentID,
		JobID:       jobID,
		RunnerLog:   logs.RunnerLog,
		AgentLog:    logs.AgentLog,
		ConsoleLog:  logs.ConsoleLog,
		WorkflowLog: logs.WorkflowLog,
	}
	var resp api.JobLogsResponse
	return c.post(ctx, "/v1/agent/jobs/logs", req, &resp)
}

func (c *ControlClient) post(ctx context.Context, path string, body any, out any) error {
	if c.BaseURL == "" {
		return fmt.Errorf("agent: control base URL empty")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		httpReq.Header.Set(api.AgentAuthHeader, api.AgentBearerPrefix+c.Token)
	}
	res, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("agent: %s: %w", path, err)
	}
	defer res.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("agent: %s: status %d: %s", path, res.StatusCode, truncate(string(respBody), 200))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("agent: %s: decode: %w", path, err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
