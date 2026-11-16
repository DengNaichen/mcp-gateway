package market

import (
	"context"
	"encoding/json"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/secrets"
	"gatewaylab/internal/store"
	"gatewaylab/internal/upstream"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

	g, err := testMarket(t)
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
	g, err := testMarket(t)
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
	if got := os.Expand(hdr, testSecrets.Lookup); got != "Bearer github_pat_secret123" {
		t.Fatalf("expected in-memory secret expansion, got %q", got)
	}
	if err := upstream.CheckEnvironment(res.Server, testSecrets.Lookup); err != nil {
		t.Fatalf("upstream.CheckEnvironment failed: %v", err)
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

	g, err := testMarket(t)
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

var testSecrets secrets.Store

type testService struct{ *Service }

func (g *testService) Close() {}
func testMarket(t *testing.T) (*testService, error) {
	testSecrets = secrets.Store{}
	db, err := store.Open(t.TempDir())
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := registry.New(registry.Registry{}, store.Registry(db.DB).Save)
	us := upstream.New(upstream.Config{LookupEnv: testSecrets.Lookup})
	t.Cleanup(func() { _ = us.Close() })
	svc := New(Options{Dir: t.TempDir(), Snapshot: reg.Snapshot, SaveServer: reg.SaveServer, SetRuntimeSecret: testSecrets.Set, ErrorDetail: func(e error) string { return e.Error() }, ImportMCP: func(ctx context.Context, id string) (int, error) {
		var server registry.Server
		for _, s := range reg.Snapshot().Servers {
			if s.ID == id {
				server = s
			}
		}
		tools, err := us.ImportTools(ctx, server)
		return len(tools), err
	}})
	return &testService{svc}, nil
}
