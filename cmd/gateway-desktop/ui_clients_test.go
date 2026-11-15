package main

import (
	"gatewaylab/internal/clientconfig"
	"github.com/egoist/mygo/ui"
	"os"
	"path/filepath"
	"testing"
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
	if !tester.HasText("已配置") {
		t.Fatal("missing configured status")
	}
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi"} {
		if err := u.changeClientConfig(id, "revoke", u.clientEndpoint()); err != nil {
			t.Fatal(err)
		}
	}
	u.ClientStatuses = m.Statuses(u.clientEndpoint())
	tester.Frame()
	if !tester.HasText("未配置") {
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
