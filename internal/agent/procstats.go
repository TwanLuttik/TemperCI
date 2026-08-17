package agent

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// procSampler estimates per-PID CPU% and RSS from /proc (Linux).
// On non-Linux hosts it returns zeros (dashboard still shows configured vCPU/RAM).
type procSampler struct {
	mu    sync.Mutex
	last  map[int]procSample
	cores float64
}

type procSample struct {
	totalTime float64 // utime+stime in seconds
	at        time.Time
}

func newProcSampler() *procSampler {
	cores := float64(runtime.NumCPU())
	if cores < 1 {
		cores = 1
	}
	return &procSampler{last: make(map[int]procSample), cores: cores}
}

// sample returns cpu percent (0–100*cores roughly normalized to 0–100 of one core * n)
// and RSS in MiB. CPU is host process share scaled to 0–100 of a single core * numCPU max.
func (s *procSampler) sample(pid int) (cpuPercent, rssMiB float64) {
	if pid <= 0 || runtime.GOOS != "linux" {
		return 0, 0
	}
	utime, stime, rssPages, err := readProcStat(pid)
	if err != nil {
		return 0, 0
	}
	const pageSize = 4096.0
	rssMiB = (float64(rssPages) * pageSize) / (1024 * 1024)
	total := utime + stime
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.last[pid]
	s.last[pid] = procSample{totalTime: total, at: now}
	if !ok || now.Sub(prev.at) < 50*time.Millisecond {
		return 0, rssMiB
	}
	dt := now.Sub(prev.at).Seconds()
	if dt <= 0 {
		return 0, rssMiB
	}
	dCPU := total - prev.totalTime
	if dCPU < 0 {
		dCPU = 0
	}
	// Percent of one CPU core (can exceed 100 with multi-threaded FC).
	cpuPercent = (dCPU / dt) * 100
	if cpuPercent > 100*s.cores {
		cpuPercent = 100 * s.cores
	}
	return cpuPercent, rssMiB
}

func readProcStat(pid int) (utimeSec, stimeSec float64, rssPages int64, err error) {
	// /proc/<pid>/stat: fields 14,15 utime stime (jiffies); rss is field 24 in pages
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, 0, err
	}
	// comm can contain spaces/parens — split after last ')'
	s := string(raw)
	i := strings.LastIndex(s, ")")
	if i < 0 || i+2 >= len(s) {
		return 0, 0, 0, fmt.Errorf("bad stat")
	}
	fields := strings.Fields(s[i+2:])
	// fields[0] is state; utime is index 11, stime 12, rss 21 (0-based after comm)
	if len(fields) < 22 {
		return 0, 0, 0, fmt.Errorf("short stat")
	}
	clk := float64(100) // typical USER_HZ
	if b, err := os.ReadFile("/proc/stat"); err == nil && len(b) > 0 {
		// keep 100 default; optional: sysconf
		_ = b
	}
	ut, _ := strconv.ParseFloat(fields[11], 64)
	st, _ := strconv.ParseFloat(fields[12], 64)
	rss, _ := strconv.ParseInt(fields[21], 10, 64)
	return ut / clk, st / clk, rss, nil
}

func dirSizeMiB(path string) float64 {
	var total int64
	_ = walkSize(path, &total)
	return float64(total) / (1024 * 1024)
}

func walkSize(path string, total *int64) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := path + "/" + e.Name()
		if e.IsDir() {
			_ = walkSize(p, total)
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		*total += info.Size()
	}
	return nil
}
