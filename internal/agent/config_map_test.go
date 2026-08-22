package agent_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestPoolConfigFromAgent_CopiesReservesAndDisk(t *testing.T) {
	dir := t.TempDir()
	img := filepath.Join(dir, "base")
	if err := os.WriteFile(img, make([]byte, 2*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.AgentConfig{
		ImagePath:            img,
		AgentToken:           "t",
		HostReserveMemoryMiB: 1024,
		HostReserveDiskMiB:   2048,
		MemoryMiB:            4096,
		VCPU:                 2,
		MinReady:             1,
		MaxReady:             2,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	pc := agent.PoolConfigFromAgent(cfg)
	if pc.ReserveRAMMiB != 1024 || pc.ReserveDiskMiB != 2048 {
		t.Fatalf("reserves %+v", pc)
	}
	if pc.DiskPerVMMiB != 2+agent.OverlaySlopMiB {
		t.Fatalf("DiskPerVMMiB=%d", pc.DiskPerVMMiB)
	}
}
