package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecisionsErrorsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 401, map[string]string{"error": "secret-test-key"})
	}))
	defer server.Close()
	s := Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}
	instructions := []string{"question"}
	_, err := AskDecisions(context.Background(), s, "secret-test-key", "input", instructions)
	if err == nil || strings.Contains(err.Error(), "secret-test-key") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error did not redact key: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AskDecisions(ctx, s, "", "input", instructions); err == nil {
		t.Fatal("cancellation ignored")
	}
	s.BaseURL += "/chat/completions"
	if _, err := AskDecisions(context.Background(), s, "", "input", instructions); err == nil {
		t.Fatal("legacy endpoint accepted")
	}
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func TestHTMLResponseReportsClearEndpointHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>SPA</body></html>"))
	}))
	defer server.Close()

	_, err := AskDecisions(context.Background(), Settings{BaseURL: server.URL + "/systemone?token=secret", Model: "jev", MaxTools: 4, TimeoutSeconds: 5}, "", "ping", []string{"check"})
	var modelErr *Error
	if !errors.As(err, &modelErr) || modelErr.Code != CodeBadResponse {
		t.Fatalf("expected CodeBadResponse, got %v", err)
	}
	if !strings.Contains(err.Error(), "HTML") || !strings.Contains(err.Error(), "/v1") || !strings.Contains(err.Error(), server.URL+"/systemone") {
		t.Fatalf("expected HTML/BaseURL hint, got: %v", err)
	}
	if strings.Contains(err.Error(), "token=secret") {
		t.Fatalf("query string leaked into error message: %v", err)
	}
}
