package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
)

func TestClientPageAndLocalActions(t *testing.T) {
	root := t.TempDir()
	m := clientconfig.New(root, filepath.Join(root, "receipts"), func(string) string { return "" })
	u := &DesktopUI{ClientConfigs: m, Runtime: &gatewayRuntime{MCPURL: "http://localhost:8788/mcp"}}
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester := ui.NewTester(u.renderClients, 1200, 1500)
	for _, name := range []string{"Codex CLI", "Claude Code", "Cursor", "OpenCode v2", "Pi"} {
		if !tester.HasText(name) {
			t.Fatal("missing client", name)
		}
	}
	for _, row := range u.ClientStatuses {
		if tester.HasText(row.Path) {
			t.Fatal("raw config path rendered", row.ID)
		}
		if _, ok := tester.Access("打开 " + row.Name + " 配置位置"); !ok {
			t.Fatal("missing accessible path action", row.ID)
		}
	}
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		if err := u.changeClientConfig(id, "write", u.clientEndpoint()); err != nil {
			t.Fatal(err)
		}
	}
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester.Frame()
	for _, row := range u.ClientStatuses {
		if row.State != "已配置" || !row.CanRevoke {
			t.Fatalf("bad UI state: %+v", row)
		}
	}
	if !tester.HasText("✓ 已配置") {
		t.Fatal("missing configured status")
	}
	if tester.HasText("撤回配置") {
		t.Fatal("revoke should be in the overflow menu")
	}
	if err := tester.Click("Codex CLI 更多操作"); err != nil {
		t.Fatal(err)
	}
	if len(tester.Menu()) == 0 {
		t.Fatal("overflow menu did not open")
	}
	tester.CloseMenu()
	u.Runtime.MCPURL = "http://localhost:9898/mcp"
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester.Frame()
	if !tester.HasText("更新配置") {
		t.Fatal("missing update action")
	}
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		if err := u.changeClientConfig(id, "update", u.clientEndpoint()); err != nil {
			t.Fatal(err)
		}
	}
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester.Frame()
	if tester.HasText("更新配置") {
		t.Fatal("update action still visible after update")
	}
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		if err := u.changeClientConfig(id, "revoke", u.clientEndpoint()); err != nil {
			t.Fatal(err)
		}
	}
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester.Frame()
	if !tester.HasText("写入配置") {
		t.Fatal("missing removed status")
	}
}

func TestConfigLocationFallback(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, want string
		file       bool
	}{
		{file, file, true}, {root, root, false}, {filepath.Join(root, "missing", "config.json"), root, false},
	} {
		got, isFile, err := nearestConfigLocation(tc.path)
		if err != nil || got != tc.want || isFile != tc.file {
			t.Fatalf("%s: %s %v %v", tc.path, got, isFile, err)
		}
	}
}

type desktopMockPluginRunner struct {
	home     string
	stateDir string
	calls    []string
}

func (r *desktopMockPluginRunner) LookPath(binary string) (string, error) {
	switch binary {
	case "claude", "codex", "pi":
		return "/mock/bin/" + binary, nil
	default:
		return "", errors.New("not found")
	}
}

func (r *desktopMockPluginRunner) Run(_ context.Context, _ []string, _ string, binary string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, binary+" "+strings.Join(args, " "))
	slug := buildinfo.Current().Slug
	ver := buildinfo.Current().Version
	pluginID := slug + "@" + slug

	switch binary {
	case "claude":
		return r.runClaude(slug, ver, pluginID, args)
	case "codex":
		return r.runCodex(slug, ver, pluginID, args)
	case "pi":
		return r.runPi(args)
	default:
		return nil, nil, errors.New("unexpected binary")
	}
}

func (r *desktopMockPluginRunner) runClaude(slug, ver, pluginID string, args []string) ([]byte, []byte, error) {
	pluginsDir := filepath.Join(r.home, ".claude", "plugins")
	_ = os.MkdirAll(pluginsDir, 0o755)
	mktPath := filepath.Join(r.stateDir, "marketplaces", clientplugin.ClientClaude)
	switch {
	case len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add":
		kmPath := filepath.Join(pluginsDir, "known_marketplaces.json")
		km := map[string]any{slug: map[string]any{"source": map[string]any{"source": "directory", "path": mktPath}}}
		b, _ := json.Marshal(km)
		return nil, nil, os.WriteFile(kmPath, b, 0o600)
	case len(args) >= 3 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "remove":
		return nil, nil, os.Remove(filepath.Join(pluginsDir, "known_marketplaces.json"))
	case len(args) >= 2 && args[0] == "plugin" && args[1] == "install":
		instPath := filepath.Join(pluginsDir, "installed_plugins.json")
		inst := map[string]any{
			"version": 2,
			"plugins": map[string]any{
				pluginID: []map[string]any{{"scope": "user", "installPath": filepath.Join(pluginsDir, "cache", slug, slug, ver), "version": ver}},
			},
		}
		b, _ := json.Marshal(inst)
		if err := os.WriteFile(instPath, b, 0o600); err != nil {
			return nil, nil, err
		}
		settingsPath := filepath.Join(r.home, ".claude", "settings.json")
		sb, _ := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{pluginID: true}})
		return nil, nil, os.WriteFile(settingsPath, sb, 0o600)
	case len(args) >= 2 && args[0] == "plugin" && args[1] == "uninstall":
		_ = os.Remove(filepath.Join(pluginsDir, "installed_plugins.json"))
		return nil, nil, os.WriteFile(filepath.Join(r.home, ".claude", "settings.json"), []byte(`{}`), 0o600)
	}
	return nil, nil, nil
}

