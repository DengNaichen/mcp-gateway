package clientplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/i18n"
	"gatewaylab/internal/workspace"
)

func (m *Manager) claudeEnv() []string {
	return []string{
		"HOME=" + m.homeDir,
		"USERPROFILE=" + m.homeDir,
		"CLAUDE_CONFIG_DIR=" + m.hostConfigDir(ClientClaude),
	}
}

func (m *Manager) codexEnv() []string {
	return []string{
		"HOME=" + m.homeDir,
		"USERPROFILE=" + m.homeDir,
		"CODEX_HOME=" + m.hostConfigDir(ClientCodex),
	}
}

func (m *Manager) piEnv() []string {
	return []string{
		"HOME=" + m.homeDir,
		"USERPROFILE=" + m.homeDir,
		"PI_CODING_AGENT_DIR=" + m.hostConfigDir(ClientPi),
	}
}

func (m *Manager) pluginID() string {
	return m.slug + "@" + m.slug
}

func (m *Manager) observeClaude(endpoint string, legacy bool) Status {
	cfgDir := m.hostConfigDir(ClientClaude)
	hookBin, dataDir := m.resolveHookBinaryAndData()
	_, cliErr := m.runner.LookPath("claude")
	cliOK := cliErr == nil

	s := Status{
		ID:            ClientClaude,
		Name:          clientconfig.ClientDisplayName(ClientClaude),
		Path:          filepath.Join(cfgDir, "plugins", "installed_plugins.json"),
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  cliOK,
		HasLegacyMCP:  legacy,
		CanInstall:    cliOK,
	}
	expected, renderErr := m.renderClaudeMarketplace(endpoint, hookBin, dataDir)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-cli-legacy")
	}
	if !cliOK {
		s.Detail = i18n.T("client.detail-claude-cli-missing")
	}

	r, rErr := m.loadReceipt(ClientClaude)
	if rErr != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	inst, instErr := readClaudeInstalledPlugin(cfgDir, m.pluginID())
	if instErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = instErr.Error()
		s.CanInstall = false
		return s
	}
	enabled, _ := readClaudeEnabledFlag(cfgDir, m.pluginID())
	mpSource, mpKnown := readClaudeMarketplaceSource(cfgDir, m.slug)

	if inst == nil {
		if r != nil && mpKnown {
			s.Detail = i18n.T("client.detail-marketplace-registered")
		}
		return s
	}

	s.Path = inst.InstallPath
	s.InstalledVersion = inst.Version
	mktDir := filepath.Join(m.stateDir, "marketplaces", ClientClaude)
	if r == nil || !mpKnown || !sameCleanedPath(mpSource, mktDir) {
		s.State = i18n.T("client.state.existing-plugin")
		s.Detail = i18n.T("client.detail-claude-foreign-plugin")
		s.CanInstall = false
		return s
	}
	if !enabled {
		s.State = i18n.T("client.state.disabled")
		s.Detail = i18n.T("client.detail-claude-disabled")
		s.CanUpdate = cliOK
		s.CanRemove = cliOK
		s.CanInstall = false
		return s
	}

	s.CanInstall = false
	s.CanRemove = cliOK
	if r.Digest != expected.Digest || inst.Version != m.version {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = cliOK
		s.Detail = i18n.T("client.detail-cli-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installClaude(endpoint string, update bool) error {
	if _, err := m.runner.LookPath("claude"); err != nil {
		return fmt.Errorf(i18n.T("client.err-claude-install"), err)
	}
	cfgDir := m.hostConfigDir(ClientClaude)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return err
	}
	r, err := m.loadReceipt(ClientClaude)
	if err != nil {
		return err
	}
	inst, err := readClaudeInstalledPlugin(cfgDir, m.pluginID())
	if err != nil {
		return err
	}
	mktDir := filepath.Join(m.stateDir, "marketplaces", ClientClaude)
	mpSource, mpKnown := readClaudeMarketplaceSource(cfgDir, m.slug)
	if inst != nil && r == nil {
		return errors.New(i18n.T("client.err-claude-foreign-plugin"))
	}
	if mpKnown && !sameCleanedPath(mpSource, mktDir) && r == nil {
		return errors.New(i18n.T("client.err-claude-marketplace-exists", mpSource))
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle, err := m.renderClaudeMarketplace(endpoint, hookBin, dataDir)
	if err != nil {
		return err
	}
	if inst != nil && !update {
		if r != nil && r.Digest == bundle.Digest {
			return nil
		}
		return errors.New(i18n.T("client.err-plugin-needs-update"))
	}
	filesMap, err := writeRenderedTree(mktDir, bundle.RootFiles)
	if err != nil {
		return err
	}

	ctx := context.Background()
	env := m.claudeEnv()
	if inst != nil {
		_, _, _ = m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "uninstall", "--scope", "user", m.pluginID())
	}
	if mpKnown {
		_, _, _ = m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "marketplace", "remove", m.slug)
	}
	if _, _, err := m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "marketplace", "add", "--scope", "user", mktDir); err != nil {
		return err
	}
	if _, _, err := m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "install", "--scope", "user", m.pluginID()); err != nil {
		return err
	}

	m.revokeLegacyMCPBestEffort(ClientClaude)
	return m.saveReceipt(pluginReceipt{
		Client:      ClientClaude,
		Version:     m.version,
		Digest:      bundle.Digest,
		Endpoint:    bundle.Endpoint,
		InstallRoot: mktDir,
		Marketplace: m.slug,
		Files:       filesMap,
	})
}

