package control

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/mcp"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

func (s *Server) mountMCP() {
	h := mcp.New(s, s.mcpVersion()).Handler()
	wrapped := s.withMCPAuth(h)
	s.mux.Handle("/mcp", wrapped)
	s.mux.Handle("/mcp/", wrapped)
}

func (s *Server) mcpToken() string {
	if s.dash != nil && s.dash.Config != nil {
		return strings.TrimSpace(s.dash.Config.MCPToken)
	}
	return ""
}

func (s *Server) mcpVersion() string {
	if s.dash != nil && strings.TrimSpace(s.dash.Version) != "" {
		return strings.TrimSpace(s.dash.Version)
	}
	return "dev"
}

func (s *Server) withMCPAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.mcpToken()
		if token == "" {
			http.NotFound(w, r)
			return
		}
		auth := r.Header.Get(api.AgentAuthHeader)
		got := auth
		if strings.HasPrefix(auth, api.AgentBearerPrefix) {
			got = strings.TrimPrefix(auth, api.AgentBearerPrefix)
		}
		if !tokenEqual(got, token) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Overview implements mcp.Fleet.
func (s *Server) Overview() map[string]any {
	return s.overviewPayload()
}

// Hosts implements mcp.Fleet.
func (s *Server) Hosts() []api.AgentInfo {
	return s.agents.List()
}

// Jobs implements mcp.Fleet.
func (s *Server) Jobs(f mcp.JobFilter) []map[string]any {
	limit := f.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	list := s.store.ListRecent(100)
	now := time.Now().UTC()
	status := strings.ToLower(strings.TrimSpace(f.Status))
	repo := strings.ToLower(strings.TrimSpace(f.Repo))
	out := make([]map[string]any, 0, limit)
	for _, a := range list {
		if a == nil {
			continue
		}
		if status != "" && strings.ToLower(string(a.Status)) != status {
			continue
		}
		if repo != "" && !strings.Contains(strings.ToLower(a.RepoFullName), repo) {
			continue
		}
		row := s.jobListRow(a, now, false, nil)
		out = append(out, map[string]any{
			"job_id":            row.JobID,
			"run_id":            row.RunID,
			"org":               row.Org,
			"repo_full_name":    row.RepoFullName,
			"name":              row.Name,
			"workflow_name":     row.WorkflowName,
			"labels":            row.Labels,
			"status":            row.Status,
			"assigned_agent_id": row.AssignedAgentID,
			"vm_id":             row.VMID,
			"warm_bind":         row.WarmBind,
			"outcome":           row.Outcome,
			"error":             row.Error,
			"created_at":        row.CreatedAt,
			"assigned_at":       row.AssignedAt,
			"started_at":        row.StartedAt,
			"finished_at":       row.FinishedAt,
			"queue_ms":          row.QueueMS,
			"bind_ms":           row.BindMS,
			"run_ms":            row.RunMS,
			"total_ms":          row.TotalMS,
			"cache_hits":        row.CacheHits,
			"cache_misses":      row.CacheMisses,
		})
		if len(out) >= limit {
			break
		}
	}
	return out
}

// Job implements mcp.Fleet.
func (s *Server) Job(id int64) (map[string]any, error) {
	if id == 0 {
		return nil, mcp.ErrNotFound
	}
	a := s.store.Get(id)
	if a == nil {
		return nil, mcp.ErrNotFound
	}
	var logs *store.JobLog
	if db := s.jobDB(); db != nil {
		logs, _ = db.GetJobLog(id)
	}
	if logs == nil {
		logs = &store.JobLog{JobID: id, Events: []store.JobEvent{}}
	}
	tm := timingsFromAssignment(a, time.Now().UTC())
	payload := map[string]any{
		"ok": true,
		"job": map[string]any{
			"job_id":            a.JobID,
			"run_id":            a.RunID,
			"org":               a.Org,
			"repo_full_name":    a.RepoFullName,
			"labels":            a.Labels,
			"status":            string(a.Status),
			"assigned_agent_id": a.AssignedAgentID,
			"vm_id":             a.VMID,
			"warm_bind":         a.WarmBind,
			"outcome":           a.Outcome,
			"error":             a.Error,
			"created_at":        a.CreatedAt,
			"assigned_at":       a.AssignedAt,
			"started_at":        a.StartedAt,
			"finished_at":       a.FinishedAt,
			"runner_name":       a.RunnerName,
			"runner_id":         a.RunnerID,
			"queue_ms":          tm.QueueMS,
			"bind_ms":           tm.BindMS,
			"run_ms":            tm.RunMS,
			"total_ms":          tm.TotalMS,
			"cache_hits":        a.CacheHits,
			"cache_misses":      a.CacheMisses,
			"cache_bytes_in":    a.CacheBytesIn,
			"cache_bytes_out":   a.CacheBytesOut,
			"name":              a.Name,
			"workflow_name":     a.WorkflowName,
		},
	}
	src := map[string]any{
		"runner_log":   logs.RunnerLog,
		"agent_log":    logs.AgentLog,
		"console_log":  logs.ConsoleLog,
		"workflow_log": logs.WorkflowLog,
		"events":       logs.Events,
		"updated_at":   logs.UpdatedAt,
	}
	mcp.AttachTruncatedLogs(payload, src)
	return payload, nil
}

// VMs implements mcp.Fleet.
func (s *Server) VMs(agentID string) []map[string]any {
	agentID = strings.TrimSpace(agentID)
	snap := s.BuildSnapshot()
	out := make([]map[string]any, 0, len(snap.VMs))
	for _, v := range snap.VMs {
		if agentID != "" && v.AgentID != agentID {
			continue
		}
		out = append(out, map[string]any{
			"agent_id":    v.AgentID,
			"id":          v.ID,
			"state":       v.State,
			"job_id":      v.JobID,
			"vcpus":       v.VCPUs,
			"memory_mib":  v.MemoryMiB,
			"pid":         v.PID,
			"cpu_percent": v.CPUPercent,
			"rss_mib":     v.RSSMiB,
			"disk_mib":    v.DiskMiB,
			"created_at":  v.CreatedAt,
			"guest_ip":    v.GuestIP,
			"host_ip":     v.HostIP,
			"tap":         v.Tap,
			"shape":       v.Shape,
		})
	}
	return out
}

// VM implements mcp.Fleet.
func (s *Server) VM(id string) (map[string]any, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, mcp.ErrNotFound
	}
	var found *api.VMUsage
	agentID := ""
	for _, a := range s.agents.List() {
		for i := range a.VMs {
			if a.VMs[i].ID == id {
				cp := a.VMs[i]
				found = &cp
				agentID = a.AgentID
				break
			}
		}
		if found != nil {
			break
		}
	}
	if found == nil {
		return nil, mcp.ErrNotFound
	}
	var job any
	if found.JobID != "" {
		if jid, err := strconv.ParseInt(strings.TrimSpace(found.JobID), 10, 64); err == nil && jid != 0 {
			if a := s.store.Get(jid); a != nil {
				row := s.jobListRow(a, time.Now().UTC(), false, nil)
				job = row
			}
		}
	}
	return map[string]any{
		"ok":       true,
		"agent_id": agentID,
		"vm":       found,
		"job":      job,
	}, nil
}

// Cache implements mcp.Fleet.
func (s *Server) Cache() map[string]any {
	return s.cacheSnapshot()
}

// SystemStatus implements mcp.Fleet.
func (s *Server) SystemStatus() map[string]any {
	return s.systemStatusPayload()
}
