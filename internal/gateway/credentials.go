package gateway

import (
	"os"
	"slices"
	"sort"
	"strings"

	"gatewaylab/internal/cliexec"
	"gatewaylab/internal/market"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/secrets"
)

func credentialPrefix(id string) string { return "server/" + id + "/" }

func serverReferences(s registry.Server) []string {
	found := map[string]bool{}
	for _, values := range []map[string]string{s.Headers, s.Env} {
		for _, value := range values {
			os.Expand(value, func(name string) string { found[name] = true; return "" })
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func serverProcessEnv(key string) string {
	if strings.HasPrefix(strings.ToUpper(key), "GATEWAY_") {
		return ""
	}
	return os.Getenv(key)
}

func (g *Gateway) lookupServerSecretOnly(id, key string) string {
	return g.credentials.Get(credentialPrefix(id) + key)
}

func (g *Gateway) lookupServerEnv(id, key string) string {
	if value := serverProcessEnv(key); value != "" {
		return value
	}
	return g.lookupServerSecretOnly(id, key)
}
func (g *Gateway) serverLookup(s registry.Server) func(string) string {
	if s.Kind == "cli" {
		return func(key string) string { return g.lookupServerSecretOnly(s.ID, key) }
	}
	return func(key string) string { return g.lookupServerEnv(s.ID, key) }
}

// SaveServer also removes credentials no longer referenced by the configuration.
func (g *Gateway) SaveServer(s registry.Server) error {
	return g.saveServerCredentials(s, nil)
}

func (g *Gateway) saveServerCredentials(s registry.Server, values map[string]string) error {
	_, err := g.saveServer(s, values)
	return err
}

// SaveServerDefinition returns the configuration with its resolved CLI pin.
func (g *Gateway) SaveServerDefinition(s registry.Server) (registry.Server, error) {
	return g.saveServer(s, nil)
}

func (g *Gateway) saveServer(s registry.Server, values map[string]string) (registry.Server, error) {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	var tools []registry.Tool
	if s.Kind == "cli" {
		var previousPin string
		for _, old := range g.registry.Servers() {
			if old.ID == s.ID {
				previousPin = old.CommandSHA256
				break
			}
		}
		var err error
		s, err = cliexec.Pin(s, previousPin)
		if err != nil {
			return registry.Server{}, err
		}
		tools = cliexec.Tools(s.ID)
	}
	refs := serverReferences(s)
	prefix := credentialPrefix(s.ID)
	changes := map[string]string{}
	for name, value := range values {
		if !secrets.ValidEnvName(name) || !slices.Contains(refs, name) {
			return registry.Server{}, invalid("凭据名称必须对应此服务引用的环境变量")
		}
		changes[prefix+name] = value
	}
	for _, key := range g.credentials.Prefix(prefix) {
		if !slices.Contains(refs, strings.TrimPrefix(key, prefix)) {
			changes[key] = ""
		}
	}
	if err := g.credentials.Update(changes, func() error { return g.registry.SaveServer(s, tools...) }); err != nil {
		return registry.Server{}, err
	}
	return s, nil
}

// SetServerCredentials updates only existing references, without returning values.
func (g *Gateway) SetServerCredentials(id string, values map[string]string) error {
	changes := map[string]string{}
	for name, value := range values {
		if !secrets.ValidEnvName(name) {
			return invalid("凭据名称必须对应此服务引用的环境变量")
		}
		changes[credentialPrefix(id)+name] = value
	}
	// Validate against the latest registry while holding the credential write lock.
	// This mutation never writes a previously read server configuration back.
	return g.credentials.Update(changes, func() error {
		for _, s := range g.registry.Snapshot().Servers {
			if s.ID != id {
				continue
			}
			refs := serverReferences(s)
			for name := range values {
				if !slices.Contains(refs, name) {
					return invalid("凭据名称必须对应此服务引用的环境变量")
				}
			}
			return nil
		}
		return invalid("未找到指定的上游服务")
	})
}

type CredentialStatus struct {
	Name        string `json:"name"`
	Configured  bool   `json:"configured"`
	Saved       bool   `json:"saved"`
	Environment bool   `json:"environment"`
}

func (g *Gateway) ServerCredentials(id string) ([]CredentialStatus, error) {
	for _, s := range g.registry.Snapshot().Servers {
		if s.ID != id {
			continue
		}
		out := []CredentialStatus{}
		for _, name := range serverReferences(s) {
			saved := g.lookupServerSecretOnly(id, name) != ""
			environment := serverProcessEnv(name) != ""
			out = append(out, CredentialStatus{Name: name, Configured: saved || s.Kind != "cli" && environment, Saved: saved, Environment: environment})
		}
		return out, nil
	}
	return nil, invalid("未找到指定的上游服务")
}

type OAuthStatus struct {
	Enabled    bool `json:"enabled,omitempty"`
	Authorized bool `json:"authorized,omitempty"`
}

func (g *Gateway) ServerOAuthInfo(id string) (*OAuthStatus, *market.OAuthRemoteTarget, error) {
	for _, s := range g.registry.Snapshot().Servers {
		if s.ID != id {
			continue
		}
		switch s.Kind {
		case "mcp":
			var oauth *OAuthStatus
			if s.Transport != "stdio" && s.OAuth {
				oauth = &OAuthStatus{
					Enabled: true, Authorized: g.upstream != nil && g.upstream.HasOAuthSession(s),
				}
			}
			return oauth, market.MatchOAuthRemote(s), nil
		case "http", "cli":
			return nil, nil, nil
		default:
			return nil, nil, invalid("未知服务协议")
		}
	}
	return nil, nil, invalid("未找到指定的上游服务")
}

func (g *Gateway) ClearServerOAuth(id string) error {
	servers := g.registry.Servers()
	i := slices.IndexFunc(servers, func(s registry.Server) bool { return s.ID == id })
	if i < 0 {
		return invalid("未找到指定的上游服务")
	}
	switch servers[i].Kind {
	case "mcp":
	case "http", "cli":
		return invalid("此服务协议不支持 OAuth 会话")
	default:
		return invalid("未知服务协议")
	}
	changes := map[string]string{}
	for _, key := range g.credentials.Prefix("oauth/" + id + "/") {
		changes[key] = ""
	}
	if len(changes) > 0 {
		if err := g.credentials.Update(changes, nil); err != nil {
			return err
		}
	}
	if g.upstream != nil {
		g.upstream.ForgetOAuth(id)
	}
	return nil
}

func (g *Gateway) DeleteServer(id string) error {
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	changes := map[string]string{}
	for _, key := range g.credentials.Prefix(credentialPrefix(id)) {
		changes[key] = ""
	}
	for _, key := range g.credentials.Prefix("oauth/" + id + "/") {
		changes[key] = ""
	}
	err := g.credentials.Update(changes, func() error { return g.registry.DeleteServer(id) })
	if err == nil {
		g.upstream.ForgetOAuth(id)
	}
	return err
}
