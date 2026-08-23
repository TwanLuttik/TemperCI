package agent

import (
	"context"

	"github.com/TwanLuttik/TemperCI/internal/api"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// ApplyAgentCmds runs operator commands delivered on heartbeat (kill VM).
func ApplyAgentCmds(ctx context.Context, pool *Pool, cmds []api.AgentCmd) int {
	if pool == nil || len(cmds) == 0 {
		return 0
	}
	n := 0
	for _, c := range cmds {
		if c.Action != api.AgentCmdKillVM || c.VMID == "" {
			continue
		}
		if err := pool.KillVM(ctx, vmm.ID(c.VMID), "dashboard"); err != nil {
			if pool.log != nil {
				pool.log.Warn("kill vm failed", "vm_id", c.VMID, "err", err)
			}
			continue
		}
		n++
	}
	return n
}
