package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
)

func TestOpenGatewayConsumesAdminToken(t *testing.T) {
	const genericToken = "generic-fixture-admin-token"
	const channelToken = "channel-fixture-admin-token"
	channelKey := buildinfo.EnvironmentKey(adminTokenEnv)
	for _, tc := range []struct {
		name, preferred string
		shared          bool
	}{
		{name: "channel precedence", preferred: channelToken},
		{name: "generic fallback"},
		{name: "shared policy retains token_env", preferred: channelToken, shared: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if channelKey == adminTokenEnv && tc.preferred != "" && !tc.shared {
				t.Skip("the stable channel uses the generic key")
			}
			t.Setenv("GATEWAY_AUTH_FILE", "")
			t.Setenv("GATEWAY_CREDENTIAL_STORE", "memory")
			t.Setenv("GATEWAY_EXECUTION_MODE", "")
			t.Setenv(adminTokenEnv, genericToken)
			if channelKey != adminTokenEnv {
				t.Setenv(channelKey, tc.preferred)
			}
			wantToken := genericToken
			if tc.shared {
				authFile := filepath.Join(t.TempDir(), "access.json")
				const config = `{"principals":[{"id":"owner","groups":["developer"],"servers":["*"],"admin":true,"token_env":"GATEWAY_ADMIN_TOKEN"}]}`
				if err := os.WriteFile(authFile, []byte(config), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("GATEWAY_AUTH_FILE", authFile)
			} else if tc.preferred != "" {
				wantToken = tc.preferred
			}
			instance, err := openGateway(t.TempDir(), "http://localhost")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = instance.Close() })
			for _, key := range []string{adminTokenEnv, channelKey} {
				if _, ok := os.LookupEnv(key); ok {
					t.Errorf("%s remains in the inherited environment", key)
				}
			}
			req := httptest.NewRequest("GET", "http://localhost/api/auth/me", nil)
			req.Header.Set("Authorization", "Bearer "+wantToken)
			out := httptest.NewRecorder()
			instance.Handler().ServeHTTP(out, req)
			var who apitypes.AuthMe
			if err := json.Unmarshal(out.Body.Bytes(), &who); err != nil || out.Code != http.StatusOK || !who.Authenticated || who.Shared != tc.shared {
				t.Fatalf("selected admin token did not authenticate: %d %s", out.Code, out.Body)
			}
		})
	}
}

func TestServeAndShutdownWaitsForInFlightRequests(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	started := make(chan struct{})
	release := make(chan struct{})
	var handlerDone atomic.Bool

	srv := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			handlerDone.Store(true)
			w.WriteHeader(http.StatusOK)
		}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdownErr := make(chan error, 1)
	go func() {
		shutdownErr <- serveAndShutdown(ctx, srv, func() error { return srv.Serve(ln) }, 2*time.Second)
	}()

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			_ = resp.Body.Close()
		}
		reqDone <- err
	}()

	<-started
	cancel()

	// Shutdown must not return while the in-flight request is still blocked.
	select {
	case err := <-shutdownErr:
		t.Fatalf("serveAndShutdown returned before in-flight request completed: err=%v handlerDone=%v", err, handlerDone.Load())
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-shutdownErr; err != nil {
		t.Fatalf("serveAndShutdown returned error: %v", err)
	}
	if !handlerDone.Load() {
		t.Fatal("expected in-flight handler to finish before serveAndShutdown returned")
	}
	if err := <-reqDone; err != nil {
		t.Fatalf("http client error: %v", err)
	}
}
