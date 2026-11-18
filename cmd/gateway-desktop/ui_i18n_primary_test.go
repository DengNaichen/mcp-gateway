package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
)

// primaryI18nBootstrap is the minimal state the primary screens need to render
// their real copy (overview KPIs, the services list, and the market tab).
func primaryI18nBootstrap(now time.Time) apitypes.Bootstrap {
	return apitypes.Bootstrap{
		MCPURL:        "http://127.0.0.1:8790/mcp",
		KeyConfigured: true,
		Access:        apitypes.AccessInfo{Actor: "developer"},
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{
					ID:        "github",
					Name:      "GitHub MCP",
					Kind:      "mcp",
					Transport: "http",
					Endpoint:  "https://api.githubcopilot.com/mcp/",
					Headers:   map[string]string{"Authorization": "Bearer ${GITHUB_TOKEN}"},
					Enabled:   true,
				},
				{
					ID:       "billing",
					Name:     "Billing REST API",
					Kind:     "http",
					Endpoint: "https://billing.internal.example/v1",
					Enabled:  true,
				},
				{
					ID:        "demo",
					Name:      "Demo Showcase",
					Kind:      "mcp",
					Transport: "http",
					Endpoint:  "http://127.0.0.1:8790/demo/mcp",
					Enabled:   true,
					Demo:      true,
				},
			},
			Tools: []apitypes.Tool{
				{
					ID:          "github.list_prs",
					ServerID:    "github",
					Name:        "list_prs",
					Description: "List open pull requests for a repository.",
					Enabled:     true,
					ReadOnly:    true,
					Actors:      []string{"developer"},
					InputSchema: map[string]any{"type": "object"},
				},
				{
					ID:          "billing.refund_charge",
					ServerID:    "billing",
					Name:        "refund_charge",
					Description: "Issue a partial or full refund for a charge ID.",
					Enabled:     true,
					Actors:      []string{"developer"},
					Method:      "POST",
					Path:        "/charges/{id}/refund",
					InputSchema: map[string]any{"type": "object"},
				},
			},
		},
		Settings: apitypes.Settings{
			BaseURL:        "https://api.openai.com/v1",
			Model:          "gpt-6-luna",
			MaxTools:       4,
			TimeoutSeconds: 30,
		},
		Setup: apitypes.SetupStatus{
			Sources:         2,
			EnabledTools:    2,
			ToolsReady:      true,
			ModelConfigured: true,
			ModelReady:      true,
			SourceChecks: map[string]apitypes.ConnectionCheck{
				"github": {Status: "success", Message: "Connected cleanly", CheckedAt: now, ToolCount: 1},
			},
		},
		Events: []apitypes.Event{
			{
				ID:         "ev-en-1",
				Time:       now,
				Kind:       "discover",
				Actor:      "developer",
				Status:     "selected",
				Title:      "list open pull requests",
				DurationMS: 21,
				Detail:     json.RawMessage(`{"eligible":12,"tools":[{"id":"github.list_prs"}]}`),
			},
		},
	}
}

func newPrimaryI18nTester(t *testing.T) (*DesktopUI, *ui.Tester) {
	t.Helper()
	boot := primaryI18nBootstrap(time.Now())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(apitypes.AuthMe{Authenticated: true})
		case "/api/market":
			_ = json.NewEncoder(w).Encode(apitypes.MarketSearchResult{Items: []apitypes.MarketServer{
				{
					ID:          "notion",
					ServerID:    "notion",
					Title:       "Notion",
					Publisher:   "Notion",
					Transport:   "http",
					Runs:        "远程",
					Endpoint:    "https://mcp.notion.com/mcp",
					Description: "Notion workspace pages and databases.",
					SignIn:      true,
					Featured:    true,
					Inputs:      []apitypes.MarketInput{},
				},
				{
					ID:          "brave-search",
					ServerID:    "brave-search",
					Title:       "Brave Search",
					Publisher:   "Brave",
					Transport:   "stdio",
					Runs:        "npx",
					Command:     "npx",
					Args:        []string{"-y", "@brave/brave-search-mcp-server"},
					Description: "Web search backed by Brave's independent index.",
					Featured:    true,
					Inputs: []apitypes.MarketInput{
						{Key: "BRAVE_API_KEY", Where: "env", Label: "Brave Search API Key", Secret: true, Required: true},
					},
				},
			}})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(srv.Close)

	app := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	if err := app.RefreshSync(); err != nil {
		t.Fatalf("initial RefreshSync: %v", err)
	}
	tester := ui.NewTester(app.View, 1360, 860)
	tester.Advance(20 * time.Millisecond)
	return app, tester
}

