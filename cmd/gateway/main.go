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
	g, err := api.Open(*data, base)
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
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			slog.Error("shutdown gateway", "error", err)
		}
	}()
	slog.Info("gateway lab ready", "url", base, "mcp", base+"/mcp", "data", *data)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve gateway: %w", err)
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}
