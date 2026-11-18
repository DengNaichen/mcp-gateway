package clientplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/i18n"
)

func (m *Manager) agyPluginDir() string {
	return filepath.Join(m.hostConfigDir(ClientAntigravity), "plugins", m.slug)
}

func (m *Manager) agyEnv() []string {
	return []string{
		"HOME=" + m.homeDir,
		"USERPROFILE=" + m.homeDir,
	}
}

func (m *Manager) observeAntigravity(endpoint string, legacy bool) Status {
	cfgDir := m.hostConfigDir(ClientAntigravity)
	pluginDir := m.agyPluginDir()
	hookBin, dataDir := m.resolveHookBinaryAndData()
	_, cliErr := m.runner.LookPath("agy")
	cliOK := cliErr == nil

	s := Status{
		ID:            ClientAntigravity,
		Name:          clientconfig.ClientDisplayName(ClientAntigravity),
		Path:          pluginDir,
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  cliOK,
		HasLegacyMCP:  legacy,
		CanInstall:    true,
	}
	expected, renderErr := m.renderAntigravityPlugin(endpoint, hookBin, dataDir)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-antigravity-legacy")
	}
	if hasLegacyAntigravityIDEMCP(m.homeDir, m.slug) {
		s.Warnings = append(s.Warnings, i18n.T("client.warn-antigravity-legacy-path"))
	}

	r, rErr := m.loadReceipt(ClientAntigravity)
	if rErr != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	info, err := os.Lstat(pluginDir)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		s.State = i18n.T("client.state.read-failed")
		s.Detail = err.Error()
		s.CanInstall = false
		return s
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.State = i18n.T("client.state.config-error")
		s.Detail = i18n.T("client.detail-antigravity-dir-invalid")
		s.CanInstall = false
		return s
	}
	if r == nil {
		s.State = i18n.T("client.state.existing-plugin")
		s.Detail = i18n.T("client.detail-antigravity-foreign-plugin")
		s.CanInstall = false
		return s
	}

	s.InstalledVersion = r.Version
	s.CanInstall = false
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, expected.RootFiles, r); err != nil {
		s.State = i18n.T("client.state.config-changed")
		s.Detail = err.Error()
		return s
	}

	enabled, err := readAntigravityPluginEnabled(cfgDir, m.slug)
	if err != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = err.Error()
		return s
	}
	s.CanRemove = true
	if !enabled {
		s.State = i18n.T("client.state.disabled")
		s.Detail = i18n.T("client.detail-antigravity-disabled")
		s.CanUpdate = true
		return s
	}
	if !verifyTreeMatches(pluginDir, r.Files) || r.Digest != expected.Digest || r.Version != m.version {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = true
		s.Detail = i18n.T("client.detail-antigravity-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installAntigravity(endpoint string, update bool) error {
	cfgDir := m.hostConfigDir(ClientAntigravity)
	pluginDir := m.agyPluginDir()
	r, err := m.loadReceipt(ClientAntigravity)
	if err != nil {
		return err
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle, err := m.renderAntigravityPlugin(endpoint, hookBin, dataDir)
	if err != nil {
		return err
	}
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, bundle.RootFiles, r); err != nil {
		return err
	}

	enabled, err := readAntigravityPluginEnabled(cfgDir, m.slug)
	if err != nil {
		return err
	}
	if r != nil {
		if r.Digest == bundle.Digest && verifyTreeMatches(pluginDir, r.Files) && enabled {
			return nil
		}
		if !update {
			return errors.New(i18n.T("client.err-antigravity-needs-update"))
		}
	}

	var filesMap map[string]string
	if _, cliErr := m.runner.LookPath("agy"); cliErr == nil {
		stageDir := filepath.Join(m.stateDir, "staging", ClientAntigravity, m.slug)
		_ = os.RemoveAll(stageDir)
		if _, err := writeRenderedTree(stageDir, bundle.RootFiles); err != nil {
			return err
		}
		ctx := context.Background()
		env := m.agyEnv()
		if _, err := os.Stat(pluginDir); err == nil {
			_, _, _ = m.runner.Run(ctx, env, m.homeDir, "agy", "plugin", "uninstall", m.slug)
			_ = os.RemoveAll(pluginDir)
		}
		if _, _, err := m.runner.Run(ctx, env, m.homeDir, "agy", "plugin", "install", stageDir); err != nil {
			return err
		}
		if !enabled {
			if _, _, err := m.runner.Run(ctx, env, m.homeDir, "agy", "plugin", "enable", m.slug); err != nil {
				return err
			}
		}
		filesMap, err = collectRenderedFileHashes(pluginDir, bundle.RootFiles)
		if err != nil {
			return err
		}
	} else {
		filesMap, err = writeRenderedTree(pluginDir, bundle.RootFiles)
		if err != nil {
			return err
		}
	}

	m.revokeLegacyMCPBestEffort(ClientAntigravity)
	return m.saveReceipt(pluginReceipt{
		Client:      ClientAntigravity,
		Version:     m.version,
		Digest:      bundle.Digest,
		Endpoint:    bundle.Endpoint,
		InstallRoot: pluginDir,
		Files:       filesMap,
	})
}

func (m *Manager) removeAntigravity() error {
	r, err := m.loadReceipt(ClientAntigravity)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-antigravity-nothing-to-remove"))
	}
	pluginDir := m.agyPluginDir()
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, nil, r); err != nil {
		return err
	}

	if _, cliErr := m.runner.LookPath("agy"); cliErr == nil {
		if _, err := os.Lstat(pluginDir); err == nil {
			if _, _, err := m.runner.Run(context.Background(), m.agyEnv(), m.homeDir, "agy", "plugin", "uninstall", m.slug); err != nil {
				return err
			}
		}
	}
	for rel := range r.Files {
		p := filepath.Join(pluginDir, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	pruneEmptyDirs(pluginDir, filepath.Dir(pluginDir))
	return m.deleteReceipt(ClientAntigravity)
}

func readAntigravityPluginEnabled(cfgDir, slug string) (bool, error) {
	cfgPath := filepath.Join(cfgDir, "config.json")
	b, err := os.ReadFile(cfgPath) //nolint:gosec // G304: local Antigravity config path
	if errors.Is(err, os.ErrNotExist) || len(strings.TrimSpace(string(b))) == 0 {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	var doc struct {
		Plugins map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return false, fmt.Errorf(i18n.T("client.err-antigravity-config-parse"), err)
	}
	if entry, ok := doc.Plugins[slug]; ok && entry.Enabled != nil {
		return *entry.Enabled, nil
	}
	return true, nil
}

func hasLegacyAntigravityIDEMCP(homeDir, slug string) bool {
	if strings.TrimSpace(homeDir) == "" {
		return false
	}
	b, err := os.ReadFile(filepath.Join(homeDir, ".gemini", "antigravity", "mcp_config.json")) //nolint:gosec // G304: local Antigravity legacy config path
	if err != nil || len(strings.TrimSpace(string(b))) == 0 {
		return false
	}
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return false
	}
	_, exists := doc.MCPServers[slug]
	return exists
}

func collectRenderedFileHashes(root string, files []renderedFile) (map[string]string, error) {
	out := make(map[string]string, len(files))
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.RelPath))
		raw, err := os.ReadFile(p) //nolint:gosec // G304: verified relative plugin file path
		if err != nil {
			return nil, fmt.Errorf(i18n.T("client.err-verify-installed-file"), f.RelPath, err)
		}
		out[f.RelPath] = sha256Hex(raw)
	}
	return out, nil
}
