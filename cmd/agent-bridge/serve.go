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
	"syscall"
	"time"

	"github.com/r3n3r0/agent-bridge/internal/config"
	"github.com/r3n3r0/agent-bridge/internal/redact"
	"github.com/r3n3r0/agent-bridge/internal/runner"
	"github.com/r3n3r0/agent-bridge/internal/server"
	"github.com/r3n3r0/agent-bridge/internal/session"
	"github.com/r3n3r0/agent-bridge/internal/stats"
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
	log := newLogger(stderr, cfg.LogFormat, cfg.LogLevel)

	if err := cfg.ValidateAuth(os.Getenv); err != nil {
		log.Error("refusing to start", "error", err)
		return 1
	}
	if err := cfg.ValidateNetwork(); err != nil {
		log.Error("refusing to start", "error", err)
		return 1
	}
	log.Info("starting agent-bridge", "auth_mode", string(cfg.AuthMode), "listen", cfg.Listen,
		"version", version.Version, "config", cfg.Path, "agents", len(cfg.Agents))
	if !config.IsLoopback(cfg.Listen) && cfg.AuthMode == config.AuthSubscription {
		log.Warn("remote access in subscription mode: this instance must serve only the account holder " +
			"(for example from their own laptop over a personal VPN), never other users")
	}
	for _, name := range cfg.AgentNames() {
		if cfg.Agents[name].PermissionMode == config.PermissionBypass {
			log.Warn("agent runs with bypassPermissions: every tool call is executed without confirmation", "agent", name)
		}
	}

	scratch, err := os.MkdirTemp("", "agent-bridge-")
	if err != nil {
		log.Error("cannot create temporary directory", "error", err)
		return 1
	}
	defer os.RemoveAll(scratch)
	cfg.SetScratchDir(scratch)

	regOpts := session.Options{
		TTL:        time.Duration(cfg.Sessions.TTLHours) * time.Hour,
		MaxEntries: cfg.Sessions.MaxEntries,
	}
	if cfg.Sessions.Store == config.SessionStoreFile {
		regOpts.Path = cfg.Sessions.Path
	}
	reg, err := session.New(regOpts)
	if err != nil {
		log.Error("cannot open session registry", "error", err)
		return 1
	}
	limiter := runner.NewLimiter(cfg.MaxConcurrent, cfg.MaxQueue, time.Duration(cfg.QueueTimeoutSeconds)*time.Second)
	st, err := stats.New(string(cfg.AuthMode), limiter, cfg.UsageLog)
	if err != nil {
		log.Error("cannot open usage log", "error", err)
		return 1
	}
	defer st.Close()

	hardStop, hardCancel := context.WithCancel(context.Background())
	defer hardCancel()
	srv := server.New(server.Options{
		Config:   cfg,
		Registry: reg,
		Limiter:  limiter,
		Stats:    st,
		Logger:   log,
		BaseEnv:  os.Environ(),
		HardStop: hardStop,
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
