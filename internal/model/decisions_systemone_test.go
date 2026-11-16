package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSystemOneRejectsChatEndpointsAndInvalidAnswers(t *testing.T) {
	for _, suffix := range []string{"/v1/chat/completions", "/v1/responses"} {
		_, err := AskDecisions(context.Background(), Settings{BaseURL: "https://example.com" + suffix, Model: "jev", MaxTools: 1, TimeoutSeconds: 5}, "", "input", []string{"question"})
		if err == nil || !strings.Contains(err.Error(), "/systemone") {
			t.Fatalf("expected /systemone error for %s, got %v", suffix, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"route_status":{"type":"choice","choice":"selected"},"tool_0":{"type":"predicate","noul":0.9}}}`))
	}))
	defer server.Close()
	if _, err := AskDecisions(context.Background(), Settings{BaseURL: server.URL + "/v1/systemone", Model: "jev", MaxTools: 1, TimeoutSeconds: 5}, "", "input", []string{"question"}); err == nil {
		t.Fatal("invalid noul type accepted")
	}
}
