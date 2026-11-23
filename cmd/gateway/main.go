package main

import (
	"context"
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

	"gatewaylab/internal/api"
	"gatewaylab/internal/buildinfo"
)

func run() error {
	defaultData := "data/" + buildinfo.Current().Channel
	if envData := os.Getenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR")); envData != "" {
		defaultData = envData
	} else if envData := os.Getenv("GATEWAY_DATA_DIR"); envData != "" {
		defaultData = envData
	}
	addr := flag.String("addr", buildinfo.DefaultAddr(), "HTTP listening address")
	data := flag.String("data", defaultData, "Local registry and settings directory")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) && os.Getenv("GATEWAY_AUTH_FILE") == "" {
		return fmt.Errorf("non-loopback listener requires GATEWAY_AUTH_FILE")
	}
	base := "http://" + *addr
	if public := os.Getenv(buildinfo.EnvironmentKey("GATEWAY_PUBLIC_URL")); public != "" {
		base = public
	}
	g, err := api.Open(*data, base, api.Options{LocalAdminToken: os.Getenv("GATEWAY_ADMIN_TOKEN")})
	if err != nil {
		return fmt.Errorf("initialize gateway: %w", err)
	}
	defer func() {
		if err := g.Close(); err != nil {
			slog.Error("close gateway database", "error", err)
		}
	}()
	server := &http.Server{Addr: *addr, Handler: g.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	g.StartMaintenance(ctx)
	slog.Info("gateway lab ready", "url", base, "mcp", base+"/mcp", "data", *data)
	return serveAndShutdown(ctx, server, server.ListenAndServe, 5*time.Second)
}

func serveAndShutdown(ctx context.Context, server *http.Server, serve func() error, timeout time.Duration) error {
	serveDone := make(chan error, 1)
	go func() {
		err := serve()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveDone <- fmt.Errorf("serve gateway: %w", err)
			return
		}
		serveDone <- nil
	}()
	select {
	case err := <-serveDone:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("shutdown gateway", "error", err)
		}
		select {
		case err := <-serveDone:
			return err
		case <-shutdown.Done():
			return nil
		}
	}
}

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
