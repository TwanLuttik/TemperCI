package agent

import (
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/config"
)

func TestExclusiveShape(t *testing.T) {
	if ExclusiveShape(6144) || ExclusiveShape(8192) || ExclusiveShape(10*1024) {
		t.Fatal("6g/8g/10g must still share the host")
	}
	if !ExclusiveShape(ExclusiveJobMiB) || !ExclusiveShape(16*1024) {
		t.Fatal("12g+ must be exclusive")
	}
}

func TestParseShapeLabel(t *testing.T) {
	cases := []struct {
		in         string
		vcpu, memG int
		ok         bool
	}{
		{"temperci-4vcpu-ubuntu-2404", 4, 0, true},
		{"temperci-4vcpu-8g-ubuntu-2404", 4, 8, true},
		{"temperci-2vcpu-4g-ubuntu-2404", 2, 4, true},
		{"temperci-8vcpu-16g-ubuntu-2404", 8, 16, true},
		{"self-hosted", 0, 0, false},
		{"ubuntu-latest", 0, 0, false},
	}
	for _, tc := range cases {
		v, g, ok := ParseShapeLabel(tc.in)
		if ok != tc.ok || v != tc.vcpu || g != tc.memG {
			t.Errorf("%s: got %d %d %v want %d %d %v", tc.in, v, g, ok, tc.vcpu, tc.memG, tc.ok)
		}
	}
}

func TestResolveJobShape_ParsesAnySize(t *testing.T) {
	catalog := []VMShape{{Label: "temperci-4vcpu-ubuntu-2404", VCPUs: 4, MemoryMiB: 8192, MinReady: 1}}
	got := ResolveJobShape([]string{"self-hosted", "temperci-8vcpu-16g-ubuntu-2404"}, catalog, 4, 8192)
	if got.VCPUs != 8 || got.MemoryMiB != 16384 {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveJobShape_LegacyFourVCPUUsesDefaultRAM(t *testing.T) {
	catalog := []VMShape{{Label: "temperci-4vcpu-ubuntu-2404", VCPUs: 4, MemoryMiB: 8192, MinReady: 1}}
	got := ResolveJobShape([]string{"temperci-4vcpu-ubuntu-2404"}, catalog, 4, 8192)
	if got.VCPUs != 4 || got.MemoryMiB != 8192 || got.Label != "temperci-4vcpu-ubuntu-2404" {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveJobShape_FallsBackToDefault(t *testing.T) {
	got := ResolveJobShape([]string{"linux"}, nil, 4, 8192)
	if got.VCPUs != 4 || got.MemoryMiB != 8192 {
		t.Fatalf("got %+v", got)
	}
}

func TestShapeLabel_LegacyFourByEight(t *testing.T) {
	if ShapeLabel(4, 8192) != "temperci-4vcpu-ubuntu-2404" {
		t.Fatalf("got %s", ShapeLabel(4, 8192))
	}
	if ShapeLabel(2, 4096) != "temperci-2vcpu-4g-ubuntu-2404" {
		t.Fatalf("got %s", ShapeLabel(2, 4096))
	}
}

func TestShapesFromConfig_LegacySingleSize(t *testing.T) {
	cfg := &config.AgentConfig{VCPU: 4, MemoryMiB: 8192, MinReady: 1, ImagePath: "/img", AgentToken: "t"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	got := ShapesFromConfig(cfg)
	if len(got) != 1 || got[0].VCPUs != 4 || got[0].MemoryMiB != 8192 || got[0].MinReady != 1 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Label != "temperci-4vcpu-ubuntu-2404" {
		t.Fatalf("label=%s", got[0].Label)
	}
}
