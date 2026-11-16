package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGeminiDecisionsErrorRedactsKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 400, map[string]any{"error": map[string]string{"code": "safety", "message": "secret-test-key"}})
	}))
	defer server.Close()
	_, err := AskDecisions(context.Background(), Settings{BaseURL: server.URL, Model: "gemini-decision-1", MaxTools: 1, TimeoutSeconds: 5}, "secret-test-key", "input", []string{"question"})
	if err == nil || strings.Contains(err.Error(), "secret-test-key") || !strings.Contains(err.Error(), "400") {
		t.Fatalf("error did not redact key: %v", err)
	}
}
