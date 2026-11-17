package clientconfig

import (
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"gatewaylab/internal/buildinfo"
)

// Client ID constants identify the six supported coding-agent hosts.
const (
	ClientCodex       = "codex"
	ClientClaude      = "claude"
	ClientCursor      = "cursor"
	ClientOpenCode    = "opencode"
	ClientPi          = "pi"
	ClientAntigravity = "antigravity"
)

// ClientSpec is the single authoritative host definition for a supported local
// coding-agent client.
type ClientSpec struct {
	ID        string
	Name      string
	EnvVar    string
	Keys      []string
	TOML      bool
	configDir func(home string, getenv func(string) string) string
	mcpPath   func(home string, getenv func(string) string) string
}

// ConfigDir resolves the host's base configuration directory under home.
func (s ClientSpec) ConfigDir(home string, getenv func(string) string) string {
	if s.configDir == nil {
		return ""
	}
	return s.configDir(home, normalizeGetenv(getenv))
}

// MCPConfigPath resolves the host's standalone MCP client configuration file.
func (s ClientSpec) MCPConfigPath(home string, getenv func(string) string) string {
	if s.mcpPath == nil {
		return ""
	}
	return s.mcpPath(home, normalizeGetenv(getenv))
}

var clientCatalog = []ClientSpec{
	{
		ID:     ClientCodex,
		Name:   "Codex CLI",
		EnvVar: "CODEX_HOME",
		Keys:   []string{"mcp_servers"},
		TOML:   true,
		configDir: func(home string, getenv func(string) string) string {
			return envOrJoin(home, getenv, "CODEX_HOME", ".codex")
		},
		mcpPath: func(home string, getenv func(string) string) string {
			return filepath.Join(envOrJoin(home, getenv, "CODEX_HOME", ".codex"), "config.toml")
		},
	},
	{
		ID:     ClientClaude,
		Name:   "Claude Code",
		EnvVar: "CLAUDE_CONFIG_DIR",
		Keys:   []string{"mcpServers"},
		configDir: func(home string, getenv func(string) string) string {
			return envOrJoin(home, getenv, "CLAUDE_CONFIG_DIR", ".claude")
		},
		mcpPath: func(home string, getenv func(string) string) string {
			if v := strings.TrimSpace(getenv("CLAUDE_CONFIG_DIR")); v != "" {
				return filepath.Join(v, ".claude.json")
			}
			return filepath.Join(home, ".claude.json")
		},
	},
	{
		ID:   ClientCursor,
		Name: "Cursor",
		Keys: []string{"mcpServers"},
		configDir: func(home string, _ func(string) string) string {
			return filepath.Join(home, ".cursor")
		},
		mcpPath: func(home string, _ func(string) string) string {
			return filepath.Join(home, ".cursor", "mcp.json")
		},
	},
	{
		ID:     ClientOpenCode,
		Name:   "OpenCode v2",
		EnvVar: "XDG_CONFIG_HOME",
		Keys:   []string{"mcp", "servers"},
		configDir: func(home string, getenv func(string) string) string {
			return filepath.Join(envOrJoin(home, getenv, "XDG_CONFIG_HOME", ".config"), "opencode")
		},
		mcpPath: func(home string, getenv func(string) string) string {
			ocPath := filepath.Join(envOrJoin(home, getenv, "XDG_CONFIG_HOME", ".config"), "opencode", "opencode.json")
			if _, err := os.Stat(strings.TrimSuffix(ocPath, ".json") + ".jsonc"); err == nil {
				return strings.TrimSuffix(ocPath, ".json") + ".jsonc"
			}
			return ocPath
		},
	},
	{
		ID:     ClientPi,
		Name:   "Pi",
		EnvVar: "PI_CODING_AGENT_DIR",
		Keys:   []string{"mcpServers"},
		configDir: func(home string, getenv func(string) string) string {
			return envOrJoin(home, getenv, "PI_CODING_AGENT_DIR", ".pi", "agent")
		},
		mcpPath: func(home string, getenv func(string) string) string {
			return filepath.Join(envOrJoin(home, getenv, "PI_CODING_AGENT_DIR", ".pi", "agent"), "mcp.json")
		},
	},
	{
		ID:   ClientAntigravity,
		Name: "Antigravity",
		Keys: []string{"mcpServers"},
		configDir: func(home string, _ func(string) string) string {
			return filepath.Join(home, ".gemini", "config")
		},
		mcpPath: func(home string, _ func(string) string) string {
			return filepath.Join(home, ".gemini", "config", "mcp_config.json")
		},
	},
}

