package control

import (
	"fmt"
	"strings"
)

const (
	svcRunning      = "running"
	svcStarting     = "starting"
	svcStopping     = "stopping"
	svcStopped      = "stopped"
	svcFailed       = "failed"
	svcUnknown      = "unknown"
	svcNotInstalled = "not_installed"
)

// systemdDisplayStatus maps a systemd is-active value plus process liveness
// into a dashboard badge: running, starting, stopping, stopped, failed, unknown.
func systemdDisplayStatus(unit string, processUp bool) string {
	switch unit {
	case "active":
		if processUp {
			return svcRunning
		}
		return svcStarting
	case "activating", "reloading":
		return svcStarting
	case "deactivating":
		return svcStopping
	case "failed":
		return svcFailed
	case "inactive":
		return svcStopped
	default:
		if processUp {
			return svcRunning
		}
		return svcUnknown
	}
}

func controlDetail(unit string, hostctl bool) string {
	if !hostctl {
		return "This dashboard is up. Install temperci-hostctl to read systemd state and restart from here."
	}
	switch unit {
	case "active":
		return "systemd unit is active"
	case "unknown":
		return "serving requests; systemd state unknown"
	default:
		return "systemd unit is " + unit
	}
}

func agentDetail(unit string, registered bool, ids []string, hostctl bool) string {
	var parts []string
	if hostctl {
		parts = append(parts, "systemd "+unit)
	} else {
		parts = append(parts, "systemd state unknown (hostctl missing)")
	}
	if registered {
		switch len(ids) {
		case 0:
			parts = append(parts, "registered")
		case 1:
			parts = append(parts, "registered "+ids[0])
		default:
			parts = append(parts, fmt.Sprintf("%d agents registered", len(ids)))
		}
	} else {
		parts = append(parts, "no agent registered")
	}
	return strings.Join(parts, " · ")
}

func overallServiceStatus(statuses ...string) string {
	rank := map[string]int{
		svcFailed:       5,
		svcStopped:      4,
		svcNotInstalled: 4,
		svcUnknown:      3,
		svcStopping:     2,
		svcStarting:     2,
		svcRunning:      1,
	}
	worst := svcRunning
	worstRank := 0
	for _, st := range statuses {
		if r := rank[st]; r > worstRank {
			worst = st
			worstRank = r
		}
	}
	if worst == svcRunning {
		return svcRunning
	}
	return worst
}

func buildSystemStatus(hostctl bool, controlUnit, agentUnit string, agentIDs []string, lastSeen string) map[string]any {
	if agentIDs == nil {
		agentIDs = []string{}
	}
	registered := len(agentIDs) > 0
	controlStatus := systemdDisplayStatus(controlUnit, true)
	agentStatus := systemdDisplayStatus(agentUnit, registered)
	agentReady := registered && (agentUnit == "active" || agentUnit == "unknown")
	if agentUnit == "inactive" || agentUnit == "failed" {
		agentReady = false
	}

	return map[string]any{
		"ok": true,
		"control": map[string]any{
			"name":    "temperci-control.service",
			"label":   "control",
			"healthy": true, // this handler is served by control
			"unit":    controlUnit,
			"status":  controlStatus,
			"ready":   controlUnit == "active" || controlUnit == "unknown",
			"detail":  controlDetail(controlUnit, hostctl),
		},
		"agent": map[string]any{
			"name":           "temperci-agent.service",
			"label":          "agent",
			"unit":           agentUnit,
			"status":         agentStatus,
			"registered":     registered,
			"registered_ids": agentIDs,
			"last_seen_at":   lastSeen,
			"ready":          agentReady,
			"detail":         agentDetail(agentUnit, registered, agentIDs, hostctl),
		},
		"overall": overallServiceStatus(controlStatus, agentStatus),
		"hostctl": hostctl,
	}
}