func (m *Manager) removeClaude() error {
	r, err := m.loadReceipt(ClientClaude)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-claude-nothing-to-remove"))
	}
	if _, err := m.runner.LookPath("claude"); err != nil {
		return fmt.Errorf(i18n.T("client.err-claude-uninstall"), err)
	}
	cfgDir := m.hostConfigDir(ClientClaude)
	ctx := context.Background()
	env := m.claudeEnv()
	if inst, _ := readClaudeInstalledPlugin(cfgDir, m.pluginID()); inst != nil {
		if _, _, err := m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "uninstall", "--scope", "user", m.pluginID()); err != nil {
			return err
		}
	}
	if _, known := readClaudeMarketplaceSource(cfgDir, m.slug); known {
		if _, _, err := m.runner.Run(ctx, env, m.homeDir, "claude", "plugin", "marketplace", "remove", m.slug); err != nil {
			return err
		}
	}
	if r.InstallRoot != "" {
		_ = os.RemoveAll(r.InstallRoot)
	}
	return m.deleteReceipt(ClientClaude)
}

func (m *Manager) observeCodex(endpoint string, legacy bool) Status {
	cfgDir := m.hostConfigDir(ClientCodex)
	hookBin, dataDir := m.resolveHookBinaryAndData()
	_, cliErr := m.runner.LookPath("codex")
	cliOK := cliErr == nil

	s := Status{
		ID:            ClientCodex,
		Name:          clientconfig.ClientDisplayName(ClientCodex),
		Path:          filepath.Join(cfgDir, "config.toml"),
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  cliOK,
		HasLegacyMCP:  legacy,
		CanInstall:    cliOK,
		Warnings:      []string{i18n.T("client.warn-codex-trust-hook")},
	}
	r, rErr := m.loadReceipt(ClientCodex)
	installationID := ""
	if r != nil {
		installationID = r.InstallationID
	}
	if workspace.HookSucceeded(dataDir, ClientCodex, installationID) {
		s.Warnings = nil
	}
	expected, renderErr := m.renderCodexMarketplace(endpoint, hookBin, dataDir, installationID)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-cli-legacy")
	}
	if !cliOK {
		s.Detail = i18n.T("client.detail-codex-cli-missing")
	}

	if rErr != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	enabled, hasPlugin, mpSource, hasMP, parseErr := readCodexPluginState(cfgDir, m.slug, m.pluginID())
	if parseErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = parseErr.Error()
		s.CanInstall = false
		return s
	}
	cacheDir := filepath.Join(cfgDir, "plugins", "cache", m.slug, m.slug)
	cacheVer := findSingleVersionDir(cacheDir)

	if !hasPlugin && cacheVer == "" {
		return s
	}
	if cacheVer != "" {
		s.Path = filepath.Join(cacheDir, cacheVer)
		s.InstalledVersion = cacheVer
	}

	mktDir := filepath.Join(m.stateDir, "marketplaces", ClientCodex)
	if r == nil || !hasMP || !sameCleanedPath(mpSource, mktDir) {
		s.State = i18n.T("client.state.existing-plugin")
		s.Detail = i18n.T("client.detail-codex-foreign-plugin")
		s.CanInstall = false
		return s
	}
	if !enabled {
		s.State = i18n.T("client.state.disabled")
		s.Detail = i18n.T("client.detail-codex-disabled")
		s.CanUpdate = cliOK
		s.CanRemove = cliOK
		s.CanInstall = false
		return s
	}

	s.CanInstall = false
	s.CanRemove = cliOK
	if r.InstallationID == "" || r.Digest != expected.Digest || (cacheVer != "" && cacheVer != m.version) {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = cliOK
		s.Detail = i18n.T("client.detail-cli-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installCodex(endpoint string, update bool) error {
	if _, err := m.runner.LookPath("codex"); err != nil {
		return fmt.Errorf(i18n.T("client.err-codex-install"), err)
	}
	cfgDir := m.hostConfigDir(ClientCodex)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return err
	}
	r, err := m.loadReceipt(ClientCodex)
	if err != nil {
		return err
	}
	_, hasPlugin, mpSource, hasMP, err := readCodexPluginState(cfgDir, m.slug, m.pluginID())
	if err != nil {
		return err
	}
	mktDir := filepath.Join(m.stateDir, "marketplaces", ClientCodex)
	if hasPlugin && r == nil {
		return errors.New(i18n.T("client.err-codex-foreign-plugin"))
	}
	if hasMP && !sameCleanedPath(mpSource, mktDir) && r == nil {
		return errors.New(i18n.T("client.err-codex-marketplace-exists", mpSource))
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	installationID, err := workspace.NewJTI()
	if err != nil {
		return err
	}
	bundle, err := m.renderCodexMarketplace(endpoint, hookBin, dataDir, installationID)
	if err != nil {
		return err
	}
	if hasPlugin && !update {
		if r != nil && r.Digest == bundle.Digest {
			return nil
		}
		return errors.New(i18n.T("client.err-plugin-needs-update"))
	}
	filesMap, err := writeRenderedTree(mktDir, bundle.RootFiles)
	if err != nil {
		return err
	}

	// Revoke any managed [mcp_servers.<slug>] entry before `codex plugin`
	// rewrites config.toml, preserving clientconfig's exact block check.
	m.revokeLegacyMCPBestEffort(ClientCodex)

	ctx := context.Background()
	env := m.codexEnv()
	if hasPlugin {
		_, _, _ = m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "remove", m.pluginID(), "--json")
	}
	if hasMP {
		_, _, _ = m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "marketplace", "remove", m.slug, "--json")
	}
	if _, _, err := m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "marketplace", "add", mktDir, "--json"); err != nil {
		return err
	}
	if _, _, err := m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "add", m.pluginID(), "--json"); err != nil {
		return err
	}

	return m.saveReceipt(pluginReceipt{
		InstallationID: installationID,
		Client:         ClientCodex,
		Version:        m.version,
		Digest:         bundle.Digest,
		Endpoint:       bundle.Endpoint,
		InstallRoot:    mktDir,
		Marketplace:    m.slug,
		Files:          filesMap,
	})
}

