// Package firecracker implements the vmm.Manager interface using the
// Firecracker VMM on Linux/KVM hosts.
//
// On non-Linux platforms, or when the firecracker binary /dev/kvm is missing,
// New returns a clear error. Unit tests should use internal/vmm/fake instead.
package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/ghacache"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
)

const backendName = "firecracker"

// Config configures the Firecracker backend.
type Config struct {
	Layout vmm.Layout
	// Binary is the path to the firecracker executable (default: "firecracker" on PATH).
	Binary string
	// JailerBinary is optional; when set, jailer is used (MVP may leave empty).
	JailerBinary string
	// SkipKVMCheck allows constructing the manager without /dev/kvm (used in tests of paths only).
	SkipKVMCheck bool
	// HTTPTimeout for Firecracker API calls.
	HTTPTimeout time.Duration
	// StopGrace is how long to wait after SIGTERM before SIGKILL.
	StopGrace time.Duration
	// RunCmd, if set, replaces the default process starter (tests).
	RunCmd func(ctx context.Context, name string, args ...string) (pid int, wait func() error, err error)
	// Network hooks (optional). Defaults are no-ops that only write markers.
	SetupNetwork    func(id vmm.ID, netDir string) (vmm.NetworkState, error)
	TeardownNetwork func(id vmm.ID, net vmm.NetworkState) error
	// CacheListenAddr, when set, NAT-redirects guest :443 on each tap to this
	// host:port so the Actions cache gateway can intercept results/blob TLS.
	// Use 127.0.0.1:port (DNAT + route_localnet); 0.0.0.0:port uses REDIRECT.
	CacheListenAddr string
}

// Manager drives Firecracker instances under a host layout root.
type Manager struct {
	cfg    Config
	layout vmm.Layout
	mu     sync.Mutex
}

// New validates the host environment and returns a Firecracker Manager.
func New(cfg Config) (*Manager, error) {
	if err := cfg.Layout.Validate(); err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("firecracker: unsupported OS %q (requires linux/kvm)", runtime.GOOS)
	}
	if cfg.Binary == "" {
		cfg.Binary = "firecracker"
	}
	if _, err := exec.LookPath(cfg.Binary); err != nil {
		// Also accept absolute path that exists.
		if _, statErr := os.Stat(cfg.Binary); statErr != nil {
			return nil, fmt.Errorf("firecracker: binary %q not found: %w", cfg.Binary, err)
		}
	}
	if !cfg.SkipKVMCheck {
		if _, err := os.Stat("/dev/kvm"); err != nil {
			return nil, fmt.Errorf("firecracker: /dev/kvm not available: %w", err)
		}
	}
	if cfg.HTTPTimeout == 0 {
		cfg.HTTPTimeout = 10 * time.Second
	}
	if cfg.StopGrace == 0 {
		cfg.StopGrace = 2 * time.Second
	}
	if cfg.SetupNetwork == nil {
		cfg.SetupNetwork = realSetupNetwork
	}
	if cfg.TeardownNetwork == nil {
		cfg.TeardownNetwork = realTeardownNetwork
	}
	if addr := strings.TrimSpace(cfg.CacheListenAddr); addr != "" && ghacache.ListenPort(addr) > 0 {
		innerSetup := cfg.SetupNetwork
		innerTear := cfg.TeardownNetwork
		cfg.SetupNetwork = func(id vmm.ID, netDir string) (vmm.NetworkState, error) {
			st, err := innerSetup(id, netDir)
			if err != nil {
				return st, err
			}
			if st.TapDevice != "" {
				if rerr := ghacache.RedirectGuestHTTPS(st.TapDevice, addr); rerr != nil {
					_ = os.WriteFile(filepath.Join(netDir, "cache.redirect.err"), []byte(rerr.Error()), 0o600)
				}
			}
			return st, nil
		}
		cfg.TeardownNetwork = func(id vmm.ID, net vmm.NetworkState) error {
			if net.TapDevice != "" {
				_ = ghacache.ClearGuestHTTPSRedirect(net.TapDevice, addr)
			}
			return innerTear(id, net)
		}
	}
	if err := os.MkdirAll(cfg.Layout.ImagesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("firecracker: images dir: %w", err)
	}
	if err := os.MkdirAll(cfg.Layout.InstancesDir(), 0o755); err != nil {
		return nil, fmt.Errorf("firecracker: instances dir: %w", err)
	}
	return &Manager{cfg: cfg, layout: cfg.Layout}, nil
}

