package agent

import "sync/atomic"

// Metrics holds process-local counters for pool observability.
type Metrics struct {
	WarmBinds   atomic.Uint64
	ColdStarts  atomic.Uint64
	DestroysOK  atomic.Uint64
	DestroyFail atomic.Uint64
	Recycles    atomic.Uint64
	Orphans     atomic.Uint64
	// LastDestroyErr is the most recent destroy error string (empty if none).
	// Updated under pool mutex; read via Pool.LastDestroyError.
}

// Snapshot is a point-in-time metrics view.
type Snapshot struct {
	WarmBinds   uint64
	ColdStarts  uint64
	DestroysOK  uint64
	DestroyFail uint64
	Recycles    uint64
	Orphans     uint64
	Counts      Counts
}

// Snapshot returns counter values (counts filled by Pool.Metrics).
func (m *Metrics) Snapshot() Snapshot {
	return Snapshot{
		WarmBinds:   m.WarmBinds.Load(),
		ColdStarts:  m.ColdStarts.Load(),
		DestroysOK:  m.DestroysOK.Load(),
		DestroyFail: m.DestroyFail.Load(),
		Recycles:    m.Recycles.Load(),
		Orphans:     m.Orphans.Load(),
	}
}
