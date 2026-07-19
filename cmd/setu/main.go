// Command setu is a lightweight, self-hosted home-automation bridge. A single
// static binary serves the embedded web UI, the JSON API, and a WebSocket for
// live updates — no reverse proxy, no separate web server, no supervisor.
//
// This file is the composition root: it loads config, wires dependencies,
// registers device types, and serves until interrupted.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"setu/internal/api"
	"setu/internal/config"
	"setu/internal/devices/example"
	"setu/internal/devices/samsung"
	"setu/internal/devices/wiz"
	"setu/internal/devices/wol"
	"setu/internal/events"
	"setu/internal/manager"
	"setu/internal/resolver"
	"setu/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if cfg.Auth.Token == "CHANGE_ME" {
		log.Warn("auth.token is still the default 'CHANGE_ME' — set a real token in your config")
	}

	// --- wire dependencies (composition root) ---
	bus := events.NewBus()
	res := resolver.NewARPResolver()

	// Register device types. Adding a brand is ONE line here.
	factory := config.NewFactory()
	example.Register(factory) // template / blueprint (no real protocol)
	wiz.Register(factory)     // Philips WiZ bulbs (UDP)
	samsung.Register(factory) // Samsung Tizen TVs (REST + WebSocket + WoL)
	wol.Register(factory)     // Wake-on-LAN targets (PC/NAS/router — magic packet)

	devices, err := factory.BuildAll(cfg.Devices, config.Deps{Resolver: res, Bus: bus})
	if err != nil {
		return err
	}
	log.Info("loaded devices", "count", len(devices))

	mgr := manager.New(bus, devices)
	defer mgr.Close()

	// --- lifecycle context (graceful shutdown on SIGINT/SIGTERM) ---
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// state poller
	poller := manager.NewPoller(mgr, bus, cfg.PollInterval.Duration(), log)
	go poller.Run(ctx)

	// --- HTTP server ---
	srv := api.New(api.Options{
		Manager: mgr,
		Poller:  poller,
		Bus:     bus,
		Token:   cfg.Auth.Token,
		Dist:    web.Dist(),
		Logger:  log,
	})
	httpServer := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ln, err := listen(cfg.Listen)
	if err != nil {
		return err
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("setu listening", "addr", cfg.Listen.String(), "tls", cfg.Listen.TLS.Enabled())
		if err := httpServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case err := <-serveErr:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// listen opens the configured listener: a Unix-domain socket when one is
// configured, otherwise TCP on the configured interface and port. When TLS is
// configured it wraps the listener so the server speaks HTTPS — stdlib only, no
// extra dependency and no behaviour change when TLS is unset.
func listen(cfg config.ListenConfig) (net.Listener, error) {
	ln, err := openListener(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.TLS.Enabled() {
		cert, err := tls.LoadX509KeyPair(cfg.TLS.Cert, cfg.TLS.Key)
		if err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("config: load TLS keypair: %w", err)
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	return ln, nil
}

// openListener opens the raw (plain) listener for the configured address.
func openListener(cfg config.ListenConfig) (net.Listener, error) {
	network, addr := cfg.Network()
	if network == "unix" {
		// Remove a stale socket file left by an unclean shutdown.
		if err := os.Remove(addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		ln, err := net.Listen("unix", addr)
		if err != nil {
			return nil, err
		}
		// Allow non-root clients (e.g. an SSH tunnel user) to connect.
		_ = os.Chmod(addr, 0o660)
		return ln, nil
	}
	return net.Listen(network, addr)
}
