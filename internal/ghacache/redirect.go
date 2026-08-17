package ghacache

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ListenPort extracts the TCP port from a listen address like "127.0.0.1:8743".
func ListenPort(addr string) int {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return 0
	}
	p, err := strconv.Atoi(addr[i+1:])
	if err != nil {
		return 0
	}
	return p
}

// RedirectGuestHTTPS installs a REDIRECT of guest TCP/443 on tap to local port.
// No-op on non-Linux. Best-effort: missing iptables is logged by the caller.
func RedirectGuestHTTPS(tap string, port int) error {
	if runtime.GOOS != "linux" || tap == "" || port <= 0 {
		return nil
	}
	spec := []string{"PREROUTING", "-t", "nat", "-i", tap, "-p", "tcp", "--dport", "443", "-j", "REDIRECT", "--to-ports", fmt.Sprintf("%d", port)}
	if exec.Command("iptables", append([]string{"-C"}, spec...)...).Run() == nil {
		return nil
	}
	if out, err := exec.Command("iptables", append([]string{"-A"}, spec...)...).CombinedOutput(); err != nil {
		return fmt.Errorf("iptables redirect: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
