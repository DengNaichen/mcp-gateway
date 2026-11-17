package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gatewaylab/internal/registry"
)

// Opt-in live acceptance: run twice with the fixed isolated temp directory and
// GATEWAY_CREDENTIAL_STORE=keychain. Set GATEWAY_LIVE_NO_BROWSER=1 on run two.
// The authorization URL handoff file contains no token or client secret.
func TestLiveOAuthRestart(t *testing.T) {
	endpoint := os.Getenv("GATEWAY_LIVE_OAUTH_ENDPOINT")
	if endpoint == "" {
		t.Skip("opt-in live OAuth acceptance")
	}
	// Keep this opt-in probe confined to one directory under the OS temp root.
	dir := filepath.Join(os.TempDir(), "mcp-gateway-oauth-live-acceptance")
	g, err := NewWithOptions(dir, "http://127.0.0.1:0", Options{PersistCredentials: true, OpenBrowser: func(raw string) error {
		if os.Getenv("GATEWAY_LIVE_NO_BROWSER") == "1" {
			return errors.New("unexpected browser after restart")
		}
		return os.WriteFile(filepath.Join(dir, "authorize-url"), []byte(raw), 0600)
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	server := registry.Server{ID: "live-oauth", Name: "Live OAuth acceptance", Kind: "mcp", Transport: "http", Endpoint: endpoint, OAuth: true, Enabled: true}
	if err := g.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	n, err := g.ImportMCP(ctx, server.ID)
	if err != nil {
		t.Fatal(g.ErrorDetail(err))
	}
	t.Logf("imported %d tools", n)
	for _, name := range []string{"whoami", "list_teams"} {
		_, err := g.upstream.CallMCP(ctx, server, registry.Tool{Name: name}, map[string]any{})
		if err != nil {
			t.Fatal(g.ErrorDetail(err))
		}
		t.Logf("%s succeeded", name)
	}
	// Check real SQLite and WAL bytes against all saved OAuth secret fields.
	paths, _ := filepath.Glob(filepath.Join(dir, "gateway.db*"))
	for _, key := range g.credentials.Prefix("oauth/") {
		var session struct {
			Config struct{ ClientSecret string }
			Token  struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
			}
		}
		if err := json.Unmarshal([]byte(g.credentials.Get(key)), &session); err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{session.Token.AccessToken, session.Token.RefreshToken, session.Config.ClientSecret} {
				if secret != "" && bytes.Contains(raw, []byte(secret)) {
					t.Fatal("OAuth secret in SQLite")
				}
			}
		}
	}
}
