package main

import (
	"cmp"
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

const adminTokenEnv = "GATEWAY_ADMIN_TOKEN"

func openGateway(dataDir, baseURL string) (*api.Instance, error) {
	channelKey := buildinfo.EnvironmentKey(adminTokenEnv)
	adminTokens := map[string]string{
		adminTokenEnv: os.Getenv(adminTokenEnv),
		channelKey:    os.Getenv(channelKey),
	}
	for key := range adminTokens {
		if err := os.Unsetenv(key); err != nil {
			return nil, fmt.Errorf("clear %s from process environment: %w", key, err)
		}
	}
	return api.Open(dataDir, baseURL, api.Options{
		LocalAdminToken: cmp.Or(adminTokens[channelKey], adminTokens[adminTokenEnv]),
		// Shared access files may reference either consumed value via token_env.
		LookupAccessToken: func(key string) string {
			if token, consumed := adminTokens[key]; consumed {
				return token
			}
			return os.Getenv(key)
		},
	})
}

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
	g, err := openGateway(*data, base)
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