// NewForTest constructs a Manager without Linux/KVM/binary checks.
// Production code must use New. Intended for unit tests of destroy/layout.
func NewForTest(layout vmm.Layout) *Manager {
	cfg := Config{
		Layout:          layout,
		Binary:          "firecracker",
		SkipKVMCheck:    true,
		HTTPTimeout:     time.Second,
		StopGrace:       50 * time.Millisecond,
		SetupNetwork:    defaultSetupNetwork,
		TeardownNetwork: defaultTeardownNetwork,
	}
	_ = os.MkdirAll(layout.ImagesDir(), 0o755)
	_ = os.MkdirAll(layout.InstancesDir(), 0o755)
	return &Manager{cfg: cfg, layout: layout}
}

// Layout returns the host layout.
func (m *Manager) Layout() vmm.Layout { return m.layout }

// Create provisions instance directories, COW overlay, and network markers.
// It does not start the Firecracker process; call Boot next.
func (m *Manager) Create(ctx context.Context, cfg vmm.Config) (*vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.RootfsPath == "" {
		return nil, fmt.Errorf("firecracker: rootfs_path is required")
	}
	if cfg.KernelPath == "" {
		return nil, fmt.Errorf("firecracker: kernel_path is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.layout.InstanceDir(cfg.ID)
	if _, err := os.Stat(dir); err == nil {
		return nil, fmt.Errorf("%w: %s", vmm.ErrExists, cfg.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	if err := os.MkdirAll(m.layout.NetDir(cfg.ID), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(m.layout.LogDir(cfg.ID), 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	// COW overlay: copy base rootfs into instance dir as a starting point.
	// Production may switch to reflink/qcow2/overlayfs; copy is correct and simple.
	if err := copyFile(cfg.RootfsPath, m.layout.OverlayPath(cfg.ID)); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("firecracker: create overlay: %w", err)
	}

	// Second disk for host↔guest inject (JIT + runner.exit). Best-effort format.
	if err := createInjectDrive(m.layout.InjectDrivePath(cfg.ID)); err != nil {
		// Tests without mkfs: leave empty file so Destroy still works.
		_ = os.WriteFile(m.layout.InjectDrivePath(cfg.ID), make([]byte, 4096), 0o600)
		_ = os.WriteFile(filepath.Join(dir, "inject.warn"), []byte(err.Error()), 0o600)
	}
	_ = os.MkdirAll(m.layout.GuestDir(cfg.ID), 0o700)

	netState, err := m.cfg.SetupNetwork(cfg.ID, m.layout.NetDir(cfg.ID))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("firecracker: setup network: %w", err)
	}

	meta := vmm.InstanceMeta{
		ID:         cfg.ID,
		State:      vmm.StateCreated,
		VCPUs:      cfg.VCPUs,
		MemoryMiB:  cfg.MemoryMiB,
		RootfsPath: cfg.RootfsPath,
		KernelPath: cfg.KernelPath,
		CreatedAt:  time.Now().UTC(),
		Metadata:   cfg.Metadata,
		Backend:    backendName,
		Network:    netState,
	}
	if err := vmm.WriteMeta(m.layout.MetaPath(cfg.ID), meta); err != nil {
		_ = m.cfg.TeardownNetwork(cfg.ID, netState)
		_ = os.RemoveAll(dir)
		return nil, err
	}
	info := meta.ToInfo()
	return &info, nil
}

// Boot starts Firecracker, configures the machine via the API, and issues InstanceStart.
func (m *Manager) Boot(ctx context.Context, id vmm.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", vmm.ErrNotFound, id)
		}
		return err
	}
	if meta.State == vmm.StateRunning {
		return fmt.Errorf("%w: %s", vmm.ErrAlreadyRunning, id)
	}

	sock := m.layout.APISockPath(id)
	_ = os.Remove(sock)

	pid, waitFn, err := m.startProcess(ctx, id, sock)
	if err != nil {
		return err
	}
	// If configuration fails, stop the process.
	if err := m.configureAndStart(ctx, id, meta, sock); err != nil {
		_ = vmm.StopProcess(pid, m.cfg.StopGrace)
		if waitFn != nil {
			_ = waitFn()
		}
		return err
	}

	if err := vmm.WritePIDFile(m.layout.PIDPath(id), pid); err != nil {
		_ = vmm.StopProcess(pid, m.cfg.StopGrace)
		return err
	}
	meta.State = vmm.StateRunning
	meta.PID = pid
	return vmm.WriteMeta(m.layout.MetaPath(id), meta)
}

// Destroy stops the VMM process, tears down network, and deletes instance dir.
// Idempotent: missing instances return nil.
func (m *Manager) Destroy(ctx context.Context, id vmm.ID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := id.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return m.destroyLocked(id)
}

func (m *Manager) destroyLocked(id vmm.ID) error {
	dir := m.layout.InstanceDir(id)
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		// Still best-effort network teardown from synthetic names.
		_ = m.cfg.TeardownNetwork(id, vmm.NetworkState{
			TapDevice: tapName(id),
			NetNS:     netNSName(id),
		})
		return nil
	}

	meta, metaErr := vmm.ReadMeta(m.layout.MetaPath(id))

	// 1. Stop guest / VMM process.
	pid := 0
	if metaErr == nil && meta.PID > 0 {
		pid = meta.PID
	} else {
		p, _ := vmm.ReadPIDFile(m.layout.PIDPath(id))
		pid = p
	}
	_ = vmm.StopProcess(pid, m.cfg.StopGrace)
	_ = os.Remove(m.layout.PIDPath(id))
	_ = os.Remove(m.layout.APISockPath(id))

	// 2. Remove VM definition / jailer resources (instance dir is the jail unit for MVP).
	// 3. Delete COW/overlay + metadata via RemoveAll.
	// 4. Remove taps/netns/proxy state.
	netState := vmm.NetworkState{TapDevice: tapName(id), NetNS: netNSName(id)}
	if metaErr == nil {
		netState = meta.Network
	}
	if err := m.cfg.TeardownNetwork(id, netState); err != nil {
		// Continue deleting disk; surface network error after.
		removeErr := os.RemoveAll(dir)
		if removeErr != nil {
			return fmt.Errorf("firecracker: destroy %s: network: %v; remove: %w", id, err, removeErr)
		}
		return fmt.Errorf("firecracker: destroy %s network: %w", id, err)
	}

	// Full scratch removal (overlay, meta, logs, net markers, sockets).
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("firecracker: remove instance %s: %w", id, err)
	}
	return nil
}

