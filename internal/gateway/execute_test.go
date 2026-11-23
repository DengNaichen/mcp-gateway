package gateway

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"

	"gatewaylab/internal/registry"
)

func TestInvokeRedactsActiveOAuthTokens(t *testing.T) {
	const accessToken = "fixture-active-access-token"
	const refreshToken = "fixture-active-refresh-token"
	t.Setenv("GATEWAY_AUTH_FILE", "")
	schema := map[string]any{"type": "object"}
	ms := mcp.NewServer(&mcp.Implementation{Name: "oauth-echo", Version: "1"}, nil)
	ms.AddTool(&mcp.Tool{Name: "echo", InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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
	server := registry.Server{ID: "oauth-echo", Name: "OAuth echo", Kind: "mcp", Transport: "http", Endpoint: host.URL, OAuth: true, Enabled: true}
	if err := g.SaveServer(server); err != nil {
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
	result, err := g.Invoke(t.Context(), InvokeRequest{ToolID: tool.ID, Fields: []string{"text"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustJSON(t, result.Data)); got != `{"text":"echoed [redacted] and [redacted]"}` {
		t.Fatalf("OAuth echo did not redact both active tokens: %s", got)
	}
}
