package clientplugin

import (
	"errors"
	"os"
	"path/filepath"

	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/i18n"
)

func (m *Manager) cursorPluginDir() string {
	return filepath.Join(m.hostConfigDir(ClientCursor), "plugins", "local", m.slug)
}

func (m *Manager) observeCursor(endpoint string, legacy bool) Status {
	pluginDir := m.cursorPluginDir()
	hookBin, dataDir := m.resolveHookBinaryAndData()

	s := Status{
		ID:            ClientCursor,
		Name:          clientconfig.ClientDisplayName(ClientCursor),
		Path:          pluginDir,
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  true,
		HasLegacyMCP:  legacy,
		CanInstall:    true,
	}
	expected, renderErr := m.renderCursorPlugin(endpoint, hookBin, dataDir)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-fs-legacy")
	}

	r, rErr := m.loadReceipt(ClientCursor)
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
		s.Detail = i18n.T("client.detail-plugin-dir-invalid")
		s.CanInstall = false
		return s
	}
	if r == nil {
		s.State = i18n.T("client.state.existing-plugin")
		s.Detail = i18n.T("client.detail-dir-unmanaged")
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
	s.CanRemove = true
	if !verifyTreeMatches(pluginDir, r.Files) || r.Digest != expected.Digest {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = true
		s.Detail = i18n.T("client.detail-plugin-file-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installCursor(endpoint string, update bool) error {
	pluginDir := m.cursorPluginDir()
	r, err := m.loadReceipt(ClientCursor)
	if err != nil {
		return err
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle, err := m.renderCursorPlugin(endpoint, hookBin, dataDir)
	if err != nil {
		return err
	}
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, bundle.RootFiles, r); err != nil {
		return err
	}
	if r != nil {
		if r.Digest == bundle.Digest && verifyTreeMatches(pluginDir, r.Files) {
			return nil
		}
		if !update {
			return errors.New(i18n.T("client.err-cursor-needs-update"))
		}
	}

	filesMap, err := writeRenderedTree(pluginDir, bundle.RootFiles)
	if err != nil {
		return err
	}
	m.revokeLegacyMCPBestEffort(ClientCursor)
	return m.saveReceipt(pluginReceipt{
		Client:      ClientCursor,
		Version:     m.version,
		Digest:      bundle.Digest,
		Endpoint:    bundle.Endpoint,
		InstallRoot: pluginDir,
		Files:       filesMap,
	})
}

func (m *Manager) removeCursor() error {
	r, err := m.loadReceipt(ClientCursor)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-cursor-nothing-to-remove"))
	}
	pluginDir := m.cursorPluginDir()
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, nil, r); err != nil {
		return err
	}
	for rel := range r.Files {
		p := filepath.Join(pluginDir, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	pruneEmptyDirs(pluginDir, filepath.Dir(pluginDir))
	return m.deleteReceipt(ClientCursor)
}

func (m *Manager) observeOpenCode(endpoint string, legacy bool) Status {
	cfgDir := m.hostConfigDir(ClientOpenCode)
	hookBin, dataDir := m.resolveHookBinaryAndData()
	pluginPath := filepath.Join(cfgDir, "plugins", m.slug+".js")

	s := Status{
		ID:            ClientOpenCode,
		Name:          clientconfig.ClientDisplayName(ClientOpenCode),
		Path:          pluginPath,
		State:         i18n.T("client.state.not-installed"),
		TargetVersion: m.version,
		CLIAvailable:  true,
		HasLegacyMCP:  legacy,
		CanInstall:    true,
	}
	expected, renderErr := m.renderOpenCodePlugin(endpoint, hookBin, dataDir)
	if renderErr != nil {
		s.State = i18n.T("client.state.config-error")
		s.Detail = renderErr.Error()
		s.CanInstall = false
		return s
	}
	if legacy {
		s.State = i18n.T("client.state.upgradable")
		s.Detail = i18n.T("client.detail-opencode-legacy")
	}

	r, rErr := m.loadReceipt(ClientOpenCode)
	if rErr != nil {
		s.State = i18n.T("client.state.record-error")
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	if err := verifyOpenCodeOwnership(cfgDir, expected.RootFiles, r); err != nil {
		s.State = i18n.T("client.state.existing-plugin")
		if r != nil {
			s.State = i18n.T("client.state.config-changed")
		}
		s.Detail = err.Error()
		s.CanInstall = false
		return s
	}
	if r == nil {
		return s
	}

	s.InstalledVersion = r.Version
	s.CanInstall = false
	s.CanRemove = true
	if !verifyTreeMatches(cfgDir, r.Files) || r.Digest != expected.Digest {
		s.State = i18n.T("client.state.update-required")
		s.CanUpdate = true
		s.Detail = i18n.T("client.detail-opencode-update")
		return s
	}
	s.State = i18n.T("client.state.installed")
	return s
}

func (m *Manager) installOpenCode(endpoint string, update bool) error {
	cfgDir := m.hostConfigDir(ClientOpenCode)
	r, err := m.loadReceipt(ClientOpenCode)
	if err != nil {
		return err
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle, err := m.renderOpenCodePlugin(endpoint, hookBin, dataDir)
	if err != nil {
		return err
	}
	if err := verifyOpenCodeOwnership(cfgDir, bundle.RootFiles, r); err != nil {
		return err
	}
	if r != nil {
		if r.Digest == bundle.Digest && verifyTreeMatches(cfgDir, r.Files) {
			return nil
		}
		if !update {
			return errors.New(i18n.T("client.err-opencode-needs-update"))
		}
	}

	if err := os.MkdirAll(cfgDir, 0o755); err != nil { //nolint:gosec // G301: OpenCode config directory requires normal user traversal
		return err
	}
	filesMap := make(map[string]string, len(bundle.RootFiles))
	for _, f := range bundle.RootFiles {
		target := filepath.Join(cfgDir, filepath.FromSlash(f.RelPath))
		if err := atomicWriteFile(target, f.Data, f.Perm); err != nil {
			return err
		}
		filesMap[f.RelPath] = sha256Hex(f.Data)
	}

	m.revokeLegacyMCPBestEffort(ClientOpenCode)
	return m.saveReceipt(pluginReceipt{
		Client:      ClientOpenCode,
		Version:     m.version,
		Digest:      bundle.Digest,
		Endpoint:    bundle.Endpoint,
		InstallRoot: cfgDir,
		Files:       filesMap,
	})
}

func (m *Manager) removeOpenCode() error {
	r, err := m.loadReceipt(ClientOpenCode)
	if err != nil {
		return err
	}
	if r == nil {
		return errors.New(i18n.T("client.err-opencode-nothing-to-remove"))
	}
	cfgDir := m.hostConfigDir(ClientOpenCode)
	if err := verifyOpenCodeOwnership(cfgDir, nil, r); err != nil {
		return err
	}
	for rel := range r.Files {
		p := filepath.Join(cfgDir, filepath.FromSlash(rel))
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		pruneEmptyDirs(filepath.Dir(p), cfgDir)
	}
	return m.deleteReceipt(ClientOpenCode)
}

func verifyOpenCodeOwnership(cfgDir string, files []renderedFile, r *pluginReceipt) error {
	if r != nil {
		for rel, wantHash := range r.Files {
			p := filepath.Join(cfgDir, filepath.FromSlash(rel))
			raw, err := os.ReadFile(p) //nolint:gosec // G304: verified relative OpenCode config path
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			if sha256Hex(raw) != wantHash {
				return errors.New(i18n.T("client.err-plugin-file-modified", p))
			}
		}
	}
	for _, f := range files {
		if r != nil {
			if _, owned := r.Files[f.RelPath]; owned {
				continue
			}
		}
		p := filepath.Join(cfgDir, filepath.FromSlash(f.RelPath))
		if _, err := os.Lstat(p); err == nil {
			return errors.New(i18n.T("client.err-file-exists-other-app", p))
		}
	}
	return nil
}
