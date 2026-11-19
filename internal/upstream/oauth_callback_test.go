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
				if strings.Contains(string(body), "授权成功") || !strings.Contains(string(body), "授权完成") || !strings.Contains(string(body), "请返回 MCP Gateway 查看连接结果") {
					t.Error("callback page did not render expected post-auth guidance without claiming connection success")
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

func TestRenderOAuthCallbackPage(t *testing.T) {
	t.Run("received", func(t *testing.T) {
		body := string(renderOAuthCallbackPage(oauthCallbackReceived, Server{
			ID:       "linear",
			Name:     "Linear",
			Endpoint: "https://mcp.linear.app/sse",
		}, ""))

		for _, want := range []string{"授权完成", "正在连接", "Linear", "mcp.linear.app", "请返回 MCP Gateway 查看连接结果"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		if strings.Contains(body, "授权成功") {
			t.Errorf("received page must not prematurely claim 授权成功")
		}
		if strings.Contains(body, "copyErrBtn") {
			t.Errorf("received page should not render error copy script")
		}
	})

	t.Run("error escapes untrusted input and falls back to server id", func(t *testing.T) {
		body := string(renderOAuthCallbackPage(oauthCallbackError, Server{
			ID:       "custom-svc",
			Endpoint: "https://auth.example.com/mcp",
		}, `access_denied: <script>alert("xss")</script>`))

		for _, want := range []string{"授权未完成", "未完成", "custom-svc", "auth.example.com", "copyErrBtn", `&lt;script&gt;alert(&#34;xss&#34;)&lt;/script&gt;`} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		if strings.Contains(body, `<script>alert("xss")</script>`) {
			t.Errorf("error detail was not HTML escaped")
		}
	})

	t.Run("invalid omits metadata divider", func(t *testing.T) {
		body := string(renderOAuthCallbackPage(oauthCallbackInvalid, Server{
			ID:       "linear",
			Name:     "Linear",
			Endpoint: "https://mcp.linear.app/sse",
		}, ""))

		for _, want := range []string{"授权链接无效", "链接无效"} {
			if !strings.Contains(body, want) {
				t.Errorf("body missing %q", want)
			}
		}
		if strings.Contains(body, `<div class="meta-section">`) {
			t.Errorf("invalid page should omit meta-section divider")
		}
	})
}
