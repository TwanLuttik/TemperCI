// Command temperci-control is the TemperCI control plane.
//
// It receives GitHub App webhooks, mints JIT self-hosted runner configs,
// schedules jobs to capacity-aware host agents, and reconciles stuck assignments.
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
)

// version is set at link time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "/etc/temperci/control.toml", "path to control plane config (TOML)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "temperci-control is the TemperCI control plane (GitHub webhooks, JIT, agent assignment).\n\n")
		fmt.Fprintf(flag.CommandLine.Output(), "Flags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	log := logging.New()

	cfg, err := config.LoadControlFile(*configPath)
	if err != nil {
		log.Error("load config", "err", err, "path", *configPath)
		os.Exit(1)
	}

	keyPEM, err := os.ReadFile(cfg.GitHubAppPrivateKeyPath)
	if err != nil {
		log.Error("read github app private key", "err", err, "path", cfg.GitHubAppPrivateKeyPath)
		os.Exit(1)
	}

	ghClient, err := github.NewClient(github.Config{
		AppID:         cfg.GitHubAppID,
		PrivateKeyPEM: keyPEM,
	})
	if err != nil {
		log.Error("init github client", "err", err)
		os.Exit(1)
	}

	store := control.NewAssignmentStore()
	agents := control.NewAgentRegistry()
	handler := control.NewHandler(ghClient, store, control.HandlerConfig{
		Org:           cfg.GitHubOrg,
		LabelPrefix:   cfg.LabelPrefix,
		RunnerGroupID: cfg.RunnerGroupID,
		Logger:        log,
	})
	srv := control.NewServer(control.ServerConfig{
		Handler:       handler,
		Store:         store,
		Agents:        agents,
		WebhookSecret: cfg.GitHubWebhookSecret,
		AgentToken:    cfg.AgentToken,
		Logger:        log,
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

	// Stuck assignment + runner registration reconciliation.
	stuckAfter := time.Duration(cfg.AssignmentStuckSeconds) * time.Second
	if cfg.AssignmentStuckSeconds < 0 {
		stuckAfter = 0
	}
	staleMinted := time.Duration(cfg.StaleMintedSeconds) * time.Second
	if cfg.StaleMintedSeconds < 0 {
		staleMinted = 0
	}
	reconciler := &control.Reconciler{
		Store:            store,
		Delete:           ghClient,
		StuckAfter:       stuckAfter,
		StaleMintedAfter: staleMinted,
		Interval:         time.Duration(cfg.ReconcileIntervalSeconds) * time.Second,
		Log:              log,
	}
	go reconciler.Run(ctx)

	go func() {
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("temperci-control listening",
		"addr", cfg.ListenAddr,
		"org", cfg.GitHubOrg,
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