func (m *Manager) removeCodex() error {
	r, err := m.loadReceipt(ClientCodex)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-codex-nothing-to-remove"))
	}
	if _, err := m.runner.LookPath("codex"); err != nil {
		return fmt.Errorf(i18n.T("client.err-codex-uninstall"), err)
	}
	cfgDir := m.hostConfigDir(ClientCodex)
	_, hasPlugin, _, hasMP, err := readCodexPluginState(cfgDir, m.slug, m.pluginID())
	if err != nil {
		return err
	}
	ctx := context.Background()
	env := m.codexEnv()
	if hasPlugin {
		if _, _, err := m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "remove", m.pluginID(), "--json"); err != nil {
			return err
		}
	}
	if hasMP {
		if _, _, err := m.runner.Run(ctx, env, m.homeDir, "codex", "plugin", "marketplace", "remove", m.slug, "--json"); err != nil {
			return err
		}
	}
	if r.InstallRoot != "" {
		_ = os.RemoveAll(r.InstallRoot)
	}
	return m.deleteReceipt(ClientCodex)
}

func (m *Manager) observePi(endpoint string, legacy bool) Status {
	cfgDir := m.hostConfigDir(ClientPi)
	pkgDir := filepath.Join(m.stateDir, "pi", m.slug)
	hookBin, dataDir := m.resolveHookBinaryAndData()
	_, cliErr := m.runner.LookPath("pi")
	cliOK := cliErr == nil

	s := Status{
		ID:            ClientPi,
		Name:          clientconfig.ClientDisplayName(ClientPi),
		Path:          filepath.Join(cfgDir, "settings.json"),
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  cliOK,
		HasLegacyMCP:  legacy,
		CanInstall:    cliOK,
	}
	expected, renderErr := m.renderPiPackage(endpoint, hookBin, dataDir)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-pi-legacy")
	}
	if !cliOK {
		s.Detail = i18n.T("client.detail-pi-cli-missing")
	}

	r, rErr := m.loadReceipt(ClientPi)
	if rErr != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	matchedEntry, err := findPiPackageEntry(cfgDir, pkgDir)
	if err != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = err.Error()
		s.CanInstall = false
		return s
	}
	if matchedEntry == "" {
		return s
	}

	s.Path = pkgDir
	if r == nil {
		s.State = i18n.T("client.state.existing-plugin")
		s.Detail = i18n.T("client.detail-pi-same-path")
		s.CanInstall = false
		return s
	}
	s.InstalledVersion = r.Version
	s.CanInstall = false
	s.CanRemove = cliOK
	if !verifyTreeMatches(pkgDir, r.Files) || r.Digest != expected.Digest {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = cliOK
		s.Detail = i18n.T("client.detail-pi-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installPi(endpoint string, update bool) error {
	if _, err := m.runner.LookPath("pi"); err != nil {
		return fmt.Errorf(i18n.T("client.err-pi-install"), err)
	}
	cfgDir := m.hostConfigDir(ClientPi)
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		return err
	}
	r, err := m.loadReceipt(ClientPi)
	if err != nil {
		return err
	}
	pkgDir := filepath.Join(m.stateDir, "pi", m.slug)
	matchedEntry, err := findPiPackageEntry(cfgDir, pkgDir)
	if err != nil {
		return err
	}
	if matchedEntry != "" && r == nil {
		return errors.New(i18n.T("client.err-pi-path-loaded"))
	}

	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle, err := m.renderPiPackage(endpoint, hookBin, dataDir)
	if err != nil {
		return err
	}
	if matchedEntry != "" && !update {
		if r != nil && r.Digest == bundle.Digest && verifyTreeMatches(pkgDir, r.Files) {
			return nil
		}
		return errors.New(i18n.T("client.err-pi-needs-update"))
	}

	filesMap, err := writeRenderedTree(pkgDir, bundle.RootFiles)
	if err != nil {
		return err
	}
	ctx := context.Background()
	env := m.piEnv()
	if _, _, err := m.runner.Run(ctx, env, cfgDir, "pi", "install", "--no-approve", pkgDir); err != nil {
		return err
	}

	m.revokeLegacyMCPBestEffort(ClientPi)
	return m.saveReceipt(pluginReceipt{
		Client:        ClientPi,
		Version:       m.version,
		Digest:        bundle.Digest,
		Endpoint:      bundle.Endpoint,
		InstallRoot:   pkgDir,
		PackageSource: pkgDir,
		Files:         filesMap,
	})
}

