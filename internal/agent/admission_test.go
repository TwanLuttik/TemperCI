package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/agent"
)

func TestAdmission_MaxFit(t *testing.T) {
	a := agent.Admission{MemoryMiB: 4096, DiskMiB: 8192, ReserveRAMMiB: 2048, ReserveDiskMiB: 5120}
	n, reason := a.MaxFit(agent.HostInventory{RAMTotalMiB: 16384, DiskFreeMiB: 100000})
	if n != 3 || reason != agent.ReasonRAMFit {
		t.Fatalf("got n=%d reason=%q want 3/ram", n, reason)
	}
	n, reason = a.MaxFit(agent.HostInventory{RAMTotalMiB: 65536, DiskFreeMiB: 20000})
	if n != 1 || reason != agent.ReasonDiskFit {
		t.Fatalf("got n=%d reason=%q want 1/disk", n, reason)
	}
	n, _ = a.MaxFit(agent.HostInventory{RAMTotalMiB: 2048, DiskFreeMiB: 100000})
	if n != 0 {
		t.Fatalf("reserve consumes all RAM: n=%d", n)
	}
}

func TestAdmission_CanCreate(t *testing.T) {
	a := agent.Admission{MemoryMiB: 4096, DiskMiB: 8192, ReserveRAMMiB: 2048, ReserveDiskMiB: 5120}
	inv := agent.HostInventory{RAMTotalMiB: 16384, RAMAvailMiB: 12000, DiskFreeMiB: 40000}
	if d := a.CanCreate(inv, 0); !d.OK {
		t.Fatalf("first VM should fit: %+v", d)
	}
	if d := a.CanCreate(inv, 3); d.OK || d.Reason != agent.ReasonRAMCommitted {
		t.Fatalf("4th 4GiB VM on 16-2 GiB: %+v", d)
	}
	lowLive := inv
	lowLive.RAMAvailMiB = 1024
	if d := a.CanCreate(lowLive, 0); d.OK || d.Reason != agent.ReasonRAMAvail {
		t.Fatalf("live RAM too low: %+v", d)
	}
	lowDisk := inv
	lowDisk.DiskFreeMiB = 9000
	if d := a.CanCreate(lowDisk, 0); d.OK || d.Reason != agent.ReasonDiskFree {
		t.Fatalf("disk 9000 < 8192+5120: %+v", d)
	}
}

func TestAdmission_Remaining(t *testing.T) {
	a := agent.Admission{MemoryMiB: 4096, DiskMiB: 1024, ReserveRAMMiB: 2048, ReserveDiskMiB: 0}
	inv := agent.HostInventory{RAMTotalMiB: 16384, RAMAvailMiB: 9000, DiskFreeMiB: 100000}
	if n := a.Remaining(inv, 0); n != 2 {
		t.Fatalf("live 9000/4096 = 2, committed 14GiB/4GiB = 3 → want 2, got %d", n)
	}
	if n := a.Remaining(inv, 3); n != 0 {
		t.Fatalf("already at committed cap: %d", n)
	}
}

func TestOverlayEstimateMiB(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "img")
	if err := os.WriteFile(p, make([]byte, 3*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	got := agent.OverlayEstimateMiB(p)
	if got != 3+agent.OverlaySlopMiB {
		t.Fatalf("got %d want %d", got, 3+agent.OverlaySlopMiB)
	}
	if agent.OverlayEstimateMiB(filepath.Join(dir, "missing")) != agent.OverlaySlopMiB {
		t.Fatal("missing image should return slop only")
	}
}

func TestClampPoolToHost(t *testing.T) {
	cfg := agent.PoolConfig{MinReady: 3, MaxReady: 4, MemoryMiB: 8192, DiskPerVMMiB: 1024, ReserveRAMMiB: 2048, ReserveDiskMiB: 0}
	out, fit, reason := agent.ClampPoolToHost(cfg, agent.HostInventory{RAMTotalMiB: 16384, DiskFreeMiB: 100000})
	if fit != 1 || reason != agent.ReasonRAMFit || out.MaxReady != 1 || out.MinReady != 1 {
		t.Fatalf("out=%+v fit=%d reason=%q", out, fit, reason)
	}
	out, fit, reason = agent.ClampPoolToHost(cfg, agent.HostInventory{RAMTotalMiB: 65536, DiskFreeMiB: 100000})
	if fit < 4 || out.MaxReady != 4 || reason != "" {
		t.Fatalf("should not clamp: out=%+v fit=%d reason=%q", out, fit, reason)
	}
}