// Specs returns a copy of all supported coding-agent host specifications in
// canonical display order.
func Specs() []ClientSpec {
	out := make([]ClientSpec, len(clientCatalog))
	for i, spec := range clientCatalog {
		spec.Keys = slices.Clone(spec.Keys)
		out[i] = spec
	}
	return out
}

// ClientIDs returns the canonical ordered list of supported coding-agent IDs.
func ClientIDs() []string {
	out := make([]string, len(clientCatalog))
	for i, spec := range clientCatalog {
		out[i] = spec.ID
	}
	return out
}

// LookupClient looks up a host specification by case-insensitive ID.
func LookupClient(id string) (ClientSpec, bool) {
	norm := strings.ToLower(strings.TrimSpace(id))
	for _, spec := range clientCatalog {
		if spec.ID == norm {
			spec.Keys = slices.Clone(spec.Keys)
			return spec, true
		}
	}
	return ClientSpec{}, false
}

// ClientDisplayName returns the human-readable host name for id, or id when unknown.
func ClientDisplayName(id string) string {
	if spec, ok := LookupClient(id); ok {
		return spec.Name
	}
	return id
}

// HostConfigDir resolves the base config directory for client id under home.
func HostConfigDir(id, home string, getenv func(string) string) string {
	spec, ok := LookupClient(id)
	if !ok {
		return ""
	}
	return spec.ConfigDir(home, getenv)
}

// ClientEndpoint appends ?agent=<clientID> to endpoint, defaulting empty
// endpoints to the active channel's default MCP URL and normalizing empty or
// root paths to /mcp.
func ClientEndpoint(endpoint, clientID string) string {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		raw = "http://" + buildinfo.DefaultAddr() + "/mcp"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return endpoint
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/mcp"
	}
	if clientID != "" {
		q := u.Query()
		q.Set("agent", clientID)
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// ResolveClientDataDir resolves the Gateway data directory for local client
// integrations (gatewayctl / clientplugin) in precedence order:
//  1. explicit (--data)
//  2. channel-specific env var (buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"))
//  3. GATEWAY_DATA_DIR
//  4. <os.UserConfigDir()>/<slug> when that directory already exists (Desktop parity)
//
// Returns "" when none is set or present so hook wrappers fall back to ~/.<slug>/data.
func ResolveClientDataDir(explicit string, getenv func(string) string) string {
	return resolveClientDataDir(explicit, getenv, os.UserConfigDir)
}

func resolveClientDataDir(explicit string, getenv func(string) string, userConfigDir func() (string, error)) string {
	if trimmed := strings.TrimSpace(explicit); trimmed != "" {
		if abs, err := filepath.Abs(trimmed); err == nil {
			return abs
		}
		return trimmed
	}
	getenv = normalizeGetenv(getenv)
	if fromChEnv := strings.TrimSpace(getenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"))); fromChEnv != "" {
		if abs, err := filepath.Abs(fromChEnv); err == nil {
			return abs
		}
		return fromChEnv
	}
	if fromEnv := strings.TrimSpace(getenv("GATEWAY_DATA_DIR")); fromEnv != "" {
		if abs, err := filepath.Abs(fromEnv); err == nil {
			return abs
		}
		return fromEnv
	}
	if userConfigDir != nil {
		if cfgDir, err := userConfigDir(); err == nil && strings.TrimSpace(cfgDir) != "" {
			desktopDir := filepath.Join(cfgDir, buildinfo.Current().Slug)
			if info, statErr := os.Stat(desktopDir); statErr == nil && info.IsDir() {
				return desktopDir
			}
		}
	}
	return ""
}

func normalizeGetenv(getenv func(string) string) func(string) string {
	if getenv == nil {
		return os.Getenv
	}
	return getenv
}

func envOrJoin(home string, getenv func(string) string, key string, fallbackParts ...string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	parts := append([]string{home}, fallbackParts...)
	return filepath.Join(parts...)
}
