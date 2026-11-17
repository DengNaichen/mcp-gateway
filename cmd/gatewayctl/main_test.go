package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
)

func TestPluginCLIStatusInstallUpdateRemoveAndPrompt(t *testing.T) {
	home := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "plugins")
	dataDir := filepath.Join(home, "."+buildinfo.Current().Slug, "data")
	binPath := filepath.Join(home, ".local", "bin", "fake-hook")

	// Seed legacy MCP config for Cursor to verify automatic migration on install.
	cc := clientconfig.New(home, filepath.Join(home, "."+buildinfo.Current().Slug, "client-config"), func(string) string { return "" })
	if err := cc.Write(clientplugin.ClientCursor, "http://127.0.0.1:8864/mcp"); err != nil {
		t.Fatalf("seed legacy cursor mcp: %v", err)
	}

	// 1. status (all)
	var out bytes.Buffer
	if err := runWithArgs([]string{"-url", "http://127.0.0.1:8864", "plugin", "status", "-home", home, "-state", stateDir, "-data", dataDir, "-bin", binPath}, &out); err != nil {
		t.Fatalf("plugin status all failed: %v", err)
	}
	var all []clientplugin.Status
	if err := json.Unmarshal(out.Bytes(), &all); err != nil {
		t.Fatalf("decode all status: %v\n%s", err, out.String())
	}
	if len(all) != 6 {
		t.Fatalf("expected 6 clients, got %d", len(all))
	}
	if all[2].ID != clientplugin.ClientCursor || !all[2].HasLegacyMCP || all[2].State != "待升级" {
		t.Fatalf("unexpected initial cursor status: %+v", all[2])
	}

	// 2. install cursor via -client and verify legacy MCP was revoked.
	out.Reset()
	if err := runWithArgs([]string{"plugin", "install", "-url", "http://127.0.0.1:8864", "-client", "cursor", "-home", home, "-state", stateDir, "-data", dataDir, "-bin", binPath}, &out); err != nil {
		t.Fatalf("plugin install cursor failed: %v", err)
	}
	var cursorSt clientplugin.Status
	if err := json.Unmarshal(out.Bytes(), &cursorSt); err != nil {
		t.Fatalf("decode cursor status: %v\n%s", err, out.String())
	}
	if cursorSt.State != "已安装" || cursorSt.HasLegacyMCP {
		t.Fatalf("expected installed without legacy mcp, got %+v", cursorSt)
	}

	// 3. update opencode after install with a changed port
	out.Reset()
	if err := runWithArgs([]string{"plugin", "install", "-url", "http://127.0.0.1:8864", "-client", "opencode", "-home", home, "-state", stateDir, "-data", dataDir, "-bin", binPath}, &out); err != nil {
		t.Fatalf("plugin install opencode failed: %v", err)
	}
	out.Reset()
	if err := runWithArgs([]string{"plugin", "update", "-url", "http://127.0.0.1:8999/mcp", "-client", "opencode", "-home", home, "-state", stateDir, "-data", dataDir, "-bin", binPath}, &out); err != nil {
		t.Fatalf("plugin update opencode failed: %v", err)
	}
	var ocSt clientplugin.Status
	if err := json.Unmarshal(out.Bytes(), &ocSt); err != nil {
		t.Fatalf("decode opencode status: %v", err)
	}
	if ocSt.State != "已安装" {
		t.Fatalf("expected opencode installed after update, got %+v", ocSt)
	}

	// 4. verify-prompt single & all
	out.Reset()
	if err := runWithArgs([]string{"-url", "http://127.0.0.1:8864", "plugin", "verify-prompt", "-client", "claude", "-home", home, "-state", stateDir}, &out); err != nil {
		t.Fatalf("verify-prompt claude failed: %v", err)
	}
	var prompt struct {
		Client   string `json:"client"`
		Endpoint string `json:"endpoint"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal(out.Bytes(), &prompt); err != nil {
		t.Fatalf("decode prompt: %v", err)
	}
	if prompt.Client != "claude" || !strings.Contains(prompt.Endpoint, "127.0.0.1:8864/mcp?agent=claude") || !strings.Contains(prompt.Prompt, "discover_tools") {
		t.Fatalf("unexpected verify-prompt output: %+v", prompt)
	}

	// 5. remove -client all (only removes installed fs clients, idempotent on others)
	out.Reset()
	if err := runWithArgs([]string{"plugin", "remove", "-client", "all", "-home", home, "-state", stateDir}, &out); err != nil {
		t.Fatalf("plugin remove all failed: %v", err)
	}
	all = nil
	if err := json.Unmarshal(out.Bytes(), &all); err != nil {
		t.Fatalf("decode after remove all: %v", err)
	}
	for _, st := range all {
		if st.CanRemove {
			t.Fatalf("unexpected remaining installed plugin: %+v", st)
		}
	}
}

func TestPluginCLIValidationErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"plugin"},
		{"plugin", "bogus"},
		{"plugin", "install"},
		{"plugin", "status", "-client", "unknown"},
		{"plugin", "status", "-client", "claude", "extra"},
	}
	for _, args := range cases {
		var out bytes.Buffer
		if err := runWithArgs(args, &out); err == nil {
			t.Fatalf("expected error for args %v, got nil", args)
		}
	}
}

func TestPluginCLIDataDirParityWithDesktopAndChannelEnv(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug

	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GATEWAY_CLIENT_CONFIG_HOME", "")
	t.Setenv("GATEWAY_DATA_DIR", "")
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), "")

	userCfgDir, err := os.UserConfigDir()
	if err != nil || userCfgDir == "" {
		t.Fatalf("resolve UserConfigDir: %v", err)
	}
	desktopDataDir := filepath.Join(userCfgDir, slug)
	if err := os.MkdirAll(desktopDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	hookBin := filepath.Join(home, "."+slug, "bin", slug+"-hook")
	if err := os.MkdirAll(filepath.Dir(hookBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookBin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate Desktop installing Cursor plugin with rt.DataDir == <UserConfigDir>/<slug>.
	desktopMgr := clientplugin.New(clientplugin.Options{
		HomeDir:       home,
		StateDir:      filepath.Join(home, "."+slug, "plugins"),
		DataDir:       desktopDataDir,
		Getenv:        os.Getenv,
		ClientConfigs: clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), os.Getenv),
	})
	if err := desktopMgr.Install(clientplugin.ClientCursor, "http://127.0.0.1:8864/mcp"); err != nil {
		t.Fatalf("desktop install cursor: %v", err)
	}

	// Running gatewayctl plugin status without -data must resolve <UserConfigDir>/<slug>
	// and match Desktop's digest instead of falsely reporting "需要更新".
	var out bytes.Buffer
	if err := runWithArgs([]string{"-url", "http://127.0.0.1:8864", "plugin", "status", "-client", "cursor"}, &out); err != nil {
		t.Fatalf("gatewayctl plugin status: %v", err)
	}
	var st clientplugin.Status
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("decode status: %v\n%s", err, out.String())
	}
	if st.State != "已安装" || st.CanUpdate {
		t.Fatalf("expected desktop-installed plugin to stay 已安装 in gatewayctl, got %+v", st)
	}

	// Channel-specific env var (GATEWAY_DEV_DATA_DIR in dev) overrides fallback.
	chEnvKey := buildinfo.EnvironmentKey("GATEWAY_DATA_DIR")
	chDataDir := filepath.Join(t.TempDir(), "channel-data")
	if chEnvKey != "GATEWAY_DATA_DIR" {
		t.Setenv("GATEWAY_DATA_DIR", filepath.Join(t.TempDir(), "generic-data"))
	}
	t.Setenv(chEnvKey, chDataDir)

	out.Reset()
	if err := runWithArgs([]string{"-url", "http://127.0.0.1:8864", "plugin", "update", "-client", "cursor"}, &out); err != nil {
		t.Fatalf("gatewayctl plugin update under channel env: %v", err)
	}
	st = clientplugin.Status{}
	if err := json.Unmarshal(out.Bytes(), &st); err != nil {
		t.Fatalf("decode status after update: %v\n%s", err, out.String())
	}
	if st.State != "已安装" {
		t.Fatalf("expected 已安装 after channel env update, got %+v", st)
	}
	wrapperPath := filepath.Join(home, ".cursor", "plugins", "local", slug, "scripts", "run-gateway-hook")
	wrapperBytes, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("read wrapper: %v", err)
	}
	if !strings.Contains(string(wrapperBytes), chDataDir) {
		t.Fatalf("expected wrapper to use channel env data dir %q, got:\n%s", chDataDir, string(wrapperBytes))
	}
}
