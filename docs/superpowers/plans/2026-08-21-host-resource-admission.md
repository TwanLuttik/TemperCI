# Host Resource Admission Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the host agent from creating a microVM the machine cannot afford, by clamping `max_ready` from host RAM/disk and refusing creates when leftover RAM or disk is too low.

**Architecture:** Pure admission math in `internal/agent/admission.go`, a swappable `InventorySource` for host samples, pool/worker using that math locally. Control still schedules on `FreeSlots`. CPU is reported only.

**Tech Stack:** Go 1.22+, existing `internal/agent` pool/worker, `/proc/meminfo` + `syscall.Statfs`, dashboard Hosts page (React).

**Spec:** [docs/superpowers/specs/2026-08-21-host-resource-admission-design.md](../specs/2026-08-21-host-resource-admission-design.md)

## Global Constraints

- Primary language is **Go 1.22+**; do not add new third-party dependencies.
- Admission is **agent-local**. Do not teach the control plane leftover math.
- CPU is **observability only**. Never refuse a create because of vCPU count.
- `InventorySource == nil` preserves today’s slot-only tests (no host check).
- Inventory sample error on the create path is **fail closed** (do not create).
- `Worker.Capacity == 0` is valid. Do **not** coerce 0 to 1.
- Warm bind is always allowed when a warm VM already exists.
- Jobs stay **pending** when `FreeSlots == 0`. Do not fail the GitHub job from admission.
- One VM size per host (`PoolConfig.VCPUs` / `MemoryMiB`). No mixed-size packing.
- Follow existing test style: `_test` packages under `internal/agent`, `go test ./internal/agent/ -count=1`.

## File structure

| File | Responsibility |
|---|---|
| `internal/agent/admission.go` | Pure math: inventory struct, `MaxFit`, `CanCreate`, `Remaining`, overlay estimate, clamp |
| `internal/agent/admission_test.go` | Table tests for the math |
| `internal/agent/inventory.go` | `InventorySource`, `StaticInventory`, `ProcInventory`, meminfo parse, Statfs |
| `internal/agent/inventory_test.go` | parseMeminfo + overlay + StaticInventory |
| `internal/agent/agent.go` | `PoolConfig` reserve fields; `PoolConfigFromAgent` mapping |
| `internal/agent/pool.go` | Clamp on `NewPool`; `canCreateLocked` resource gate; snapshots |
| `internal/agent/pool_test.go` | Clamp + refuse-create + warm-bind-still-works |
| `internal/agent/worker.go` | `FreeSlots = min(slots, warm+RemainingCreates)`; Capacity 0 stays 0 |
| `internal/agent/worker_test.go` | Resource-full does not claim; one warm still claims |
| `internal/agent/client.go` | Register `resources` |
| `internal/config/config.go` | `host_reserve_memory_mib`, `host_reserve_disk_mib` |
| `internal/config/config_test.go` | Defaults and negative rejection |
| `internal/api/types.go` | `HostResources` on register + `AgentInfo` |
| `internal/control/agents.go` | Persist `Resources` on register |
| `internal/control/scheduler_test.go` | Register stores resources; hosts JSON includes them |
| `cmd/temperci-agent/main.go` | Wire `ProcInventory`; worker capacity from effective max |
| `deploy/agent.example.toml` | Document reserve keys |
| `docs/architecture/job-lifecycle.md` | Admission rules |
| `docs/architecture/install-targets.md` | Resource table |
| `web/src/api.ts` | `Host.resources` |
| `web/src/pages/HostsPage.tsx` | Leftover RAM/disk + clamp reason |

---

### Task 1: Pure admission math

**Files:**
- Create: `internal/agent/admission.go`
- Test: `internal/agent/admission_test.go`

**Interfaces:**
- Consumes: nothing
- Produces:
  - `const DefaultReserveRAMMiB = 2048`
  - `const DefaultReserveDiskMiB = 5120`
  - `const OverlaySlopMiB = 256`
  - `const ReasonRAMCommitted = "ram_committed"`
  - `const ReasonRAMAvail = "ram_available"`
  - `const ReasonDiskFree = "disk_free"`
  - `const ReasonRAMFit = "ram"`
  - `const ReasonDiskFit = "disk"`
  - `type HostInventory struct { RAMTotalMiB, RAMAvailMiB, DiskTotalMiB, DiskFreeMiB, NumCPU int }`
  - `type Admission struct { MemoryMiB, DiskMiB, ReserveRAMMiB, ReserveDiskMiB int }`
  - `type AdmitDecision struct { OK bool; Reason string }`
  - `func (a Admission) MaxFit(inv HostInventory) (n int, reason string)`
  - `func (a Admission) CanCreate(inv HostInventory, allocated int) AdmitDecision`
  - `func (a Admission) Remaining(inv HostInventory, allocated int) int`
  - `func OverlayEstimateMiB(imagePath string) int`
  - `func ClampPoolToHost(cfg PoolConfig, inv HostInventory) (out PoolConfig, fit int, reason string)`

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/admission_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -count=1 -run 'TestAdmission_|TestOverlayEstimateMiB|TestClampPoolToHost'`

Expected: FAIL with `undefined: agent.Admission` (and the other new names).

- [ ] **Step 3: Implement the math**

Create `internal/agent/admission.go`:

```go
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
```

`PoolConfig` does not yet have `DiskPerVMMiB` / reserve fields. Add them now on the existing struct in `internal/agent/agent.go` (no behavior change):

In `PoolConfig` after `MemoryMiB int`, add:

```go
	// DiskPerVMMiB is the overlay estimate used by admission (0 = derive from image).
	DiskPerVMMiB int
	// ReserveRAMMiB / ReserveDiskMiB are host headroom. 0 is a valid "no extra reserve".
	ReserveRAMMiB  int
	ReserveDiskMiB int
