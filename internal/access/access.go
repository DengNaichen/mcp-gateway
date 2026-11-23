package access

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
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/input"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/secrets"
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
type Policy struct {
	principals []Principal
	hashes     [][32]byte
	mu         sync.Mutex
	sessions   map[string]authSession
}
type principalContextKey struct{}

func LoadPolicy(baseURL string, lookupToken func(string) string) (*Policy, error) {
	path := os.Getenv("GATEWAY_AUTH_FILE")
	if path == "" {
		u, err := url.Parse(baseURL)
		if err != nil || !LoopbackHost(u.Hostname()) {
			return nil, invalid("非本机监听必须配置 GATEWAY_AUTH_FILE")
		}
		return nil, nil
	}
	if lookupToken == nil {
		lookupToken = os.Getenv
	}
	b, err := input.ReadFileBounded(path, 1<<20)
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
	if err := input.EnsureJSONEnd(decoder, invalid); err != nil {
		return nil, err
	}
	if len(config.Principals) == 0 || len(config.Principals) > 1000 {
		return nil, invalid("认证配置需要 1–1000 个身份")
	}
	policy := &Policy{sessions: map[string]authSession{}}
	ids := map[string]bool{}
	hashes := map[string]bool{}
	admin := false
	for _, entry := range config.Principals {
		if !registry.ValidID(entry.ID) || ids[entry.ID] || len(entry.Groups) == 0 || len(entry.Servers) == 0 {
			return nil, invalid("身份需要唯一 ID、groups 和 servers 范围")
		}
		ids[entry.ID] = true
		if (entry.TokenEnv == "") == (entry.TokenSHA256 == "") {
			return nil, invalid("身份必须且只能配置 token_env 或 token_sha256")
		}
		var hash [32]byte
		if entry.TokenEnv != "" {
			if !secrets.ValidEnvName(entry.TokenEnv) {
				return nil, invalid("token_env 必须是环境变量名称")
			}
			token := lookupToken(entry.TokenEnv)
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
func LoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func (p *Policy) Authenticate(token string) (Principal, bool) {
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
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalContextKey{}).(Principal)
	return p, ok
}
func RequestActor(ctx context.Context, actor string) string {
	if p, ok := PrincipalFrom(ctx); ok {
		return p.ID
	}
	return ActorName(actor)
}
func (p *Policy) IdentityAllowsTool(ctx context.Context, t registry.Tool, actor string) bool {
	if p, ok := PrincipalFrom(ctx); ok {
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
			if slices.Contains(t.Actors, "*") || slices.Contains(t.Actors, group) {
				return true
			}
		}
		return false
	}
	return p == nil && Allowed(t, actor)
}

// WithDeclaredPrincipal resolves a configured identity for administrative simulations.
func (p *Policy) WithDeclaredPrincipal(ctx context.Context, actor string) (context.Context, error) {
	if p == nil {
		return ctx, nil
	}
	for _, principal := range p.principals {
		if principal.ID == actor {
			return context.WithValue(ctx, principalContextKey{}, principal), nil
		}
	}
	return ctx, invalid("共享模式的 actor 必须是已配置的身份 ID")
}

func ActorName(actor string) string {
	if actor == "" {
		return "developer"
	}
	return actor
}
func Allowed(t registry.Tool, actor string) bool {
	return t.Enabled && (slices.Contains(t.Actors, "*") || slices.Contains(t.Actors, ActorName(actor)))
}
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}
func (p *Policy) Principals() []Principal {
	if p == nil {
		return nil
	}
	out := make([]Principal, len(p.principals))
	for i, v := range p.principals {
		v.Groups = slices.Clone(v.Groups)
		v.Servers = slices.Clone(v.Servers)
		v.Tools = slices.Clone(v.Tools)
		out[i] = v
	}
	return out
}
func (p *Policy) Session(id string) (Principal, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	return s.Principal, ok && s.Expires.After(time.Now())
}
func (p *Policy) Logout(id string) { p.mu.Lock(); defer p.mu.Unlock(); delete(p.sessions, id) }
func (p *Policy) Login(token string) (Principal, string, time.Time, error) {
	principal, ok := p.Authenticate(token)
	if !ok {
		return Principal{}, "", time.Time{}, fault.New(fault.Unauthorized, "访问令牌无效", nil)
	}
	if !principal.Admin {
		return Principal{}, "", time.Time{}, fault.New(fault.Forbidden, "管理页面需要 admin 身份；调用方请使用 MCP 或 CLI", nil)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return Principal{}, "", time.Time{}, fmt.Errorf("create login session: %w", err)
	}
	id := base64.RawURLEncoding.EncodeToString(random)
	expires := time.Now().Add(8 * time.Hour)
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, session := range p.sessions {
		if session.Expires.Before(time.Now()) {
			delete(p.sessions, key)
		}
	}
	if len(p.sessions) >= 1024 {
		return Principal{}, "", time.Time{}, fault.New(fault.RateLimited, "活跃会话过多", nil)
	}
	p.sessions[id] = authSession{Principal: principal, Expires: expires}
	return principal, id, expires, nil
}
