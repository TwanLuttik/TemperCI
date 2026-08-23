// Command temperci-agent is the TemperCI host agent.
//
// It maintains a warm microVM pool, polls the control plane for job assignments,
// injects JIT + starts the runner, and tears down guests plus host scratch after every job.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/agent"
	"github.com/TwanLuttik/TemperCI/internal/cleanup"
	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/ghacache"
	"github.com/TwanLuttik/TemperCI/internal/logging"
	"github.com/TwanLuttik/TemperCI/internal/ocicache"
	"github.com/TwanLuttik/TemperCI/internal/vmm"
	"github.com/TwanLuttik/TemperCI/internal/vmm/fake"
	"github.com/TwanLuttik/TemperCI/internal/vmm/firecracker"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "/etc/temperci/agent.toml", "path to agent config (TOML)")
	// Optional demo flags for local testing without control plane.
	demoBind := flag.Bool("demo-bind", false, "after pool is warm, bind a fake job then finish it (local smoke; skips control poll)")
	demoJobID := flag.String("demo-job-id", "demo-job", "job id used with -demo-bind")
	noWorker := flag.Bool("no-worker", false, "do not poll control plane for jobs (pool only)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "temperci-agent is the TemperCI host agent (warm pool, claim jobs, bind, teardown).\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := logging.New()

	cfg, err := config.LoadAgentFile(*configPath)
	if err != nil {
		log.Error("load config", "err", err, "path", *configPath)
		os.Exit(1)
	}

	layout := vmm.NewLayout(cfg.DataDir)
	if err := cleanup.EnsureLayout(layout); err != nil {
		log.Error("ensure layout", "err", err)
		os.Exit(1)
	}

	mgr, err := newVMM(cfg, layout, cfg.CacheListenAddr)
	if err != nil {
		log.Error("init vmm", "err", err, "backend", cfg.VMMBackend)
		os.Exit(1)
	}

	runner := newRunner(cfg, layout, log)

	cleaner := &cleanup.Cleaner{VMM: mgr, Layout: layout, Log: log}
	poolCfg := agent.PoolConfigFromAgent(cfg)
	var readyCheck func(id vmm.ID) bool
	if cfg.VMMBackend == "firecracker" {
		var injectMu sync.Mutex
		lastInject := map[vmm.ID]time.Time{}
		readyCheck = func(id vmm.ID) bool {
			p := filepath.Join(layout.GuestDir(id), "agent.ready")
			if b, err := os.ReadFile(p); err == nil && len(b) > 0 {
				return true
			}
			injectMu.Lock()
			if time.Since(lastInject[id]) < 500*time.Millisecond {
				injectMu.Unlock()
				return false
			}
			lastInject[id] = time.Now()
			injectMu.Unlock()
			// Fallback if PVE/firewall drops the UDP mailbox: copy from inject.
			b, err := firecracker.ReadInjectFile(layout, id, "agent.ready")
			if err != nil || len(b) == 0 {
				return false
			}
			_ = os.MkdirAll(layout.GuestDir(id), 0o700)
			_ = os.WriteFile(p, b, 0o600)
			return true
		}
	}
	mailbox := &agent.MailboxHub{Layout: layout}
	pool, err := agent.NewPool(poolCfg, agent.PoolDeps{
		VMM:        mgr,
		Cleaner:    cleaner,
		Runner:     runner,
		Log:        log,
		Inventory:  agent.ProcInventory{DataDir: cfg.DataDir},
		ReadyCheck: readyCheck,
		Mailbox:    mailbox,
	})
	if err != nil {
		log.Error("init pool", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := pool.Start(ctx); err != nil {
		log.Error("pool start", "err", err)
		os.Exit(1)
	}
	log.Info("temperci-agent started",
		"version", version,
		"backend", cfg.VMMBackend,
		"data_dir", cfg.DataDir,
		"min_ready", cfg.MinReady,
		"max_ready", pool.EffectiveMaxReady(),
		"configured_max_ready", pool.ConfiguredMaxReady(),
		"clamp_reason", pool.ClampReason(),
		"agent_id", cfg.AgentID,
		"control_url", cfg.ControlURL,
		"image_path", cfg.ImagePath,
		"job_deadline_seconds", cfg.JobDeadlineSeconds,
	)

	// Optional local metrics + admin (drain/reload). Prefer 127.0.0.1.
	if cfg.MetricsListenAddr != "" {
		local := agent.NewLocalServer(pool, cfg.AgentID, cfg.AgentToken, log)
		httpServer := &http.Server{
			Addr:              cfg.MetricsListenAddr,
			Handler:           local.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Info("agent metrics listening", "addr", cfg.MetricsListenAddr)
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("agent metrics server failed", "err", err)
			}
		}()
		go func() {
			<-ctx.Done()
			shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			_ = httpServer.Shutdown(shutdownCtx)
		}()
	}

	if *demoBind {
		go runDemoBind(ctx, pool, log, *demoJobID)
	}

	var workerDone chan struct{}
	if !*noWorker && !*demoBind {
		httpClient, err := agent.NewHTTPClientTLS(agent.ClientTLSConfig{
			CAFile:             cfg.TLSCAFile,
			CertFile:           cfg.TLSCertFile,
			KeyFile:            cfg.TLSKeyFile,
			InsecureSkipVerify: cfg.TLSInsecureSkipVerify,
		})
		if err != nil {
			log.Error("tls client", "err", err)
			os.Exit(1)
		}
		// Plain HTTP still works; TLS config only applies to https://.
		if cfg.TLSCAFile == "" && cfg.TLSCertFile == "" && !cfg.TLSInsecureSkipVerify {
			httpClient = nil // default
		}
		client := agent.NewControlClient(cfg.ControlURL, cfg.AgentID, cfg.AgentToken, httpClient)
		waitReal := cfg.VMMBackend == "firecracker" && cfg.JobSimulateSeconds == 0
		deadline := time.Duration(cfg.JobDeadlineSeconds) * time.Second
		if waitReal && deadline <= 0 {
			deadline = 6 * time.Hour
		}
		log.Info("worker config",
			"wait_real_runner", waitReal,
			"job_simulate_seconds", cfg.JobSimulateSeconds,
			"job_deadline", deadline.String(),
			"vmm_backend", cfg.VMMBackend,
		)
		var cacheGW *ghacache.Gateway
		var ociGW *ocicache.Gateway
		if cfg.CacheListenAddr != "" {
			st, err := ghacache.Open(layout.CacheDir(), cfg.CacheMaxBytes)
			if err != nil {
				log.Error("open cache store", "err", err)
				os.Exit(1)
			}
			cacheGW = ghacache.NewGateway(st)
			ociStore, err := ocicache.Open(layout.OCICacheDir(), cfg.OCICacheMaxBytes)
			if err != nil {
				log.Error("open oci cache store", "err", err)
				os.Exit(1)
			}
			ociGW = ocicache.NewGateway(ociStore)
			ca, err := ghacache.LoadOrCreateCA(filepath.Join(layout.CacheDir(), "ca"))
			if err != nil {
				log.Error("cache CA", "err", err)
				os.Exit(1)
			}
			ix := &ghacache.Intercept{
				Handler:  ocicache.Mux(ociGW.Handler(), cacheGW.Handler()),
				CA:       ca,
				Classify: ocicache.ShouldTerminate,
			}
			go func() {
				log.Info("actions+oci cache gateway listening",
					"addr", cfg.CacheListenAddr,
					"dir", layout.CacheDir(),
					"oci_dir", layout.OCICacheDir(),
					"mode", "sni-intercept",
				)
				if err := ix.ListenAndServe(cfg.CacheListenAddr); err != nil && err != http.ErrServerClosed {
					log.Error("cache gateway failed", "err", err)
				}
			}()
		}
		worker := &agent.Worker{
			Client:         client,
			Pool:           pool,
			Log:            log,
			PollInterval:   time.Duration(cfg.PollIntervalSeconds) * time.Second,
			JobSimulate:    time.Duration(cfg.JobSimulateSeconds) * time.Second,
			JobDeadline:    deadline,
			Capacity:       pool.EffectiveMaxReady(),
			WaitRealRunner: waitReal,
			Cache:          cacheGW,
			OCI:            ociGW,
		}
		workerDone = make(chan struct{})
		go func() {
			defer close(workerDone)
			if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
				log.Error("worker stopped", "err", err)
				cancel()
			}
		}()
	}

	// Periodic capacity logs (structured ops).
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m := pool.Metrics()
				log.Info("pool status",
					"warm", m.Counts.Warm,
					"busy", m.Counts.Busy,
					"pool_boot", m.Counts.PoolBoot,
					"destroying", m.Counts.Destroying,
					"warm_binds", m.WarmBinds,
					"cold_starts", m.ColdStarts,
					"destroys_ok", m.DestroysOK,
					"destroy_fail", m.DestroyFail,
					"recycles", m.Recycles,
					"orphans", m.Orphans,
				)
			}
		}
	}()

	<-ctx.Done()
	log.Info("shutting down; draining in-flight jobs before destroying VMs")
	drainFor := time.Duration(cfg.JobDeadlineSeconds) * time.Second
	if drainFor <= 0 {
		drainFor = 6 * time.Hour
	}
	if workerDone != nil {
		select {
		case <-workerDone:
			log.Info("in-flight jobs drained")
		case <-time.After(drainFor):
			log.Error("worker drain timed out; leftover busy VMs will be destroyed", "timeout", drainFor.String())
		}
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()
	if err := pool.Shutdown(shutdownCtx); err != nil {
		log.Error("pool shutdown", "err", err)
		os.Exit(1)
	}
}

