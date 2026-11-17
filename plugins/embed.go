// Package pluginassets embeds the canonical official coding-agent plugin assets
// read at runtime by internal/clientplugin.
package pluginassets

import "embed"

// Files embeds only the runtime templates and assets rendered by clientplugin.
//
//go:embed mcp-gateway/README.md mcp-gateway/assets/icon.png mcp-gateway/rules/mcp-gateway.mdc mcp-gateway/scripts/run-gateway-hook mcp-gateway/skills/mcp-gateway/SKILL.md opencode/index.js pi/extensions/mcp-gateway.ts pi/README.md antigravity/README.md antigravity/rules/AGENTS.md
var Files embed.FS