// Exists reports whether the instance directory is present.
func (m *Manager) Exists(ctx context.Context, id vmm.ID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := id.Validate(); err != nil {
		return false, err
	}
	_, err := os.Stat(m.layout.InstanceDir(id))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Info returns instance metadata.
func (m *Manager) Info(ctx context.Context, id vmm.ID) (*vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", vmm.ErrNotFound, id)
		}
		return nil, err
	}
	info := meta.ToInfo()
	return &info, nil
}

// List returns all instances under the layout.
func (m *Manager) List(ctx context.Context) ([]vmm.Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(m.layout.InstancesDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []vmm.Info
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := vmm.ID(e.Name())
		if err := id.Validate(); err != nil {
			continue
		}
		meta, err := vmm.ReadMeta(m.layout.MetaPath(id))
		if err != nil {
			out = append(out, vmm.Info{ID: id, State: vmm.StateCreated})
			continue
		}
		out = append(out, meta.ToInfo())
	}
	return out, nil
}

func (m *Manager) startProcess(ctx context.Context, id vmm.ID, sock string) (int, func() error, error) {
	args := []string{
		"--api-sock", sock,
		"--id", string(id),
	}
	if m.cfg.RunCmd != nil {
		return m.cfg.RunCmd(ctx, m.cfg.Binary, args...)
	}
	cmd := exec.CommandContext(ctx, m.cfg.Binary, args...)
	// Capture guest serial (console=ttyS0) + FC logs for operator diagnosis.
	logDir := m.layout.LogDir(id)
	_ = os.MkdirAll(logDir, 0o755)
	consolePath := filepath.Join(logDir, "console.log")
	consoleFile, err := os.OpenFile(consolePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		cmd.Stdout = nil
		cmd.Stderr = nil
	} else {
		cmd.Stdout = consoleFile
		cmd.Stderr = consoleFile
	}
	if err := cmd.Start(); err != nil {
		if consoleFile != nil {
			_ = consoleFile.Close()
		}
		return 0, nil, fmt.Errorf("firecracker: start: %w", err)
	}
	wait := func() error {
		err := cmd.Wait()
		if consoleFile != nil {
			_ = consoleFile.Close()
		}
		return err
	}
	return cmd.Process.Pid, wait, nil
}

