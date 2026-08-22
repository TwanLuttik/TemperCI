package github

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAPIBase = "https://api.github.com"

// Config configures a GitHub App API client.
type Config struct {
	AppID         int64
	PrivateKeyPEM []byte
	// InstallationID is used when generating installation tokens if not
	// overridden per-call. Webhook-driven flows pass the event installation id.
	InstallationID int64
	// BaseURL overrides api.github.com (tests). No trailing slash.
	BaseURL string
	// HTTPClient is optional; defaults to http.DefaultClient.
	HTTPClient *http.Client
}

// Client talks to the GitHub REST API as a GitHub App.
type Client struct {
	appID          int64
	key            *rsa.PrivateKey
	installationID int64
	baseURL        string
	http           *http.Client

	mu            sync.Mutex
	cachedToken   string
	cachedExpiry  time.Time
	cachedInstall int64
}

// NewClient builds a Client from Config.
func NewClient(cfg Config) (*Client, error) {
	if cfg.AppID == 0 {
		return nil, fmt.Errorf("github: AppID is required")
	}
	key, err := ParseRSAPrivateKeyPEM(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	base := cfg.BaseURL
	if base == "" {
		base = defaultAPIBase
	}
	base = strings.TrimRight(base, "/")
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{
		appID:          cfg.AppID,
		key:            key,
		installationID: cfg.InstallationID,
		baseURL:        base,
		http:           hc,
	}, nil
}

// GenerateJITConfigRequest is the body for org-level generate-jitconfig.
type GenerateJITConfigRequest struct {
	Org           string
	Name          string
	RunnerGroupID int64
	Labels        []string
	WorkFolder    string
	// InstallationID overrides the client default when non-zero.
	InstallationID int64
}

// GenerateJITConfigResponse is the subset of the API response we keep.
type GenerateJITConfigResponse struct {
	Runner           RunnerInfo `json:"runner"`
	EncodedJITConfig string     `json:"encoded_jit_config"`
}

// RunnerInfo is the runner metadata returned with a JIT config.
type RunnerInfo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// GenerateJITConfig mints a just-in-time runner config for the organization.
func (c *Client) GenerateJITConfig(ctx context.Context, req GenerateJITConfigRequest) (*GenerateJITConfigResponse, error) {
	if req.Org == "" {
		return nil, fmt.Errorf("github: org is required")
	}
	if len(req.Labels) == 0 {
		return nil, fmt.Errorf("github: labels are required")
	}
	if req.RunnerGroupID == 0 {
		return nil, fmt.Errorf("github: runner_group_id is required")
	}
	if req.Name == "" {
		req.Name = "temperci"
	}
	if req.WorkFolder == "" {
		req.WorkFolder = "_work"
	}

	installID := req.InstallationID
	if installID == 0 {
		installID = c.installationID
	}
	if installID == 0 {
		return nil, fmt.Errorf("github: installation id is required")
	}

	token, err := c.installationToken(ctx, installID)
	if err != nil {
		return nil, err
	}

	body := map[string]any{
		"name":            req.Name,
		"runner_group_id": req.RunnerGroupID,
		"labels":          req.Labels,
		"work_folder":     req.WorkFolder,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := c.baseURL + "/orgs/" + req.Org + "/actions/runners/generate-jitconfig"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("github: generate-jitconfig request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github: generate-jitconfig: status %d: %s", resp.StatusCode, truncate(string(respBody), 256))
	}
	var out GenerateJITConfigResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("github: decode generate-jitconfig: %w", err)
	}
	if out.EncodedJITConfig == "" {
		return nil, fmt.Errorf("github: generate-jitconfig response missing encoded_jit_config")
	}
	return &out, nil
}

func (c *Client) installationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	if c.cachedToken != "" && c.cachedInstall == installationID && time.Now().Before(c.cachedExpiry.Add(-2*time.Minute)) {
		tok := c.cachedToken
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	jwtStr, err := mintAppJWT(c.appID, c.key, time.Now())
	if err != nil {
		return "", err
	}

	url := c.baseURL + "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+jwtStr)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("github: installation token request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github: installation token: status %d: %s", resp.StatusCode, truncate(string(respBody), 256))
	}
	var tok installationTokenResponse
	if err := json.Unmarshal(respBody, &tok); err != nil {
		return "", fmt.Errorf("github: decode installation token: %w", err)
	}
	if tok.Token == "" {
		return "", fmt.Errorf("github: installation token response missing token")
	}

	c.mu.Lock()
	c.cachedToken = tok.Token
	c.cachedExpiry = tok.ExpiresAt
	if c.cachedExpiry.IsZero() {
		c.cachedExpiry = time.Now().Add(50 * time.Minute)
	}
	c.cachedInstall = installationID
	c.mu.Unlock()
	return tok.Token, nil
}

