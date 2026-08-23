package api

import (
	"testing"
	"time"
)

func TestSortVMUsage_OldestBootFirst(t *testing.T) {
	t1 := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	t3 := t1.Add(2 * time.Minute)
	vms := []VMUsage{
		{ID: "c", CreatedAt: t3},
		{ID: "a", CreatedAt: t1},
		{ID: "b", CreatedAt: t2},
	}
	SortVMUsage(vms)
	if vms[0].ID != "a" || vms[1].ID != "b" || vms[2].ID != "c" {
		t.Fatalf("order = %s,%s,%s", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}

func TestSortVMUsage_TieBreaksByID(t *testing.T) {
	at := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	vms := []VMUsage{
		{ID: "z", CreatedAt: at},
		{ID: "m", CreatedAt: at},
		{ID: "a", CreatedAt: at},
	}
	SortVMUsage(vms)
	if vms[0].ID != "a" || vms[1].ID != "m" || vms[2].ID != "z" {
		t.Fatalf("order = %s,%s,%s", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}

func TestSortVMUsage_MissingCreatedAtLastThenByID(t *testing.T) {
	at := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	vms := []VMUsage{
		{ID: "new-undated"},
		{ID: "old", CreatedAt: at},
		{ID: "also-undated"},
	}
	SortVMUsage(vms)
	if vms[0].ID != "old" || vms[1].ID != "also-undated" || vms[2].ID != "new-undated" {
		t.Fatalf("order = %s,%s,%s", vms[0].ID, vms[1].ID, vms[2].ID)
	}
}
