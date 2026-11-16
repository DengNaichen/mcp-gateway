package model

import (
	"context"
	"encoding/json"
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
