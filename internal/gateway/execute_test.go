package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/registry"
)

func TestInvokeRedactsActiveOAuthTokens(t *testing.T) {
	const accessToken = "fixture-active-access-token"
	const refreshToken = "fixture-active-refresh-token"
	const serviceCredential = "fixture-configured-service-credential"
	const sensitiveField = "fixture-sensitive-argument"
	t.Setenv("GATEWAY_AUTH_FILE", "")
	schema := map[string]any{"type": "object"}
	ms := mcp.NewServer(&mcp.Implementation{Name: "oauth-echo", Version: "1"}, nil)
	ms.AddTool(&mcp.Tool{Name: "echo", InputSchema: schema}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if strings.Contains(string(req.Params.Arguments), sensitiveField) {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: string(req.Params.Arguments)}},
			}, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "echoed " + accessToken + " and " + refreshToken}},
		}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+accessToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(host.Close)

	g, _, err := New(t.TempDir(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	server := registry.Server{ID: "oauth-echo", Name: "OAuth echo", Kind: "mcp", Transport: "http", Endpoint: host.URL, OAuth: true, Enabled: true, Headers: map[string]string{"X-Private": "prefix-${AUDIT_PRIVATE}"}}
	if err := g.saveServerCredentials(server, map[string]string{"AUDIT_PRIVATE": serviceCredential}); err != nil {
		t.Fatal(err)
	}
	tool := registry.Tool{ID: "oauth-echo.echo", ServerID: server.ID, Name: "echo", Description: "Echo", Actors: []string{"developer"}, InputSchema: schema, Enabled: true}
	if err := g.registry.SaveTool(tool); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("oauth/%s/%x", server.ID, sha256.Sum256([]byte(server.Endpoint)))
	session := map[string]any{
		"config": oauth2.Config{ClientID: "fixture", Endpoint: oauth2.Endpoint{TokenURL: host.URL + "/token"}},
		"token":  oauth2.Token{AccessToken: accessToken, RefreshToken: refreshToken, TokenType: "Bearer"},
	}
	if err := g.credentials.Update(map[string]string{key: string(mustJSON(t, session))}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := g.Invoke(t.Context(), InvokeRequest{ToolID: tool.ID, Fields: []string{"text", accessToken, refreshToken, serviceCredential}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustJSON(t, result.Data)); got != `{"text":"echoed [redacted] and [redacted]"}` {
		t.Fatalf("OAuth echo did not redact both active tokens: %s", got)
	}
	if _, err := g.Invoke(t.Context(), InvokeRequest{
		ToolID:    tool.ID,
		Arguments: map[string]any{"credentials": []any{map[string]any{"password": sensitiveField}}},
	}); err == nil || !strings.Contains(err.Error(), sensitiveField) {
		t.Fatal("upstream fixture must echo the argument in an error")
	}
	// Exercising the same production recorder with typed detail verifies that
	// JSON fields are scrubbed even when nested inside structs and slices.
	g.activity.Record("test", "developer", "error", "nested", time.Now(), struct {
		Args []map[string]string `json:"args"`
	}{Args: []map[string]string{{"Access-Token": sensitiveField, "nested": accessToken + " " + refreshToken + " " + serviceCredential}}})
	page, err := g.activity.Query(activity.Filter{})
	if err != nil || len(page.Items) != 3 {
		t.Fatalf("persisted audit: %+v, %v", page, err)
	}
	for _, event := range page.Items {
		detail := string(event.Detail)
		for _, secret := range []string{accessToken, refreshToken, serviceCredential, sensitiveField} {
			if strings.Contains(detail, secret) {
				t.Fatalf("credential leaked into persisted %s detail: %s", event.Kind, detail)
			}
		}
		if (event.Status == "success" || event.Kind == "test") && !strings.Contains(detail, "[redacted]") {
			t.Fatalf("redaction was not exercised for %s: %s", event.Kind, detail)
		}
	}
}
