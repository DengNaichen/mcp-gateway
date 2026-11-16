package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMarketServersCuratedAndRegistrySearch(t *testing.T) {
	origURL := marketRegistryURL
	defer func() { marketRegistryURL = origURL }()

	regSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query().Get("search"); q != "postgres" {
			t.Fatalf("unexpected search query: %q", q)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"servers": []map[string]any{
				{
					"server": map[string]any{
						"name":        "io.github.modelcontextprotocol/server-postgres",
						"title":       "PostgreSQL",
						"description": "Read-only SQL queries and schema inspection.",
						"repository":  map[string]any{"url": "https://github.com/modelcontextprotocol/servers"},
						"packages": []map[string]any{{
							"registryType": "npm",
							"identifier":   "@modelcontextprotocol/server-postgres",
							"transport":    map[string]any{"type": "stdio"},
						}},
					},
					"_meta": map[string]any{
						"io.modelcontextprotocol.registry/official": map[string]any{"status": "active"},
					},
				},
				{
					// Duplicate endpoint with curated context7 should be deduplicated.
					"server": map[string]any{
						"name":        "com.upstash/context7",
						"title":       "Context7 Dup",
						"description": "Dup",
						"remotes": []map[string]any{{
							"type": "streamable-http",
							"url":  "https://mcp.context7.com/mcp",
						}},
					},
				},
			},
		})
	}))
	defer regSrv.Close()
	marketRegistryURL = regSrv.URL

	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	all := g.MarketServers(context.Background(), "")
	if len(all.Items) != 20 || all.RegistryError != "" {
		t.Fatalf("expected 20 curated market items without error, got %d err=%q", len(all.Items), all.RegistryError)
	}

	res := g.MarketServers(context.Background(), "postgres")
	if res.RegistryError != "" {
		t.Fatalf("unexpected registry error: %s", res.RegistryError)
	}
	foundRegistry := false
	for _, item := range res.Items {
		if item.ServerID == "modelcontextprotocol-server-postgres" && item.Runs == "npx" {
			foundRegistry = true
		}
	}
	if !foundRegistry {
		t.Fatalf("expected registry result modelcontextprotocol-server-postgres, got %+v", res.Items)
	}

	// Registry failure degrades gracefully.
	marketRegistryURL = "http://127.0.0.1:1"
	degraded := g.MarketServers(context.Background(), "github")
	if len(degraded.Items) == 0 || degraded.RegistryError == "" {
		t.Fatalf("expected curated match plus registry_error, got items=%d err=%q", len(degraded.Items), degraded.RegistryError)
	}
}

func TestInstallMarketServerSecretsAndArgs(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	// Use a short pre-canceled context for ImportMCP so unit tests stay offline and fast
	// while still verifying server persistence, env-var redaction, and arg injection.
	installCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Missing required token returns error.
	if _, err := g.InstallMarketServer(installCtx, InstallMarketRequest{ID: "github"}); err == nil {
		t.Fatalf("expected error installing github without required PAT")
	}

	// Installing github stores ${ENV_VAR} placeholder on disk and keeps raw token only in memory.
	res, err := g.InstallMarketServer(installCtx, InstallMarketRequest{
		ID:     "github",
		Inputs: map[string]string{"Authorization": "github_pat_secret123"},
	})
	if err != nil {
		t.Fatalf("install github: %v", err)
	}
	hdr := res.Server.Headers["Authorization"]
	if !strings.Contains(hdr, "${") || strings.Contains(hdr, "github_pat_secret123") {
		t.Fatalf("expected env var placeholder in header, got %q", hdr)
	}
	if got := g.expandEnv(hdr); got != "Bearer github_pat_secret123" {
		t.Fatalf("expected in-memory secret expansion, got %q", got)
	}
	if err := checkEnvironmentWithLookup(res.Server, g.lookupEnv); err != nil {
		t.Fatalf("checkEnvironmentWithLookup failed: %v", err)
	}

	// Installing filesystem appends folder argument.
	fsRes, err := g.InstallMarketServer(installCtx, InstallMarketRequest{
		ID:     "filesystem",
		Inputs: map[string]string{"dir": "/tmp/workspace"},
	})
	if err != nil {
		t.Fatalf("install filesystem: %v", err)
	}
	if got := fsRes.Server.Args[len(fsRes.Server.Args)-1]; got != "/tmp/workspace" {
		t.Fatalf("expected directory appended to args, got %v", fsRes.Server.Args)
	}

	// Market listing marks installed servers.
	listed := g.MarketServers(context.Background(), "")
	installedCount := 0
	for _, item := range listed.Items {
		if item.Installed {
			installedCount++
		}
	}
	if installedCount != 2 {
		t.Fatalf("expected 2 installed servers in market listing, got %d", installedCount)
	}
}

func TestMarketIconCacheAndAllowlist(t *testing.T) {
	calls := 0
	imgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"></svg>`))
	}))
	defer imgSrv.Close()

	registerAllowedMarketIcon("test-icon-srv", imgSrv.URL+"/logo.svg")

	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	data, ct, err := g.MarketIcon(context.Background(), "test-icon-srv", "")
	if err != nil || ct != "image/svg+xml" || len(data) == 0 || calls != 1 {
		t.Fatalf("unexpected first icon fetch: ct=%q len=%d calls=%d err=%v", ct, len(data), calls, err)
	}

	// Second call must hit disk cache without hitting upstream again.
	imgSrv.Close()
	data2, ct2, err := g.MarketIcon(context.Background(), "test-icon-srv", "")
	if err != nil || ct2 != "image/svg+xml" || string(data2) != string(data) || calls != 1 {
		t.Fatalf("expected cached icon fetch, calls=%d err=%v", calls, err)
	}

	// Disallow unregistered URLs.
	if _, _, err := g.MarketIcon(context.Background(), "unknown", "https://evil.example/a.png"); err == nil {
		t.Fatalf("expected error for unregistered icon URL")
	}
}

func TestOAuthBrowserSignInAndImportMCP(t *testing.T) {
	origOpen := openBrowserURL
	defer func() { openBrowserURL = origOpen }()

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
				"issuer":                            authSrv.URL,
				"authorization_endpoint":            authSrv.URL + "/authorize",
				"token_endpoint":                    authSrv.URL + "/token",
				"registration_endpoint":             authSrv.URL + "/register",
				"code_challenge_methods_supported":  []string{"S256"},
				"grant_types_supported":             []string{"authorization_code", "refresh_token"},
				"response_types_supported":          []string{"code"},
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
	openBrowserURL = func(rawURL string) error {
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

	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	res, err := g.InstallMarketServer(context.Background(), InstallMarketRequest{
		ID: "custom-saas",
		Server: &MarketServer{
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
