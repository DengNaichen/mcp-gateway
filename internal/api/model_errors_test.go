package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/gateway"
	"gatewaylab/internal/model"
)

// useRateLimitedModel points the gateway at a provider that always answers 429
// with a delay longer than the gateway's retry cap, so the gateway must give up
// at once and pass the delay on instead of waiting.
func useRateLimitedModel(t *testing.T, g *gateway.Gateway) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		respond(w, 429, map[string]any{"error": map[string]any{
			"code": 429, "status": "RESOURCE_EXHAUSTED", "message": "Rate limit exceeded for key live-model-key. Please retry in 119.2s.",
			"details": []map[string]any{{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "119.2s"}},
		}})
	}))
	t.Cleanup(server.Close)
	settings, _ := g.Model().Config()
	settings.BaseURL, settings.Model = server.URL, "fixture"
	if err := g.SaveSettings(settings, "live-model-key", false); err != nil {
		t.Fatal(err)
	}
	return &calls
}

func TestUpstreamRateLimitSurfacesAs429(t *testing.T) {
	g, _ := testGateway(t)
	registerMultiServiceUpstream(t, g)
	calls := useRateLimitedModel(t, g)
	host := httptest.NewServer(Handler(g))
	defer host.Close()

	body, _ := json.Marshal(map[string]any{"intent": "list github issues"})
	started := time.Now()
	resp, err := http.Post(host.URL+"/api/discover", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "120" || payload["code"] != model.CodeRateLimited || payload["retry_after_seconds"] != float64(120) {
		t.Fatalf("got %d Retry-After=%q %v", resp.StatusCode, resp.Header.Get("Retry-After"), payload)
	}
	if strings.Contains(fmt.Sprint(payload), "live-model-key") {
		t.Fatalf("HTTP error leaked the model key: %v", payload)
	}
	if calls.Load() != 1 || time.Since(started) > 5*time.Second {
		t.Fatalf("gateway waited out a delay beyond its cap: %d calls in %s", calls.Load(), time.Since(started))
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "rate-limit-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(session)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "discover_tools", Arguments: map[string]any{"intent": "list github issues"}})
	if err != nil {
		t.Fatalf("tool failure must be a tool result, not a protocol error: %v", err)
	}
	structured, _ := result.StructuredContent.(map[string]any)
	if !result.IsError || structured["code"] != model.CodeRateLimited || structured["retry_after_seconds"] != float64(120) || structured["status"] != float64(429) {
		t.Fatalf("MCP result lacks structured rate-limit error: %+v", result)
	}
	if text := result.Content[0].(*mcp.TextContent).Text; strings.Contains(text, "live-model-key") || !strings.Contains(text, "[redacted]") {
		t.Fatalf("MCP error text not redacted: %s", text)
	}
}

func TestModelErrorStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		err        error
		status     int
		code       string
		retryAfter string
	}{
		{&model.Error{Kind: model.ErrRateLimited, Code: model.CodeRateLimited, Message: "限速", RetryAfter: 10500 * time.Millisecond}, 429, model.CodeRateLimited, "11"},
		{&model.Error{Kind: model.ErrRateLimited, Code: model.CodeQuotaExhausted, Message: "配额用尽"}, 429, model.CodeQuotaExhausted, ""},
		{&model.Error{Kind: model.ErrTimeout, Code: model.CodeTimeout, Message: "超时"}, 504, model.CodeTimeout, ""},
		{&model.Error{Kind: model.ErrUpstream, Code: model.CodeAuthFailed, Message: "key 无效"}, 502, model.CodeAuthFailed, ""},
		{&model.Error{Kind: model.ErrUpstream, Code: model.CodeUnavailable, Message: "上游故障"}, 502, model.CodeUnavailable, ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			w := httptest.NewRecorder()
			fail(w, tc.err)
			var payload map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.status || payload["code"] != tc.code || w.Header().Get("Retry-After") != tc.retryAfter {
				t.Fatalf("got %d %v Retry-After=%q", w.Code, payload, w.Header().Get("Retry-After"))
			}
		})
	}
}