func TestPrimarySurfacesRenderEnglish(t *testing.T) {
	i18n.WithLang(i18n.LangEN, func() {
		if i18n.Current() != i18n.LangEN {
			t.Fatalf("active language = %q, want en", i18n.Current())
		}
		app, tester := newPrimaryI18nTester(t)

		// Overview: nav labels plus KPI and pulse card copy.
		for _, want := range []string{"Overview", "Gateway requests", "Request pulse", "Context savings", "Connected services"} {
			if !tester.HasText(want) {
				t.Fatalf("expected English overview text %q, texts=%v", want, tester.Texts())
			}
		}
		for _, zh := range []string{"概览", "网关请求", "请求脉冲", "上下文节省", "已接入服务"} {
			if tester.HasText(zh) {
				t.Fatalf("overview still shows Chinese %q in en mode", zh)
			}
		}

		// Services list.
		app.Navigate(ViewServices)
		tester.Frame()
		for _, want := range []string{"Services", "Connected", "Market", "Add service", "Billing REST API", "1 ready"} {
			if !tester.HasText(want) {
				t.Fatalf("expected English services text %q, texts=%v", want, tester.Texts())
			}
		}
		for _, zh := range []string{"添加服务", "已接入", "市场", "就绪"} {
			if tester.HasText(zh) {
				t.Fatalf("services page still shows Chinese %q in en mode", zh)
			}
		}

		// Add-service modal tabs.
		if err := tester.Click("Add service"); err != nil {
			t.Fatalf("open add service modal: %v", err)
		}
		for _, tab := range []string{
			"MCP remote HTTP / SSE",
			"OpenAPI / Swagger import",
			"Paste mcpServers JSON",
			"MCP Stdio (local process)",
		} {
			if !tester.HasText(tab) {
				t.Fatalf("expected English add-service tab %q, texts=%v", tab, tester.Texts())
			}
		}
		if tester.HasText("MCP 远程 HTTP / SSE") {
			t.Fatal("add-service modal still shows Chinese tab label")
		}
		if err := tester.Click("Close"); err != nil {
			t.Fatalf("close add service modal: %v", err)
		}

		// Service detail: back link and sub-tabs.
		if err := tester.Click("Billing REST API"); err != nil {
			t.Fatalf("open service detail: %v", err)
		}
		for _, want := range []string{"← Back to services", "Connection & auth", "Health & maintenance"} {
			if !tester.HasText(want) {
				t.Fatalf("expected English service detail text %q, texts=%v", want, tester.Texts())
			}
		}
		if tester.HasText("← 返回服务列表") || tester.HasText("连接与认证配置") {
			t.Fatal("service detail still shows Chinese copy in en mode")
		}

		// Back to the list, then switch to the market sub-tab with preloaded items
		// before rendering so renderMarketContent does not start a background fetch.
		if err := tester.Click("← Back to services"); err != nil {
			t.Fatalf("back from service detail: %v", err)
		}
		app.servicesState().subtab = 1
		mkt := app.marketState()
		mkt.loaded = true
		mkt.items = []apitypes.MarketServer{
			{
				ID: "notion", ServerID: "notion", Title: "Notion", Publisher: "Notion",
				Transport: "http", Runs: "远程", Endpoint: "https://mcp.notion.com/mcp",
				Description: "Notion workspace pages and databases.", SignIn: true, Featured: true,
				Inputs: []apitypes.MarketInput{},
			},
			{
				ID: "brave-search", ServerID: "brave-search", Title: "Brave Search", Publisher: "Brave",
				Transport: "stdio", Runs: "npx", Command: "npx", Description: "Web search backed by Brave's independent index.",
				Featured: true,
				Inputs: []apitypes.MarketInput{
					{Key: "BRAVE_API_KEY", Where: "env", Label: "Brave Search API Key", Secret: true, Required: true},
				},
			},
		}
		tester.Frame()
		for _, want := range []string{"Cloud SaaS", "Local tools", "Remote", "Sign-in required", "Credentials required", "Add..."} {
			if !tester.HasText(want) {
				t.Fatalf("expected English market text %q, texts=%v", want, tester.Texts())
			}
		}
		for _, zh := range []string{"云端 SaaS", "本地工具", "需登录", "需凭据"} {
			if tester.HasText(zh) {
				t.Fatalf("market still shows Chinese %q in en mode", zh)
			}
		}
	})
}

