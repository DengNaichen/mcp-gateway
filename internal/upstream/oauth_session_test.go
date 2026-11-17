package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// Exercise real DCR, PKCE callback, token exchange, MCP requests, and a new
// Service instance on every connection. The backing store stands in for Keychain.
func TestOAuthSessionRestartRefreshAndRecovery(t *testing.T) {
	var mu sync.Mutex
	saved := map[string]string{}
	mode, generation, browsers := "", 1, 0
	config := Config{
		LoadOAuth: func(k string) string { mu.Lock(); defer mu.Unlock(); return saved[k] },
		SaveOAuth: func(k, v string) error { mu.Lock(); defer mu.Unlock(); saved[k] = v; return nil },
		OpenBrowser: func(raw string) error {
			browsers++
			u, err := url.Parse(raw)
			if err != nil {
				return err
			}
			cb, err := url.Parse(u.Query().Get("redirect_uri"))
			if err != nil {
				return err
			}
			q := cb.Query()
			q.Set("code", "code")
			q.Set("state", u.Query().Get("state"))
			cb.RawQuery = q.Encode()
			resp, err := http.Get(cb.String())
			if err != nil {
				return err
			}
			return resp.Body.Close()
		},
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true})
	var endpoint string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
			_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{endpoint}})
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token", "registration_endpoint": endpoint + "/register", "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"client_id":"registered-client","token_endpoint_auth_method":"none"}`))
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") == "refresh_token" {
				if mode == "offline" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":"temporary","error_description":"secret-body"}`))
					return
				}
				if mode == "revoked" {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				if r.Form.Get("refresh_token") != fmt.Sprintf("refresh-%d", generation) {
					t.Error("stale refresh token")
				}
				generation++
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("access-%d", generation), "refresh_token": fmt.Sprintf("refresh-%d", generation), "token_type": "Bearer", "expires_in": 3600})
		case "/mcp":
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer access-%d", generation) {
				w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+endpoint+`/.well-known/oauth-protected-resource"`)
				w.WriteHeader(401)
				return
			}
			handler.ServeHTTP(w, r)
		default:
			w.WriteHeader(404)
		}
	}))
	defer ts.Close()
	endpoint = ts.URL
	server := Server{ID: "custom", Transport: "http", Endpoint: endpoint + "/mcp", OAuth: true}
	connect := func(wantErr string) {
		t.Helper()
		svc := New(config)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		session, err := svc.connectMCP(ctx, ctx, server)
		if wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), wantErr) {
				t.Fatalf("expected %q, got %v", wantErr, err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.ListTools(ctx, nil); err != nil {
			t.Fatal(err)
		}
		closeSession(session)
	}
	expire := func() {
		mu.Lock()
		defer mu.Unlock()
		key := oauthSessionKey(server)
		var session oauthSession
		if err := json.Unmarshal([]byte(saved[key]), &session); err != nil {
			t.Fatal(err)
		}
		session.Token.Expiry = time.Now().Add(-time.Hour)
		raw, _ := json.Marshal(session)
		saved[key] = string(raw)
	}
	connect("")
	connect("")
	if browsers != 1 {
		t.Fatalf("reopened browser after restart: %d", browsers)
	}
	expire()
	connect("")
	connect("")
	if generation != 2 || browsers != 1 {
		t.Fatal("refresh or restart failed")
	}
	expire()
	mode = "offline"
	connect("会话已保留")
	if saved[oauthSessionKey(server)] == "" {
		t.Fatal("network error erased session")
	}
	mode = ""
	connect("")
	connect("")
	if generation != 3 {
		t.Fatal("rotated refresh token was not restored")
	}
	expire()
	mode = "revoked"
	connect("授权已失效")
	if saved[oauthSessionKey(server)] != "" {
		t.Fatal("revoked session retained")
	}
	mode = ""
	connect("")
	if browsers != 2 {
		t.Fatal("reauthorization did not recover")
	}
	if oauthSessionKey(server) == oauthSessionKey(Server{ID: server.ID, Endpoint: "https://other.example"}) {
		t.Fatal("endpoint collision")
	}
	isolated := New(Config{})
	if source, err := isolated.restoreOAuth(oauthSessionKey(server)); err != nil || source != nil {
		t.Fatal("instance isolation failed")
	}
}

func TestOAuthSessionPersistenceFailureRetriesWithoutLeaking(t *testing.T) {
	fail := false
	svc := New(Config{SaveOAuth: func(string, string) error {
		if fail {
			return errors.New("store unavailable")
		}
		return nil
	}})
	src, err := svc.newOAuthSource("key", &oauth2.Config{}, &oauth2.Token{AccessToken: "one"})
	if err != nil {
		t.Fatal(err)
	}
	source := src.(*oauthSessionSource)
	source.source = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "rotated", RefreshToken: "private"})
	fail = true
	if _, err := source.Token(); err == nil {
		t.Fatal("persistence error ignored")
	}
	fail = false
	if _, err := source.Token(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source.saved, "rotated") {
		t.Fatal("did not retry persistence")
	}
}
