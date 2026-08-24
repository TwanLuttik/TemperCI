package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
)

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func toolDefs() []toolDef {
	empty := map[string]any{"type": "object", "properties": map[string]any{}}
	return []toolDef{
		{
			Name:        "fleet_overview",
			Description: "TemperCI fleet health: org, warm/busy VMs, job counts, cache hit rate, webhook status.",
			InputSchema: empty,
		},
		{
			Name:        "list_hosts",
			Description: "Registered host agents with capacity, warm/busy counts, last seen, and host resources.",
			InputSchema: empty,
		},
		{
			Name:        "list_jobs",
			Description: "Recent GitHub Actions jobs TemperCI ran. Filter by status (minted, assigned, started, finished, failed) or repo.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{"type": "string", "description": "Assignment status filter"},
					"repo":   map[string]any{"type": "string", "description": "org/repo substring match"},
					"limit":  map[string]any{"type": "integer", "description": "Max jobs (default 20, max 100)"},
				},
			},
		},
		{
			Name:        "get_job",
			Description: "One job: metadata, timings, outcome, and truncated runner/agent/console/workflow logs.",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []string{"job_id"},
				"properties": map[string]any{"job_id": map[string]any{"type": "integer", "description": "GitHub workflow job id"}},
			},
		},
		{
			Name:        "list_vms",
			Description: "Live microVMs reported by agents (state, CPU, RSS, bound job).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"agent_id": map[string]any{"type": "string", "description": "Restrict to one host agent"}},
			},
		},
		{
			Name:        "get_vm",
			Description: "One microVM plus its bound job if any.",
			InputSchema: map[string]any{
				"type":       "object",
				"required":   []string{"vm_id"},
				"properties": map[string]any{"vm_id": map[string]any{"type": "string"}},
			},
		},
		{
			Name:        "get_cache",
			Description: "Host-local Actions cache inventory (bytes, repos, keys) across agents.",
			InputSchema: empty,
		},
		{
			Name:        "get_system_status",
			Description: "Control-plane and host-agent process/systemd status.",
			InputSchema: empty,
		},
	}
}

func (s *Server) callTool(params json.RawMessage) map[string]any {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return toolError("invalid tools/call params")
	}
	args := map[string]any{}
	if len(p.Arguments) > 0 && string(p.Arguments) != "null" {
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return toolError("invalid arguments")
		}
	}
	if s.fleet == nil {
		return toolError("fleet unavailable")
	}
	var (
		out any
		err error
	)
	switch p.Name {
	case "fleet_overview":
		out = s.fleet.Overview()
	case "list_hosts":
		out = map[string]any{"hosts": s.fleet.Hosts()}
	case "list_jobs":
		out = map[string]any{"jobs": s.fleet.Jobs(parseJobFilter(args))}
	case "get_job":
		id, ok := intArg(args, "job_id")
		if !ok || id == 0 {
			return toolError("job_id is required")
		}
		out, err = s.fleet.Job(id)
	case "list_vms":
		agentID, _ := args["agent_id"].(string)
		out = map[string]any{"vms": s.fleet.VMs(strings.TrimSpace(agentID))}
	case "get_vm":
		id, _ := args["vm_id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			return toolError("vm_id is required")
		}
		out, err = s.fleet.VM(id)
	case "get_cache":
		out = s.fleet.Cache()
	case "get_system_status":
		out = s.fleet.SystemStatus()
	default:
		return toolError("unknown tool: " + p.Name)
	}
	if err != nil {
		return toolError(err.Error())
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolError("encode result")
	}
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(raw)}},
	}
}

func parseJobFilter(args map[string]any) JobFilter {
	f := JobFilter{}
	if s, ok := args["status"].(string); ok {
		f.Status = strings.TrimSpace(s)
	}
	if s, ok := args["repo"].(string); ok {
		f.Repo = strings.TrimSpace(s)
	}
	if n, ok := intArg(args, "limit"); ok {
		f.Limit = int(n)
	}
	return f
}

func intArg(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

func toolError(msg string) map[string]any {
	return map[string]any{
		"isError": true,
		"content": []map[string]any{{"type": "text", "text": msg}},
	}
}

func truncateTail(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	return s[len(s)-max:], true
}

// AttachTruncatedLogs copies log fields from src onto dst, keeping only the tail.
func AttachTruncatedLogs(dst map[string]any, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	logs := map[string]any{}
	for _, k := range []string{"runner_log", "agent_log", "console_log", "workflow_log"} {
		text, _ := src[k].(string)
		cut, truncated := truncateTail(text, maxLogBytes)
		if truncated {
			logs[k] = cut
			logs[k+"_truncated"] = true
			logs[k+"_bytes"] = len(text)
		} else if text != "" {
			logs[k] = cut
		}
	}
	if ev, ok := src["events"]; ok {
		logs["events"] = ev
	}
	if t, ok := src["updated_at"]; ok {
		logs["updated_at"] = t
	}
	dst["logs"] = logs
}
