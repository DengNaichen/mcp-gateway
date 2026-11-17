package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

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
