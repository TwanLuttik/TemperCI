package control

import (
	"context"
	"sort"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/github"
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
	JobID           int64                    `json:"job_id"`
	RunID           int64                    `json:"run_id"`
	Org             string                   `json:"org"`
	RepoFullName    string                   `json:"repo_full_name"`
	Name            string                   `json:"name,omitempty"`
	WorkflowName    string                   `json:"workflow_name,omitempty"`
	Labels          []string                 `json:"labels"`
	Status          string                   `json:"status"`
	AssignedAgentID string                   `json:"assigned_agent_id,omitempty"`
	VMID            string                   `json:"vm_id,omitempty"`
	WarmBind        bool                     `json:"warm_bind,omitempty"`
	Outcome         string                   `json:"outcome,omitempty"`
	Error           string                   `json:"error,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	AssignedAt      time.Time                `json:"assigned_at,omitempty"`
	StartedAt       time.Time                `json:"started_at,omitempty"`
	FinishedAt      time.Time                `json:"finished_at,omitempty"`
	QueueMS         int64                    `json:"queue_ms,omitempty"`
	BindMS          int64                    `json:"bind_ms,omitempty"`
	RunMS           int64                    `json:"run_ms,omitempty"`
	TotalMS         int64                    `json:"total_ms,omitempty"`
	CacheHits       int                      `json:"cache_hits,omitempty"`
	CacheMisses     int                      `json:"cache_misses,omitempty"`
	Steps           []github.WorkflowJobStep `json:"steps,omitempty"`
}

func (s *Server) jobListRow(a *Assignment, now time.Time, fetchMeta bool, req interface{ Context() context.Context }) jobRowWS {
	if a == nil {
		return jobRowWS{}
	}
	tm := timingsFromAssignment(a, now)
	return jobRowWS{
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
		Error:           a.Error,
		CreatedAt:       a.CreatedAt,
		AssignedAt:      a.AssignedAt,
		StartedAt:       a.StartedAt,
		FinishedAt:      a.FinishedAt,
		QueueMS:         tm.QueueMS,
		BindMS:          tm.BindMS,
		RunMS:           tm.RunMS,
		TotalMS:         tm.TotalMS,
		CacheHits:       a.CacheHits,
		CacheMisses:     a.CacheMisses,
		Steps:           s.jobListSteps(a, fetchMeta, req),
	}
}

func (s *Server) jobListSteps(a *Assignment, fetchMeta bool, req interface{ Context() context.Context }) []github.WorkflowJobStep {
	if a == nil {
		return nil
	}
	if fetchMeta && (a.Status == AssignmentStarted || a.Status == AssignmentAssigned) {
		if meta := s.ensureJobMeta(req, a); meta != nil && len(meta.Steps) > 0 {
			return meta.Steps
		}
	}
	if meta := s.cachedJob(a.JobID); meta != nil && len(meta.Steps) > 0 {
		return meta.Steps
	}
	return nil
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
	GuestIP    string    `json:"guest_ip,omitempty"`
	HostIP     string    `json:"host_ip,omitempty"`
	Tap        string    `json:"tap,omitempty"`
	Shape      string    `json:"shape,omitempty"`
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
				GuestIP:    v.GuestIP,
				HostIP:     v.HostIP,
				Tap:        v.TapDevice,
				Shape:      v.Shape,
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
		jobs = append(jobs, s.jobListRow(a, now, false, nil))
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

type jobLogsWS struct {
	Type           string    `json:"type"`
	Time           time.Time `json:"time"`
	JobID          int64     `json:"job_id"`
	RunnerLog      string    `json:"runner_log,omitempty"`
	AgentLog       string    `json:"agent_log,omitempty"`
	ConsoleLog     string    `json:"console_log,omitempty"`
	WorkflowLog    string    `json:"workflow_log,omitempty"`
	WorkflowOffset int       `json:"workflow_offset,omitempty"`
	WorkflowAppend string    `json:"workflow_append,omitempty"`
}

// PublishJobLogs pushes the current job log blob to dashboard sockets.
// The frame is a full snapshot of the fields provided (plus stored
// workflow_log when only an append arrived) so a dropped frame is safe.
func (s *Server) PublishJobLogs(jobID int64, runner, agent, console, workflow string) {
	if s == nil || s.hub == nil || jobID == 0 {
		return
	}
	if s.hub.ClientCount() == 0 && s.hub.onSend == nil {
		return
	}
	if live := s.liveWorkflow(jobID); live != "" && (workflow == "" || len(live) >= len(workflow)) {
		workflow = live
	}
	if workflow == "" {
		if db := s.jobDB(); db != nil {
			if cur, err := db.GetJobLog(jobID); err == nil {
				workflow = cur.WorkflowLog
			}
		}
	}
	if runner == "" && agent == "" && console == "" && workflow == "" {
		return
	}
	s.hub.BroadcastJSON(jobLogsWS{
		Type:        "job_logs",
		Time:        time.Now().UTC(),
		JobID:       jobID,
		RunnerLog:   runner,
		AgentLog:    agent,
		ConsoleLog:  console,
		WorkflowLog: workflow,
	})
}

// PublishJobLogsDelta pushes new workflow bytes. Dropped frames are healed
// by the next full snapshot (REST, persist tick, or finished upload).
func (s *Server) PublishJobLogsDelta(jobID int64, offset int, chunk string) {
	if s == nil || s.hub == nil || jobID == 0 || chunk == "" {
		return
	}
	if s.hub.ClientCount() == 0 && s.hub.onSend == nil {
		return
	}
	s.hub.BroadcastJSON(jobLogsWS{
		Type:           "job_logs",
		Time:           time.Now().UTC(),
		JobID:          jobID,
		WorkflowOffset: offset,
		WorkflowAppend: chunk,
	})
}

// PublishSnapshot pushes current state to all dashboard WebSocket clients.
func (s *Server) PublishSnapshot() {
	if s.hub == nil {
		return
	}
	if s.hub.ClientCount() == 0 {
		return
	}
	snap := s.BuildSnapshot()
	if snap.Overview != nil {
		snap.Overview["ws_clients"] = s.hub.ClientCount()
	}
	s.hub.BroadcastJSON(snap)
}