func (m *Manager) configureAndStart(ctx context.Context, id vmm.ID, meta vmm.InstanceMeta, sock string) error {
	client, err := m.apiClient(sock)
	if err != nil {
		return err
	}

	// Machine config
	if err := client.put(ctx, "/machine-config", map[string]any{
		"vcpu_count":   meta.VCPUs,
		"mem_size_mib": meta.MemoryMiB,
	}); err != nil {
		return fmt.Errorf("firecracker: machine-config: %w", err)
	}
	// Boot source (static IP via kernel cmdline when net files present).
	if err := client.put(ctx, "/boot-source", map[string]any{
		"kernel_image_path": meta.KernelPath,
		"boot_args":         bootArgs(id, m.layout.NetDir(id)),
	}); err != nil {
		return fmt.Errorf("firecracker: boot-source: %w", err)
	}
	// Root drive (COW overlay path)
	if err := client.put(ctx, "/drives/rootfs", map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   m.layout.OverlayPath(id),
		"is_root_device": true,
		"is_read_only":   false,
	}); err != nil {
		return fmt.Errorf("firecracker: drives: %w", err)
	}
	// Inject drive (/dev/vdb) — JIT + runner.exit for guest agent.
	injectPath := m.layout.InjectDrivePath(id)
	if _, err := os.Stat(injectPath); err == nil {
		if err := client.put(ctx, "/drives/inject", map[string]any{
			"drive_id":       "inject",
			"path_on_host":   injectPath,
			"is_root_device": false,
			"is_read_only":   false,
		}); err != nil {
			return fmt.Errorf("firecracker: inject drive: %w", err)
		}
	}
	// Network interface when a real tap was provisioned.
	if meta.Network.TapDevice != "" {
		if _, err := os.Stat(filepath.Join(m.layout.NetDir(id), "host_ip")); err == nil {
			if err := client.put(ctx, "/network-interfaces/eth0", map[string]any{
				"iface_id":      "eth0",
				"host_dev_name": meta.Network.TapDevice,
				"guest_mac":     "AA:FC:00:00:00:01",
			}); err != nil {
				return fmt.Errorf("firecracker: network-interfaces: %w", err)
			}
		}
	}
	// Start
	if err := client.put(ctx, "/actions", map[string]any{
		"action_type": "InstanceStart",
	}); err != nil {
		return fmt.Errorf("firecracker: InstanceStart: %w", err)
	}
	return nil
}

type apiClient struct {
	http    *http.Client
	baseURL string
}

func (m *Manager) apiClient(sock string) (*apiClient, error) {
	// Wait briefly for the socket to appear.
	deadline := time.Now().Add(m.cfg.HTTPTimeout)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("firecracker: api sock %s not ready", sock)
		}
		time.Sleep(10 * time.Millisecond)
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	return &apiClient{
		http:    &http.Client{Transport: tr, Timeout: m.cfg.HTTPTimeout},
		baseURL: "http://localhost",
	}, nil
}

func (c *apiClient) put(ctx context.Context, path string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}
	return nil
}

func defaultSetupNetwork(id vmm.ID, netDir string) (vmm.NetworkState, error) {
	tap := tapName(id)
	ns := netNSName(id)
	proxy := filepath.Join(netDir, "proxy.marker")
	if err := os.WriteFile(proxy, []byte(string(id)+"\n"), 0o600); err != nil {
		return vmm.NetworkState{}, err
	}
	if err := os.WriteFile(filepath.Join(netDir, "tap"), []byte(tap+"\n"), 0o600); err != nil {
		return vmm.NetworkState{}, err
	}
	if err := os.WriteFile(filepath.Join(netDir, "netns"), []byte(ns+"\n"), 0o600); err != nil {
		return vmm.NetworkState{}, err
	}
	// Real `ip tuntap` / netns creation is deferred to host install scripts /
	// later networking package. Markers make orphan cleanup deterministic.
	return vmm.NetworkState{
		TapDevice:   tap,
		NetNS:       ns,
		ProxyMarker: proxy,
	}, nil
}

func defaultTeardownNetwork(id vmm.ID, net vmm.NetworkState) error {
	_ = id
	if net.ProxyMarker != "" {
		_ = os.Remove(net.ProxyMarker)
	}
	// Best-effort real device cleanup when running as root on Linux.
	if runtime.GOOS == "linux" {
		if net.TapDevice != "" {
			_ = exec.Command("ip", "link", "del", net.TapDevice).Run()
		}
		if net.NetNS != "" {
			_ = exec.Command("ip", "netns", "del", net.NetNS).Run()
		}
	}
	return nil
}

// Linux IFNAMSIZ is 16 (15 usable chars). VM ids are long — hash to a short name.
func tapName(id vmm.ID) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return fmt.Sprintf("tc%08x", h.Sum32()) // 10 chars
}

func netNSName(id vmm.ID) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return fmt.Sprintf("tn%08x", h.Sum32())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
