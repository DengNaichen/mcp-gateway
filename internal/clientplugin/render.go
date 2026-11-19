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
	if m.activeRelease != nil {
		hookBin = m.updates.hookPath(m.activeRelease.Version)
	}
	if hookBin == "" {
		hookBin = workspace.ResolveHookBinary(m.homeDir, m.dataDir)
	}
	if m.dataDir != "" {
		if absData, err := filepath.Abs(m.dataDir); err == nil {
			defData, defErr := workspace.DefaultDataDir()
			if defErr != nil || !workspace.SameFilePath(absData, defData) {
				dataDir = absData
			}
		}
	}
	return hookBin, dataDir
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

type templateReplacement struct {
	old string
	new string
	all bool
}

func renderTemplateAsset(rel string, replacements ...templateReplacement) ([]byte, error) {
	src := string(mustReadAsset(rel))
	for _, rep := range replacements {
		if !strings.Contains(src, rep.old) {
			return nil, fmt.Errorf("plugin template %s missing placeholder %q", rel, rep.old)
		}
		if rep.all {
			src = strings.ReplaceAll(src, rep.old, rep.new)
		} else {
			src = strings.Replace(src, rep.old, rep.new, 1)
		}
	}
	return []byte(src), nil
}

func (m *Manager) renderSkillMarkdown() ([]byte, error) {
	if m.slug == "mcp-gateway" {
		return mustReadAsset("mcp-gateway/skills/mcp-gateway/SKILL.md"), nil
	}
	// Replace frontmatter skill name and CLI references cleanly for Dev isolation.
	return renderTemplateAsset(
		"mcp-gateway/skills/mcp-gateway/SKILL.md",
		templateReplacement{old: "name: mcp-gateway\n", new: "name: " + m.slug + "\n"},
		templateReplacement{old: "gatewayctl ", new: m.slug + "ctl ", all: true},
	)
}

func (m *Manager) renderHookWrapper(hookBin, dataDir string) ([]byte, error) {
	return renderTemplateAsset(
		"mcp-gateway/scripts/run-gateway-hook",
		templateReplacement{old: `default_bin=""`, new: `default_bin=` + workspace.ShellQuote(hookBin)},
		templateReplacement{old: `default_slug="mcp-gateway"`, new: `default_slug=` + workspace.ShellQuote(m.slug)},
		templateReplacement{old: `default_data_dir=""`, new: `default_data_dir=` + workspace.ShellQuote(dataDir)},
	)
}

