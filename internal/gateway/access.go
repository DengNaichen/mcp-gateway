package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

type Principal struct {
	ID         string   `json:"id"`
	Groups     []string `json:"groups"`
	Servers    []string `json:"servers"`
	Tools      []string `json:"tools,omitempty"`
	AllowWrite bool     `json:"allow_write"`
	Admin      bool     `json:"admin"`
}
type PrincipalConfig struct {
	Principal
	TokenEnv    string `json:"token_env,omitempty"`
	TokenSHA256 string `json:"token_sha256,omitempty"`
}
type authSession struct {
	Principal Principal
	Expires   time.Time
}
type accessPolicy struct {
	principals []Principal
	hashes     [][32]byte
	mu         sync.Mutex
	sessions   map[string]authSession
}
type principalContextKey struct{}

func loadAccessPolicy(baseURL string) (*accessPolicy, error) {
	path := os.Getenv("GATEWAY_AUTH_FILE")
	if path == "" {
		u, err := url.Parse(baseURL)
		if err != nil || !loopbackHost(u.Hostname()) {
			return nil, invalid("非本机监听必须配置 GATEWAY_AUTH_FILE")
		}
		return nil, nil
	}
	b, err := readFileBounded(path, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read access configuration: %w", err)
	}
	var config struct {
		Principals []PrincipalConfig `json:"principals"`
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode access configuration: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, err
	}
	if len(config.Principals) == 0 || len(config.Principals) > 1000 {
		return nil, invalid("认证配置需要 1–1000 个身份")
	}
	policy := &accessPolicy{sessions: map[string]authSession{}}
	ids := map[string]bool{}
	hashes := map[string]bool{}
	admin := false
	for _, entry := range config.Principals {
		if !validID(entry.ID) || ids[entry.ID] || len(entry.Groups) == 0 || len(entry.Servers) == 0 {
			return nil, invalid("身份需要唯一 ID、groups 和 servers 范围")
		}
		ids[entry.ID] = true
		if (entry.TokenEnv == "") == (entry.TokenSHA256 == "") {
			return nil, invalid("身份必须且只能配置 token_env 或 token_sha256")
		}
		var hash [32]byte
		if entry.TokenEnv != "" {
			if !secretEnvName.MatchString(entry.TokenEnv) {
				return nil, invalid("token_env 必须是环境变量名称")
			}
			token := os.Getenv(entry.TokenEnv)
			if len(token) < 24 {
				return nil, invalid("访问令牌环境变量未配置或少于 24 字节：" + entry.TokenEnv)
			}
			hash = sha256.Sum256([]byte(token))
		} else {
			decoded, err := hex.DecodeString(entry.TokenSHA256)
			if err != nil || len(decoded) != 32 {
				return nil, invalid("token_sha256 必须是 SHA256 的十六进制值")
			}
			copy(hash[:], decoded)
		}
		encoded := hex.EncodeToString(hash[:])
		if hashes[encoded] {
			return nil, invalid("不同身份不能共用访问令牌")
		}
		hashes[encoded] = true
		policy.principals = append(policy.principals, entry.Principal)
		policy.hashes = append(policy.hashes, hash)
		admin = admin || entry.Admin
	}
	if !admin {
		return nil, invalid("认证配置至少需要一个 admin 身份")
	}
	return policy, nil
}
func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (p *accessPolicy) authenticate(token string) (Principal, bool) {
	hash := sha256.Sum256([]byte(token))
	index := -1
	for i, want := range p.hashes {
		if subtle.ConstantTimeCompare(hash[:], want[:]) == 1 {
			index = i
		}
	}
	if index < 0 || token == "" {
		return Principal{}, false
	}
	return p.principals[index], true
}
func principalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}
func requestActor(ctx context.Context, actor string) string {
	if p, ok := principalFrom(ctx); ok {
		return p.ID
	}
	return actorName(actor)
}
func (g *Gateway) authorizedTool(ctx context.Context, t Tool, actor string) bool {
	if p, ok := principalFrom(ctx); ok {
		if !t.Enabled || !p.AllowWrite && !t.ReadOnly {
			return false
		}
		if !slices.Contains(p.Servers, "*") && !slices.Contains(p.Servers, t.ServerID) {
			return false
		}
		if len(p.Tools) > 0 && !slices.Contains(p.Tools, "*") && !slices.Contains(p.Tools, t.ID) {
			return false
		}
		for _, group := range p.Groups {
			if slices.Contains(t.Actors, group) {
				return true
			}
		}
		return false
	}
	return g.access == nil && allowed(t, actor)
}
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
func (g *Gateway) protectAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g.access == nil {
			host := r.Host
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			if !loopbackHost(host) {
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
		var principal Principal
		authenticated := false
		authorization := r.Header.Get("Authorization")
		if strings.HasPrefix(authorization, "Bearer ") {
			principal, authenticated = g.access.authenticate(strings.TrimPrefix(authorization, "Bearer "))
		} else if authorization == "" {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				g.access.mu.Lock()
				session, ok := g.access.sessions[cookie.Value]
				if ok && session.Expires.After(time.Now()) {
					principal = session.Principal
					authenticated = true
				}
				g.access.mu.Unlock()
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalContextKey{}, principal)))
	})
}
func (g *Gateway) registerAccess(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		principal, ok := principalFrom(r.Context())
		respond(w, 200, map[string]any{"authenticated": ok, "shared": g.access != nil, "principal": principal})
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if g.access == nil {
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
		principal, ok := g.access.authenticate(input.Token)
		if !ok {
			fail(w, problem(401, "访问令牌无效", nil))
			return
		}
		if !principal.Admin {
			fail(w, problem(403, "管理页面需要 admin 身份；调用方请使用 MCP 或 CLI", nil))
			return
		}
		random := make([]byte, 32)
		if _, err := rand.Read(random); err != nil {
			fail(w, fmt.Errorf("create login session: %w", err))
			return
		}
		id := base64.RawURLEncoding.EncodeToString(random)
		expires := time.Now().Add(8 * time.Hour)
		g.access.mu.Lock()
		for key, session := range g.access.sessions {
			if session.Expires.Before(time.Now()) {
				delete(g.access.sessions, key)
			}
		}
		if len(g.access.sessions) >= 1024 {
			g.access.mu.Unlock()
			fail(w, problem(429, "活跃会话过多", nil))
			return
		}
		g.access.sessions[id] = authSession{Principal: principal, Expires: expires}
		g.access.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(g.baseURL, "https://"), SameSite: http.SameSiteStrictMode, Expires: expires})
		w.Header().Set("Cache-Control", "no-store")
		respond(w, 200, map[string]any{"principal": principal})
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if g.access != nil {
			if cookie, err := r.Cookie("gateway_session"); err == nil {
				g.access.mu.Lock()
				delete(g.access.sessions, cookie.Value)
				g.access.mu.Unlock()
			}
		}
		http.SetCookie(w, &http.Cookie{Name: "gateway_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		respond(w, 200, map[string]bool{"signed_out": true})
	})
}
func (g *Gateway) acceptanceActor(ctx context.Context, actor string) (context.Context, error) {
	if g.access == nil {
		return ctx, nil
	}
	for _, principal := range g.access.principals {
		if principal.ID == actor {
			return context.WithValue(ctx, principalContextKey{}, principal), nil
		}
	}
	return ctx, invalid("共享模式的路由验收 actor 必须是已配置的身份 ID")
}
