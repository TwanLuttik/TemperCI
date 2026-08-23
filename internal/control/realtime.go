package control

import (
	"sort"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// RealtimeSnapshot is pushed over the dashboard WebSocket.
type RealtimeSnapshot struct {
	Type     string          `json:"type"` // "snapshot"
	Time     time.Time       `json:"time"`
	Overview map[string]any  `json:"overview"`
	Hosts    []api.AgentInfo `json:"hosts"`
	Jobs     []jobRowWS      `json:"jobs"`
	VMs      []vmRowWS       `json:"vms"`
}

type jobRowWS struct {
	JobID           int64     `json:"job_id"`
	RunID           int64     `json:"run_id"`
	Org             string    `json:"org"`
	RepoFullName    string    `json:"repo_full_name"`
	Name            string    `json:"name,omitempty"`
	WorkflowName    string    `json:"workflow_name,omitempty"`
	Labels          []string  `json:"labels"`
	Status          string    `json:"status"`
	AssignedAgentID string    `json:"assigned_agent_id,omitempty"`
	VMID            string    `json:"vm_id,omitempty"`
	WarmBind        bool      `json:"warm_bind,omitempty"`
	Outcome         string    `json:"outcome,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	QueueMS         int64     `json:"queue_ms,omitempty"`
	BindMS          int64     `json:"bind_ms,omitempty"`
	RunMS           int64     `json:"run_ms,omitempty"`
	TotalMS         int64     `json:"total_ms,omitempty"`
	CacheHits       int       `json:"cache_hits,omitempty"`
	CacheMisses     int       `json:"cache_misses,omitempty"`
}

type vmRowWS struct {
	AgentID    string    `json:"agent_id"`
	ID         string    `json:"id"`
	State      string    `json:"state"`
	JobID      string    `json:"job_id,omitempty"`
	VCPUs      int       `json:"vcpus"`
	MemoryMiB  int       `json:"memory_mib"`
	PID        int       `json:"pid,omitempty"`
	CPUPercent float64   `json:"cpu_percent"`
	RSSMiB     float64   `json:"rss_mib"`
	DiskMiB    float64   `json:"disk_mib,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
}

// BuildSnapshot assembles the current fleet view for WebSocket clients.
func (s *Server) BuildSnapshot() RealtimeSnapshot {
	counts := s.store.CountByStatus()
	agents := s.agents.List()
	var warm, busy int
	var vms []vmRowWS
	for _, a := range agents {
		warm += a.Warm
		busy += a.Busy
		for _, v := range a.VMs {
			vms = append(vms, vmRowWS{
				AgentID:    a.AgentID,
				ID:         v.ID,
				State:      v.State,
				JobID:      v.JobID,
				VCPUs:      v.VCPUs,
				MemoryMiB:  v.MemoryMiB,
				PID:        v.PID,
				CPUPercent: v.CPUPercent,
				RSSMiB:     v.RSSMiB,
				DiskMiB:    v.DiskMiB,
				CreatedAt:  v.CreatedAt,
			})
		}
	}
	sort.SliceStable(vms, func(i, j int) bool {
		ai, aj := vms[i].CreatedAt, vms[j].CreatedAt
		zi, zj := ai.IsZero(), aj.IsZero()
		if zi != zj {
			return !zi
		}
		if !zi && !ai.Equal(aj) {
			return ai.Before(aj)
		}
		if vms[i].AgentID != vms[j].AgentID {
			return vms[i].AgentID < vms[j].AgentID
		}
		return vms[i].ID < vms[j].ID
	})
	list := s.store.ListRecent(100)
	now := time.Now().UTC()
	jobs := make([]jobRowWS, 0, len(list))
	for _, a := range list {
		tm := timingsFromAssignment(a, now)
		jobs = append(jobs, jobRowWS{
			JobID:           a.JobID,
			RunID:           a.RunID,
			Org:             a.Org,
			RepoFullName:    a.RepoFullName,
			Name:            a.Name,
			WorkflowName:    a.WorkflowName,
			Labels:          a.Labels,
			Status:          string(a.Status),
			AssignedAgentID: a.AssignedAgentID,
			VMID:            a.VMID,
			WarmBind:        a.WarmBind,
			Outcome:         a.Outcome,
			CreatedAt:       a.CreatedAt,
			QueueMS:         tm.QueueMS,
			BindMS:          tm.BindMS,
			RunMS:           tm.RunMS,
			TotalMS:         tm.TotalMS,
			CacheHits:       a.CacheHits,
			CacheMisses:     a.CacheMisses,
		})
	}
	p50, p95 := recentRunPercentiles(list)
	cacheHits, cacheMisses, _, _ := recentCacheTotals(list)
	var cacheBytes, cacheMax int64
	for _, a := range agents {
		if a.Cache != nil {
			cacheBytes += a.Cache.Bytes
			cacheMax += a.Cache.MaxBytes
		}
	}
	org := ""
	fleetReady := false
	listen := ""
	if s.dash != nil && s.dash.Config != nil {
		org = s.dash.Config.GitHubOrg
		fleetReady = s.dash.FleetReady
		listen = s.dash.Config.ListenAddr
	}
	wh := s.webhookSnapshot("", listen)
	received, _ := wh["received"].(bool)
	lastEvent, _ := wh["last_event"].(string)
	return RealtimeSnapshot{
		Type: "snapshot",
		Time: time.Now().UTC(),
		Overview: map[string]any{
			"fleet_ready":        fleetReady,
			"org":                org,
			"webhook_received":   received,
			"webhook_last_event": lastEvent,
			"agents_registered":  len(agents),
			"warm":               warm,
			"busy":               busy,
			"jobs_pending":       s.store.PendingLen(),
			"jobs_minted":        counts.Minted,
			"jobs_assigned":      counts.Assigned,
			"jobs_started":       counts.Started,
			"jobs_finished":      counts.Finished,
			"jobs_failed":        counts.Failed,
			"run_p50_ms":         p50,
			"run_p95_ms":         p95,
			"cache_hits":         cacheHits,
			"cache_misses":       cacheMisses,
			"cache_bytes":        cacheBytes,
			"cache_max_bytes":    cacheMax,
			"ws_clients":         0,
		},
		Hosts: agents,
		Jobs:  jobs,
		VMs:   vms,
	}
}

// PublishSnapshot pushes current state to all dashboard WebSocket clients.
func (s *Server) PublishSnapshot() {
	if s.hub == nil {
		return
	}
	snap := s.BuildSnapshot()
	if snap.Overview != nil {
		snap.Overview["ws_clients"] = s.hub.ClientCount()
	}
	s.hub.BroadcastJSON(snap)
}
