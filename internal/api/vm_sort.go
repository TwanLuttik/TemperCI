package api

import "sort"

// SortVMUsage orders VMs oldest-boot first so dashboard lists stay stable
// (new guests append at the bottom). Missing CreatedAt sorts last; ties use ID.
func SortVMUsage(vms []VMUsage) {
	sort.SliceStable(vms, func(i, j int) bool {
		ai, aj := vms[i].CreatedAt, vms[j].CreatedAt
		zi, zj := ai.IsZero(), aj.IsZero()
		if zi != zj {
			return !zi
		}
		if !zi && !ai.Equal(aj) {
			return ai.Before(aj)
		}
		return vms[i].ID < vms[j].ID
	})
}
