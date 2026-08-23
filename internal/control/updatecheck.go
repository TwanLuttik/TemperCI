package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultReleaseRepo = "TwanLuttik/TemperCI"
	defaultGitHubAPI   = "https://api.github.com"
	defaultUpdateTTL   = 6 * time.Hour
	defaultUpdateFail  = 15 * time.Minute
)

// VersionStatus is the dashboard payload for the running build and latest release.
type VersionStatus struct {
	OK              bool   `json:"ok"`
	Version         string `json:"version"`
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	ReleaseURL      string `json:"release_url,omitempty"`
	CheckedAt       string `json:"checked_at,omitempty"`
	CheckError      string `json:"check_error,omitempty"`
}

// UpdateCheckerConfig wires a GitHub latest-release probe with a TTL cache.
type UpdateCheckerConfig struct {
	Current string
	Repo    string // owner/name; empty uses TwanLuttik/TemperCI
	APIBase string // tests; empty uses api.github.com
	Client  *http.Client
	TTL     time.Duration
	FailTTL time.Duration
	Now     func() time.Time
}

// UpdateChecker fetches GitHub /releases/latest at most once per TTL.
type UpdateChecker struct {
	current string
	repo    string
	apiBase string
	http    *http.Client
	ttl     time.Duration
	failTTL time.Duration
	now     func() time.Time

	mu      sync.Mutex
	ready   chan struct{}
	cached  VersionStatus
	expires time.Time
	etag    string
}

// NewUpdateChecker builds a checker. Zero values get production defaults.
func NewUpdateChecker(cfg UpdateCheckerConfig) *UpdateChecker {
	current := strings.TrimSpace(cfg.Current)
	if current == "" {
		current = "dev"
	}
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = defaultReleaseRepo
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.APIBase), "/")
	if base == "" {
		base = defaultGitHubAPI
	}
	hc := cfg.Client
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultUpdateTTL
	}
	failTTL := cfg.FailTTL
	if failTTL <= 0 {
		failTTL = defaultUpdateFail
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &UpdateChecker{
		current: current,
		repo:    repo,
		apiBase: base,
		http:    hc,
		ttl:     ttl,
		failTTL: failTTL,
		now:     now,
	}
}

// Status returns the running version plus a cached latest-release snapshot.
func (c *UpdateChecker) Status(ctx context.Context) VersionStatus {
	if c == nil {
		return VersionStatus{OK: true, Version: "dev"}
	}
	c.mu.Lock()
	now := c.now()
	if !c.expires.IsZero() && now.Before(c.expires) {
		st := c.cached
		c.mu.Unlock()
		return st
	}
	if c.ready != nil {
		ch := c.ready
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return c.currentOnly("check cancelled")
		}
		c.mu.Lock()
		st := c.cached
		c.mu.Unlock()
		return st
	}
	ready := make(chan struct{})
	c.ready = ready
	etag := c.etag
	prev := c.cached
	c.mu.Unlock()

	st := c.refresh(ctx, etag, prev)

	c.mu.Lock()
	c.cached = st
	if st.CheckError != "" && prev.Latest == "" {
		c.expires = c.now().Add(c.failTTL)
	} else {
		c.expires = c.now().Add(c.ttl)
	}
	close(ready)
	c.ready = nil
	c.mu.Unlock()
	return st
}

func (c *UpdateChecker) currentOnly(err string) VersionStatus {
	c.mu.Lock()
	st := c.cached
	c.mu.Unlock()
	if st.Version == "" {
		st.Version = c.current
		st.OK = true
	}
	if err != "" && st.CheckError == "" {
		st.CheckError = err
	}
	return st
}

func (c *UpdateChecker) refresh(ctx context.Context, etag string, prev VersionStatus) VersionStatus {
	st := VersionStatus{OK: true, Version: c.current}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase+"/repos/"+c.repo+"/releases/latest", nil)
	if err != nil {
		st.CheckError = err.Error()
		return mergePrev(st, prev)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "TemperCI/"+c.current)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		st.CheckError = err.Error()
		return mergePrev(st, prev)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		out := mergePrev(st, prev)
		out.CheckedAt = c.now().UTC().Format(time.RFC3339)
		out.CheckError = ""
		return out
	}
	if resp.StatusCode != http.StatusOK {
		st.CheckError = fmt.Sprintf("github releases: HTTP %d", resp.StatusCode)
		return mergePrev(st, prev)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		st.CheckError = err.Error()
		return mergePrev(st, prev)
	}
	tag := strings.TrimSpace(body.TagName)
	st.Latest = tag
	st.ReleaseURL = strings.TrimSpace(body.HTMLURL)
	st.UpdateAvailable = updateAvailable(c.current, tag)
	st.CheckedAt = c.now().UTC().Format(time.RFC3339)
	if next := strings.TrimSpace(resp.Header.Get("ETag")); next != "" {
		c.mu.Lock()
		c.etag = next
		c.mu.Unlock()
	}
	return st
}

func mergePrev(st, prev VersionStatus) VersionStatus {
	if st.Latest == "" && prev.Latest != "" {
		st.Latest = prev.Latest
		st.ReleaseURL = prev.ReleaseURL
		st.UpdateAvailable = updateAvailable(st.Version, prev.Latest)
		if st.CheckedAt == "" {
			st.CheckedAt = prev.CheckedAt
		}
	}
	return st
}

func updateAvailable(current, latest string) bool {
	lt, okL := parseSemver(latest)
	if !okL {
		return false
	}
	cur, okC := parseSemver(current)
	if !okC {
		return true
	}
	return compareSemver(lt, cur) > 0
}

type semver struct{ major, minor, patch int }

func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 3 {
		return semver{}, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return semver{}, false
	}
	if maj < 0 || min < 0 || pat < 0 {
		return semver{}, false
	}
	return semver{maj, min, pat}, true
}

func compareSemver(a, b semver) int {
	if a.major != b.major {
		return a.major - b.major
	}
	if a.minor != b.minor {
		return a.minor - b.minor
	}
	return a.patch - b.patch
}
