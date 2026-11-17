package clientplugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/workspace"
	pluginassets "gatewaylab/plugins"
)

type renderedFile struct {
	RelPath string
	Data    []byte
	Perm    fs.FileMode
}

type renderedBundle struct {
	RootFiles []renderedFile
	Digest    string
	HookBin   string
	Endpoint  string
}

func (m *Manager) resolveHookBinaryAndData() (hookBin string, dataDir string) {
	hookBin = m.binPath
	if hookBin == "" {
		hookBin = workspace.ResolveHookBinary(m.homeDir, m.dataDir)
	}
	if m.dataDir != "" {
		if absData, err := filepath.Abs(m.dataDir); err == nil {
			defData, defErr := workspace.DefaultDataDir()
			if defErr != nil || !sameCleanedPath(absData, defData) {
				dataDir = absData
			}
		}
	}
	return hookBin, dataDir
}

func sameCleanedPath(a, b string) bool {
	ra, ea := filepath.EvalSymlinks(a)
	rb, eb := filepath.EvalSymlinks(b)
	if ea == nil && eb == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func mustReadAsset(rel string) []byte {
	b, err := pluginassets.Files.ReadFile(rel)
	if err != nil {
		panic(fmt.Sprintf("missing embedded plugin asset %s: %v", rel, err))
	}
	return b
}

func encodePrettyJSON(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return buf.Bytes()
}

func (m *Manager) renderSkillMarkdown() []byte {
	raw := string(mustReadAsset("mcp-gateway/skills/mcp-gateway/SKILL.md"))
	if m.slug == "mcp-gateway" {
		return []byte(raw)
	}
	// Replace frontmatter skill name and CLI references cleanly for Dev isolation.
	raw = strings.Replace(raw, "name: mcp-gateway\n", "name: "+m.slug+"\n", 1)
	raw = strings.ReplaceAll(raw, "gatewayctl ", m.slug+"ctl ")
	return []byte(raw)
}

func (m *Manager) renderHookWrapper(hookBin, dataDir string) []byte {
	raw := string(mustReadAsset("mcp-gateway/scripts/run-gateway-hook"))
	raw = strings.Replace(raw, `default_bin=""`, `default_bin=`+workspace.ShellQuote(hookBin), 1)
	raw = strings.Replace(raw, `default_slug="mcp-gateway"`, `default_slug=`+workspace.ShellQuote(m.slug), 1)
	raw = strings.Replace(raw, `default_data_dir=""`, `default_data_dir=`+workspace.ShellQuote(dataDir), 1)
	return []byte(raw)
}

func (m *Manager) renderClaudeMarketplace(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientClaude)
	prefix := "plugins/" + m.slug + "/"
	desc := fmt.Sprintf("Connects Claude Code to %s with semantic tool discovery, scoped invocation, and automatic workspace provenance signing.", m.appName)

	marketplace := map[string]any{
		"$schema":     "https://anthropic.com/claude-code/marketplace.schema.json",
		"name":        m.slug,
		"description": fmt.Sprintf("Official %s plugin marketplace for Claude Code.", m.appName),
		"owner": map[string]any{
			"name": "Naicheng Deng",
			"url":  "https://github.com/DengNaichen",
		},
		"plugins": []any{
			map[string]any{
				"name":        m.slug,
				"description": desc,
				"source":      "./plugins/" + m.slug,
				"category":    "development",
			},
		},
	}

	manifest := map[string]any{
		"$schema":          "https://anthropic.com/claude-code/plugin.schema.json",
		"name":             m.slug,
		"version":          m.version,
		"description":      desc,
		"author":           map[string]any{"name": "Naicheng Deng", "url": "https://github.com/DengNaichen"},
		"homepage":         "https://github.com/DengNaichen/mcp-gateway",
		"repository":       "https://github.com/DengNaichen/mcp-gateway",
		"privacyPolicyUrl": "https://github.com/DengNaichen/mcp-gateway#readme",
		"license":          "GPL-3.0",
		"keywords":         []string{"mcp", "gateway", "tool-discovery", "scope", "coding-agent"},
		"skills":           []string{"./skills/" + m.slug},
		"hooks":            "./hooks/hooks.json",
		"mcpServers":       "./.mcp.json",
	}

	mcpDoc := map[string]any{
		"mcpServers": map[string]any{
			m.slug: map[string]any{
				"type": "http",
				"url":  agentURL,
			},
		},
	}

	hooksDoc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": workspace.NativePreToolMatcher(m.slug),
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": `sh "${CLAUDE_PLUGIN_ROOT}/scripts/run-gateway-hook" claude`,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	files := []renderedFile{
		{RelPath: ".claude-plugin/marketplace.json", Data: encodePrettyJSON(marketplace), Perm: 0o644},
		{RelPath: prefix + ".claude-plugin/plugin.json", Data: encodePrettyJSON(manifest), Perm: 0o644},
		{RelPath: prefix + ".mcp.json", Data: encodePrettyJSON(mcpDoc), Perm: 0o644},
		{RelPath: prefix + "hooks/hooks.json", Data: encodePrettyJSON(hooksDoc), Perm: 0o644},
		{RelPath: prefix + "scripts/run-gateway-hook", Data: m.renderHookWrapper(hookBin, dataDir), Perm: 0o755},
		{RelPath: prefix + "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
		{RelPath: prefix + "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}

func (m *Manager) renderCodexMarketplace(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientCodex)
	prefix := "plugins/" + m.slug + "/"
	desc := fmt.Sprintf("Connects Codex to %s with semantic tool discovery, scoped invocation, and automatic workspace provenance signing.", m.appName)

	marketplace := map[string]any{
		"name":      m.slug,
		"interface": map[string]any{"displayName": m.appName},
		"plugins": []any{
			map[string]any{
				"name": m.slug,
				"source": map[string]any{
					"source": "local",
					"path":   "./plugins/" + m.slug,
				},
				"policy": map[string]any{
					"installation":   "AVAILABLE",
					"authentication": "ON_INSTALL",
				},
				"category": "Developer Tools",
			},
		},
	}

	manifest := map[string]any{
		"name":        m.slug,
		"version":     m.version,
		"description": desc,
		"author":      map[string]any{"name": "Naicheng Deng", "url": "https://github.com/DengNaichen"},
		"homepage":    "https://github.com/DengNaichen/mcp-gateway",
		"repository":  "https://github.com/DengNaichen/mcp-gateway",
		"license":     "GPL-3.0",
		"keywords":    []string{"mcp", "gateway", "tool-discovery", "scope", "coding-agent"},
		"skills":      "./skills/",
		"mcpServers":  "./.mcp.json",
		"hooks":       "./hooks/hooks.json",
		"interface": map[string]any{
			"displayName":      m.appName,
			"shortDescription": "Semantic tool discovery & scoped MCP execution.",
			"longDescription":  "Connects Codex to MCP Gateway for on-demand tool discovery, schema inspection, field-projected invocation, and automatic repository-scoped provenance signing.",
			"developerName":    "Naicheng Deng",
			"category":         "Developer Tools",
			"capabilities":     []string{"Read", "Write", "Lifecycle hooks"},
			"websiteURL":       "https://github.com/DengNaichen/mcp-gateway",
			"composerIcon":     "./assets/icon.png",
			"logo":             "./assets/icon.png",
			"defaultPrompt": []string{
				"Discover available tools in MCP Gateway for my current task.",
				"Inspect the schema and invoke the authorized gateway tool.",
			},
		},
	}

	mcpDoc := map[string]any{
		"mcpServers": map[string]any{
			m.slug: map[string]any{
				"type": "http",
				"url":  agentURL,
			},
		},
	}

	hooksDoc := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": workspace.NativePreToolMatcher(m.slug),
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": `sh "${PLUGIN_ROOT}/scripts/run-gateway-hook" codex`,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	files := []renderedFile{
		{RelPath: ".agents/plugins/marketplace.json", Data: encodePrettyJSON(marketplace), Perm: 0o644},
		{RelPath: prefix + ".codex-plugin/plugin.json", Data: encodePrettyJSON(manifest), Perm: 0o644},
		{RelPath: prefix + ".mcp.json", Data: encodePrettyJSON(mcpDoc), Perm: 0o644},
		{RelPath: prefix + "hooks/hooks.json", Data: encodePrettyJSON(hooksDoc), Perm: 0o644},
		{RelPath: prefix + "scripts/run-gateway-hook", Data: m.renderHookWrapper(hookBin, dataDir), Perm: 0o755},
		{RelPath: prefix + "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
		{RelPath: prefix + "assets/icon.png", Data: mustReadAsset("mcp-gateway/assets/icon.png"), Perm: 0o644},
		{RelPath: prefix + "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}

func (m *Manager) renderCursorPlugin(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientCursor)
	manifest := map[string]any{
		"name":        m.slug,
		"displayName": m.appName,
		"version":     m.version,
		"description": fmt.Sprintf("Connects Cursor to %s with semantic tool discovery, rules, skills, and preToolUse workspace provenance signing.", m.appName),
		"author":      map[string]any{"name": "Naicheng Deng", "url": "https://github.com/DengNaichen"},
		"homepage":    "https://github.com/DengNaichen/mcp-gateway",
		"repository":  "https://github.com/DengNaichen/mcp-gateway",
		"license":     "GPL-3.0",
		"logo":        "assets/icon.png",
		"keywords":    []string{"mcp", "gateway", "tool-discovery", "scope", "coding-agent"},
		"category":    "developer-tools",
		"skills":      "./skills/",
		"rules":       "./rules/",
		"hooks":       "./hooks/hooks.json",
		"mcpServers":  "./mcp.json",
	}

	mcpDoc := map[string]any{
		"mcpServers": map[string]any{
			m.slug: map[string]any{
				"url": agentURL,
			},
		},
	}

	hooksDoc := map[string]any{
		"version": 1,
		"hooks": map[string]any{
			"preToolUse": []any{
				map[string]any{
					"matcher": workspace.CursorPreToolMatcher(),
					"command": `sh "${CURSOR_PLUGIN_ROOT}/scripts/run-gateway-hook" cursor`,
					"timeout": 5,
				},
			},
		},
	}

	files := []renderedFile{
		{RelPath: ".cursor-plugin/plugin.json", Data: encodePrettyJSON(manifest), Perm: 0o644},
		{RelPath: "mcp.json", Data: encodePrettyJSON(mcpDoc), Perm: 0o644},
		{RelPath: "hooks/hooks.json", Data: encodePrettyJSON(hooksDoc), Perm: 0o644},
		{RelPath: "rules/" + m.slug + ".mdc", Data: mustReadAsset("mcp-gateway/rules/mcp-gateway.mdc"), Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
		{RelPath: "scripts/run-gateway-hook", Data: m.renderHookWrapper(hookBin, dataDir), Perm: 0o755},
		{RelPath: "assets/icon.png", Data: mustReadAsset("mcp-gateway/assets/icon.png"), Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}

func (m *Manager) renderOpenCodePlugin(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientOpenCode)
	var extraArgs []string
	if dataDir != "" {
		extraArgs = []string{"--data", dataDir}
	}
	slugJSON, _ := json.Marshal(m.slug)
	urlJSON, _ := json.Marshal(agentURL)
	binJSON, _ := json.Marshal(hookBin)
	extraJSON, _ := json.Marshal(extraArgs)
	toolRegex := fmt.Sprintf(`/^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:%s)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/`, workspace.SlugRegex(m.slug))

	src := string(mustReadAsset("opencode/index.js"))
	src = strings.Replace(src, `const DEFAULT_SLUG = "mcp-gateway";`, `const DEFAULT_SLUG = `+string(slugJSON)+`;`, 1)
	src = strings.Replace(src, `const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=opencode";`, `const DEFAULT_MCP_URL = `+string(urlJSON)+`;`, 1)
	src = strings.Replace(src, `const DEFAULT_HOOK_BIN = "";`, `const DEFAULT_HOOK_BIN = `+string(binJSON)+`;`, 1)
	src = strings.Replace(src, `const DEFAULT_EXTRA_ARGS = [];`, `const DEFAULT_EXTRA_ARGS = `+string(extraJSON)+`;`, 1)
	src = strings.Replace(src, `const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;`, `const TOOL_RE = `+toolRegex+`;`, 1)

	files := []renderedFile{
		{RelPath: "plugins/" + m.slug + ".js", Data: []byte(src), Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}

func (m *Manager) renderPiPackage(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientPi)
	var extraArgs []string
	if dataDir != "" {
		extraArgs = []string{"--data", dataDir}
	}
	slugJSON, _ := json.Marshal(m.slug)
	urlJSON, _ := json.Marshal(agentURL)
	binJSON, _ := json.Marshal(hookBin)
	extraJSON, _ := json.Marshal(extraArgs)
	toolRegex := fmt.Sprintf(`/^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:%s)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/`, workspace.SlugRegex(m.slug))

	extSrc := string(mustReadAsset("pi/extensions/mcp-gateway.ts"))
	extSrc = strings.Replace(extSrc, `const DEFAULT_SLUG = "mcp-gateway";`, `const DEFAULT_SLUG = `+string(slugJSON)+`;`, 1)
	extSrc = strings.Replace(extSrc, `const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=pi";`, `const DEFAULT_MCP_URL = `+string(urlJSON)+`;`, 1)
	extSrc = strings.Replace(extSrc, `const DEFAULT_HOOK_BIN = "";`, `const DEFAULT_HOOK_BIN = `+string(binJSON)+`;`, 1)
	extSrc = strings.Replace(extSrc, `const DEFAULT_EXTRA_ARGS: string[] = [];`, `const DEFAULT_EXTRA_ARGS: string[] = `+string(extraJSON)+`;`, 1)
	extSrc = strings.Replace(extSrc, `const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;`, `const TOOL_RE = `+toolRegex+`;`, 1)

	pkgDoc := map[string]any{
		"name":        "@dengnaichen/pi-" + m.slug,
		"version":     m.version,
		"private":     true,
		"description": fmt.Sprintf("Official Pi package for %s: registers the gateway MCP server, signs workspace provenance tokens on tool_call, and bundles the gateway skill.", m.appName),
		"keywords":    []string{"pi-package", "mcp", m.slug},
		"license":     "GPL-3.0",
		"pi": map[string]any{
			"extensions": []string{"./extensions/" + m.slug + ".ts"},
			"skills":     []string{"./skills"},
		},
		"peerDependencies": map[string]any{
			"@earendil-works/pi-coding-agent": "*",
		},
	}

	files := []renderedFile{
		{RelPath: "package.json", Data: encodePrettyJSON(pkgDoc), Perm: 0o644},
		{RelPath: "extensions/" + m.slug + ".ts", Data: []byte(extSrc), Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("pi/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}

func (m *Manager) renderAntigravityPlugin(endpoint, hookBin, dataDir string) renderedBundle {
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientAntigravity)
	desc := fmt.Sprintf("Connects Google Antigravity (CLI and Desktop) to %s with semantic tool discovery, rules, skills, and PreToolUse workspace provenance signing.", m.appName)
	manifest := map[string]any{
		"name":        m.slug,
		"displayName": m.appName,
		"version":     m.version,
		"description": desc,
		"author":      map[string]any{"name": "Naicheng Deng", "url": "https://github.com/DengNaichen"},
		"homepage":    "https://github.com/DengNaichen/mcp-gateway",
		"repository":  "https://github.com/DengNaichen/mcp-gateway",
		"license":     "GPL-3.0",
		"logo":        "assets/icon.png",
		"keywords":    []string{"mcp", "gateway", "tool-discovery", "scope", "antigravity"},
	}

	mcpDoc := map[string]any{
		"mcpServers": map[string]any{
			m.slug: map[string]any{
				"url": agentURL,
			},
		},
	}

	hooksDoc := map[string]any{
		m.slug: map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": workspace.AntigravityPreToolMatcher(m.slug),
					"hooks": []any{
						map[string]any{
							"type":    "command",
							"command": `sh "./scripts/run-gateway-hook" antigravity`,
							"timeout": 5,
						},
					},
				},
			},
		},
	}

	files := []renderedFile{
		{RelPath: ".antigravity-plugin/plugin.json", Data: encodePrettyJSON(manifest), Perm: 0o644},
		{RelPath: "mcp_config.json", Data: encodePrettyJSON(mcpDoc), Perm: 0o644},
		{RelPath: "hooks.json", Data: encodePrettyJSON(hooksDoc), Perm: 0o644},
		{RelPath: "rules/AGENTS.md", Data: mustReadAsset("antigravity/rules/AGENTS.md"), Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: m.renderSkillMarkdown(), Perm: 0o644},
		{RelPath: "scripts/run-gateway-hook", Data: m.renderHookWrapper(hookBin, dataDir), Perm: 0o755},
		{RelPath: "assets/icon.png", Data: mustReadAsset("antigravity/assets/icon.png"), Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("antigravity/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}
}