```

Do **not** change `PoolConfigFromAgent` yet (Task 3). Tests in this task construct `PoolConfig` literals.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -count=1 -run 'TestAdmission_|TestOverlayEstimateMiB|TestClampPoolToHost'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/admission.go internal/agent/admission_test.go internal/agent/agent.go
git commit -m "feat(agent): add host resource admission math"
```

---

### Task 2: Host inventory sampler

**Files:**
- Create: `internal/agent/inventory.go`
- Test: `internal/agent/inventory_test.go`

**Interfaces:**
- Consumes: `HostInventory` from Task 1
- Produces:
  - `type InventorySource interface { Sample() (HostInventory, error) }`
  - `type StaticInventory struct { Inv HostInventory; Err error }`
  - `func (s StaticInventory) Sample() (HostInventory, error)`
  - `type ProcInventory struct { DataDir string }`
  - `func (p ProcInventory) Sample() (HostInventory, error)`
  - `func ParseMeminfo(data []byte) (totalMiB, availMiB int, err error)` (exported for tests)

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/inventory_test.go`:

```go
package agent_test

import (
	"strings"
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -count=1 -run 'TestParseMeminfo|TestStaticInventory|TestProcInventory_SampleHasDisk'`

Expected: FAIL with `undefined: agent.ParseMeminfo`

- [ ] **Step 3: Implement inventory**

Create `internal/agent/inventory.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -count=1 -run 'TestParseMeminfo|TestStaticInventory|TestProcInventory_SampleHasDisk'`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/inventory.go internal/agent/inventory_test.go
git commit -m "feat(agent): sample host RAM and disk for admission"
```

---

### Task 3: Config knobs

**Files:**
- Modify: `internal/config/config.go` (`AgentConfig` + `Validate`)
- Modify: `internal/agent/agent.go` (`PoolConfigFromAgent`)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `DefaultReserveRAMMiB` / `DefaultReserveDiskMiB` — **do not import agent from config** (cycle). Duplicate the default numbers `2048` and `5120` in `config.Validate`.
- Produces:
  - `AgentConfig.HostReserveMemoryMiB int \`toml:"host_reserve_memory_mib"\``
  - `AgentConfig.HostReserveDiskMiB int \`toml:"host_reserve_disk_mib"\``
  - `Validate`: `< 0` error; `== 0` becomes 2048 / 5120
  - `PoolConfigFromAgent` copies those into `ReserveRAMMiB` / `ReserveDiskMiB` and sets `DiskPerVMMiB = OverlayEstimateMiB(cfg.ImagePath)`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestAgentConfig_ReserveDefaults(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 2048 || cfg.HostReserveDiskMiB != 5120 {
		t.Fatalf("defaults = ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}

func TestAgentConfig_ReserveExplicitZeroBecomesDefault(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveMemoryMiB: 0, HostReserveDiskMiB: 0}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 2048 || cfg.HostReserveDiskMiB != 5120 {
		t.Fatalf("0 must default, got ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}

func TestAgentConfig_ReserveNegative(t *testing.T) {
	cfg := AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveMemoryMiB: -1}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative ram reserve error")
	}
	cfg = AgentConfig{ImagePath: "/img", AgentToken: "t", HostReserveDiskMiB: -5}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative disk reserve error")
	}
}

func TestLoadAgentFile_ReserveKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent.toml")
	content := `
image_path = "/img/base"
agent_token = "shared-secret"
host_reserve_memory_mib = 1024
host_reserve_disk_mib = 2048
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadAgentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostReserveMemoryMiB != 1024 || cfg.HostReserveDiskMiB != 2048 {
		t.Fatalf("got ram %d disk %d", cfg.HostReserveMemoryMiB, cfg.HostReserveDiskMiB)
	}
}
```

Add a test in `internal/agent` for the mapping. Create `internal/agent/config_map_test.go`:

```go
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
		ImagePath:             img,
		AgentToken:            "t",
		HostReserveMemoryMiB:  1024,
		HostReserveDiskMiB:    2048,
		MemoryMiB:             4096,
		VCPU:                  2,
		MinReady:              1,
		MaxReady:              2,
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config/ ./internal/agent/ -count=1 -run 'TestAgentConfig_Reserve|TestLoadAgentFile_ReserveKeys|TestPoolConfigFromAgent_CopiesReservesAndDisk'`

Expected: FAIL (`HostReserveMemoryMiB` undefined, or mapping fields stay 0).

- [ ] **Step 3: Implement config + mapping**

In `internal/config/config.go` on `AgentConfig`, after `MaxTotalVMs`:

```go
	// HostReserveMemoryMiB is RAM kept for the host OS (0 = default 2048).
	HostReserveMemoryMiB int `toml:"host_reserve_memory_mib"`
	// HostReserveDiskMiB is disk kept free on data_dir (0 = default 5120).
	HostReserveDiskMiB int `toml:"host_reserve_disk_mib"`
```

In `Validate`, after the `MaxTotalVMs` checks:

```go
	if c.HostReserveMemoryMiB < 0 {
		return fmt.Errorf("config: host_reserve_memory_mib must be >= 0")
	}
	if c.HostReserveMemoryMiB == 0 {
		c.HostReserveMemoryMiB = 2048
	}
	if c.HostReserveDiskMiB < 0 {
		return fmt.Errorf("config: host_reserve_disk_mib must be >= 0")
	}
	if c.HostReserveDiskMiB == 0 {
		c.HostReserveDiskMiB = 5120
	}
```

In `internal/agent/agent.go` `PoolConfigFromAgent`, set:

```go
		ReserveRAMMiB:  cfg.HostReserveMemoryMiB,
		ReserveDiskMiB: cfg.HostReserveDiskMiB,
		DiskPerVMMiB:   OverlayEstimateMiB(cfg.ImagePath),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ ./internal/agent/ -count=1 -run 'TestAgentConfig_Reserve|TestLoadAgentFile_ReserveKeys|TestPoolConfigFromAgent_CopiesReservesAndDisk|TestLoadAgentFile$'`

Expected: PASS (existing `TestLoadAgentFile` still passes; defaults apply when keys omitted).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/agent/agent.go internal/agent/config_map_test.go
git commit -m "feat(config): add host RAM and disk reserve knobs"
```

---

### Task 4: Pool clamp and create gate

**Files:**
- Modify: `internal/agent/pool.go` (`Pool`, `PoolDeps`, `NewPool`, `canCreateLocked`)
- Modify: `internal/agent/pool_test.go` (new tests at the end)
- Modify: `internal/agent/agent.go` only if a getter is cleaner on `Pool`

**Interfaces:**
- Consumes: `InventorySource`, `Admission`, `ClampPoolToHost`, `AdmitDecision`
- Produces:
  - `PoolDeps.Inventory InventorySource` (optional)
  - `func (p *Pool) EffectiveMaxReady() int`
  - `func (p *Pool) ConfiguredMaxReady() int`
  - `func (p *Pool) RemainingCreates() int`
  - `func (p *Pool) LastAdmitReason() string`
  - `func (p *Pool) ClampReason() string`
  - `func (p *Pool) InventorySample() (HostInventory, error)` — empty + nil when no source
  - `canCreateLocked` also refuses when inventory sample fails or `CanCreate` is not OK

Behavior:

1. `NewPool` applies existing MinReady/MaxReady defaults **first**.
2. If `DiskPerVMMiB <= 0` and `ImagePath` is set, set `DiskPerVMMiB = OverlayEstimateMiB(ImagePath)`.
3. If `deps.Inventory != nil`, `Sample()` once:
   - error → log, set `MinReady=0`, `MaxReady=0`, `clampReason="inventory_error"`
   - ok → `ClampPoolToHost`; if `MaxReady` dropped, store `clampReason`
4. Store `configuredMax` as the pre-clamp `MaxReady`.
5. `canCreateLocked`: existing `max_total_vms` check, then if inventory is set: sample; on error return false and set `lastAdmit="inventory_error"`; else `CanCreate(inv, counts.Total()+createInFlight)`.

`RemainingCreates` (needs lock internally):

```go
func (p *Pool) RemainingCreates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remainingCreatesLocked()
}
```

If `p.inventory == nil`, return a large number (`1 << 20`) so slot math is unchanged for old tests.

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/pool_test.go`:

```go
func TestPool_ClampsMinReadyToHostRAM(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 16384, RAMAvailMiB: 14000, DiskTotalMiB: 200000, DiskFreeMiB: 200000, NumCPU: 8,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 3, MaxReady: 3, MemoryMiB: 8192, DiskPerVMMiB: 256,
		ReserveRAMMiB: 2048, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm+p.Counts().PoolBoot >= 1 })
	time.Sleep(150 * time.Millisecond)
	c := p.Counts()
	if c.Warm+c.PoolBoot+c.Busy > 1 {
		t.Fatalf("clamped host must not boot >1 VM: %+v", c)
	}
	if p.EffectiveMaxReady() != 1 {
		t.Fatalf("EffectiveMaxReady=%d", p.EffectiveMaxReady())
	}
	if p.ConfiguredMaxReady() != 3 {
		t.Fatalf("ConfiguredMaxReady=%d", p.ConfiguredMaxReady())
	}
	if p.ClampReason() != agent.ReasonRAMFit {
		t.Fatalf("ClampReason=%q", p.ClampReason())
	}
}

func TestPool_LiveRAMBlocksColdCreate(t *testing.T) {
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 512, DiskTotalMiB: 200000, DiskFreeMiB: 200000, NumCPU: 8,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 0, MaxReady: 2, MemoryMiB: 4096, DiskPerVMMiB: 256,
		ReserveRAMMiB: 0, ReserveDiskMiB: 0,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = inv
	})
	_, err := p.Bind(context.Background(), agent.JobPayload{JobID: "cold", JITConfig: "x"})
	if err == nil || !errors.Is(err, agent.ErrNoCapacity) {
		t.Fatalf("want ErrNoCapacity, got %v", err)
	}
	if p.LastAdmitReason() != agent.ReasonRAMAvail {
		t.Fatalf("LastAdmitReason=%q", p.LastAdmitReason())
	}
}

func TestPool_WarmBindWhenCreateBlocked(t *testing.T) {
	// Huge RAM at start so one warm VM boots; then flip live RAM down.
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	p := testPool(t, agent.PoolConfig{
		MinReady: 1, MaxReady: 2, MemoryMiB: 4096, DiskPerVMMiB: 256,
		ReserveRAMMiB: 0, ReserveDiskMiB: 0, BindWait: 200 * time.Millisecond,
	}, func(d *agent.PoolDeps, _ *fake.Manager) {
		d.Inventory = cur
	})
	waitFor(t, 2*time.Second, func() bool { return p.Counts().Warm >= 1 })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000})
	res, err := p.Bind(context.Background(), agent.JobPayload{JobID: "warm", JITConfig: "x"})
	if err != nil {
		t.Fatalf("warm bind must succeed: %v", err)
	}
	if !res.WarmStart {
		t.Fatal("expected warm bind")
	}
}

type atomicInventory struct {
	mu  sync.Mutex
	inv agent.HostInventory
}

func (a *atomicInventory) Sample() (agent.HostInventory, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inv, nil
}

func (a *atomicInventory) set(inv agent.HostInventory) {
	a.mu.Lock()
	a.inv = inv
	a.mu.Unlock()
}
```

`testPool` already applies option funcs to `PoolDeps`. `waitFor` already exists in this file (if not, use the same helper as `worker_test.go`). Confirm `waitFor` is in `pool_test.go`; if missing, copy:

```go
func waitFor(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -count=1 -run 'TestPool_ClampsMinReadyToHostRAM|TestPool_LiveRAMBlocksColdCreate|TestPool_WarmBindWhenCreateBlocked'`

Expected: FAIL (`Inventory` field undefined, or pool boots 3 VMs / cold bind succeeds).

- [ ] **Step 3: Wire the pool**

In `internal/agent/pool.go` on `Pool`:

```go
	inventory     InventorySource
	configuredMax int
	clampReason   string
	lastAdmit     string
```

On `PoolDeps`:

```go
	Inventory InventorySource
```

In `NewPool`, after existing MaxReady defaults and before `return &Pool{...}`:

```go
	if cfg.DiskPerVMMiB <= 0 {
		cfg.DiskPerVMMiB = OverlayEstimateMiB(cfg.ImagePath)
	}
	configuredMax := cfg.MaxReady
	clampReason := ""
	if deps.Inventory != nil {
		inv, err := deps.Inventory.Sample()
		if err != nil {
			log.Error("host inventory failed; refusing VMs", "err", err)
			cfg.MinReady = 0
			cfg.MaxReady = 0
			clampReason = "inventory_error"
		} else {
			var why string
			cfg, _, why = ClampPoolToHost(cfg, inv)
			if cfg.MaxReady < configuredMax {
				clampReason = why
				log.Info("host admission clamped max_ready",
					"configured", configuredMax,
					"effective", cfg.MaxReady,
					"reason", why,
					"ram_total_mib", inv.RAMTotalMiB,
					"disk_free_mib", inv.DiskFreeMiB,
				)
			}
		}
	}
```

Pass the new fields into the `Pool` literal: `inventory: deps.Inventory, configuredMax: configuredMax, clampReason: clampReason`.

Replace `canCreateLocked`:

```go
func (p *Pool) canCreateLocked() bool {
	c := p.countsLocked()
	total := c.Total() + p.createInFlight
	if total >= p.cfg.MaxTotalVMs {
		p.lastAdmit = "max_total_vms"
		p.log.Error("refusing create: at max_total_vms",
			"total", total,
			"max_total_vms", p.cfg.MaxTotalVMs,
			"destroying", c.Destroying,
			"last_destroy_err", p.lastDestroyErr,
		)
		return false
	}
	if p.inventory == nil {
		return true
	}
	inv, err := p.inventory.Sample()
	if err != nil {
		p.lastAdmit = "inventory_error"
		p.log.Error("refusing create: inventory sample failed", "err", err)
		return false
	}
	dec := p.admission().CanCreate(inv, total)
	if !dec.OK {
		p.lastAdmit = dec.Reason
		p.log.Error("refusing create: host resources",
			"reason", dec.Reason,
			"allocated", total,
			"memory_mib", p.cfg.MemoryMiB,
			"ram_avail_mib", inv.RAMAvailMiB,
			"disk_free_mib", inv.DiskFreeMiB,
		)
		return false
	}
	return true
}

func (p *Pool) admission() Admission {
	return Admission{
		MemoryMiB:      p.cfg.MemoryMiB,
		DiskMiB:        p.cfg.DiskPerVMMiB,
		ReserveRAMMiB:  p.cfg.ReserveRAMMiB,
		ReserveDiskMiB: p.cfg.ReserveDiskMiB,
	}
}

func (p *Pool) remainingCreatesLocked() int {
	if p.inventory == nil {
		return 1 << 20
	}
	inv, err := p.inventory.Sample()
	if err != nil {
		p.lastAdmit = "inventory_error"
		return 0
	}
	total := p.countsLocked().Total() + p.createInFlight
	return p.admission().Remaining(inv, total)
}

func (p *Pool) EffectiveMaxReady() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfg.MaxReady
}

func (p *Pool) ConfiguredMaxReady() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.configuredMax > 0 {
		return p.configuredMax
	}
	return p.cfg.MaxReady
}

func (p *Pool) RemainingCreates() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.remainingCreatesLocked()
}

func (p *Pool) LastAdmitReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAdmit
}

func (p *Pool) ClampReason() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.clampReason
}

func (p *Pool) InventorySample() (HostInventory, error) {
	if p == nil || p.inventory == nil {
		return HostInventory{}, nil
	}
	return p.inventory.Sample()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -count=1 -run 'TestPool_'`

Expected: PASS (existing pool tests plus the three new ones). Existing tests do not set `Inventory`, so they keep slot-only behavior.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/pool.go internal/agent/pool_test.go
git commit -m "feat(agent): clamp pool and refuse creates on host leftover"
```

---

### Task 5: Worker FreeSlots includes leftover

**Files:**
- Modify: `internal/agent/worker.go` (`Run` Capacity coerce; `snapshot`)
- Modify: `internal/agent/worker_test.go`

**Interfaces:**
- Consumes: `Pool.RemainingCreates()`, `Pool.Counts()`
- Produces: `FreeSlots = min(max(Capacity-used, 0), Warm+RemainingCreates)`; `Capacity < 0` becomes 0; `Capacity == 0` stays 0

- [ ] **Step 1: Write the failing tests**

Append to `internal/agent/worker_test.go`:

```go
func TestWorker_ResourceFullDoesNotClaim(t *testing.T) {
	var mu sync.Mutex
	var claims int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			_ = json.NewEncoder(w).Encode(api.RegisterResponse{OK: true, AgentID: "rf"})
		case "/v1/agent/jobs/claim":
			mu.Lock()
			claims++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ClaimResponse{OK: true, Job: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	inv := agent.StaticInventory{Inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 0, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 4096,
		DiskPerVMMiB: 256, ReconcileInterval: time.Hour, BindWait: 50 * time.Millisecond,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{}, Inventory: inv,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Shutdown(context.Background()) }()

	w := &agent.Worker{
		Client: agent.NewControlClient(ts.URL, "rf", "t", ts.Client()),
		Pool:   pool, Capacity: 2, PollInterval: 15 * time.Millisecond,
	}
	go func() { _ = w.Run(ctx) }()
	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	n := claims
	mu.Unlock()
	if n != 0 {
		t.Fatalf("claimed %d times; leftover RAM cannot create and warm=0", n)
	}
}

func TestWorker_WarmStillClaimsWhenCreateBlocked(t *testing.T) {
	var mu sync.Mutex
	var lastFree int
	var claims int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/agent/register":
			_ = json.NewEncoder(w).Encode(api.RegisterResponse{OK: true, AgentID: "rw"})
		case "/v1/agent/jobs/claim":
			var req api.ClaimRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			mu.Lock()
			claims++
			lastFree = req.FreeSlots
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(api.ClaimResponse{OK: true, Job: nil})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	root := t.TempDir()
	layout := vmm.NewLayout(root)
	_ = cleanup.EnsureLayout(layout)
	img := filepath.Join(layout.ImagesDir(), "b")
	_ = os.WriteFile(img, []byte("b"), 0o600)
	mgr, _ := fake.New(layout)
	cur := &atomicInventory{inv: agent.HostInventory{
		RAMTotalMiB: 65536, RAMAvailMiB: 60000, DiskTotalMiB: 200000, DiskFreeMiB: 200000,
	}}
	pool, err := agent.NewPool(agent.PoolConfig{
		MinReady: 1, MaxReady: 2, ImagePath: img, VCPUs: 1, MemoryMiB: 4096,
		DiskPerVMMiB: 256, ReconcileInterval: 20 * time.Millisecond, BindWait: time.Second,
	}, agent.PoolDeps{
		VMM: mgr, Cleaner: &cleanup.Cleaner{VMM: mgr, Layout: layout},
		Runner: &agent.StubRunner{}, Inventory: cur,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := pool.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pool.Shutdown(context.Background()) }()
	waitFor(t, 2*time.Second, func() bool { return pool.Counts().Warm >= 1 })
	cur.set(agent.HostInventory{RAMTotalMiB: 65536, RAMAvailMiB: 100, DiskTotalMiB: 200000, DiskFreeMiB: 200000})

	w := &agent.Worker{
		Client: agent.NewControlClient(ts.URL, "rw", "t", ts.Client()),
		Pool:   pool, Capacity: 2, PollInterval: 15 * time.Millisecond,
	}
	go func() { _ = w.Run(ctx) }()
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return claims > 0 && lastFree == 1
	})
}
```

`atomicInventory` lives in `pool_test.go` in the same `agent_test` package, so this file can use it.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/agent/ -count=1 -run 'TestWorker_ResourceFullDoesNotClaim|TestWorker_WarmStillClaimsWhenCreateBlocked'`

Expected: FAIL (`TestWorker_ResourceFullDoesNotClaim` sees claims > 0 because snapshot only uses `Capacity-used`).

- [ ] **Step 3: Change snapshot and Capacity coerce**

In `internal/agent/worker.go` `Run`, replace:

```go
	if w.Capacity <= 0 {
		w.Capacity = 1
	}
```

with:

```go
	if w.Capacity < 0 {
		w.Capacity = 0
	}
```

In `snapshot()`, replace the free-slot block:

```go
	free := w.Capacity - used
	if free < 0 {
		free = 0
	}
	bindBudget := c.Warm + w.Pool.RemainingCreates()
	if bindBudget < free {
		free = bindBudget
	}
	if free < 0 {
		free = 0
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/agent/ -count=1 -run 'TestWorker_'`

Expected: PASS. Confirm `TestWorker_NoCapacityDoesNotClaim` still passes (it uses Capacity 1 + busy VM, not Capacity 0).

- [ ] **Step 5: Commit**

```bash
git add internal/agent/worker.go internal/agent/worker_test.go
git commit -m "feat(agent): stop claiming when leftover cannot boot another VM"
```

---

### Task 6: Wire agent, register payload, control registry

**Files:**
- Modify: `internal/api/types.go`
- Modify: `internal/agent/client.go` (`CapacitySnapshot`, `Register`)
- Modify: `internal/agent/worker.go` (`snapshot` fills `Resources`)
- Modify: `internal/control/agents.go` (`Register` copies `Resources`)
- Modify: `cmd/temperci-agent/main.go`
- Test: `internal/control/scheduler_test.go` (append)

**Interfaces:**
- Consumes: pool getters from Task 4
- Produces:
  - `api.HostResources` with JSON keys: `ram_total_mib`, `ram_avail_mib`, `disk_total_mib`, `disk_free_mib`, `num_cpu`, `configured_max_ready`, `effective_max_ready`, `clamp_reason`, `last_admit_reason`
  - `RegisterRequest.Resources *HostResources`
  - `AgentInfo.Resources *HostResources`
  - `CapacitySnapshot.Resources *api.HostResources`
  - Agent main: `PoolDeps.Inventory = agent.ProcInventory{DataDir: cfg.DataDir}`; `Worker.Capacity = pool.EffectiveMaxReady()`

Add on `Pool`:

```go
func (p *Pool) HostResources() *api.HostResources
```

Implementation:

```go
func (p *Pool) HostResources() *api.HostResources {
	if p == nil {
		return nil
	}
	inv, err := p.InventorySample()
	if err != nil {
		return &api.HostResources{
			ConfiguredMaxReady: p.ConfiguredMaxReady(),
			EffectiveMaxReady:  p.EffectiveMaxReady(),
			ClampReason:        p.ClampReason(),
			LastAdmitReason:    "inventory_error",
		}
	}
	if p.inventory == nil {
		return nil
	}
	return &api.HostResources{
		RAMTotalMiB:        inv.RAMTotalMiB,
		RAMAvailMiB:        inv.RAMAvailMiB,
		DiskTotalMiB:       inv.DiskTotalMiB,
		DiskFreeMiB:        inv.DiskFreeMiB,
		NumCPU:             inv.NumCPU,
		ConfiguredMaxReady: p.ConfiguredMaxReady(),
		EffectiveMaxReady:  p.EffectiveMaxReady(),
		ClampReason:        p.ClampReason(),
		LastAdmitReason:    p.LastAdmitReason(),
	}
}
```

`InventorySample` currently returns zero value + nil when inventory is nil — `HostResources` must distinguish that. Change `InventorySample` callers: `HostResources` should check `p.inventory == nil` internally (same package — unexported field is visible). Keep `HostResources` in `pool.go`.

- [ ] **Step 1: Write the failing control test**

Append to `internal/control/scheduler_test.go`:

```go
func TestRegister_StoresHostResources(t *testing.T) {
	srv, _ := testAgentServer(t)
	req := agentReq(t, http.MethodPost, "/v1/agent/register", "agent-shared-token", api.RegisterRequest{
		AgentID:     "box-1",
		MaxCapacity: 1,
		Capacity:    1,
		Resources: &api.HostResources{
			RAMTotalMiB:        16384,
			RAMAvailMiB:        9000,
			DiskFreeMiB:        100000,
			NumCPU:             8,
			ConfiguredMaxReady: 4,
			EffectiveMaxReady:  1,
			ClampReason:        "ram",
		},
	})
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d", rr.Code)
	}
	info := srv.Agents().Get("box-1")
	if info == nil || info.Resources == nil {
		t.Fatal("expected resources on agent")
	}
	if info.Resources.EffectiveMaxReady != 1 || info.Resources.ClampReason != "ram" || info.Resources.NumCPU != 8 {
		t.Fatalf("resources %+v", info.Resources)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/control/ -count=1 -run TestRegister_StoresHostResources`

Expected: FAIL (`Resources` undefined).

- [ ] **Step 3: Implement types, registry, client, worker snapshot, main**

In `internal/api/types.go` add:

```go
// HostResources is the agent's view of leftover host compute and the clamped slot cap.
type HostResources struct {
	RAMTotalMiB        int    `json:"ram_total_mib"`
	RAMAvailMiB        int    `json:"ram_avail_mib"`
	DiskTotalMiB       int    `json:"disk_total_mib"`
	DiskFreeMiB        int    `json:"disk_free_mib"`
	NumCPU             int    `json:"num_cpu"`
	ConfiguredMaxReady int    `json:"configured_max_ready"`
	EffectiveMaxReady  int    `json:"effective_max_ready"`
	ClampReason        string `json:"clamp_reason,omitempty"`
	LastAdmitReason    string `json:"last_admit_reason,omitempty"`
}
```

On `RegisterRequest`:

```go
	Resources *HostResources `json:"resources,omitempty"`
```

On `AgentInfo`:

```go
	Resources *HostResources `json:"resources,omitempty"`
```

In `internal/control/agents.go` `Register`, after cache copy:

```go
	if req.Resources != nil {
		cp := *req.Resources
		info.Resources = &cp
	}
```

In `internal/agent/client.go` `CapacitySnapshot`:

```go
	Resources *api.HostResources
```

In `Register`, set `Resources: cap.Resources`.

In `internal/agent/worker.go` `snapshot()` return, add:

```go
		Resources:   w.Pool.HostResources(),
```

Add `HostResources()` on `Pool` as specified above (`internal/agent/pool.go`). Import `internal/api` in `pool.go` if not already imported.

In `cmd/temperci-agent/main.go`:

`NewPool` deps:

```go
	pool, err := agent.NewPool(poolCfg, agent.PoolDeps{
		VMM:       mgr,
		Cleaner:   cleaner,
		Runner:    runner,
		Log:       log,
		Inventory: agent.ProcInventory{DataDir: cfg.DataDir},
	})
```

After successful `NewPool`, log clamp:

```go
	log.Info("temperci-agent started",
		// existing keys...
		"max_ready", pool.EffectiveMaxReady(),
		"configured_max_ready", pool.ConfiguredMaxReady(),
		"clamp_reason", pool.ClampReason(),
	)
```

Worker:

```go
			Capacity:       pool.EffectiveMaxReady(),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/control/ ./internal/agent/ ./internal/api/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/api/types.go internal/control/agents.go internal/control/scheduler_test.go internal/agent/client.go internal/agent/worker.go internal/agent/pool.go cmd/temperci-agent/main.go
git commit -m "feat: report host leftover and effective max_ready to control"
```

---

### Task 7: Dashboard hosts page

**Files:**
- Modify: `web/src/api.ts` (`Host` type)
- Modify: `web/src/pages/HostsPage.tsx`
- Test: `internal/control/dashboard_status_test.go` or append a hosts JSON test in `internal/control/scheduler_test.go`

**Interfaces:**
- Consumes: `AgentInfo.resources` already returned by `GET /api/v1/hosts`
- Produces: Hosts table columns **Free**, **Max** (show `effective/configured` when they differ), **RAM avail**, **Disk free**, **CPU** (display only), clamp/refuse hint

- [ ] **Step 1: Write the failing Go hosts JSON assertion**

Append to `internal/control/scheduler_test.go`:

```go
func TestHostsAPI_IncludesResources(t *testing.T) {
	srv, _ := testAgentServer(t)
	_ = srv.Agents().Register(api.RegisterRequest{
		AgentID: "box-1", Capacity: 1, MaxCapacity: 1,
		Resources: &api.HostResources{RAMAvailMiB: 9000, DiskFreeMiB: 80000, NumCPU: 16, EffectiveMaxReady: 1, ConfiguredMaxReady: 4, ClampReason: "ram"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/hosts", nil)
	// If hosts requires UI auth in this test server, use the same helper other dashboard tests use.
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, req)
	if rr.Code == http.StatusUnauthorized || rr.Code == http.StatusForbidden {
		t.Skip("hosts API requires UI auth in this fixture; covered by dashboard_status_test pattern")
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"clamp_reason":"ram"`) {
		t.Fatalf("expected resources in hosts JSON: %s", rr.Body.String())
	}
}
```

Add `"strings"` to the `scheduler_test.go` import block.

If `GET /api/v1/hosts` always 401 in this fixture, look at `internal/control/dashboard_status_test.go` for how they call dashboard routes and use that helper instead. Do not skip as a permanent outcome — move the assertion onto a working authenticated request.

- [ ] **Step 2: Run test**

Run: `go test ./internal/control/ -count=1 -run TestHostsAPI_IncludesResources`

Expected: FAIL or skip-then-fix until the JSON contains `clamp_reason`. After Task 6, this may already PASS at the API layer. That is OK — keep the test.

- [ ] **Step 3: Update the Hosts page**

In `web/src/api.ts` extend `Host`:

```ts
export type HostResources = {
  ram_total_mib?: number;
  ram_avail_mib?: number;
  disk_total_mib?: number;
  disk_free_mib?: number;
  num_cpu?: number;
  configured_max_ready?: number;
  effective_max_ready?: number;
  clamp_reason?: string;
  last_admit_reason?: string;
};