// WorkflowJobDetail is GET /repos/{owner}/{repo}/actions/jobs/{job_id}.
type WorkflowJobDetail struct {
	ID         int64             `json:"id"`
	Name       string            `json:"name"`
	Status     string            `json:"status"`
	Conclusion string            `json:"conclusion"`
	HTMLURL    string            `json:"html_url"`
	Steps      []WorkflowJobStep `json:"steps"`
}

// WorkflowJobStep is one step inside a GitHub Actions job.
type WorkflowJobStep struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	Number      int    `json:"number"`
	StartedAt   string `json:"started_at,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

// GetJob fetches a GitHub Actions job including its step list.
// installationID may be 0 to use the client default.
func (c *Client) GetJob(ctx context.Context, owner, repo string, jobID, installationID int64) (*WorkflowJobDetail, error) {
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("github: owner and repo are required")
	}
	if jobID == 0 {
		return nil, fmt.Errorf("github: job id is required")
	}
	installID := installationID
	if installID == 0 {
		installID = c.installationID
	}
	if installID == 0 {
		return nil, fmt.Errorf("github: installation id is required")
	}
	token, err := c.installationToken(ctx, installID)
	if err != nil {
		return nil, err
	}
	url := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/jobs/" + strconv.FormatInt(jobID, 10)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("github: get job request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("github: read job: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github: get job: status %d: %s", resp.StatusCode, truncate(string(body), 256))
	}
	var out WorkflowJobDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: decode job: %w", err)
	}
	if out.ID == 0 {
		out.ID = jobID
	}
	return &out, nil
}

// WorkflowRun is GET /repos/{owner}/{repo}/actions/runs/{run_id}.
type WorkflowRun struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// GetRun fetches a GitHub Actions workflow run (workflow title / path).
func (c *Client) GetRun(ctx context.Context, owner, repo string, runID, installationID int64) (*WorkflowRun, error) {
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("github: owner and repo are required")
	}
	if runID == 0 {
		return nil, fmt.Errorf("github: run id is required")
	}
	installID := installationID
	if installID == 0 {
		installID = c.installationID
	}
	if installID == 0 {
		return nil, fmt.Errorf("github: installation id is required")
	}
	token, err := c.installationToken(ctx, installID)
	if err != nil {
		return nil, err
	}
	url := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/runs/" + strconv.FormatInt(runID, 10)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("github: get run request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("github: read run: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("github: get run: status %d: %s", resp.StatusCode, truncate(string(body), 256))
	}
	var out WorkflowRun
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("github: decode run: %w", err)
	}
	return &out, nil
}

const maxJobLogBytes = 2 << 20

// DownloadJobLogs fetches the official GitHub Actions job log (step output).
// installationID may be 0 to use the client default.
func (c *Client) DownloadJobLogs(ctx context.Context, owner, repo string, jobID, installationID int64) (string, error) {
	if owner == "" || repo == "" {
		return "", fmt.Errorf("github: owner and repo are required")
	}
	if jobID == 0 {
		return "", fmt.Errorf("github: job id is required")
	}
	installID := installationID
	if installID == 0 {
		installID = c.installationID
	}
	if installID == 0 {
		return "", fmt.Errorf("github: installation id is required")
	}
	token, err := c.installationToken(ctx, installID)
	if err != nil {
		return "", err
	}
	url := c.baseURL + "/repos/" + owner + "/" + repo + "/actions/jobs/" + strconv.FormatInt(jobID, 10) + "/logs"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("github: job logs request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJobLogBytes+1))
	if err != nil {
		return "", fmt.Errorf("github: read job logs: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github: job logs: status %d: %s", resp.StatusCode, truncate(string(body), 256))
	}
	if len(body) > maxJobLogBytes {
		body = body[len(body)-maxJobLogBytes:]
	}
	text := string(body)
	text = strings.TrimPrefix(text, "\uFEFF")
	return text, nil
}

// DeleteOrgRunner removes an organization self-hosted runner by id.
// Used to clean stuck JIT registrations that never completed a job.
// A 404 response is treated as success (already gone).
// installationID may be 0 to use the client default.
func (c *Client) DeleteOrgRunner(ctx context.Context, org string, runnerID, installationID int64) error {
	if org == "" {
		return fmt.Errorf("github: org is required")
	}
	if runnerID == 0 {
		return fmt.Errorf("github: runner id is required")
	}
	installID := installationID
	if installID == 0 {
		installID = c.installationID
	}
	if installID == 0 {
		return fmt.Errorf("github: installation id is required")
	}
	token, err := c.installationToken(ctx, installID)
	if err != nil {
		return err
	}
	url := c.baseURL + "/orgs/" + org + "/actions/runners/" + strconv.FormatInt(runnerID, 10)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Accept", "application/vnd.github+json")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("github: delete runner request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github: delete runner: status %d: %s", resp.StatusCode, truncate(string(respBody), 256))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