// TestReadinessCopyEnglish covers the readiness labels that the services list
// and detail drawer surface without needing a rendered frame.
func TestReadinessCopyEnglish(t *testing.T) {
	i18n.WithLang(i18n.LangEN, func() {
		creds := &apitypes.ServerCredentials{Credentials: []apitypes.CredentialStatus{
			{Name: "GITHUB_TOKEN", Configured: false},
		}}
		srv := apitypes.Server{
			ID:        "github",
			Name:      "GitHub MCP",
			Kind:      "mcp",
			Transport: "http",
			Endpoint:  "https://api.githubcopilot.com/mcp/",
			Headers:   map[string]string{"Authorization": "Bearer ${GITHUB_TOKEN}"},
			Enabled:   true,
		}
		tools := []apitypes.Tool{{
			ID:       "github.list_prs",
			ServerID: "github",
			Name:     "list_prs",
			Enabled:  true,
			ReadOnly: true,
		}}

		r := evaluateServiceReadiness(srv, tools, apitypes.ConnectionCheck{}, false, creds, nil)
		if r.Kind != readinessMissingCreds {
			t.Fatalf("expected readinessMissingCreds, got %v", r.Kind)
		}
		if r.BadgeLabel != "Missing credentials" {
			t.Fatalf("BadgeLabel = %q, want English label", r.BadgeLabel)
		}
		if !strings.Contains(r.AlertDetail, "Neither the current gateway process nor the system keychain provides") {
			t.Fatalf("AlertDetail = %q, want English guidance", r.AlertDetail)
		}
		if strings.Contains(r.AlertDetail, "钥匙串") || strings.Contains(r.RowSummary, "缺少凭据") {
			t.Fatalf("readiness copy still mixed Chinese in en mode: %+v", r)
		}
		if _, label := r.RowAction(); label != "Add credentials" {
			t.Fatalf("RowAction label = %q, want Add credentials", label)
		}
		if got := triageAttention.Label(); got != "Needs attention" {
			t.Fatalf("triage label = %q, want Needs attention", got)
		}
		if got := triagePending.Label(); got != "Unverified" {
			t.Fatalf("triage label = %q, want Unverified", got)
		}

		// The zh-CN catalog must still be the faithful default for tests that
		// assert on Chinese copy.
		i18n.Set(i18n.LangZH)
		rZH := evaluateServiceReadiness(srv, tools, apitypes.ConnectionCheck{}, false, creds, nil)
		if rZH.BadgeLabel != "缺少凭据" {
			t.Fatalf("zh BadgeLabel = %q, want 缺少凭据", rZH.BadgeLabel)
		}
	})
}

// TestPrimaryCatalogEnglishHasNoCJK guards the two catalogs authored for the
// primary sweep: every key used by these screens carries real English copy.
func TestPrimaryCatalogEnglishHasNoCJK(t *testing.T) {
	keys := []string{
		"app.tray.status.running",
		"app.dialog.port_conflict.detail",
		"app.toast.refresh_failed",
		"overview.metric.requests",
		"overview.pulse.subtitle",
		"overview.detail.title",
		"services.page.title",
		"services.tabs.connection",
		"services.err.args_format",
		"readiness.no_tools.alert_detail",
		"readiness.cred.detail_missing",
		"market.search.placeholder",
		"market.toast.installed",
		"market.runs.remote",
		"market.catalog.notion.description",
		"market.catalog.filesystem.dir_label",
		"market.err.required",
	}
	for _, key := range keys {
		en := i18n.TLang(i18n.LangEN, key)
		zh := i18n.TLang(i18n.LangZH, key)
		if en == key {
			t.Fatalf("missing en translation for %q", key)
		}
		if zh == key {
			t.Fatalf("missing zh-CN translation for %q", key)
		}
		if strings.ContainsFunc(en, func(r rune) bool { return r >= 0x4e00 && r <= 0x9fff }) {
			t.Fatalf("en translation for %q still contains CJK: %q", key, en)
		}
	}
}
