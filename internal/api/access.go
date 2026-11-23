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

func checkLoopbackHost(rawHost string) bool {
	host := rawHost
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1] // bracketed IPv6 literal without a port
	}
	return access.LoopbackHost(host)
}

func (g *server) authenticateRequest(r *http.Request) (access.Principal, bool) {
	authorization := r.Header.Get("Authorization")
	if strings.HasPrefix(authorization, "Bearer ") {
		return g.access.Authenticate(strings.TrimPrefix(authorization, "Bearer "))
	}
	if authorization == "" {
		if cookie, err := r.Cookie("gateway_session"); err == nil {
			return g.access.Session(cookie.Value)
		}
	}
	return access.Principal{}, false
}

func (g *server) securityMode() string {
	if g.access != nil {
		return apitypes.SecurityModeSharedBoundary
	}
	return apitypes.SecurityModeLocalGuardrail
}

func (g *server) protectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.access == nil {
			if !checkLoopbackHost(r.Host) {
				fail(w, problem(403, "本机模式不接受外部 Host，请配置认证后共享", nil))
				return
			}
			if g.localAdmin != nil {
				bearer, hasBearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
				if g.localAdmin.Authenticate(r.Header.Get("X-Gateway-Admin-Token")) || (hasBearer && g.localAdmin.Authenticate(bearer)) {
					r = r.WithContext(access.WithLocalAdmin(r.Context()))
				} else if rt, matched := apiRouteIndex.lookup(r); matched && rt.requiresAdmin() {
					fail(w, problem(403, "此操作需要有效的本机 Gateway 管理令牌", nil))
					return
				}
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
		principal, authenticated := g.authenticateRequest(r)
		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gateway"`)
			fail(w, problem(401, "需要有效的 Gateway 访问令牌", nil))
			return
		}
		allowNonAdmin := r.URL.Path == "/mcp" || (matched && !rt.requiresAdmin())
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
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, apitypes.AuthMe{
			Authenticated: ok || access.IsLocalAdmin(r.Context()),
			Shared:        g.access != nil,
			SecurityMode:  g.securityMode(),
			Principal:     wirePrincipal(principal),
		})
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
