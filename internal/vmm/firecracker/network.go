package firecracker

import (
	"fmt"
	"hash/fnv"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

// guestNet returns host/guest IPs for a unique /30 carved from 10.231.0.0/16.
func guestNet(id vmm.ID) (hostIP, guestIP, gateway, prefix string, err error) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	// 16384 /30s in a /16; keep index away from .0
	idx := int(h.Sum32()%16000) + 1
	base := idx * 4
	b1 := (base >> 8) & 0xff
	b2 := base & 0xff
	// 10.231.b1.b2 is network; +1 host, +2 guest
	hostIP = fmt.Sprintf("10.231.%d.%d", b1, b2+1)
	guestIP = fmt.Sprintf("10.231.%d.%d", b1, b2+2)
	gateway = hostIP
	prefix = "30"
	// validate
	if net.ParseIP(hostIP) == nil || net.ParseIP(guestIP) == nil {
		return "", "", "", "", fmt.Errorf("bad ip derivation")
	}
	return hostIP, guestIP, gateway, prefix, nil
}

func realSetupNetwork(id vmm.ID, netDir string) (vmm.NetworkState, error) {
	if runtime.GOOS != "linux" {
		return defaultSetupNetwork(id, netDir)
	}
	tap := tapName(id)
	hostIP, guestIP, gw, prefix, err := guestNet(id)
	if err != nil {
		return vmm.NetworkState{}, err
	}
	// Create tap
	_ = exec.Command("ip", "link", "del", tap).Run()
	if out, err := exec.Command("ip", "tuntap", "add", "dev", tap, "mode", "tap").CombinedOutput(); err != nil {
		// Fall back to markers-only if not permitted.
		st, e2 := defaultSetupNetwork(id, netDir)
		_ = os.WriteFile(filepath.Join(netDir, "network.error"), []byte(fmt.Sprintf("tuntap: %v %s", err, out)), 0o600)
		return st, e2
	}
	if out, err := exec.Command("ip", "addr", "add", hostIP+"/"+prefix, "dev", tap).CombinedOutput(); err != nil {
		_ = exec.Command("ip", "link", "del", tap).Run()
		return vmm.NetworkState{}, fmt.Errorf("ip addr: %w (%s)", err, out)
	}
	if out, err := exec.Command("ip", "link", "set", "dev", tap, "up").CombinedOutput(); err != nil {
		_ = exec.Command("ip", "link", "del", tap).Run()
		return vmm.NetworkState{}, fmt.Errorf("ip link up: %w (%s)", err, out)
	}
	// Enable forwarding + NAT (best effort).
	_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte("1"), 0o644)
	if exec.Command("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", "10.231.0.0/16", "-j", "MASQUERADE").Run() != nil {
		_ = exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", "10.231.0.0/16", "-j", "MASQUERADE").Run()
	}
	if exec.Command("iptables", "-C", "FORWARD", "-i", tap, "-j", "ACCEPT").Run() != nil {
		_ = exec.Command("iptables", "-A", "FORWARD", "-i", tap, "-j", "ACCEPT").Run()
	}
	if exec.Command("iptables", "-C", "FORWARD", "-o", tap, "-j", "ACCEPT").Run() != nil {
		_ = exec.Command("iptables", "-A", "FORWARD", "-o", tap, "-j", "ACCEPT").Run()
	}
	// PVE/Tailscale INPUT DROP swallows guest packets to the TAP host IP.
	// UDP 9876 = ready/exit + log chunks. TCP 9877 = optional log stream.
	for _, spec := range mailboxInputSpecs(tap) {
		if exec.Command("iptables", append([]string{"-C"}, spec...)...).Run() != nil {
			_ = exec.Command("iptables", append([]string{"-I"}, spec...)...).Run()
		}
	}

	proxy := filepath.Join(netDir, "proxy.marker")
	_ = os.WriteFile(proxy, []byte(string(id)+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(netDir, "tap"), []byte(tap+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(netDir, "host_ip"), []byte(hostIP+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(netDir, "guest_ip"), []byte(guestIP+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(netDir, "gateway"), []byte(gw+"\n"), 0o600)
	_ = os.WriteFile(filepath.Join(netDir, "prefix"), []byte(prefix+"\n"), 0o600)

	return vmm.NetworkState{
		TapDevice:   tap,
		NetNS:       "", // not using netns for simple NAT path
		ProxyMarker: proxy,
	}, nil
}

// mailboxInputSpecs accept guest mailbox traffic on the TAP (insert before PVE DROP).
func mailboxInputSpecs(tap string) [][]string {
	if tap == "" {
		return nil
	}
	return [][]string{
		{"INPUT", "-i", tap, "-p", "udp", "--dport", "9876", "-j", "ACCEPT"},
		{"INPUT", "-i", tap, "-p", "tcp", "--dport", "9877", "-j", "ACCEPT"},
	}
}

// mailboxInputSpec is the UDP ready/exit rule (tests).
func mailboxInputSpec(tap string) []string {
	specs := mailboxInputSpecs(tap)
	if len(specs) == 0 {
		return nil
	}
	return specs[0]
}

func realTeardownNetwork(id vmm.ID, net vmm.NetworkState) error {
	_ = id
	if net.ProxyMarker != "" {
		_ = os.Remove(net.ProxyMarker)
	}
	if runtime.GOOS == "linux" && net.TapDevice != "" {
		_ = exec.Command("iptables", "-D", "FORWARD", "-i", net.TapDevice, "-j", "ACCEPT").Run()
		_ = exec.Command("iptables", "-D", "FORWARD", "-o", net.TapDevice, "-j", "ACCEPT").Run()
		for _, spec := range mailboxInputSpecs(net.TapDevice) {
			_ = exec.Command("iptables", append([]string{"-D"}, spec...)...).Run()
		}
		_ = exec.Command("ip", "link", "del", net.TapDevice).Run()
	}
	return nil
}

// bootArgs builds Linux kernel cmdline including static IP for the guest NIC.
func bootArgs(id vmm.ID, netDir string) string {
	base := "console=ttyS0 reboot=k panic=1 pci=off root=/dev/vda rw systemd.unified_cgroup_hierarchy=1 selinux=0"
	guestIP := readTrim(filepath.Join(netDir, "guest_ip"))
	gw := readTrim(filepath.Join(netDir, "gateway"))
	prefix := readTrim(filepath.Join(netDir, "prefix"))
	if guestIP == "" || gw == "" {
		return base
	}
	mask := "255.255.255.252"
	if prefix == "24" {
		mask = "255.255.255.0"
	}
	// ip=client-ip:server-ip:gw-ip:netmask:hostname:device:autoconf
	ipCfg := fmt.Sprintf("ip=%s::%s:%s:temperci:eth0:off", guestIP, gw, mask)
	return base + " " + ipCfg
}

func readTrim(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
