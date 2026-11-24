package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"
)

func TestBearerTokenAndSessionCookieJar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/auth/login":
			var req apitypes.LoginRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode login: %v", err)
			}
			if req.Token != "secret-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bad token"}`))
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "gw_session", Value: "sess-123", Path: "/"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"principal":{"id":"developer","admin":true}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/auth/me":
			c, err := r.Cookie("gw_session")
			if err != nil || c.Value != "sess-123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"missing session"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authenticated":true,"shared":true,"principal":{"id":"developer","admin":true}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/discover":
			if got := r.Header.Get("Authorization"); got != "Bearer bearer-123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"missing bearer"}`))
				return
			}
			var req apitypes.DiscoverRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode discover: %v", err)
			}
			if !req.SummaryOnly {
				t.Fatalf("expected summary_only=true, got %+v", req)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"d1","status":"selected","summary":"ok","tools":[{"id":"github.list_prs","server_id":"github","name":"list_prs","description":"list","tags":[],"read_only":true}],"duration_ms":42,"prompt_tokens":100}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/market/icon":
			if got := r.Header.Get("Authorization"); got != "Bearer bearer-123" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg/>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()

	// Cookie jar persists session from Login -> AuthMe.
	cookieClient := New(srv.URL)
	if _, err := cookieClient.AuthMe(ctx); !IsUnauthorized(err) {
		t.Fatalf("expected unauthorized before login, got %v", err)
	}
	if _, err := cookieClient.Login(ctx, "wrong"); !IsUnauthorized(err) || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected 401 APIError without ErrUnauthorized wrap on /auth/login, got %v", err)
	}
	principal, err := cookieClient.Login(ctx, "secret-token")
	if err != nil || principal.ID != "developer" || !principal.Admin {
		t.Fatalf("Login failed: principal=%+v err=%v", principal, err)
	}
	me, err := cookieClient.AuthMe(ctx)
	if err != nil || !me.Authenticated || me.Principal.ID != "developer" {
		t.Fatalf("AuthMe with cookie failed: me=%+v err=%v", me, err)
	}

	// Bearer token option authenticates JSON and binary requests.
	bearerClient := New(srv.URL, WithToken("bearer-123"), WithTimeout(5*time.Second))
	disc, err := bearerClient.DiscoverSummary(ctx, apitypes.DiscoverRequest{Intent: "list prs"})
	if err != nil || len(disc.Tools) != 1 || disc.Tools[0].ID != "github.list_prs" || disc.PromptTokens != 100 {
		t.Fatalf("DiscoverSummary unexpected: res=%+v err=%v", disc, err)
	}
	icon, ct, err := bearerClient.MarketIcon(ctx, "github", "")
	if err != nil || string(icon) != "<svg/>" || ct != "image/svg+xml" {
		t.Fatalf("MarketIcon unexpected: icon=%q ct=%q err=%v", string(icon), ct, err)
	}
}

func TestLocalAdminTokenIsSeparateAndStaysAtGateway(t *testing.T) {
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("admin request followed an HTTP redirect")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(redirected.Close)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Gateway-Admin-Token"); got != "local-admin-token" {
			t.Error("missing local admin token")
		}
		if got := r.Header.Get("Authorization"); got != "Bearer shared-token" {
			t.Error("local admin option rewrote bearer authorization")
		}
		http.Redirect(w, r, redirected.URL, http.StatusFound)
	}))
	t.Cleanup(gateway.Close)

	client := New(gateway.URL, WithToken("shared-token"), WithAdminToken("local-admin-token"))
	_, err := client.AuthMe(t.Context())
	var redirect *APIError
	if !errors.As(err, &redirect) || redirect.Status != http.StatusFound {
		t.Fatalf("expected redirect response at original gateway, got %v", err)
	}
}

func TestRateLimitAndErrorMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/discover":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limited","detail":"provider 429","code":"rate_limited","retry_after_seconds":7}`))
		case "/api/settings":
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"upstream failed","retry_after_seconds":3}`))
		case "/api/scopes/rules":
			var rule apitypes.ScopeRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				t.Fatalf("decode rule: %v", err)
			}
			if rule.Grants == nil {
				t.Fatal("expected non-nil empty Grants slice")
			}
			_ = json.NewEncoder(w).Encode(rule)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := New(srv.URL)
	ctx := context.Background()

	_, err := c.DiscoverSummary(ctx, apitypes.DiscoverRequest{Intent: "x"})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusTooManyRequests || apiErr.RetryAfter != 7*time.Second || apiErr.Code != "rate_limited" || apiErr.Detail != "provider 429" {
		t.Fatalf("unexpected APIError: %+v", apiErr)
	}

	err = c.SaveSettings(ctx, apitypes.Settings{}, "", false)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway || apiErr.RetryAfter != 3*time.Second {
		t.Fatalf("expected fallback RetryAfter from JSON body, got %v", err)
	}

	// Nil Grants must marshal as [] rather than null.
	saved, err := c.SaveScopeRule(ctx, apitypes.ScopeRule{RepoKey: "*"})
	if err != nil || saved.Grants == nil || len(saved.Grants) != 0 {
		t.Fatalf("unexpected SaveScopeRule result: saved=%+v err=%v", saved, err)
	}
}

func TestUnauthorizedErrorFormatting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(srv.URL)
	_, err := c.Bootstrap(context.Background())
	if !IsUnauthorized(err) || !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized, got %v", err)
	}
	if !strings.Contains(err.Error(), ErrUnauthorized.Error()) {
		t.Fatalf("expected error message to include ErrUnauthorized text, got %q", err.Error())
	}
}
