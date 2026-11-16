package api

import (
	"context"
	"encoding/json"
	"fmt"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/market"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestOAuthBrowserSignInAndImportMCP(t *testing.T) {

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "saas-oauth-mcp", Version: "1.0.0"}, nil)
	mcp.AddTool(mcpSrv, &mcp.Tool{
		Name:        "search_pages",
		Description: "Search workspace pages.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, args struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return mcpSrv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	var authSrv *httptest.Server
	authSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              authSrv.URL + "/mcp",
				"authorization_servers": []string{authSrv.URL},
			})
		case "/.well-known/oauth-authorization-server":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                authSrv.URL,
				"authorization_endpoint":                authSrv.URL + "/authorize",
				"token_endpoint":                        authSrv.URL + "/token",
				"registration_endpoint":                 authSrv.URL + "/register",
				"code_challenge_methods_supported":      []string{"S256"},
				"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
				"response_types_supported":              []string{"code"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/register":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"client_id":                  "dcr-client-1",
				"token_endpoint_auth_method": "none",
			})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "oauth-access-token-123",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		case "/mcp":
			if r.Header.Get("Authorization") != "Bearer oauth-access-token-123" {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, authSrv.URL))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mcpHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer authSrv.Close()

	// Simulate user completing browser login on the redirect callback.
	openBrowserURL := func(rawURL string) error {
		u, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		q := u.Query()
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")
		go func() {
			cb := fmt.Sprintf("%s?code=auth-code-xyz&state=%s&iss=%s", redirectURI, url.QueryEscape(state), url.QueryEscape(authSrv.URL))
			resp, err := http.Get(cb)
			if err == nil {
				_ = resp.Body.Close()
			}
		}()
		return nil
	}

	g, err := gateway.NewWithOptions(t.TempDir(), "http://127.0.0.1:0", gateway.Options{OpenBrowser: openBrowserURL})
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	res, err := g.Market().InstallMarketServer(context.Background(), market.InstallMarketRequest{
		ID: "custom-saas",
		Server: &market.MarketServer{
			ID:        "custom-saas",
			ServerID:  "custom-saas",
			Title:     "Custom SaaS",
			Publisher: "SaaS",
			Transport: "http",
			Runs:      "远程",
			Endpoint:  authSrv.URL + "/mcp",
			SignIn:    true,
		},
	})
	if err != nil {
		t.Fatalf("install oauth server: %v", err)
	}
	if res.SyncError != "" || res.ToolCount != 1 || !res.Server.OAuth {
		t.Fatalf("expected 1 imported tool after OAuth flow, got count=%d oauth=%v syncErr=%q", res.ToolCount, res.Server.OAuth, res.SyncError)
	}
}
