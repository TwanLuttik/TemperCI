package ghacache

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ListenPort extracts the TCP port from a listen address like "127.0.0.1:8743".
func ListenPort(addr string) int {
	_, port := SplitListenAddr(addr)
	return port
}

// SplitListenAddr returns host and port from "127.0.0.1:8743" or ":8743".
func SplitListenAddr(addr string) (host string, port int) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", 0
	}
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		// Fall back to "...:port" without brackets (IPv4 / hostname).
		i := strings.LastIndex(addr, ":")
		if i < 0 {
			return "", 0
		}
		n, convErr := strconv.Atoi(addr[i+1:])
		if convErr != nil {
			return "", 0
		}
		return addr[:i], n
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return h, 0
	}
	return h, n
}

// GuestHTTPSRedirectSpec builds the iptables PREROUTING args that send guest
// TCP/443 to the cache intercept listener.
//
// iptables REDIRECT rewrites the destination to the tap's host IP (10.231.x.1),
// not 127.0.0.1. A gateway bound to 127.0.0.1:port never sees those packets,
// so the guest cannot reach GitHub and jobs stay queued. Loopback binds use
// DNAT to 127.0.0.1 plus route_localnet instead.
func GuestHTTPSRedirectSpec(tap, listenAddr string) (spec []string, loopback bool) {
	host, port := SplitListenAddr(listenAddr)
	if tap == "" || port <= 0 {
		return nil, false
	}
	base := []string{"PREROUTING", "-t", "nat", "-i", tap, "-p", "tcp", "--dport", "443"}
	switch strings.ToLower(strings.Trim(host, "[]")) {
	case "", "0.0.0.0", "::", "*":
		return append(base, "-j", "REDIRECT", "--to-ports", strconv.Itoa(port)), false
	case "127.0.0.1", "localhost", "::1":
		return append(base, "-j", "DNAT", "--to-destination", fmt.Sprintf("127.0.0.1:%d", port)), true
	default:
		return append(base, "-j", "DNAT", "--to-destination", fmt.Sprintf("%s:%d", host, port)), false
	}
}

func legacyRedirectSpec(tap, listenAddr string) []string {
	_, port := SplitListenAddr(listenAddr)
	if tap == "" || port <= 0 {
		return nil
	}
	return []string{"PREROUTING", "-t", "nat", "-i", tap, "-p", "tcp", "--dport", "443", "-j", "REDIRECT", "--to-ports", strconv.Itoa(port)}
}

// GuestHTTPSInputSpec is the filter INPUT rule that must sit in front of host
// firewalls (PVEFW-HOST-IN, Tailscale). After PREROUTING DNAT/REDIRECT, guest
// SYNs arrive on the tap with dport=listenPort. PVE accepts only lo + a few
// management ports and otherwise DROP — without this, jobs stay queued.
func GuestHTTPSInputSpec(tap, listenAddr string) []string {
	_, port := SplitListenAddr(listenAddr)
	if tap == "" || port <= 0 {
		return nil
	}
	return []string{"INPUT", "-i", tap, "-p", "tcp", "--dport", strconv.Itoa(port), "-j", "ACCEPT"}
}

// RedirectGuestHTTPS installs NAT so guest :443 reaches listenAddr.
// No-op on non-Linux. Best-effort: missing iptables is returned to the caller.
func RedirectGuestHTTPS(tap, listenAddr string) error {
	if runtime.GOOS != "linux" {
		return nil
	}
	spec, loopback := GuestHTTPSRedirectSpec(tap, listenAddr)
	if spec == nil {
		return nil
	}
	if loopback {
		enableLocalnetRedirect(tap)
	}
	if exec.Command("iptables", append([]string{"-C"}, spec...)...).Run() != nil {
		if out, err := exec.Command("iptables", append([]string{"-A"}, spec...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("iptables redirect: %w (%s)", err, strings.TrimSpace(string(out)))
		}
	}
	if input := GuestHTTPSInputSpec(tap, listenAddr); input != nil {
		if exec.Command("iptables", append([]string{"-C"}, input...)...).Run() != nil {
			// -I INPUT (position 1): PVEFW-INPUT jumps and DROPs; an append
			// after that jump is never reached.
			if out, err := exec.Command("iptables", append([]string{"-I"}, input...)...).CombinedOutput(); err != nil {
				return fmt.Errorf("iptables input accept: %w (%s)", err, strings.TrimSpace(string(out)))
			}
		}
	}
	return nil
}

// ClearGuestHTTPSRedirect removes the NAT rule for tap (current + legacy REDIRECT).
func ClearGuestHTTPSRedirect(tap, listenAddr string) error {
	if runtime.GOOS != "linux" || tap == "" {
		return nil
	}
	if spec, _ := GuestHTTPSRedirectSpec(tap, listenAddr); spec != nil {
		_ = exec.Command("iptables", append([]string{"-D"}, spec...)...).Run()
	}
	if legacy := legacyRedirectSpec(tap, listenAddr); legacy != nil {
		_ = exec.Command("iptables", append([]string{"-D"}, legacy...)...).Run()
	}
	if input := GuestHTTPSInputSpec(tap, listenAddr); input != nil {
		_ = exec.Command("iptables", append([]string{"-D"}, input...)...).Run()
	}
	return nil
}

func enableLocalnetRedirect(tap string) {
	_ = os.WriteFile("/proc/sys/net/ipv4/conf/all/route_localnet", []byte("1"), 0o644)
	if tap != "" {
		_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+tap+"/route_localnet", []byte("1"), 0o644)
		_ = os.WriteFile("/proc/sys/net/ipv4/conf/"+tap+"/rp_filter", []byte("0"), 0o644)
	}
}
