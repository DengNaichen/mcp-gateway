package clientplugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) cursorPluginDir() string {
	return filepath.Join(m.homeDir, ".cursor", "plugins", "local", m.slug)
}

func (m *Manager) openCodeConfigDir() string {
	xdg := strings.TrimSpace(m.getenv("XDG_CONFIG_HOME"))
	if xdg == "" {
		xdg = filepath.Join(m.homeDir, ".config")
	}
	return filepath.Join(xdg, "opencode")
}

func (m *Manager) observeCursor(endpoint string, legacy bool) Status {
	pluginDir := m.cursorPluginDir()
	hookBin, dataDir := m.resolveHookBinaryAndData()
	expected := m.renderCursorPlugin(endpoint, hookBin, dataDir)

	s := Status{
		ID:            ClientCursor,
		Name:          clientDisplayName(ClientCursor),
		Path:          pluginDir,
		State:         "未安装",
		TargetVersion: m.version,
		CLIAvailable:  true,
		HasLegacyMCP:  legacy,
		CanInstall:    true,
	}
	if legacy {
		s.State = "待升级"
		s.Detail = "当前仅配置了全局 MCP，升级为官方插件后可自动加载 Rules、Skill 与 Hook"
	}

	r, rErr := m.loadReceipt(ClientCursor)
	if rErr != nil {
		s.State = "记录异常"
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	info, err := os.Lstat(pluginDir)
	if errors.Is(err, os.ErrNotExist) {
		return s
	}
	if err != nil {
		s.State = "读取失败"
		s.Detail = err.Error()
		s.CanInstall = false
		return s
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.State = "配置异常"
		s.Detail = "目标路径不是普通插件目录"
		s.CanInstall = false
		return s
	}
	if r == nil {
		s.State = "已有插件"
		s.Detail = "目录已存在但无本应用写入记录，未覆盖"
		s.CanInstall = false
		return s
	}

	s.InstalledVersion = r.Version
	s.CanInstall = false
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, expected.RootFiles, r); err != nil {
		s.State = "配置已变更"
		s.Detail = err.Error()
		return s
	}
	s.CanRemove = true
	if !verifyTreeMatches(pluginDir, r.Files) || r.Digest != expected.Digest {
		s.State = "需要更新"
		s.CanUpdate = true
		s.Detail = "Gateway 地址或插件文件需要更新"
		return s
	}
	s.State = "已安装"
	return s
}

func (m *Manager) installCursor(endpoint string, update bool) error {
	pluginDir := m.cursorPluginDir()
	r, err := m.loadReceipt(ClientCursor)
	if err != nil {
		return err
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle := m.renderCursorPlugin(endpoint, hookBin, dataDir)
	if err := verifyOwnedFilesAbsentOrMatching(pluginDir, bundle.RootFiles, r); err != nil {
		return err
	}
	if r != nil {
		if r.Digest == bundle.Digest && verifyTreeMatches(pluginDir, r.Files) {
			return nil
		}
		if !update {
			return errors.New("当前 Cursor 官方插件需要更新，请执行更新插件")
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
		return errors.New("没有可卸载的 Cursor 官方插件记录")
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
	cfgDir := m.openCodeConfigDir()
	hookBin, dataDir := m.resolveHookBinaryAndData()
	expected := m.renderOpenCodePlugin(endpoint, hookBin, dataDir)
	pluginPath := filepath.Join(cfgDir, "plugins", m.slug+".js")

	s := Status{
		ID:            ClientOpenCode,
		Name:          clientDisplayName(ClientOpenCode),
		Path:          pluginPath,
		State:         "未安装",
		TargetVersion: m.version,
		CLIAvailable:  true,
		HasLegacyMCP:  legacy,
		CanInstall:    true,
	}
	if legacy {
		s.State = "待升级"
		s.Detail = "当前仅配置了全局 MCP，升级为 OpenCode v2 插件后可自动注册并签名"
	}

	r, rErr := m.loadReceipt(ClientOpenCode)
	if rErr != nil {
		s.State = "记录异常"
		s.Detail = rErr.Error()
		s.CanInstall = false
		return s
	}

	if err := verifyOpenCodeOwnership(cfgDir, expected.RootFiles, r); err != nil {
		s.State = "已有插件"
		if r != nil {
			s.State = "配置已变更"
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
		s.State = "需要更新"
		s.CanUpdate = true
		s.Detail = "Gateway 地址或 OpenCode v2 插件需要更新"
		return s
	}
	s.State = "已安装"
	return s
}

func (m *Manager) installOpenCode(endpoint string, update bool) error {
	cfgDir := m.openCodeConfigDir()
	r, err := m.loadReceipt(ClientOpenCode)
	if err != nil {
		return err
	}
	hookBin, dataDir := m.resolveHookBinaryAndData()
	bundle := m.renderOpenCodePlugin(endpoint, hookBin, dataDir)
	if err := verifyOpenCodeOwnership(cfgDir, bundle.RootFiles, r); err != nil {
		return err
	}
	if r != nil {
		if r.Digest == bundle.Digest && verifyTreeMatches(cfgDir, r.Files) {
			return nil
		}
		if !update {
			return errors.New("当前 OpenCode v2 插件需要更新，请执行更新插件")
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
		return errors.New("没有可卸载的 OpenCode v2 插件记录")
	}
	cfgDir := m.openCodeConfigDir()
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
				return fmt.Errorf("插件文件 %s 已被外部修改，未覆盖", p)
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
			return fmt.Errorf("目标文件 %s 已存在且不属于本应用，未覆盖", p)
		}
	}
	return nil
}
