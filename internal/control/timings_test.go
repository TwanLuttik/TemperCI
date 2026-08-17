package control

import (
	"testing"
	"time"
)

func TestComputeJobTimings_FinishedJob(t *testing.T) {
	created := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	assigned := created.Add(2 * time.Second)
	started := assigned.Add(500 * time.Millisecond)
	finished := started.Add(10 * time.Second)

	got := ComputeJobTimings(created, assigned, started, finished, finished)
	if got.QueueMS != 2000 {
		t.Fatalf("queue_ms=%d want 2000", got.QueueMS)
	}
	if got.BindMS != 500 {
		t.Fatalf("bind_ms=%d want 500", got.BindMS)
	}
	if got.RunMS != 10000 {
		t.Fatalf("run_ms=%d want 10000", got.RunMS)
	}
	if got.TotalMS != 12500 {
		t.Fatalf("total_ms=%d want 12500", got.TotalMS)
	}
}

func TestComputeJobTimings_RunningUsesNow(t *testing.T) {
	created := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	assigned := created.Add(time.Second)
	started := assigned.Add(time.Second)
	now := started.Add(5 * time.Second)

	got := ComputeJobTimings(created, assigned, started, time.Time{}, now)
	if got.RunMS != 5000 {
		t.Fatalf("run_ms=%d want 5000", got.RunMS)
	}
	if got.TotalMS != 7000 {
		t.Fatalf("total_ms=%d want 7000", got.TotalMS)
	}
}

func TestPercentileInt64(t *testing.T) {
	vals := []int64{10, 20, 30, 40, 50}
	if p := PercentileInt64(vals, 50); p != 30 {
		t.Fatalf("p50=%d want 30", p)
	}
	if p := PercentileInt64(vals, 95); p != 50 {
		t.Fatalf("p95=%d want 50", p)
	}
	if p := PercentileInt64(nil, 50); p != 0 {
		t.Fatalf("empty p50=%d", p)
	}
}