func newVMM(cfg *config.AgentConfig, layout vmm.Layout, cacheListen string) (vmm.Manager, error) {
	switch cfg.VMMBackend {
	case "fake":
		return fake.New(layout)
	case "firecracker":
		return firecracker.New(firecracker.Config{
			Layout:          layout,
			CacheListenAddr: cacheListen,
		})
	default:
		return nil, fmt.Errorf("unknown vmm_backend %q", cfg.VMMBackend)
	}
}

func newRunner(cfg *config.AgentConfig, layout vmm.Layout, log *slog.Logger) agent.RunnerStarter {
	fileGuest := &agent.FileGuestExec{Layout: layout}
	var guest agent.GuestExec = fileGuest
	if cfg.VMMBackend == "firecracker" {
		guest = &agent.FirecrackerGuestExec{Inner: fileGuest, Layout: layout}
	}
	r := &agent.InjectRunner{
		Guest: guest,
		Log:   log,
	}
	if pem, err := os.ReadFile(filepath.Join(layout.CacheDir(), "ca", "ca.crt")); err == nil {
		r.CacheCAPEM = pem
	}
	return r
}

func runDemoBind(ctx context.Context, pool *agent.Pool, log interface {
	Info(string, ...any)
	Error(string, ...any)
}, jobID string) {
	// Wait until at least one warm VM exists.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if pool.Counts().Warm > 0 {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	res, err := pool.Bind(ctx, agent.JobPayload{
		JobID:     jobID,
		JITConfig: "demo-jit-not-for-production",
	})
	if err != nil {
		log.Error("demo bind failed", "err", err)
		return
	}
	log.Info("demo bind ok", "vm_id", string(res.VMID), "warm_bind", res.WarmStart, "job_id", res.JobID)
	// Simulate short job.
	select {
	case <-ctx.Done():
		return
	case <-time.After(500 * time.Millisecond):
	}
	if err := pool.JobFinished(ctx, res.VMID, "demo_complete"); err != nil {
		log.Error("demo job finished failed", "err", err)
		return
	}
	m := pool.Metrics()
	log.Info("demo complete",
		"warm", m.Counts.Warm,
		"warm_binds", m.WarmBinds,
		"cold_starts", m.ColdStarts,
	)
}