func (r *desktopMockPluginRunner) runCodex(slug, ver, pluginID string, args []string) ([]byte, []byte, error) {
	codexDir := filepath.Join(r.home, ".codex")
	_ = os.MkdirAll(codexDir, 0o755)
	cfgPath := filepath.Join(codexDir, "config.toml")
	mktPath := filepath.Join(r.stateDir, "marketplaces", clientplugin.ClientCodex)
	cacheDir := filepath.Join(codexDir, "plugins", "cache", slug, slug, ver)

	switch {
	case len(args) >= 2 && args[0] == "plugin" && args[1] == "add":
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			return nil, nil, err
		}
		content := "[plugins.\"" + pluginID + "\"]\nenabled = true\n\n[marketplaces." + slug + "]\nsource_type = \"local\"\nsource = \"" + mktPath + "\"\n"
		return nil, nil, os.WriteFile(cfgPath, []byte(content), 0o600)
	case len(args) >= 2 && args[0] == "plugin" && args[1] == "remove":
		_ = os.RemoveAll(filepath.Join(codexDir, "plugins", "cache", slug))
		return nil, nil, os.WriteFile(cfgPath, []byte(""), 0o600)
	}
	return nil, nil, nil
}

func (r *desktopMockPluginRunner) runPi(args []string) ([]byte, []byte, error) {
	settingsPath := filepath.Join(r.home, ".pi", "agent", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settingsPath), 0o700)
	if len(args) >= 2 && args[0] == "install" {
		src := args[len(args)-1]
		b, _ := json.Marshal(map[string]any{"packages": []string{src}})
		return nil, nil, os.WriteFile(settingsPath, b, 0o600)
	}
	if len(args) >= 2 && args[0] == "remove" {
		return nil, nil, os.WriteFile(settingsPath, []byte(`{"packages":[]}`), 0o600)
	}
	return nil, nil, nil
}

func waitPluginIdle(t *testing.T, tester *ui.Tester, u *DesktopUI) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tester.Frame()
		done := func() bool {
			leave := u.enterHeadlessFrame()
			defer leave()
			return u.BusyCount == 0 && !u.ClientsBusy && !u.PluginsBusy
		}()
		if done {
			tester.Frame()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for plugin UI async action")
		}
		time.Sleep(2 * time.Millisecond)
		tester.Advance(10 * time.Millisecond)
	}
}

