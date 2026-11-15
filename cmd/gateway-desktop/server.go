package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/gateway"
)

type instanceMode string

const (
	modeOwned    instanceMode = "owned"
	modeAttached instanceMode = "attached"
)

// portConflictError indicates that the target listen address is already bound
// by a non-Gateway process (health check did not identify MCP Gateway).
type portConflictError struct {
	Addr string
	Err  error
}

func (e *portConflictError) Error() string {
	return fmt.Sprintf("address %s is already in use: %v", e.Addr, e.Err)
}

func (e *portConflictError) Unwrap() error { return e.Err }

type gatewayRuntime struct {
	Mode       instanceMode
	ListenAddr string
	UIURL      string
	MCPURL     string
	DataDir    string

	shutdownOnce sync.Once
	shutdownFn   func(context.Context) error
}

func (r *gatewayRuntime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	var err error
	r.shutdownOnce.Do(func() {
		if r.shutdownFn != nil {
			err = r.shutdownFn(ctx)
		}
	})
	return err
}

func validateListenAddr(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) && os.Getenv("GATEWAY_AUTH_FILE") == "" {
		return fmt.Errorf("non-loopback listener (%s) requires GATEWAY_AUTH_FILE", addr)
	}
	return nil
}

// connectHostPort normalizes a listen address into a dialable host:port for
// local health probing and WebView loading.
func connectHostPort(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port), nil
}

func loopbackHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
}

// probeGatewayHealth checks whether baseURL is a live MCP Gateway instance via
// GET /api/health (returning {"status":"ok","version":"..."}).
func probeGatewayHealth(ctx context.Context, baseURL, dataDir string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/health", nil)
	if err != nil {
		return false
	}
	resp, err := loopbackHTTPClient(1200 * time.Millisecond).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(strings.ToLower(ct), "application/json") {
		return false
	}
	var payload struct {
		Status       string `json:"status"`
		Version      string `json:"version"`
		Channel      string `json:"channel"`
		Application  string `json:"application"`
		InstanceID   string `json:"instance_id"`
		DataIdentity string `json:"data_identity"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return false
	}
	if payload.Status != "ok" || payload.Version != buildinfo.Current().Version || payload.Channel != buildinfo.Current().Channel || payload.Application != buildinfo.Current().Identifier || payload.InstanceID == "" || payload.DataIdentity != buildinfo.DataIdentity(dataDir) {
		return false
	}
	// Also confirm /api/auth/me exists (200 in local mode or 401 Bearer in shared mode)
	// so generic {"status":"ok","version":"..."} services are not misidentified.
	authReq, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/auth/me", nil)
	if err != nil {
		return false
	}
	authResp, err := loopbackHTTPClient(1200 * time.Millisecond).Do(authReq)
	if err != nil {
		return false
	}
	defer authResp.Body.Close()
	if authResp.StatusCode == http.StatusUnauthorized {
		return strings.Contains(authResp.Header.Get("WWW-Authenticate"), `realm="gateway"`)
	}
	if authResp.StatusCode != http.StatusOK {
		return false
	}
	var authPayload map[string]any
	if err := json.NewDecoder(authResp.Body).Decode(&authPayload); err != nil {
		return false
	}
	_, hasShared := authPayload["shared"]
	return hasShared
}

func waitGatewayReady(ctx context.Context, baseURL, dataDir string) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if probeGatewayHealth(ctx, baseURL, dataDir) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for gateway readiness at %s: %w", baseURL, ctx.Err())
		case <-ticker.C:
		}
	}
}

// startOrAttachGateway binds addr and starts an embedded Gateway instance. If
// addr is already occupied by a healthy MCP Gateway instance, it attaches to
// that instance instead of crashing; if occupied by another process, it returns
// a *portConflictError so the caller can prompt or fall back cleanly.
func startOrAttachGateway(ctx context.Context, addr, dataDir string, assets fs.FS) (*gatewayRuntime, *portConflictError, error) {
	if err := validateListenAddr(addr); err != nil {
		return nil, nil, err
	}
	ln, listenErr := net.Listen("tcp", addr)
	if listenErr != nil {
		dialAddr, err := connectHostPort(addr)
		if err != nil {
			return nil, nil, fmt.Errorf("listen %s: %w", addr, listenErr)
		}
		candidateUI := "http://" + dialAddr
		probeCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		if probeGatewayHealth(probeCtx, candidateUI, dataDir) {
			slog.Info("reusing existing mcp gateway instance", "url", candidateUI)
			return &gatewayRuntime{
				Mode:       modeAttached,
				ListenAddr: dialAddr,
				UIURL:      candidateUI,
				MCPURL:     candidateUI + "/mcp",
			}, nil, nil
		}
		return nil, &portConflictError{Addr: addr, Err: listenErr}, nil
	}

	actualAddr := ln.Addr().String()
	dialAddr, err := connectHostPort(actualAddr)
	if err != nil {
		_ = ln.Close()
		return nil, nil, err
	}
	uiURL := "http://" + dialAddr
	baseURL := uiURL
	if public := strings.TrimSpace(os.Getenv(buildinfo.EnvironmentKey("GATEWAY_PUBLIC_URL"))); public != "" {
		baseURL = strings.TrimRight(public, "/")
	}

	g, err := gateway.New(dataDir, baseURL)
	if err != nil {
		_ = ln.Close()
		return nil, nil, fmt.Errorf("initialize gateway: %w", err)
	}

	maintCtx, cancelMaint := context.WithCancel(ctx)
	g.StartMaintenance(maintCtx)

	server := &http.Server{
		Handler:           g.Handler(assets),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	serveDone := make(chan error, 1)
	go func() {
		err := server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("gateway http server stopped unexpectedly", "error", err)
			serveDone <- err
			return
		}
		serveDone <- nil
	}()

	shutdownFn := func(shutdownCtx context.Context) error {
		cancelMaint()
		var joined error
		if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			joined = errors.Join(joined, fmt.Errorf("shutdown http server: %w", err))
		}
		select {
		case <-serveDone:
		case <-shutdownCtx.Done():
		}
		if err := g.Close(); err != nil {
			joined = errors.Join(joined, fmt.Errorf("close gateway: %w", err))
		}
		return joined
	}

	readyCtx, cancelReady := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReady()
	if err := waitGatewayReady(readyCtx, uiURL, dataDir); err != nil {
		stopCtx, cancelStop := context.WithTimeout(context.Background(), 3*time.Second)
		_ = shutdownFn(stopCtx)
		cancelStop()
		return nil, nil, err
	}

	slog.Info("gateway desktop server ready", "url", uiURL, "mcp", baseURL+"/mcp", "data", dataDir)
	return &gatewayRuntime{
		Mode:       modeOwned,
		ListenAddr: dialAddr,
		UIURL:      uiURL,
		MCPURL:     baseURL + "/mcp",
		DataDir:    dataDir,
		shutdownFn: shutdownFn,
	}, nil, nil
}
