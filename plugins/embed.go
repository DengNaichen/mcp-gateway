// Package pluginassets embeds the canonical official coding-agent plugin assets.
package pluginassets

import "embed"

// Files embeds the shared plugin directory, OpenCode v2 plugin, and Pi package templates.
//
//go:embed all:mcp-gateway all:opencode all:pi
var Files embed.FS
