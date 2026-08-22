package agent_test

import (
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/agent"
)

func TestParseMeminfo(t *testing.T) {
	raw := []byte("MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\n")
	total, avail, err := agent.ParseMeminfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	if total != 16000 || avail != 8000 {
		t.Fatalf("total=%d avail=%d want 16000/8000", total, avail)
	}
}

func TestParseMeminfo_MissingAvailable(t *testing.T) {
	_, _, err := agent.ParseMeminfo([]byte("MemTotal: 1024 kB\n"))
	if err == nil {
		t.Fatal("expected error when MemAvailable missing")
	}
}

func TestStaticInventory(t *testing.T) {
	s := agent.StaticInventory{Inv: agent.HostInventory{RAMTotalMiB: 1, NumCPU: 8}}
	got, err := s.Sample()
	if err != nil || got.RAMTotalMiB != 1 || got.NumCPU != 8 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	s.Err = errSentinel{}
	if _, err := s.Sample(); err == nil {
		t.Fatal("expected injected error")
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "boom" }

func TestProcInventory_SampleHasDisk(t *testing.T) {
	p := agent.ProcInventory{DataDir: t.TempDir()}
	inv, err := p.Sample()
	if err != nil {
		t.Fatal(err)
	}
	if inv.DiskFreeMiB <= 0 || inv.DiskTotalMiB <= 0 {
		t.Fatalf("disk unset: %+v", inv)
	}
	if inv.NumCPU < 1 {
		t.Fatalf("numCPU=%d", inv.NumCPU)
	}
	if inv.RAMTotalMiB <= 0 {
		t.Fatalf("ram unset: %+v", inv)
	}
}
