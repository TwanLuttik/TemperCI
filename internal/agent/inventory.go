package agent

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// InventorySource samples the host the agent is running on.
type InventorySource interface {
	Sample() (HostInventory, error)
}

// StaticInventory returns a fixed sample (tests).
type StaticInventory struct {
	Inv HostInventory
	Err error
}

// Sample implements InventorySource.
func (s StaticInventory) Sample() (HostInventory, error) {
	return s.Inv, s.Err
}

// ProcInventory reads /proc/meminfo and Statfs(DataDir).
type ProcInventory struct {
	DataDir string
}

// Sample implements InventorySource.
func (p ProcInventory) Sample() (HostInventory, error) {
	inv := HostInventory{NumCPU: runtime.NumCPU()}
	if inv.NumCPU < 1 {
		inv.NumCPU = 1
	}
	total, free, err := diskStat(p.DataDir)
	if err != nil {
		return inv, err
	}
	inv.DiskTotalMiB = total
	inv.DiskFreeMiB = free

	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		if runtime.GOOS != "linux" {
			// Fake/dev on macOS: do not RAM-cap; disk still applies.
			inv.RAMTotalMiB = 1 << 20
			inv.RAMAvailMiB = 1 << 20
			return inv, nil
		}
		return inv, fmt.Errorf("agent: read meminfo: %w", err)
	}
	inv.RAMTotalMiB, inv.RAMAvailMiB, err = ParseMeminfo(raw)
	if err != nil {
		return inv, err
	}
	return inv, nil
}

// ParseMeminfo reads MemTotal and MemAvailable from a /proc/meminfo blob (kB → MiB).
func ParseMeminfo(data []byte) (totalMiB, availMiB int, err error) {
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(rest))
		if len(fields) == 0 {
			continue
		}
		n, perr := strconv.ParseInt(fields[0], 10, 64)
		if perr != nil {
			continue
		}
		mib := int(n / 1024)
		switch key {
		case "MemTotal":
			totalMiB = mib
			haveTotal = true
		case "MemAvailable":
			availMiB = mib
			haveAvail = true
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, err
	}
	if !haveTotal || !haveAvail {
		return 0, 0, fmt.Errorf("agent: meminfo missing MemTotal or MemAvailable")
	}
	return totalMiB, availMiB, nil
}

func diskStat(path string) (totalMiB, freeMiB int, err error) {
	if path == "" {
		path = "."
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("agent: statfs %s: %w", path, err)
	}
	bsize := int64(st.Bsize)
	if bsize <= 0 {
		bsize = 4096
	}
	total := (int64(st.Blocks) * bsize) / (1024 * 1024)
	free := (int64(st.Bavail) * bsize) / (1024 * 1024)
	return int(total), int(free), nil
}
