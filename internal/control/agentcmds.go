package control

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/TwanLuttik/TemperCI/internal/api"
)

// cmdQueue holds operator commands until the target agent heartbeats.
type cmdQueue struct {
	mu   sync.Mutex
	seq  atomic.Int64
	byID map[string][]api.AgentCmd
}

func newCmdQueue() *cmdQueue {
	return &cmdQueue{byID: make(map[string][]api.AgentCmd)}
}

func (q *cmdQueue) enqueueKill(agentID, vmID string, jobID int64) {
	if q == nil {
		return
	}
	agentID = strings.TrimSpace(agentID)
	vmID = strings.TrimSpace(vmID)
	if agentID == "" || vmID == "" {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.byID[agentID] = append(q.byID[agentID], api.AgentCmd{
		ID:     strconv.FormatInt(q.seq.Add(1), 10),
		Action: api.AgentCmdKillVM,
		VMID:   vmID,
		JobID:  jobID,
	})
}

func (q *cmdQueue) take(agentID string) []api.AgentCmd {
	if q == nil || agentID == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	cmds := q.byID[agentID]
	if len(cmds) == 0 {
		return nil
	}
	delete(q.byID, agentID)
	return cmds
}
