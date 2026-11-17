package main

import (
	"bytes"
	"encoding/json"
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
	if len(all) != 5 {
		t.Fatalf("expected 5 clients, got %d", len(all))
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