export type Host = {
  agent_id: string;
  capacity?: number;
  max_capacity?: number;
  warm?: number;
  busy?: number;
  last_seen_at?: string;
  labels?: string[];
  resources?: HostResources;
  vms?: {
    id: string;
    state: string;
    cpu_percent?: number;
    rss_mib?: number;
    memory_mib?: number;
  }[];
};
```

In `web/src/pages/HostsPage.tsx`:

- Description: `Capacity is leftover job slots. Max is clamped to host RAM/disk. CPU is informational.`
- Add columns after **Max**: **RAM**, **Disk**, **CPU**
- Max cell: if `resources.configured_max_ready` and `resources.effective_max_ready` differ, render `effective/configured` (mono) and the `clamp_reason` as muted text (`ram` or `disk`).
- RAM cell: `resources.ram_avail_mib` as `X GiB` (`(mib/1024).toFixed(1)`), fallback `—`
- Disk cell: same for `disk_free_mib`
- CPU cell: `resources.num_cpu ?? "—"` (no warning color)
- If `last_admit_reason` is set and not empty, show it on the agent row as muted text (`refused: ram_available`)
- Bump `EmptyState` `colSpan` to the new column count

Helper in the file:

```ts
function fmtMiB(mib?: number): string {
  if (mib == null || Number.isNaN(mib)) return "—";
  if (mib >= 1024) return `${(mib / 1024).toFixed(1)} GiB`;
  return `${mib} MiB`;
}
```

- [ ] **Step 4: Typecheck / unit test**

Run: `cd web && npx tsc -p tsconfig.app.json --noEmit`

Expected: no errors.

Run: `go test ./internal/control/ -count=1 -run 'TestRegister_StoresHostResources|TestHostsAPI_IncludesResources'`

Expected: PASS

If a dashboard dev server is already running, open `/hosts`, confirm a registered agent shows RAM/disk/CPU and a clamp reason when `effective != configured`. If no browser tools and no running UI, say so in the PR/commit body; the Go hosts JSON test is the required proof.

- [ ] **Step 5: Commit**

```bash
git add web/src/api.ts web/src/pages/HostsPage.tsx internal/control/scheduler_test.go
git commit -m "feat(ui): show host leftover RAM, disk, and clamped max"
```

Do **not** rebuild `internal/webui/dist` unless this repo’s current working tree already treats dist as source-of-truth for the embedded UI (it does). After the TS change, run `make build-ui` and commit the new `internal/webui/dist/assets/*` plus `index.html` in the same commit or a follow-up `chore: rebuild embedded dashboard`.

---

### Task 8: Operator docs

**Files:**
- Modify: `deploy/agent.example.toml`
- Modify: `docs/architecture/job-lifecycle.md`
- Modify: `docs/architecture/install-targets.md`

**Interfaces:**
- Consumes: config key names and admission rules from the spec
- Produces: documented reserve defaults and the create-gate behavior

- [ ] **Step 1: Edit the example agent config**

In `deploy/agent.example.toml`, after the `max_ready` / `max_total_vms` block, add:

```toml
# Host headroom the agent will not give to microVMs.
# Effective max_ready is min(max_ready, how many VMs fit in leftover RAM/disk).
# A create is refused if MemAvailable < memory_mib or free disk < overlay+reserve.
# CPU is not a hard gate.
# host_reserve_memory_mib = 2048
# host_reserve_disk_mib = 5120
```

Leave the keys commented so existing copies keep the defaults.

- [ ] **Step 2: Edit job-lifecycle.md**

After the Warm pool “Rules” list, add:

```markdown
### Host resource admission

`max_ready` is the operator’s desired cap, not a promise the hardware can keep.

1. On agent start, sample host RAM (`MemTotal`) and free disk on `data_dir`. Compute how many VMs of this host’s `memory_mib` and overlay size fit after `host_reserve_memory_mib` (default 2 GiB) and `host_reserve_disk_mib` (default 5 GiB). Clamp `min_ready` / `max_ready` down to that fit.
2. Before every create (warm replenish or cold bind), refuse if committed guest RAM, live `MemAvailable`, or leftover disk cannot cover the next VM plus reserve.
3. Existing warm VMs may still bind (their RAM is already committed).
4. The worker reports `FreeSlots = min(effective slots, warm + remaining creates)`. Control does not assign when that is 0; the job stays pending.
5. vCPU count is reported on the host snapshot and is not a create gate.
```

- [ ] **Step 3: Edit install-targets.md**

Replace the resource table rows so they match:

```markdown
| Setting | Purpose |
|---------|---------|
| `min_ready` / `max_ready` | Desired warm pool / concurrent jobs. Agent clamps this to host RAM+disk. |
| `vcpu` / `memory_mib` | Job shape. RAM is a hard create gate; vCPU is not. |
| `host_reserve_memory_mib` / `host_reserve_disk_mib` | Headroom left for the host OS, cache, and overlays (defaults 2048 / 5120). |
| Disk path for images + scratch (`data_dir`) | Prefer fast **local** NVMe; avoid shared Ceph/NFS for `instances/` |
```

Remove the now-wrong “Max concurrent busy VMs | Protect the host” row (that is `max_ready` + admission).

- [ ] **Step 4: No test run required beyond a quick compile**

Run: `go test ./internal/config/ ./internal/agent/ -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add deploy/agent.example.toml docs/architecture/job-lifecycle.md docs/architecture/install-targets.md
git commit -m "docs: describe host leftover admission for microVMs"
```

---

## Self-review

**Spec coverage**

| Spec requirement | Task |
|---|---|
| Clamp `max_ready` from host RAM + disk at start | 1, 4 |
| Refuse create on committed RAM, MemAvailable, disk free | 1, 4 |
| `FreeSlots` uses leftover so control does not assign | 5 |
| CPU observability only | 6, 7 (NumCPU displayed, never gated) |
| Agent-local; control stores snapshot only | 6 |
| Warm bind still works | 4, 5 |
| Inventory failure fail-closed | 2, 4 |
| Nil inventory keeps old tests | 4 |
| Capacity 0 valid | 5 |
| Config reserve knobs + defaults | 3 |
| Dashboard leftover + clamp reason | 7 |
| Operator docs | 8 |

**Placeholder scan:** none. Types, reasons, defaults, and test bodies are written out.

**Type consistency:** `HostInventory`, `Admission`, `AdmitDecision`, `InventorySource`, `StaticInventory`, `ProcInventory`, `HostResources`, reason constants, and pool getters are named the same in every task.
