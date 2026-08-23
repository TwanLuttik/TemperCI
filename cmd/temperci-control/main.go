// Command temperci-control is the TemperCI control plane.
//
// It receives GitHub App webhooks, mints JIT self-hosted runner configs,
// schedules jobs to capacity-aware host agents, reconciles stuck assignments,
// and serves the operator dashboard (setup wizard + fleet UI).
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/TwanLuttik/TemperCI/internal/config"
	"github.com/TwanLuttik/TemperCI/internal/control"
	"github.com/TwanLuttik/TemperCI/internal/github"
	"github.com/TwanLuttik/TemperCI/internal/logging"
	"github.com/TwanLuttik/TemperCI/internal/store"
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "/etc/temperci/control.toml", "path to control plane config (TOML)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "temperci-control is the TemperCI control plane (GitHub webhooks, JIT, agent assignment, dashboard).\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := logging.New()

	cfg, err := loadOrBootstrapConfig(*configPath)
	if err != nil {
		log.Error("load config", "err", err, "path", *configPath)
		os.Exit(1)
	}

	db, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Error("open sqlite", "err", err, "path", cfg.SQLitePath)
		os.Exit(1)
	}
	defer db.Close()

	// Prefer DB setup flag if config was written before setup_completed was known.
	if done, _ := db.SetupCompleted(); done {
		cfg.SetupCompleted = true
	}

	storeMem, err := control.NewAssignmentStoreWithPersister(control.NewStorePersister(db))
	if err != nil {
		log.Error("load persisted assignments", "err", err)
		os.Exit(1)
	}
	log.Info("loaded assignments", "count", storeMem.Len(), "pending", storeMem.PendingLen())
	agents := control.NewAgentRegistry()

	dash := &control.DashboardConfig{
		Config:     cfg,
		ConfigPath: *configPath,
		Store:      db,
		FleetReady: false,
		Version:    version,
		Updates:    control.NewUpdateChecker(control.UpdateCheckerConfig{Current: version}),
	}

	var handler *control.Handler
	var ghClient *github.Client
	if !cfg.NeedsSetup() {
		keyPEM, err := os.ReadFile(cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			log.Error("read github app private key", "err", err, "path", cfg.GitHubAppPrivateKeyPath)
			os.Exit(1)
		}
		ghClient, err = github.NewClient(github.Config{
			AppID:         cfg.GitHubAppID,
			PrivateKeyPEM: keyPEM,
		})
		if err != nil {
			log.Error("init github client", "err", err)
			os.Exit(1)
		}
		handler = control.NewHandler(ghClient, storeMem, control.HandlerConfig{
			Org:           cfg.GitHubOrg,
			LabelPrefix:   cfg.LabelPrefix,
			RunnerGroupID: cfg.RunnerGroupID,
			Logger:        log,
		})
		dash.FleetReady = true
	} else {
		log.Info("setup mode enabled — open dashboard to complete first-run wizard", "addr", cfg.ListenAddr)
	}

	hub := control.NewHub(log)
	dash.Hub = hub
	var jobLogs control.JobLogDownloader
	if ghClient != nil {
		jobLogs = ghClient
	}
	srv := control.NewServer(control.ServerConfig{
		Handler:       handler,
		Store:         storeMem,
		Agents:        agents,
		WebhookSecret: cfg.GitHubWebhookSecret,
		AgentToken:    cfg.AgentToken,
		Logger:        log,
		Dashboard:     dash,
		Hub:           hub,
		JobLogs:       jobLogs,
		RunnerDelete:  ghClient,
	})

	tlsFiles := control.TLSFiles{
		CertFile:     cfg.TLSCertFile,
		KeyFile:      cfg.TLSKeyFile,
		ClientCAFile: cfg.TLSClientCAFile,
	}
	tlsCfg, err := tlsFiles.ServerTLSConfig()
	if err != nil {
		log.Error("tls config", "err", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         tlsCfg,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Periodic snapshot for WebSocket dashboards (agent heartbeats also trigger publishes).
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				srv.PublishSnapshot()
			}
		}
	}()

	if handler != nil {
		stuckAfter := time.Duration(cfg.AssignmentStuckSeconds) * time.Second
		if cfg.AssignmentStuckSeconds < 0 {
			stuckAfter = 0
		}
		staleMinted := time.Duration(cfg.StaleMintedSeconds) * time.Second
		if cfg.StaleMintedSeconds < 0 {
			staleMinted = 0
		}
		var deleteClient *github.Client
		if keyPEM, err := os.ReadFile(cfg.GitHubAppPrivateKeyPath); err == nil {
			if gh, err := github.NewClient(github.Config{AppID: cfg.GitHubAppID, PrivateKeyPEM: keyPEM}); err == nil {
				deleteClient = gh
			}
		}
		reconciler := &control.Reconciler{
			Store:            storeMem,
			Delete:           deleteClient,
			StuckAfter:       stuckAfter,
			StaleMintedAfter: staleMinted,
			Interval:         time.Duration(cfg.ReconcileIntervalSeconds) * time.Second,
			Log:              log,
		}
		go reconciler.Run(ctx)
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("temperci-control listening",
		"addr", cfg.ListenAddr,
		"org", cfg.GitHubOrg,
		"setup", cfg.NeedsSetup(),
		"auth_mode", cfg.AuthMode,
		"tls", tlsFiles.Enabled(),
		"mtls", cfg.TLSClientCAFile != "",
		"version", version,
	)
	var serveErr error
	if tlsFiles.Enabled() {
		serveErr = httpServer.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	} else {
		serveErr = httpServer.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		log.Error("http server failed", "err", serveErr)
		os.Exit(1)
	}
}

// loadOrBootstrapConfig loads TOML, or creates a minimal setup-mode config when the file is missing.
func loadOrBootstrapConfig(path string) (*config.ControlConfig, error) {
	cfg, err := config.LoadControlFile(path)
	if err == nil {
		return cfg, nil
	}
	if !os.IsNotExist(err) {
		// LoadControlFile wraps errors — try reading raw.
		if _, statErr := os.Stat(path); statErr == nil {
			return nil, err
		}
	}
	// Missing file: bootstrap setup mode defaults (operator can use wizard).
	cfg = &config.ControlConfig{
		ListenAddr:              "0.0.0.0:8080",
		GitHubAppPrivateKeyPath: "/etc/temperci/github-app.pem",
		AuthMode:                "open",
		SetupCompleted:          false,
		DataDir:                 "/var/lib/temperci",
		SQLitePath:              "/var/lib/temperci/control.db",
		HostctlPath:             "/usr/local/bin/temperci-hostctl",
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}
