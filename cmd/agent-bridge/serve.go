package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/login"
	"github.com/r3n3r0/agent-bridge/internal/media"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/server"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
	"github.com/r3n3r0/agent-bridge/internal/tools"
	"github.com/r3n3r0/agent-bridge/internal/turn"
	"github.com/r3n3r0/agent-bridge/internal/version"
)

func serve(args []string, stderr io.Writer) int {
	fl, err := parseFlags("serve", args, stderr, nil)
	if err != nil {
		return 2
	}
	cfg, err := config.Load(fl, os.Getenv)
	if err != nil {
		fmt.Fprintf(stderr, "configuration is not valid:\n%v\n", redact.String(err.Error()))
		return 1
	}
	redact.AddSecret(cfg.APIKey)
	for _, k := range config.CredentialEnv(cfg.Backend) {
		if strings.Contains(k, "KEY") || strings.Contains(k, "TOKEN") {
			redact.AddSecret(os.Getenv(k))
		}
	}
	log := newLogger(stderr, cfg.LogFormat, cfg.LogLevel)

	if err := cfg.ValidateAuth(os.Getenv); err != nil {
		log.Error("refusing to start", "error", err)
		return 1
	}
	if err := cfg.ValidateNetwork(); err != nil {
		log.Error("refusing to start", "error", err)
		return 1
	}
	backend, err := runner.NewBackend(cfg.Backend)
	if err != nil {
		log.Error("refusing to start", "error", err)
		return 1
	}
	log.Info("starting agent-bridge", "backend", backend.Name(), "auth_mode", string(cfg.AuthMode), "listen", cfg.Listen,
		"version", version.Version, "config", cfg.Path, "agents", len(cfg.Agents))
	if !config.IsLoopback(cfg.Listen) && cfg.AuthMode == config.AuthSubscription {
		log.Warn("remote access in subscription mode: this instance must serve only the account holder " +
			"(for example from their own laptop over a personal VPN), never other users")
	}
	for _, name := range cfg.AgentNames() {
		if a := cfg.Agents[name]; a.PermissionMode == config.PermissionBypass || a.Sandbox == config.CodexSandboxFull {
			log.Warn("agent runs without permission checks or sandbox: every tool call is executed as is", "agent", name)
		}
	}

	scratch, err := os.MkdirTemp("", "agent-bridge-")
	if err != nil {
		log.Error("cannot create temporary directory", "error", err)
		return 1
	}
	defer os.RemoveAll(scratch)
	cfg.SetScratchDir(scratch)

	storeOpts := session.Options{Policy: session.Policy{
		TTL:        time.Duration(cfg.Sessions.TTLHours) * time.Hour,
		MaxEntries: cfg.Sessions.MaxEntries,
	}}
	if cfg.Sessions.Store == config.SessionStoreFile {
		storeOpts.Path = cfg.Sessions.Path
	}
	store, err := session.NewStore(storeOpts)
	if err != nil {
		log.Error("cannot open session store", "error", err)
		return 1
	}
	var sink stats.UsageSink = stats.DiscardUsage{}
	if cfg.UsageLog != "" {
		if sink, err = stats.OpenJSONLUsageLog(cfg.UsageLog); err != nil {
			log.Error("cannot open usage log", "error", err)
			return 1
		}
	}
	defer sink.Close()

	var broker tools.Broker
	var helper []string
	if cfg.ClientTools.Enabled {
		exe, err := os.Executable()
		if err != nil {
			log.Error("cannot locate the bridge executable for client functions", "error", err)
			return 1
		}
		b, err := tools.NewHTTPBroker()
		if err != nil {
			log.Error("cannot start the client functions broker", "error", err)
			return 1
		}
		defer b.Close()
		broker, helper = b, []string{exe, "mcp-tools"}
	}

	var images media.Resolver
	if cfg.Images.Enabled {
		images = media.NewHTTPResolver(media.Options{
			MaxBytes:             cfg.Images.MaxBytes,
			FetchURLs:            cfg.Images.FetchURLs,
			AllowPrivateNetworks: cfg.Images.AllowPrivateNetworks,
			Timeout:              time.Duration(cfg.Images.FetchTimeoutSeconds) * time.Second,
		})
	}

	// Wiring: every component below is used through its interface.
	limiter := runner.NewFIFOLimiter(cfg.MaxConcurrent, cfg.MaxQueue, time.Duration(cfg.QueueTimeoutSeconds)*time.Second)
	collector := stats.NewCollector(string(cfg.AuthMode), limiter, sink)
	hardStop, hardCancel := context.WithCancel(context.Background())
	defer hardCancel()
	turns := turn.NewService(turn.Deps{
		Store:    store,
		Locker:   session.NewKeyLocker(),
		Executor: runner.CLIExecutor{},
		Limiter:  limiter,
		Recorder: collector,
		Broker:   broker,
		Images:   images,
		Logger:   log,
	}, turn.Settings{
		Backend:         backend,
		Binary:          cfg.Binary,
		AuthMode:        cfg.AuthMode,
		OnBusy:          cfg.Sessions.OnBusy,
		BaseEnv:         os.Environ(),
		HardStop:        hardStop,
		ToolHelper:      helper,
		ToolWaitTimeout: time.Duration(cfg.ClientTools.WaitTimeoutSeconds) * time.Second,
		ToolBatchWindow: time.Duration(cfg.ClientTools.BatchWindowMs) * time.Millisecond,
		MaxImages:       cfg.Images.MaxCount,
	})
	var logins login.Manager
	if cfg.Admin {
		logins = login.NewProcessManager(login.Options{CLIs: login.CLIs(cfg), BaseEnv: os.Environ(), Logger: log})
		log.Info("admin page enabled", "path", "/admin")
	}
	srv := server.New(server.Options{
		Config: cfg,
		Turns:  turns,
		Stats:  collector,
		Logger: log,
		Logins: logins,
	})
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Error("cannot listen", "error", err)
		return 1
	}
	log.Info("listening", "addr", ln.Addr().String())

	serveErr := make(chan error, 1)
	go func() { serveErr <- httpSrv.Serve(ln) }()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "error", err)
			return 1
		}
		return 0
	case <-sigCtx.Done():
	}

	grace := time.Duration(cfg.ShutdownGraceSeconds) * time.Second
	log.Info("shutting down, waiting for running turns", "grace", grace.String())
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		log.Warn("grace period expired, terminating running turns")
		hardCancel()
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		if err := httpSrv.Shutdown(ctx2); err != nil {
			_ = httpSrv.Close()
		}
	}
	log.Info("stopped")
	return 0
}
