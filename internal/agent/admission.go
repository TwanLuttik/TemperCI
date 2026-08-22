package agent

import "os"

const (
	DefaultReserveRAMMiB  = 2048
	DefaultReserveDiskMiB = 5120
	OverlaySlopMiB        = 256

	ReasonRAMCommitted = "ram_committed"
	ReasonRAMAvail     = "ram_available"
	ReasonDiskFree     = "disk_free"
	ReasonRAMFit       = "ram"
	ReasonDiskFit      = "disk"
)

// HostInventory is a point-in-time sample of the machine the agent runs on.
type HostInventory struct {
	RAMTotalMiB  int
	RAMAvailMiB  int
	DiskTotalMiB int
	DiskFreeMiB  int
	NumCPU       int
}

// Admission is the per-VM cost plus host headroom used to accept or refuse a create.
type Admission struct {
	MemoryMiB      int
	DiskMiB        int
	ReserveRAMMiB  int
	ReserveDiskMiB int
}

// AdmitDecision is the result of CanCreate.
type AdmitDecision struct {
	OK     bool
	Reason string
}

func (a Admission) memory() int {
	if a.MemoryMiB <= 0 {
		return 2048
	}
	return a.MemoryMiB
}

// MaxFit is how many VMs of this size fit on the host after reserve.
// reason is ReasonRAMFit or ReasonDiskFit (whichever bound is tighter).
func (a Admission) MaxFit(inv HostInventory) (n int, reason string) {
	mem := a.memory()
	usableRAM := inv.RAMTotalMiB - a.ReserveRAMMiB
	if usableRAM < 0 {
		usableRAM = 0
	}
	n = usableRAM / mem
	reason = ReasonRAMFit
	if a.DiskMiB > 0 {
		usableDisk := inv.DiskFreeMiB - a.ReserveDiskMiB
		if usableDisk < 0 {
			usableDisk = 0
		}
		diskFit := usableDisk / a.DiskMiB
		if diskFit < n {
			n = diskFit
			reason = ReasonDiskFit
		}
	}
	return n, reason
}

// CanCreate reports whether one more VM may be provisioned given allocated instances
// (warm+busy+pool_boot+destroying+createInFlight).
func (a Admission) CanCreate(inv HostInventory, allocated int) AdmitDecision {
	if allocated < 0 {
		allocated = 0
	}
	mem := a.memory()
	if allocated*mem+mem > inv.RAMTotalMiB-a.ReserveRAMMiB {
		return AdmitDecision{Reason: ReasonRAMCommitted}
	}
	if inv.RAMAvailMiB < mem {
		return AdmitDecision{Reason: ReasonRAMAvail}
	}
	if a.DiskMiB > 0 && inv.DiskFreeMiB < a.DiskMiB+a.ReserveDiskMiB {
		return AdmitDecision{Reason: ReasonDiskFree}
	}
	return AdmitDecision{OK: true}
}

// Remaining is how many additional creates would still pass CanCreate.
func (a Admission) Remaining(inv HostInventory, allocated int) int {
	if !a.CanCreate(inv, allocated).OK {
		return 0
	}
	mem := a.memory()
	usable := inv.RAMTotalMiB - a.ReserveRAMMiB - allocated*mem
	ramN := usable / mem
	liveN := inv.RAMAvailMiB / mem
	if liveN < ramN {
		ramN = liveN
	}
	if ramN < 0 {
		ramN = 0
	}
	if a.DiskMiB > 0 {
		diskN := (inv.DiskFreeMiB - a.ReserveDiskMiB) / a.DiskMiB
		if diskN < ramN {
			ramN = diskN
		}
	}
	return ramN
}

// OverlayEstimateMiB is the host disk we expect one new instance to consume.
func OverlayEstimateMiB(imagePath string) int {
	fi, err := os.Stat(imagePath)
	if err != nil || fi.Size() <= 0 {
		return OverlaySlopMiB
	}
	return int(fi.Size()/(1024*1024)) + OverlaySlopMiB
}

// ClampPoolToHost lowers MinReady/MaxReady so they cannot exceed host fit.
// reason is empty when configured MaxReady already fits.
func ClampPoolToHost(cfg PoolConfig, inv HostInventory) (PoolConfig, int, string) {
	a := Admission{
		MemoryMiB:      cfg.MemoryMiB,
		DiskMiB:        cfg.DiskPerVMMiB,
		ReserveRAMMiB:  cfg.ReserveRAMMiB,
		ReserveDiskMiB: cfg.ReserveDiskMiB,
	}
	fit, why := a.MaxFit(inv)
	if cfg.MaxReady > fit {
		cfg.MaxReady = fit
		if cfg.MinReady > cfg.MaxReady {
			cfg.MinReady = cfg.MaxReady
		}
		return cfg, fit, why
	}
	return cfg, fit, ""
}
