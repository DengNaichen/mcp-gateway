package api

import (
	"net"
	"net/http"
	"strings"

	"gatewaylab/internal/access"
	"gatewaylab/internal/apitypes"
)

type apiRouteMatcher struct {
	mux    *http.ServeMux
	routes map[string]route
}

var apiRouteIndex = newAPIRouteMatcher(apiRoutes)

func newAPIRouteMatcher(routes []route) apiRouteMatcher {
	mux := http.NewServeMux()
	byPattern := make(map[string]route, len(routes))
	noop := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	for _, rt := range routes {
		pattern := rt.Method + " " + rt.Path
		mux.Handle(pattern, noop)
		byPattern[pattern] = rt
	}
	return apiRouteMatcher{mux: mux, routes: byPattern}
}

func (m apiRouteMatcher) lookup(r *http.Request) (route, bool) {
	_, pattern := m.mux.Handler(r)
	if pattern == "" {
		return route{}, false
	}
	rt, ok := m.routes[pattern]
	return rt, ok
}

func (g *server) protectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.access == nil {
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
		rt, matched := apiRouteIndex.lookup(r)
		if matched && rt.Public {
			next.ServeHTTP(w, r)
			return
		}
		var principal access.Principal
		authenticated := false
		authorization := r.Header.Get("Authorization")
		if strings.HasPrefix(authorization, "Bearer ") {
			principal, authenticated = g.access.Authenticate(strings.TrimPrefix(authorization, "Bearer "))
		} else if authorization == "" {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				principal, authenticated = g.access.Session(cookie.Value)
			}
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gateway"`)
			fail(w, problem(401, "需要有效的 Gateway 访问令牌", nil))
			return
		}
		allowNonAdmin := r.URL.Path == "/mcp" || (matched && (rt.Invocation || rt.Authenticated))
		if !principal.Admin && !allowNonAdmin {
			fail(w, problem(403, "此操作需要管理身份", nil))
			return
		}
		next.ServeHTTP(w, r.WithContext(access.WithPrincipal(r.Context(), principal)))
	})
}

func (g *server) registerAccess(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := access.PrincipalFrom(r.Context())
		respond(w, 200, apitypes.AuthMe{Authenticated: ok, Shared: g.access != nil, Principal: wirePrincipal(principal)})
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if g.access == nil {
			fail(w, problem(409, "本机模式没有启用身份认证", nil))
			return
		}
		var input apitypes.LoginRequest
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		principal, id, expires, err := g.access.Login(input.Token)
		if err != nil {
			fail(w, err)
			return
		}

		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(g.baseURL, "https://"), SameSite: http.SameSiteStrictMode, Expires: expires}) //nolint:gosec // G124: Secure is set conditionally on TLS/https base URL so loopback http still works; logout cookie only clears the session
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, apitypes.LoginResult{Principal: wirePrincipal(principal)})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if g.access != nil {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				g.access.Logout(cookie.Value)
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1}) //nolint:gosec // G124: Secure is set conditionally on TLS/https base URL so loopback http still works; logout cookie only clears the session
		respond(w, 200, apitypes.SignedOut{SignedOut: true})
	})
}
