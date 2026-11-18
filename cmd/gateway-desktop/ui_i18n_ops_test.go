package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/i18n"
)

// cjkTextRe matches any CJK character so the ops views can be asserted to be
// fully translated in English mode.
var cjkTextRe = regexp.MustCompile(`[\x{3000}-\x{303f}\x{4e00}-\x{9fff}\x{ff00}-\x{ffef}]`)

// opsFormatVerbRe strips printf verbs so a zh-CN template can be split into the
// literal fragments that must never show up in English mode.
var opsFormatVerbRe = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*(?:\*|\d+)?(?:\.(?:\*|\d+))?[vTtbcdoqxXUeEfFgGspw%]`)

// opsZhFragments loads the zh-CN catalogs of this work item and returns their
// literal (non-format) fragments. Shared components owned by other files (for
// example the theme copy button) are intentionally not part of this set.
func opsZhFragments(t *testing.T) []string {
	t.Helper()
	var fragments []string
	for _, name := range []string{"desktop_ops.json", "clients.json"} {
		path := filepath.Join("..", "..", "internal", "i18n", "locales", "zh-CN", name)
		raw, err := os.ReadFile(path) //nolint:gosec // fixed test-owned path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		for _, value := range entries {
			for _, fragment := range opsFormatVerbRe.Split(value, -1) {
				fragment = strings.TrimSpace(fragment)
				if len([]rune(fragment)) >= 2 && cjkTextRe.MatchString(fragment) {
					fragments = append(fragments, fragment)
				}
			}
		}
	}
	return fragments
}

func opsI18nTestBootstrap() apitypes.Bootstrap {
	boot := apitypes.Bootstrap{}
	boot.MCPURL = "http://127.0.0.1:8788/mcp"
	boot.Settings = apitypes.Settings{BaseURL: "https://api.example.com/v1", Model: "test-model", MaxTools: 8, TimeoutSeconds: 30}
	boot.KeyConfigured = true
	boot.CredentialsPersistent = true
	boot.Registry.Servers = []apitypes.Server{
		{ID: "github", Name: "GitHub", Description: "Code hosting", Enabled: true},
		{ID: "demo", Name: "Demo", Demo: true},
	}
	boot.Registry.Tools = []apitypes.Tool{
		{ID: "github.list_issues", ServerID: "github", Name: "list_issues", Description: "List issues", Enabled: true, ReadOnly: true, Actors: []string{"*"}, Tags: []string{"issues"}},
		{ID: "github.create_issue", ServerID: "github", Name: "create_issue", Description: "Create an issue", Enabled: true},
	}
	boot.Profiles = []apitypes.ModelProfile{
		{ID: "prof-1", BaseURL: "https://api.example.com/v1", Model: "test-model", MaxTools: 8, TimeoutSeconds: 30, Active: true, KeyConfigured: true},
	}
	boot.Setup = apitypes.SetupStatus{Sources: 1, EnabledTools: 2, ToolsReady: true, ModelConfigured: true, ModelReady: true}
	boot.Access = apitypes.AccessInfo{Actor: "developer"}
	boot.Scopes = apitypes.ScopeOverview{
		Enforce: true,
		Repositories: []apitypes.Repository{
			{ID: "repo-1", Name: "mcp-gateway", RootPaths: []string{"/work/mcp-gateway"}},
		},
		Observed: []apitypes.ObservedRepo{
			{Fingerprint: "fp-1", Name: "demo-app", Root: "/work/demo-app", LastAgent: "claude", RequestCount: 3},
		},
		Rules: []apitypes.ScopeRule{
			{RepoKey: "*", Grants: []apitypes.ServiceGrant{}},
			{RepoKey: "repo-1", Grants: []apitypes.ServiceGrant{{ServerID: "github", Access: "ro"}}},
		},
	}
	boot.Events = []apitypes.Event{
		{ID: "ev-1", Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Kind: "invoke", Actor: "developer", Status: "ok", Title: "list_issues", DurationMS: 42},
	}
	return boot
}

// TestDesktopOpsViewsRenderEnglish renders every localized ops view in English
// mode and asserts both English anchors and that no Chinese text leaks through.
func TestDesktopOpsViewsRenderEnglish(t *testing.T) {
	fragments := opsZhFragments(t)
	boot := opsI18nTestBootstrap()
	views := []struct {
		name  string
		title string
		build func(u *DesktopUI) func(*ui.Context)
	}{
		{name: "catalog", title: "Tool catalog", build: func(u *DesktopUI) func(*ui.Context) { return u.renderCatalog }},
		{name: "clients", title: "Connected clients", build: func(u *DesktopUI) func(*ui.Context) { return u.renderClients }},
		{name: "repos", title: "Repository bindings", build: func(u *DesktopUI) func(*ui.Context) { return u.renderRepos }},
		{name: "logs", title: "Request logs", build: func(u *DesktopUI) func(*ui.Context) { return u.renderLogs }},
		{name: "playground", title: "Routing sandbox", build: func(u *DesktopUI) func(*ui.Context) { return u.renderPlayground }},
		{name: "settings", title: "Access & routing", build: func(u *DesktopUI) func(*ui.Context) { return u.renderSettings }},
	}
	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			i18n.WithLang(i18n.LangEN, func() {
				u := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
				u.ClientConfigs = nil
				u.ClientPlugins = nil
				state := boot
				u.State = &state
				u.Loading = false
				render := view.build(u)
				tester := ui.NewTester(func(c *ui.Context) {
					defer u.enterHeadlessFrame()()
					render(c)
				}, 1400, 1600)
				if !tester.HasText(view.title) {
					t.Fatalf("missing English anchor %q", view.title)
				}
				for _, text := range tester.Texts() {
					for _, fragment := range fragments {
						if strings.Contains(text, fragment) {
							t.Fatalf("untranslated Chinese text in English mode: %q (fragment %q)", text, fragment)
						}
					}
				}
			})
		})
	}
}

// TestDesktopOpsEnglishSamples pins a representative set of English
// translations, including format-verb templates, so a regression in either
// locale file is caught immediately.
func TestDesktopOpsEnglishSamples(t *testing.T) {
	i18n.WithLang(i18n.LangEN, func() {
		cases := []struct{ key, want string }{
			{"ops.catalog-title", "Tool catalog"},
			{"ops.clients-title", "Connected clients"},
			{"ops.logs-title", "Request logs"},
			{"ops.repos-title", "Repository bindings"},
			{"ops.settings-title", "Access & routing"},
			{"ops.playground", "Routing sandbox"},
			{"ops.plugins-installed-count", "1 / 2 official plugins installed"},
			{"ops.more-actions-for", "More actions for Claude Code"},
			{"ops.client-error-detail", "Claude Code: config error"},
			{"ops.page-indicator", "Page 2 / 3"},
			{"ops.key-not-configured", "Key not configured"},
			{"ops.saving", "Saving…"},
		}
		for _, tc := range cases {
			args := []any{}
			switch tc.key {
			case "ops.plugins-installed-count":
				args = []any{1, 2}
			case "ops.more-actions-for":
				args = []any{"Claude Code"}
			case "ops.client-error-detail":
				args = []any{"Claude Code", "config error"}
			case "ops.page-indicator":
				args = []any{2, 3}
			}
			if got := i18n.T(tc.key, args...); got != tc.want {
				t.Fatalf("T(%q) = %q, want %q", tc.key, got, tc.want)
			}
		}
	})
	i18n.WithLang(i18n.LangZH, func() {
		if got := i18n.T("ops.catalog-title"); got != "工具目录" {
			t.Fatalf("zh-CN ops.catalog-title = %q", got)
		}
		if got := i18n.T("ops.plugins-installed-count", 1, 2); got != "官方插件已安装 1 / 2" {
			t.Fatalf("zh-CN formatted template = %q", got)
		}
	})
}

// TestClientConfigStatusesLocalized verifies the client state machine renders
// localized state labels while keeping the existing English-mode comparisons.
func TestClientConfigStatusesLocalized(t *testing.T) {
	root := t.TempDir()
	m := clientconfig.New(root, filepath.Join(root, "receipts"), func(string) string { return "" })
	u := &DesktopUI{ClientConfigs: m, Runtime: &gatewayRuntime{MCPURL: "http://localhost:8788/mcp"}}
	for _, id := range []string{"codex", "claude", "cursor", "opencode", "pi", "antigravity"} {
		if err := u.changeClientConfig(id, "write", u.clientEndpoint()); err != nil {
			t.Fatal(err)
		}
	}

	i18n.WithLang(i18n.LangZH, func() {
		for _, row := range m.Statuses(u.clientEndpoint()) {
			if row.State != "已配置" || !row.CanRevoke {
				t.Fatalf("zh-CN state = %+v", row)
			}
		}
	})

	i18n.WithLang(i18n.LangEN, func() {
		for _, row := range m.Statuses(u.clientEndpoint()) {
			if row.State != "Configured" || !row.CanRevoke {
				t.Fatalf("en state = %+v", row)
			}
		}
		u.ClientStatuses = m.Statuses(u.clientEndpoint())
		noteClientStatusLang(i18n.Current())
		u.Loading = false
		tester := ui.NewTester(func(c *ui.Context) {
			defer u.enterHeadlessFrame()()
			u.renderClients(c)
		}, 1200, 1500)
		if !tester.HasText("✓ Configured") {
			t.Fatal("missing English configured status")
		}
		if _, ok := tester.Access("Open Codex CLI config location"); !ok {
			t.Fatal("missing English accessible path action")
		}
	})
	noteClientStatusLang(i18n.Current())
}

// TestDesktopOpsLocaleFilesTranslated guards the locale files themselves: every
// English string must be non-empty and free of Chinese characters.
func TestDesktopOpsLocaleFilesTranslated(t *testing.T) {
	for _, name := range []string{"desktop_ops.json", "clients.json"} {
		path := filepath.Join("..", "..", "internal", "i18n", "locales", "en", name)
		raw, err := os.ReadFile(path) //nolint:gosec // fixed test-owned path
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var entries map[string]string
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(entries) == 0 {
			t.Fatalf("%s has no entries", path)
		}
		for key, value := range entries {
			if value == "" {
				t.Fatalf("%s: key %q is empty", name, key)
			}
			if cjkTextRe.MatchString(value) {
				t.Fatalf("%s: key %q still contains Chinese: %q", name, key, value)
			}
		}
	}
}

// TestClientStatusLangTracking verifies the cached client status rows are known
// to be stale after a language switch, which is what triggers their rebuild.
func TestClientStatusLangTracking(t *testing.T) {
	defer i18n.WithLang(i18n.LangZH, func() { noteClientStatusLang(i18n.Current()) })
	i18n.WithLang(i18n.LangZH, func() { noteClientStatusLang(i18n.Current()) })
	i18n.WithLang(i18n.LangEN, func() {
		if !staleClientStatusLang() {
			t.Fatal("rows built in zh-CN must be stale in en")
		}
		noteClientStatusLang(i18n.Current())
		if staleClientStatusLang() {
			t.Fatal("rows built in en must not be stale in en")
		}
	})
}
