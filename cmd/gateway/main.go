package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gatewaylab/internal/gateway"
	"gatewaylab/web"
)

func run() error {
	addr := flag.String("addr", "127.0.0.1:8788", "HTTP listening address")
	data := flag.String("data", "data", "Local registry and settings directory")
	flag.Parse()
	base := "http://" + *addr
	g, err := gateway.New(*data, base)
	if err != nil {
		return fmt.Errorf("initialize gateway: %w", err)
	}
	defer func() {
		if err := g.Close(); err != nil {
			slog.Error("close gateway database", "error", err)
		}
	}()
	assets, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return fmt.Errorf("load frontend: %w", err)
	}
	server := &http.Server{Addr: *addr, Handler: g.Handler(assets), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
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
