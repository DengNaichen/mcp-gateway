package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A failed OAuth exchange must survive the SDK's discover -> initialize fallback.
// The loopback callback belongs to one authorization attempt, so a second browser
// cannot complete a request after the first callback has already been consumed.
func TestOAuthFailureDoesNotRestartBrowserDuringProtocolFallback(t *testing.T) {
	for _, failure := range []string{"issuer", "exchange"} {
		t.Run(failure, func(t *testing.T) {
			var endpoint string
			var browsers atomic.Int32
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					_ = json.NewEncoder(w).Encode(map[string]any{"resource": endpoint + "/mcp", "authorization_servers": []string{endpoint}})
				case "/.well-known/oauth-authorization-server":
					_ = json.NewEncoder(w).Encode(map[string]any{"issuer": endpoint, "authorization_endpoint": endpoint + "/authorize", "token_endpoint": endpoint + "/token", "registration_endpoint": endpoint + "/register", "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true})
				case "/register":
					w.WriteHeader(http.StatusCreated)
					_, _ = fmt.Fprint(w, `{"client_id":"test-client","token_endpoint_auth_method":"none"}`)
				case "/token":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, `{"error":"invalid_grant"}`)
				case "/mcp":
					w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+endpoint+`/.well-known/oauth-protected-resource/mcp"`)
					w.WriteHeader(http.StatusUnauthorized)
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer ts.Close()
			endpoint = ts.URL
			svc := New(Config{OpenBrowser: func(raw string) error {
				browsers.Add(1)
				u, err := url.Parse(raw)
				if err != nil {
					return err
				}
				cb, err := url.Parse(u.Query().Get("redirect_uri"))
				if err != nil {
					return err
				}
				q := cb.Query()
				q.Set("code", "test-code")
				q.Set("state", u.Query().Get("state"))
				if failure != "issuer" {
					q.Set("iss", endpoint)
				}
				cb.RawQuery = q.Encode()
				resp, err := http.Get(cb.String())
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if strings.Contains(string(body), "授权成功") || !strings.Contains(string(body), "已收到授权回调") {
					t.Error("callback reported success before the authorization flow finished")
				}
				return err
			}})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := svc.connectMCP(ctx, ctx, Server{ID: "test", Endpoint: endpoint + "/mcp", OAuth: true})
			want := "token exchange failed"
			if failure == "issuer" {
				want = "iss parameter support"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("want original OAuth error %q, got %v", want, err)
			}
			if got := browsers.Load(); got != 1 {
				t.Errorf("opened browser %d times; want 1", got)
			}
			if ctx.Err() != nil {
				t.Errorf("waited for deadline after completed callback: %v", ctx.Err())
			}
		})
	}
}