func TestClientPluginPageAndActions(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug
	stateDir := filepath.Join(home, "."+slug, "plugins")
	hookBin := filepath.Join(home, ".local", "bin", slug+"-hook")
	if err := os.MkdirAll(filepath.Dir(hookBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var revealed []string
	origReveal := openConfigLocation
	openConfigLocation = func(path string) error {
		revealed = append(revealed, path)
		return nil
	}
	t.Cleanup(func() { openConfigLocation = origReveal })

	cc := clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), func(string) string { return "" })
	// Seed legacy global MCP config on Codex to test one-click 升级为插件.
	if err := cc.Write(clientplugin.ClientCodex, "http://localhost:8788/mcp"); err != nil {
		t.Fatalf("seed legacy codex: %v", err)
	}
	// Seed a foreign conflicting OpenCode plugin file without receipt to test 检查配置.
	foreignOpenCode := filepath.Join(home, ".config", "opencode", "plugins", slug+".js")
	if err := os.MkdirAll(filepath.Dir(foreignOpenCode), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreignOpenCode, []byte("// foreign custom plugin\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &desktopMockPluginRunner{home: home, stateDir: stateDir}
	cp := clientplugin.New(clientplugin.Options{
		HomeDir:       home,
		StateDir:      stateDir,
		BinPath:       hookBin,
		Getenv:        func(string) string { return "" },
		Runner:        runner,
		ClientConfigs: cc,
	})

	u := &DesktopUI{
		ClientConfigs: cc,
		ClientPlugins: cp,
		Runtime:       &gatewayRuntime{MCPURL: "http://localhost:8788/mcp"},
		scrollStates:  make(map[ViewID]*ui.ScrollState),
	}
	u.ClientStatuses = cc.Statuses(u.clientEndpoint())
	u.PluginStatuses = cp.Statuses(u.clientEndpoint())

	tester := ui.NewTester(func(c *ui.Context) {
		leave := u.enterHeadlessFrame()
		defer leave()
		u.drainToasts(c)
		u.renderClients(c)
	}, 1280, 960)
	for _, name := range []string{"Codex CLI", "Claude Code", "Cursor", "OpenCode v2", "Pi"} {
		if !tester.HasText(name) {
			t.Fatalf("missing client name %q", name)
		}
	}
	// Ensure raw config paths are never rendered directly in row text.
	for _, row := range u.PluginStatuses {
		if tester.HasText(row.Path) {
			t.Fatalf("raw config path %q rendered directly for %s", row.Path, row.ID)
		}
	}

	// Verify initial affordances: Codex -> 升级为插件, Claude/Cursor/Pi -> 安装插件, OpenCode v2 -> 检查配置.
	if !tester.HasText("升级为插件") || !tester.HasText("安装插件") || !tester.HasText("检查配置") {
		t.Fatalf("expected 升级为插件, 安装插件, and 检查配置 buttons, texts=%v", tester.Texts())
	}
	// Click 检查配置 on conflicting OpenCode v2 -> shows alert box and opens config location.
	if err := tester.Click("OpenCode v2 检查配置"); err != nil {
		t.Fatalf("click OpenCode v2 检查配置: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if !tester.HasText("插件操作失败") || len(revealed) == 0 || revealed[len(revealed)-1] != foreignOpenCode {
		t.Fatalf("expected conflict alert & reveal path, err=%q revealed=%v", u.PluginError, revealed)
	}
	// Clean foreign file so OpenCode can also be installed below.
	if err := os.Remove(foreignOpenCode); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("刷新状态"); err != nil {
		t.Fatalf("click 刷新状态: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if u.PluginError != "" {
		t.Fatalf("expected refresh to clear resolved conflict error, got %q", u.PluginError)
	}

	// Install / Upgrade all 5 clients through UI buttons.
	for _, btnLabel := range []string{
		"Codex CLI 升级为插件",
		"Claude Code 安装插件",
		"Cursor 安装插件",
		"OpenCode v2 安装插件",
		"Pi 安装插件",
	} {
		if err := tester.Click(btnLabel); err != nil {
			t.Fatalf("click %q: %v", btnLabel, err)
		}
		waitPluginIdle(t, tester, u)
		if u.PluginError != "" {
			t.Fatalf("unexpected error after %q: %s", btnLabel, u.PluginError)
		}
	}
	if !tester.HasText("✓ 已安装") {
		t.Fatal("expected ✓ 已安装 status after installing plugins")
	}
	// Codex legacy global MCP must have been auto-revoked upon plugin upgrade.
	for _, st := range u.ClientStatuses {
		if st.ID == clientplugin.ClientCodex && st.CanRevoke {
			t.Fatalf("expected codex legacy MCP auto-revoked, got %+v", st)
		}
	}

	// Change gateway port -> Cursor/Claude/OpenCode/Codex/Pi should surface 更新插件.
	u.Runtime.MCPURL = "http://localhost:9999/mcp"
	if err := tester.Click("刷新状态"); err != nil {
		t.Fatalf("click 刷新状态 after port change: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if !tester.HasText("更新插件") {
		t.Fatalf("expected 更新插件 after endpoint change, texts=%v", tester.Texts())
	}
	if err := tester.Click("Claude Code 更新插件"); err != nil {
		t.Fatalf("click Claude Code 更新插件: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if u.PluginError != "" {
		t.Fatalf("unexpected error after update: %s", u.PluginError)
	}

	// Overflow menu: 复制验证 Prompt -> clipboard receives verification prompt.
	if err := tester.Click("Claude Code 更多操作"); err != nil {
		t.Fatalf("open Claude overflow menu: %v", err)
	}
	if err := tester.ChooseMenuItem("复制验证 Prompt"); err != nil {
		t.Fatalf("choose 复制验证 Prompt: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if gotClip := tester.Clipboard(); !strings.Contains(gotClip, "discover_tools") || !strings.Contains(gotClip, "Claude Code") {
		t.Fatalf("unexpected verification prompt in clipboard: %q", gotClip)
	}

	// Overflow menu: 卸载插件 -> Cursor transitions back to 安装插件.
	if err := tester.Click("Cursor 更多操作"); err != nil {
		t.Fatalf("open Cursor overflow menu: %v", err)
	}
	if err := tester.ChooseMenuItem("卸载插件"); err != nil {
		t.Fatalf("choose 卸载插件: %v", err)
	}
	waitPluginIdle(t, tester, u)
	if _, ok := tester.Access("Cursor 安装插件"); !ok {
		t.Fatal("expected Cursor 安装插件 button after uninstall")
	}
}
