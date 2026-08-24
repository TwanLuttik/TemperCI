// Package mcp implements a read-only Model Context Protocol server for the
// TemperCI control plane (Streamable HTTP JSON-RPC at /mcp).
//
// The official Go SDK requires Go 1.25; this package implements the
// initialize / tools/list / tools/call subset on Go 1.22 so the fleet
// endpoint can live in temperci-control.
package mcp

import (
	"errors"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

const (
	// ProtocolVersion is the MCP revision this server speaks.
	ProtocolVersion = "2025-03-26"
	// ServerName is advertised in initialize.
	ServerName = "temperci"
	// maxLogBytes is the tail kept for each job log field (AI context).
	maxLogBytes = 8 << 10
)

// ErrNotFound is returned by Fleet.Job / Fleet.VM when the id is unknown.
var ErrNotFound = errors.New("not found")

// JobFilter narrows list_jobs.
type JobFilter struct {
	Status string
	Repo   string
	Limit  int
}

// Fleet is the read-only control-plane surface MCP tools query.
// Implementations must not expose JIT configs, tokens, or PEMs.
type Fleet interface {
	Overview() map[string]any
	Hosts() []api.AgentInfo
	Jobs(JobFilter) []map[string]any
	Job(id int64) (map[string]any, error)
	VMs(agentID string) []map[string]any
	VM(id string) (map[string]any, error)
	Cache() map[string]any
	SystemStatus() map[string]any
}
