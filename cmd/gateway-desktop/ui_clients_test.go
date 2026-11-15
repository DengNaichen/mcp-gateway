package main

import (
	"gatewaylab/internal/clientconfig"
	"github.com/egoist/mygo/ui"
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