func (m *Manager) removePi() error {
	r, err := m.loadReceipt(ClientPi)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-pi-nothing-to-remove"))
	}
	if _, err := m.runner.LookPath("pi"); err != nil {
		return fmt.Errorf(i18n.T("client.err-pi-uninstall"), err)
	}
	cfgDir := m.hostConfigDir(ClientPi)
	pkgDir := filepath.Join(m.stateDir, "pi", m.slug)
	if r.InstallRoot != "" {
		pkgDir = r.InstallRoot
	}
	matchedEntry, err := findPiPackageEntry(cfgDir, pkgDir)
	if err != nil {
		return err
	}
	if matchedEntry != "" {
		if _, _, err := m.runner.Run(context.Background(), m.piEnv(), cfgDir, "pi", "remove", "--no-approve", pkgDir); err != nil {
			return err
		}
	}
	if pkgDir != "" {
		_ = os.RemoveAll(pkgDir)
	}
	return m.deleteReceipt(ClientPi)
}

type claudeInstalledRecord struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
}

func readClaudeInstalledPlugin(cfgDir, pluginID string) (*claudeInstalledRecord, error) {
	b, err := readOptionalFile(filepath.Join(cfgDir, "plugins", "installed_plugins.json"))
	if err != nil || len(b) == 0 {
		return nil, err
	}
	var doc struct {
		Plugins map[string][]claudeInstalledRecord `json:"plugins"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf(i18n.T("client.err-installed-plugins-parse"), err)
	}
	entries := doc.Plugins[pluginID]
	if len(entries) == 0 {
		return nil, nil
	}
	for i := range entries {
		if entries[i].Scope == "user" {
			return &entries[i], nil
		}
	}
	return &entries[0], nil
}

func readClaudeEnabledFlag(cfgDir, pluginID string) (bool, bool) {
	b, err := readOptionalFile(filepath.Join(cfgDir, "settings.json"))
	if err != nil || len(b) == 0 {
		return false, false
	}
	var doc struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(b, &doc); err != nil || doc.EnabledPlugins == nil {
		return false, false
	}
	val, ok := doc.EnabledPlugins[pluginID]
	return val, ok
}

func readClaudeMarketplaceSource(cfgDir, name string) (string, bool) {
	b, err := readOptionalFile(filepath.Join(cfgDir, "plugins", "known_marketplaces.json"))
	if err != nil || len(b) == 0 {
		return "", false
	}
	var doc map[string]struct {
		Source struct {
			Source string `json:"source"`
			Path   string `json:"path"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", false
	}
	entry, ok := doc[name]
	if !ok {
		return "", false
	}
	if entry.Source.Path != "" {
		return entry.Source.Path, true
	}
	return entry.InstallLocation, true
}

