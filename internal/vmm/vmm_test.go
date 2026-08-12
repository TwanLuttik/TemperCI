package vmm_test

import (
	"testing"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

func TestIDValidate(t *testing.T) {
	cases := []struct {
		id    vmm.ID
		ok    bool
	}{
		{"vm-1", true},
		{"VM_2", true},
		{"", false},
		{"has space", false},
		{"../etc", false},
		{"a/b", false},
	}
	for _, tc := range cases {
		err := tc.id.Validate()
		if tc.ok && err != nil {
			t.Errorf("%q: unexpected err %v", tc.id, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q: expected error", tc.id)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	err := (vmm.Config{ID: "x", VCPUs: 0, MemoryMiB: 1}).Validate()
	if err == nil {
		t.Fatal("expected vcpus error")
	}
	err = (vmm.Config{ID: "x", VCPUs: 1, MemoryMiB: 0}).Validate()
	if err == nil {
		t.Fatal("expected memory error")
	}
	if err := (vmm.Config{ID: "x", VCPUs: 1, MemoryMiB: 512}).Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLayoutPaths(t *testing.T) {
	l := vmm.NewLayout("/var/lib/temperci")
	if l.ImagesDir() != "/var/lib/temperci/images" {
		t.Fatal(l.ImagesDir())
	}
	id := vmm.ID("abc")
	if l.InstanceDir(id) != "/var/lib/temperci/instances/abc" {
		t.Fatal(l.InstanceDir(id))
	}
	if l.MetaPath(id) != "/var/lib/temperci/instances/abc/meta.json" {
		t.Fatal(l.MetaPath(id))
	}
}
