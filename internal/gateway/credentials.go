package gateway

import (
	"os"
	"slices"
	"sort"
	"strings"

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

func (g *Gateway) lookupServerEnv(id, key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	if value := g.credentials.Get(credentialPrefix(id) + key); value != "" {
		return value
	}
	return ""
}
func (g *Gateway) serverLookup(s registry.Server) func(string) string {
	return func(key string) string { return g.lookupServerEnv(s.ID, key) }
}

// SaveServer also removes credentials no longer referenced by the configuration.
func (g *Gateway) SaveServer(s registry.Server) error {
	return g.saveServerCredentials(s, nil)
}

func (g *Gateway) saveServerCredentials(s registry.Server, values map[string]string) error {
	refs := serverReferences(s)
	prefix := credentialPrefix(s.ID)
	changes := map[string]string{}
	for name, value := range values {
		if !secrets.ValidEnvName(name) || !slices.Contains(refs, name) {
			return invalid("凭据名称必须对应此服务引用的环境变量")
		}
		changes[prefix+name] = value
	}
	for _, key := range g.credentials.Prefix(prefix) {
		if !slices.Contains(refs, strings.TrimPrefix(key, prefix)) {
			changes[key] = ""
		}
	}
	return g.credentials.Update(changes, func() error { return g.registry.SaveServer(s) })
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
		for _, s := range g.Snapshot().Servers {
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
	for _, s := range g.Snapshot().Servers {
		if s.ID != id {
			continue
		}
		out := []CredentialStatus{}
		for _, name := range serverReferences(s) {
			out = append(out, CredentialStatus{Name: name, Configured: g.lookupServerEnv(id, name) != "", Saved: g.credentials.Get(credentialPrefix(id)+name) != "", Environment: os.Getenv(name) != ""})
		}
		return out, nil
	}
	return nil, invalid("未找到指定的上游服务")
}

func (g *Gateway) DeleteServer(id string) error {
	changes := map[string]string{}
	for _, key := range g.credentials.Prefix(credentialPrefix(id)) {
		changes[key] = ""
	}
	for _, key := range g.credentials.Prefix("oauth/" + id + "/") {
		changes[key] = ""
	}
	err := g.credentials.Update(changes, func() error {
		return g.registry.Update(func(r registry.Registry) (registry.Registry, error) {
			r.Servers = slices.DeleteFunc(r.Servers, func(s registry.Server) bool { return s.ID == id })
			r.Tools = slices.DeleteFunc(r.Tools, func(t registry.Tool) bool { return t.ServerID == id })
			return r, nil
		})
	})
	if err == nil {
		g.upstream.ForgetOAuth(id)
	}
	return err
}

func (g *Gateway) CredentialsPersistent() bool { return g.credentials.Persistent() }
