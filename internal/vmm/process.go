package vmm

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ReadPIDFile reads a process id from path. Returns 0 if the file is missing
// or contains a non-positive value.
func ReadPIDFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("vmm: invalid pid file %s: %w", path, err)
	}
	if pid < 0 {
		return 0, fmt.Errorf("vmm: invalid pid %d", pid)
	}
	return pid, nil
}

// WritePIDFile stores pid at path.
func WritePIDFile(path string, pid int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o600)
}

// StopProcess sends SIGTERM then SIGKILL to pid if it is still alive.
// pid <= 0 is a no-op. Missing processes are ignored (idempotent).
func StopProcess(pid int, grace time.Duration) error {
	if pid <= 0 {
		return nil
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	// Signal 0 checks existence on Unix; on platforms where it always
	// succeeds we still attempt terminate below.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return nil
	}
	_ = proc.Signal(syscall.SIGTERM)
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = proc.Signal(syscall.SIGKILL)
	return nil
}