func readCodexPluginState(cfgDir, marketplaceName, pluginID string) (enabled, hasPlugin bool, mpSource string, hasMP bool, err error) {
	b, err := readOptionalFile(filepath.Join(cfgDir, "config.toml"))
	if err != nil || len(b) == 0 {
		return false, false, "", false, err
	}
	var doc struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
		Marketplaces map[string]struct {
			Source string `toml:"source"`
		} `toml:"marketplaces"`
	}
	if err := toml.Unmarshal(b, &doc); err != nil {
		return false, false, "", false, fmt.Errorf(i18n.T("client.err-codex-config-parse"), err)
	}
	if p, ok := doc.Plugins[pluginID]; ok {
		hasPlugin = true
		enabled = p.Enabled
	}
	if mp, ok := doc.Marketplaces[marketplaceName]; ok {
		hasMP = true
		mpSource = mp.Source
	}
	return enabled, hasPlugin, mpSource, hasMP, nil
}

func findSingleVersionDir(cacheDir string) string {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return e.Name()
		}
	}
	return ""
}

func findPiPackageEntry(cfgDir, targetPkgDir string) (string, error) {
	b, err := readOptionalFile(filepath.Join(cfgDir, "settings.json"))
	if err != nil || len(b) == 0 {
		return "", err
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf(i18n.T("client.err-pi-settings-parse"), err)
	}
	rawPkgs, ok := doc["packages"].([]any)
	if !ok {
		return "", nil
	}
	for _, item := range rawPkgs {
		var src string
		switch v := item.(type) {
		case string:
			src = v
		case map[string]any:
			if s, ok := v["source"].(string); ok {
				src = s
			}
		}
		src = strings.TrimSpace(src)
		if src == "" {
			continue
		}
		if strings.HasPrefix(src, "npm:") || strings.HasPrefix(src, "git:") || strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") || strings.HasPrefix(src, "ssh://") {
			continue
		}
		candidate := src
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cfgDir, candidate)
		}
		if sameCleanedPath(candidate, targetPkgDir) {
			return src, nil
		}
	}
	return "", nil
}