func (m *Manager) renderClaudeMarketplace(endpoint, hookBin, dataDir string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientClaude, endpoint, hookBin, dataDir, ""}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientClaude)
	prefix := "plugins/" + m.slug + "/"
	desc := fmt.Sprintf("Connects Claude Code to %s with semantic tool discovery, scoped invocation, and automatic workspace provenance signing.", m.appName)

	hookScript, err := m.renderHookWrapper(hookBin, dataDir)
	if err != nil {
		return renderedBundle{}, err
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}

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
		{RelPath: prefix + "scripts/run-gateway-hook", Data: hookScript, Perm: 0o755},
		{RelPath: prefix + "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
		{RelPath: prefix + "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}

func (m *Manager) renderCodexMarketplace(endpoint, hookBin, dataDir, installationID string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientCodex, endpoint, hookBin, dataDir, installationID}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientCodex)
	prefix := "plugins/" + m.slug + "/"
	desc := fmt.Sprintf("Connects Codex to %s with semantic tool discovery, scoped invocation, and automatic workspace provenance signing.", m.appName)

	hookScript, err := m.renderHookWrapper(hookBin, dataDir)
	if err != nil {
		return renderedBundle{}, err
	}
	if installationID != "" {
		hookScript = bytes.ReplaceAll(hookScript, []byte(`exec "$hook_bin" "$host"`), []byte(`exec "$hook_bin" "$host" --installation `+workspace.ShellQuote(installationID)))
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}

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
		{RelPath: prefix + "scripts/run-gateway-hook", Data: hookScript, Perm: 0o755},
		{RelPath: prefix + "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
		{RelPath: prefix + "assets/icon.png", Data: mustReadAsset("mcp-gateway/assets/icon.png"), Perm: 0o644},
		{RelPath: prefix + "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}

func (m *Manager) renderCursorPlugin(endpoint, hookBin, dataDir string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientCursor, endpoint, hookBin, dataDir, ""}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientCursor)
	hookScript, err := m.renderHookWrapper(hookBin, dataDir)
	if err != nil {
		return renderedBundle{}, err
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}
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
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
		{RelPath: "scripts/run-gateway-hook", Data: hookScript, Perm: 0o755},
		{RelPath: "assets/icon.png", Data: mustReadAsset("mcp-gateway/assets/icon.png"), Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("mcp-gateway/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}

func (m *Manager) renderOpenCodePlugin(endpoint, hookBin, dataDir string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientOpenCode, endpoint, hookBin, dataDir, ""}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientOpenCode)
	var extraArgs []string
	if dataDir != "" {
		extraArgs = []string{"--data", dataDir}
	}
	slugJSON, _ := json.Marshal(m.slug)
	urlJSON, _ := json.Marshal(agentURL)
	binJSON, _ := json.Marshal(hookBin)
	extraJSON, _ := json.Marshal(extraArgs)
	toolRegex := workspace.PluginToolRegexLiteral(m.slug)

	src, err := renderTemplateAsset(
		"opencode/index.js",
		templateReplacement{old: `const DEFAULT_SLUG = "mcp-gateway";`, new: `const DEFAULT_SLUG = ` + string(slugJSON) + `;`},
		templateReplacement{old: `const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=opencode";`, new: `const DEFAULT_MCP_URL = ` + string(urlJSON) + `;`},
		templateReplacement{old: `const DEFAULT_HOOK_BIN = "";`, new: `const DEFAULT_HOOK_BIN = ` + string(binJSON) + `;`},
		templateReplacement{old: `const DEFAULT_EXTRA_ARGS = [];`, new: `const DEFAULT_EXTRA_ARGS = ` + string(extraJSON) + `;`},
		templateReplacement{old: `const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;`, new: `const TOOL_RE = ` + toolRegex + `;`},
	)
	if err != nil {
		return renderedBundle{}, err
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}

	files := []renderedFile{
		{RelPath: "plugins/" + m.slug + ".js", Data: src, Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}

func (m *Manager) renderPiPackage(endpoint, hookBin, dataDir string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientPi, endpoint, hookBin, dataDir, ""}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientPi)
	var extraArgs []string
	if dataDir != "" {
		extraArgs = []string{"--data", dataDir}
	}
	slugJSON, _ := json.Marshal(m.slug)
	urlJSON, _ := json.Marshal(agentURL)
	binJSON, _ := json.Marshal(hookBin)
	extraJSON, _ := json.Marshal(extraArgs)
	toolRegex := workspace.PluginToolRegexLiteral(m.slug)

	extSrc, err := renderTemplateAsset(
		"pi/extensions/mcp-gateway.ts",
		templateReplacement{old: `const DEFAULT_SLUG = "mcp-gateway";`, new: `const DEFAULT_SLUG = ` + string(slugJSON) + `;`},
		templateReplacement{old: `const DEFAULT_MCP_URL = "http://127.0.0.1:8788/mcp?agent=pi";`, new: `const DEFAULT_MCP_URL = ` + string(urlJSON) + `;`},
		templateReplacement{old: `const DEFAULT_HOOK_BIN = "";`, new: `const DEFAULT_HOOK_BIN = ` + string(binJSON) + `;`},
		templateReplacement{old: `const DEFAULT_EXTRA_ARGS: string[] = [];`, new: `const DEFAULT_EXTRA_ARGS: string[] = ` + string(extraJSON) + `;`},
		templateReplacement{old: `const TOOL_RE = /^(?:mcp__(?:plugin_[A-Za-z0-9_-]+?_)?)?(?:mcp[-_]gateway)[-_]{1,2}(discover_tools|get_tool_schema|invoke_tool)$/;`, new: `const TOOL_RE = ` + toolRegex + `;`},
	)
	if err != nil {
		return renderedBundle{}, err
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}

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
		{RelPath: "extensions/" + m.slug + ".ts", Data: extSrc, Perm: 0o644},
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("pi/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}

func (m *Manager) renderAntigravityPlugin(endpoint, hookBin, dataDir string) (renderedBundle, error) {
	if m.activeRelease != nil {
		return renderReleasedBundle(m.activeRelease, releaseRender{ClientAntigravity, endpoint, hookBin, dataDir, ""}), nil
	}
	agentURL := clientconfig.ClientEndpoint(endpoint, ClientAntigravity)
	hookScript, err := m.renderHookWrapper(hookBin, dataDir)
	if err != nil {
		return renderedBundle{}, err
	}
	skillMD, err := m.renderSkillMarkdown()
	if err != nil {
		return renderedBundle{}, err
	}
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
		{RelPath: "skills/" + m.slug + "/SKILL.md", Data: skillMD, Perm: 0o644},
		{RelPath: "scripts/run-gateway-hook", Data: hookScript, Perm: 0o755},
		{RelPath: "assets/icon.png", Data: mustReadAsset("mcp-gateway/assets/icon.png"), Perm: 0o644},
		{RelPath: "README.md", Data: mustReadAsset("antigravity/README.md"), Perm: 0o644},
	}
	return renderedBundle{
		RootFiles: files,
		Digest:    hashRenderedFiles(files, agentURL, hookBin, dataDir, m.version),
		HookBin:   hookBin,
		Endpoint:  agentURL,
	}, nil
}
