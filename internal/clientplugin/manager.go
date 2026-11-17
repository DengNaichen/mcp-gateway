// Package clientplugin installs, updates and removes the official MCP Gateway
// plugins of the MCP clients (Claude Code, Codex, Cursor, OpenCode, Pi, Antigravity)
// on the user's own machine. Like clientconfig it is a local-machine capability
// used by the desktop app and gatewayctl, never part of the server API.
package clientplugin

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/workspace"
)

func validateEndpoint(endpoint string) (string, error) {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		trimmed = "http://" + buildinfo.DefaultAddr() + "/mcp"
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("无效的 MCP HTTP 地址")
	}
	return trimmed, nil
}

func (m *Manager) legacyStatusMap(endpoint string) map[string]clientconfig.Status {
	out := make(map[string]clientconfig.Status, len(clientconfig.ClientIDs()))
	if m.clientConfigs == nil {
		return out
	}
	for _, row := range m.clientConfigs.Statuses(endpoint) {
		out[row.ID] = row
	}
	return out
}

func (m *Manager) revokeLegacyMCPBestEffort(id string) {
	if m.clientConfigs == nil {
		return
	}
	_ = m.clientConfigs.Revoke(id)
}

func (m *Manager) syncHookIfPossible() {
	if m.homeDir == "" || m.binPath != "" {
		return
	}
	if m.dataDir != "" {
		absData, err1 := filepath.Abs(m.dataDir)
		absHome, err2 := filepath.Abs(m.homeDir)
		if err1 == nil && err2 == nil && workspace.HasPathPrefix(absData, absHome) {
			_, _ = workspace.SyncHookBinary(m.homeDir)
		}
		return
	}
	if workspace.FindBuiltHookBinary() != "" {
		_, _ = workspace.SyncHookBinary(m.homeDir)
	}
}

// Statuses inspects all five clients and returns their current official plugin states.
func (m *Manager) Statuses(endpoint string) []Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	if strings.TrimSpace(endpoint) == "" {
		endpoint = "http://" + buildinfo.DefaultAddr() + "/mcp"
	}
	legacy := m.legacyStatusMap(endpoint)
	hasLegacy := func(id string) bool {
		row, ok := legacy[id]
		return ok && (row.CanRevoke || row.CanUpdate || row.State == "已有配置")
	}

	ids := clientconfig.ClientIDs()
	out := make([]Status, 0, len(ids))
	for _, id := range ids {
		switch id {
		case ClientCodex:
			out = append(out, m.observeCodex(endpoint, hasLegacy(id)))
		case ClientClaude:
			out = append(out, m.observeClaude(endpoint, hasLegacy(id)))
		case ClientCursor:
			out = append(out, m.observeCursor(endpoint, hasLegacy(id)))
		case ClientOpenCode:
			out = append(out, m.observeOpenCode(endpoint, hasLegacy(id)))
		case ClientPi:
			out = append(out, m.observePi(endpoint, hasLegacy(id)))
		case ClientAntigravity:
			out = append(out, m.observeAntigravity(endpoint, hasLegacy(id)))
		}
	}
	return out
}

// Install installs the official plugin for client id targeting endpoint.
func (m *Manager) Install(id, endpoint string) error {
	return m.apply(id, endpoint, false)
}

// Update updates an existing managed official plugin for client id.
func (m *Manager) Update(id, endpoint string) error {
	return m.apply(id, endpoint, true)
}

func (m *Manager) apply(id, endpoint string, update bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := clientconfig.LookupClient(id); !ok {
		return errClientUnknown
	}
	validURL, err := validateEndpoint(endpoint)
	if err != nil {
		return err
	}
	m.syncHookIfPossible()

	switch id {
	case ClientCodex:
		return m.installCodex(validURL, update)
	case ClientClaude:
		return m.installClaude(validURL, update)
	case ClientCursor:
		return m.installCursor(validURL, update)
	case ClientOpenCode:
		return m.installOpenCode(validURL, update)
	case ClientPi:
		return m.installPi(validURL, update)
	case ClientAntigravity:
		return m.installAntigravity(validURL, update)
	default:
		return errClientUnknown
	}
}

// Remove uninstalls the managed official plugin for client id.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch id {
	case ClientCodex:
		return m.removeCodex()
	case ClientClaude:
		return m.removeClaude()
	case ClientCursor:
		return m.removeCursor()
	case ClientOpenCode:
		return m.removeOpenCode()
	case ClientPi:
		return m.removePi()
	case ClientAntigravity:
		return m.removeAntigravity()
	default:
		return errClientUnknown
	}
}

// VerificationPrompt builds a Roster-style copyable verification prompt for the
// target client so users can confirm MCP connectivity, skills, and scope hooks.
func (m *Manager) VerificationPrompt(id, endpoint string) string {
	agentURL := clientconfig.ClientEndpoint(endpoint, id)
	return fmt.Sprintf(
		"Verify the %s (%s) official plugin connection at %s:\n"+
			"1. Confirm the `%s` MCP server is connected and its plugin/skill instructions are loaded.\n"+
			"2. Call `discover_tools` with `{\"intent\": \"list available gateway capabilities\", \"summary_only\": true}`.\n"+
			"3. Report the discovered tool IDs and whether workspace scope signing succeeded, or stop and report any exact error (`scope_required`, auth, or connection failure).",
		m.appName,
		clientconfig.ClientDisplayName(id),
		agentURL,
		m.slug,
	)
}
