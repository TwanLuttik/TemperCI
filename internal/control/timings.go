package control

import (
	"math"
	"sort"
	"time"
)

// JobTimings is derived queue/bind/run/total duration in milliseconds.
type JobTimings struct {
	QueueMS int64 `json:"queue_ms,omitempty"`
	BindMS  int64 `json:"bind_ms,omitempty"`
	RunMS   int64 `json:"run_ms,omitempty"`
	TotalMS int64 `json:"total_ms,omitempty"`
}

// ComputeJobTimings derives durations. Zero finished/assigned/started fall back to now
// for in-flight phases. Negative intervals are clamped to zero.
func ComputeJobTimings(created, assigned, started, finished, now time.Time) JobTimings {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	end := finished
	if end.IsZero() {
		end = now
	}
	var t JobTimings
	if !created.IsZero() {
		t.TotalMS = msSince(created, end)
		qEnd := assigned
		if qEnd.IsZero() {
			qEnd = now
		}
		t.QueueMS = msSince(created, qEnd)
	}
	if !assigned.IsZero() {
		bEnd := started
		if bEnd.IsZero() {
			bEnd = now
		}
		t.BindMS = msSince(assigned, bEnd)
	}
	if !started.IsZero() {
		rEnd := finished
		if rEnd.IsZero() {
			rEnd = now
		}
		t.RunMS = msSince(started, rEnd)
	}
	return t
}

func msSince(start, end time.Time) int64 {
	if start.IsZero() || end.IsZero() {
		return 0
	}
	n := end.Sub(start).Milliseconds()
	if n < 0 {
		return 0
	}
	return n
}

// PercentileInt64 is nearest-rank percentile (p in 0–100). Empty input is 0.
func PercentileInt64(vals []int64, p float64) int64 {
	if len(vals) == 0 {
		return 0
	}
	cp := append([]int64(nil), vals...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	if p <= 0 {
		return cp[0]
	}
	if p >= 100 {
		return cp[len(cp)-1]
	}
	idx := int(math.Ceil(p/100*float64(len(cp)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func timingsFromAssignment(a *Assignment, now time.Time) JobTimings {
	if a == nil {
		return JobTimings{}
	}
	return ComputeJobTimings(a.CreatedAt, a.AssignedAt, a.StartedAt, a.FinishedAt, now)
}

func recentRunPercentiles(list []*Assignment) (p50, p95 int64) {
	var runs []int64
	for _, a := range list {
		if a == nil || a.Status != AssignmentFinished || a.StartedAt.IsZero() || a.FinishedAt.IsZero() {
			continue
		}
		if n := msSince(a.StartedAt, a.FinishedAt); n > 0 {
			runs = append(runs, n)
		}
	}
	return PercentileInt64(runs, 50), PercentileInt64(runs, 95)
}

func recentCacheTotals(list []*Assignment) (hits, misses int, bytesIn, bytesOut int64) {
	for _, a := range list {
		if a == nil {
			continue
		}
		hits += a.CacheHits
		misses += a.CacheMisses
		bytesIn += a.CacheBytesIn
		bytesOut += a.CacheBytesOut
	}
	return
}
