// Package pluginassets embeds the canonical official coding-agent plugin assets.
package pluginassets

import "embed"

// Files embeds the shared plugin directory, OpenCode v2 plugin, Pi package, and Antigravity plugin templates.
//
//go:embed all:mcp-gateway all:opencode all:pi all:antigravity
var Files embed.FS
