package api

import (
	"net"
	"net/http"
	"strings"

	"gatewaylab/internal/access"
)

func invocationEndpoint(r *http.Request) bool {
	if r.URL.Path == "/mcp" {
		return true
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/discover" || r.URL.Path == "/api/invoke") {
		return true
	}
	if r.Method == http.MethodGet && (r.URL.Path == "/api/catalog" || strings.HasPrefix(r.URL.Path, "/api/tools/") && strings.HasSuffix(r.URL.Path, "/schema")) {
		return true
	}
	return false
}

func (g *server) protectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.Access() == nil {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
				host = host[1 : len(host)-1] // bracketed IPv6 literal without a port
			}
			if !access.LoopbackHost(host) {
				fail(w, problem(403, "本机模式不接受外部 Host，请配置认证后共享", nil))
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/health" || r.URL.Path == "/api/auth/login" || strings.HasPrefix(r.URL.Path, "/api/maintenance/sources/") && strings.Contains(r.URL.Path, "/webhook/") {
			next.ServeHTTP(w, r)
			return // Webhooks authenticate their provider-specific signature.
		}
		var principal access.Principal
		authenticated := false
		authorization := r.Header.Get("Authorization")
		if strings.HasPrefix(authorization, "Bearer ") {
			principal, authenticated = g.Access().Authenticate(strings.TrimPrefix(authorization, "Bearer "))
		} else if authorization == "" {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				principal, authenticated = g.Access().Session(cookie.Value)
			}
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gateway"`)
			fail(w, problem(401, "需要有效的 Gateway 访问令牌", nil))
			return
		}
		if !principal.Admin && !invocationEndpoint(r) && r.URL.Path != "/api/auth/me" && r.URL.Path != "/api/auth/logout" {
			fail(w, problem(403, "此操作需要管理身份", nil))
			return
		}
		next.ServeHTTP(w, r.WithContext(access.WithPrincipal(r.Context(), principal)))
	})
}

func (g *server) registerAccess(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := access.PrincipalFrom(r.Context())
		respond(w, 200, map[string]any{"authenticated": ok, "shared": g.Access() != nil, "principal": principal})
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if g.Access() == nil {
			fail(w, problem(409, "本机模式没有启用身份认证", nil))
			return
		}
		var input struct {
			Token string `json:"token"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		principal, id, expires, err := g.Access().Login(input.Token)
		if err != nil {
			fail(w, err)
			return
		}

		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(g.BaseURL(), "https://"), SameSite: http.SameSiteStrictMode, Expires: expires}) //nolint:gosec // G124: Secure is set conditionally on TLS/https base URL so loopback http still works; logout cookie only clears the session
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, map[string]any{"principal": principal})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if g.Access() != nil {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				g.Access().Logout(cookie.Value)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1}) //nolint:gosec // G124: Secure is set conditionally on TLS/https base URL so loopback http still works; logout cookie only clears the session
		respond(w, 200, map[string]bool{"signed_out": true})
	})
}
