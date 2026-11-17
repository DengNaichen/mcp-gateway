package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
)

func TestParseDesktopFlagsAndPSNFiltering(t *testing.T) {
	addr, custom, data, err := parseDesktopFlags([]string{"-psn_0_123456"})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if addr != buildinfo.DefaultAddr() || custom || data != "" {
		t.Fatalf("got addr=%q custom=%v data=%q", addr, custom, data)
	}

	addr, custom, data, err = parseDesktopFlags([]string{"-psn_0_99", "-addr", "127.0.0.1:9900", "-data", "./tmpdata"})
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if addr != "127.0.0.1:9900" || !custom || data != "./tmpdata" {
		t.Fatalf("got addr=%q custom=%v data=%q", addr, custom, data)
	}
}

func TestResolveDataDir(t *testing.T) {
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), "")
	defaultDir, err := resolveDataDir("")
	if err != nil {
		t.Fatalf("resolveDataDir default failed: %v", err)
	}
	if !filepath.IsAbs(defaultDir) || filepath.Base(defaultDir) != buildinfo.Current().Slug {
		t.Fatalf("unexpected default data dir: %q", defaultDir)
	}

	tmp := t.TempDir()
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), tmp)
	envDir, err := resolveDataDir("")
	if err != nil || envDir != tmp {
		t.Fatalf("expected env dir %q, got %q (err=%v)", tmp, envDir, err)
	}

	explicit := filepath.Join(tmp, "custom")
	got, err := resolveDataDir(explicit)
	if err != nil || got != explicit {
		t.Fatalf("expected explicit dir %q, got %q (err=%v)", explicit, got, err)
	}
}

func TestMergePATHAndMarkers(t *testing.T) {
	sep := string(os.PathListSeparator)
	merged := mergePATH(
		"/usr/bin"+sep+"/bin",
		"/opt/homebrew/bin"+sep+"/usr/bin/",
		"/usr/local/bin"+sep+"",
	)
	want := strings.Join([]string{"/usr/bin", "/bin", "/opt/homebrew/bin", "/usr/local/bin"}, sep)
	if merged != want {
		t.Fatalf("mergePATH = %q, want %q", merged, want)
	}

	noisy := "Welcome to zsh!\n" + pathBeginMarker + "/opt/homebrew/bin:/usr/bin" + pathEndMarker + "\nbye\n"
	extracted := extractMarkedValue(noisy, pathBeginMarker, pathEndMarker)
	if extracted != "/opt/homebrew/bin:/usr/bin" {
		t.Fatalf("extractMarkedValue = %q", extracted)
	}
}

func TestStartAttachAndPortConflict(t *testing.T) {

	ctx := context.Background()
	owned, conflict, err := startOrAttachGateway(ctx, "127.0.0.1:0", t.TempDir())
	if err != nil || conflict != nil {
		t.Fatalf("start owned gateway failed: conflict=%v err=%v", conflict, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = owned.Shutdown(stopCtx)
	}()

	if owned.Mode != modeOwned {
		t.Fatalf("expected modeOwned, got %s", owned.Mode)
	}

	// An unrelated data directory must not silently attach to this backend.
	foreign, foreignConflict, foreignErr := startOrAttachGateway(ctx, owned.ListenAddr, t.TempDir())
	if foreign != nil || foreignConflict == nil || foreignErr != nil {
		t.Fatalf("foreign data dir attached: %v %v", foreignConflict, foreignErr)
	}
	// Starting the same identity and data directory should detect /api/health and attach.
	attached, conflict, err := startOrAttachGateway(ctx, owned.ListenAddr, owned.DataDir)
	if err != nil || conflict != nil {
		t.Fatalf("attach gateway failed: conflict=%v err=%v", conflict, err)
	}
	if attached.Mode != modeAttached || attached.UIURL != owned.UIURL {
		t.Fatalf("unexpected attached runtime: %+v", attached)
	}

	// Shutting down attached instance must leave the owned instance alive.
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	if err := attached.Shutdown(stopCtx); err != nil {
		t.Fatalf("attached shutdown error: %v", err)
	}
	cancel()
	if !probeGatewayHealth(ctx, owned.UIURL, owned.DataDir) {
		t.Fatal("owned gateway should remain healthy after attached client shuts down")
	}

	// Verify apiclient.Client against the live owned gateway.
	client := apiclient.New(owned.UIURL)
	boot, err := client.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("client.Bootstrap failed: %v", err)
	}
	if boot.MCPURL == "" {
		t.Fatal("expected non-empty mcp_url from apitypes.Bootstrap")
	}

	// Verify headless mygo/ui rendering across all navigation tabs.
	uiApp := NewDesktopUI(owned)
	uiApp.State = &boot
	uiApp.Telemetry = ComputeGatewayTelemetry(boot)
	uiApp.Loading = false

	tester := ui.NewTester(uiApp.View, 1280, 820)
	if !tester.HasText(buildinfo.Current().Name) {
		t.Fatal("expected native UI sidebar to render MCP Gateway")
	}
	for _, navLabel := range []string{"上游服务", "工具目录", "访问与路由", "请求日志", "路由沙盒", "概览"} {
		if err := tester.Click(navLabel); err != nil {
			t.Fatalf("click nav %q: %v", navLabel, err)
		}
	}

	// Shutting down owned instance must release the port.
	stopOwnedCtx, cancelOwned := context.WithTimeout(context.Background(), 5*time.Second)
	if err := owned.Shutdown(stopOwnedCtx); err != nil {
		t.Fatalf("owned shutdown error: %v", err)
	}
	cancelOwned()
	if probeGatewayHealth(ctx, owned.UIURL, owned.DataDir) {
		t.Fatal("owned gateway should stop responding after shutdown")
	}

	// Port occupied by an unrelated HTTP server must return *portConflictError.
	otherLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen dummy server: %v", err)
	}
	otherSrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	})}
	go func() { _ = otherSrv.Serve(otherLn) }()
	defer func() { _ = otherSrv.Close() }()

	rt, conflict, err := startOrAttachGateway(ctx, otherLn.Addr().String(), t.TempDir())
	if err != nil || rt != nil || conflict == nil {
		t.Fatalf("expected portConflictError, got rt=%v conflict=%v err=%v", rt, conflict, err)
	}
}

func TestComputeGatewayTelemetryAndActorMatrix(t *testing.T) {
	now := time.Now()
	detailHit, _ := json.Marshal(map[string]any{
		"eligible":      10,
		"prompt_tokens": 120,
		"output_tokens": 30,
		"tools": []map[string]any{
			{"id": "srv.list_items"},
			{"id": "srv.create_item"},
		},
	})
	detailErr, _ := json.Marshal(map[string]any{
		"error": "upstream timeout\ndetail line 2",
	})
	boot := apitypes.Bootstrap{
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "srv", Name: "Demo Srv", Kind: "http", Endpoint: "http://127.0.0.1:9999", Enabled: true},
				{ID: "demo", Name: "Built-in", Kind: "mcp", Endpoint: "http://127.0.0.1:8788/demo/mcp", Enabled: true, Demo: true},
			},
			Tools: []apitypes.Tool{
				{ID: "srv.list_items", ServerID: "srv", Name: "list_items", Enabled: true, ReadOnly: true, Actors: []string{"developer", "observer"}},
				{ID: "srv.create_item", ServerID: "srv", Name: "create_item", Enabled: true, ReadOnly: false, Actors: []string{"developer"}},
			},
		},
		Events: []apitypes.Event{
			{ID: "1", Time: now, Kind: "discover", Actor: "developer", Status: "selected", Title: "list items", DurationMS: 42, Detail: detailHit},
			{ID: "2", Time: now.Add(-time.Second), Kind: "discover", Actor: "observer", Status: "no_match", Title: "drop db", DurationMS: 80},
			{ID: "3", Time: now.Add(-2 * time.Second), Kind: "invoke", Actor: "developer", Status: "error", Title: "srv.create_item", DurationMS: 120, Detail: detailErr},
		},
	}

	tel := ComputeGatewayTelemetry(boot)
	if tel.DiscoverCount != 2 || tel.InvokeCount != 1 || tel.SelectedCount != 1 || tel.NoMatchCount != 1 || tel.ErrorCount != 1 {
		t.Fatalf("unexpected telemetry counts: %+v", tel)
	}
	if tel.HitRatePct == nil || *tel.HitRatePct != 50 {
		t.Fatalf("unexpected HitRatePct: %v", tel.HitRatePct)
	}
	if tel.CompressionPct == nil || *tel.CompressionPct != 80 {
		t.Fatalf("unexpected CompressionPct: %v", tel.CompressionPct)
	}
	if tel.TotalTokens != 150 {
		t.Fatalf("expected 150 tokens, got %d", tel.TotalTokens)
	}
	if len(tel.TopTools) != 2 || len(tel.AttentionItems) != 2 {
		t.Fatalf("unexpected topTools=%d attention=%d", len(tel.TopTools), len(tel.AttentionItems))
	}

	matrix := ComputeActorMatrix(boot.Registry.Servers, boot.Registry.Tools, "")
	if len(matrix) < 2 {
		t.Fatalf("expected at least 2 actors in matrix, got %d", len(matrix))
	}
	if matrix[0].Actor != "developer" || matrix[0].ReadOnlyTools != 1 || matrix[0].WriteTools != 1 {
		t.Fatalf("unexpected developer row: %+v", matrix[0])
	}
	if matrix[1].Actor != "observer" || matrix[1].ReadOnlyTools != 1 || matrix[1].WriteTools != 0 {
		t.Fatalf("unexpected observer row: %+v", matrix[1])
	}
}

func TestNativeUIRealisticDataAllPagesAndModals(t *testing.T) {
	now := time.Now()
	schemaJSON := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":  map[string]any{"type": "string", "description": "owner/name"},
			"state": map[string]any{"type": "string", "enum": []string{"open", "closed"}},
		},
		"required": []string{"repo"},
	}
	detailSelected, _ := json.Marshal(map[string]any{
		"eligible":      12,
		"prompt_tokens": 240,
		"output_tokens": 45,
		"summary":       "Selected GitHub PR query tools for developer.",
		"tools": []map[string]any{
			{"id": "github.list_prs", "reason": "Direct match for open pull requests"},
		},
	})
	detailNoMatch, _ := json.Marshal(map[string]any{
		"eligible": 12,
		"reason":   "No billing delete tool enabled for observer.",
	})
	detailErr, _ := json.Marshal(map[string]any{
		"error": "upstream HTTP 502: bad gateway",
	})

	boot := apitypes.Bootstrap{
		MCPURL:        "http://127.0.0.1:8788/mcp",
		KeyConfigured: true,
		Access: apitypes.AccessInfo{
			Shared: false,
			Actor:  "developer",
		},
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{
					ID:          "github",
					Name:        "GitHub MCP",
					Description: "Repository & PR automation via stdio",
					Kind:        "mcp",
					Transport:   "stdio",
					Command:     "npx",
					Args:        []string{"-y", "@modelcontextprotocol/server-github"},
					Enabled:     true,
				},
				{
					ID:          "billing",
					Name:        "Billing REST API",
					Description: "Internal invoice & refund endpoints",
					Kind:        "http",
					Endpoint:    "https://billing.internal.example/v1",
					Enabled:     true,
				},
				{
					ID:          "demo",
					Name:        "Demo Showcase",
					Description: "Built-in demo server",
					Kind:        "mcp",
					Transport:   "http",
					Endpoint:    "http://127.0.0.1:8788/demo/mcp",
					Enabled:     true,
					Demo:        true,
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
					Actors:      []string{"developer", "observer"},
					Tags:        []string{"git", "pr"},
					InputSchema: schemaJSON,
					Adapter:     "mcp",
					Revision:    "rev-1",
				},
				{
					ID:          "billing.refund_charge",
					ServerID:    "billing",
					Name:        "refund_charge",
					Description: "Issue a partial or full refund for a charge ID.",
					Enabled:     true,
					ReadOnly:    false,
					Actors:      []string{"developer"},
					Tags:        []string{"finance", "write"},
					Method:      "POST",
					Path:        "/charges/{id}/refund",
					InputSchema: schemaJSON,
					Adapter:     "http",
					Revision:    "rev-2",
				},
				{
					ID:          "billing.get_invoice",
					ServerID:    "billing",
					Name:        "get_invoice",
					Description: "Fetch invoice details by ID.",
					Enabled:     false,
					ReadOnly:    true,
					Actors:      []string{"developer", "observer"},
					Method:      "GET",
					Path:        "/invoices/{id}",
					InputSchema: schemaJSON,
					Adapter:     "http",
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
				"github": {
					Status:    "ok",
					Message:   "Connected cleanly",
					CheckedAt: now,
					ToolCount: 1,
				},
				"billing": {
					Status:    "error",
					Message:   "TLS handshake timeout",
					Detail:    "context deadline exceeded",
					CheckedAt: now.Add(-time.Minute),
					ToolCount: 2,
				},
			},
		},
		Events: []apitypes.Event{
			{
				ID:         "ev-1",
				Time:       now,
				Kind:       "discover",
				Actor:      "developer",
				Status:     "selected",
				Title:      "list open pull requests",
				DurationMS: 38,
				Detail:     detailSelected,
			},
			{
				ID:         "ev-2",
				Time:       now.Add(-5 * time.Second),
				Kind:       "discover",
				Actor:      "observer",
				Status:     "no_match",
				Title:      "refund customer invoice",
				DurationMS: 64,
				Detail:     detailNoMatch,
			},
			{
				ID:         "ev-3",
				Time:       now.Add(-10 * time.Second),
				Kind:       "invoke",
				Actor:      "developer",
				Status:     "error",
				Title:      "billing.refund_charge",
				DurationMS: 145,
				Detail:     detailErr,
			},
		},
	}

	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case r.URL.Path == "/api/auth/me":
			_ = json.NewEncoder(w).Encode(apitypes.AuthMe{Authenticated: true, Shared: false})
		case r.URL.Path == "/api/market":
			_ = json.NewEncoder(w).Encode(apitypes.MarketSearchResult{
				Items: []apitypes.MarketServer{
					{
						ID:          "context7",
						ServerID:    "context7",
						Title:       "Context7",
						Publisher:   "Upstash",
						Transport:   "http",
						Runs:        "远程",
						Endpoint:    "https://mcp.context7.com/mcp",
						Description: "任意库的最新文档与代码示例。",
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
						Description: "基于 Brave 独立索引的网页搜索。",
						Featured:    true,
						Inputs: []apitypes.MarketInput{
							{Key: "BRAVE_API_KEY", Where: "env", Label: "Brave Search API Key", Secret: true, Required: true},
						},
					},
				},
			})
		case r.URL.Path == "/api/market/icon":
			w.Header().Set("Content-Type", "image/svg+xml")
			_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"></svg>`))
		case r.URL.Path == "/api/maintenance" || strings.HasSuffix(r.URL.Path, "/maintenance"):
			_ = json.NewEncoder(w).Encode(apitypes.MaintenanceStatus{
				Runtime: map[string]any{"status": "ok"},
			})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer mockSrv.Close()

	rt := &gatewayRuntime{
		UIURL:  mockSrv.URL,
		MCPURL: boot.MCPURL,
		Mode:   modeAttached,
	}
	uiApp := NewDesktopUI(rt)
	uiApp.State = &boot
	uiApp.Telemetry = ComputeGatewayTelemetry(boot)
	uiApp.Loading = false

	tester := ui.NewTester(uiApp.View, 1360, 860)

	// 1. Overview (KPIs, 12h Pulse, Recent Requests / Upstream Switcher, Slide Drawer).
	if !tester.HasText("概览") || !tester.HasText("请求脉冲") {
		t.Fatal("expected overview heading and 12h pulse card")
	}
	if !tester.HasText("GitHub MCP") || !tester.HasText("refund customer invoice") {
		t.Fatal("expected overview to render realistic service and recent request item")
	}
	if err := tester.Click("refund customer invoice"); err != nil {
		t.Fatalf("open overview slide drawer: %v", err)
	}
	if !tester.HasText("请求详情") || !tester.HasText("按 Esc 关闭") {
		t.Fatal("expected overview slide-over drawer to open")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close overview slide drawer: %v", err)
	}
	if err := tester.Click("高频工具"); err != nil {
		t.Fatalf("switch overview right tab to top tools: %v", err)
	}
	if !tester.HasText("list_prs") {
		t.Fatal("expected top tools tab to render list_prs")
	}
	if err := tester.Click("连接客户端"); err != nil {
		t.Fatalf("open clients page: %v", err)
	}
	if uiApp.CurrentView() != ViewClients {
		t.Fatal("expected client configuration page")
	}

	// 2. Services list, Add Service Modal, and Service Detail tabs.
	if err := tester.Click("上游服务"); err != nil {
		t.Fatalf("nav services: %v", err)
	}
	if !tester.HasText("Billing REST API") {
		t.Fatal("expected services list to render Billing REST API")
	}
	if err := tester.Click("添加上游服务"); err != nil {
		t.Fatalf("open add service modal: %v", err)
	}
	for _, tab := range []string{"MCP 远程 HTTP / SSE", "OpenAPI / Swagger 导入", "粘贴 mcpServers JSON", "MCP Stdio 本地进程"} {
		if err := tester.Click(tab); err != nil {
			t.Fatalf("click add modal tab %q: %v", tab, err)
		}
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close add service modal: %v", err)
	}
	if err := tester.Click("管理详情"); err != nil {
		t.Fatalf("open service detail: %v", err)
	}
	if !tester.HasText("← 返回上游服务列表") {
		t.Fatal("expected service detail back button")
	}
	for _, subtab := range []string{"连接与认证配置", "健康与维护概览"} {
		if err := tester.Click(subtab); err != nil {
			t.Fatalf("click detail subtab %q: %v", subtab, err)
		}
	}
	// Let any triggered async maintenance fetch settle before leaving detail view.
	tester.Advance(50 * time.Millisecond)
	if err := tester.Click("← 返回上游服务列表"); err != nil {
		t.Fatalf("back from service detail: %v", err)
	}

	// 2b. MCP Market.
	mktState := uiApp.marketState()
	mktState.loaded = true
	mktState.items = []apitypes.MarketServer{
		{
			ID:          "notion",
			ServerID:    "notion",
			Title:       "Notion",
			Publisher:   "Notion",
			Transport:   "http",
			Runs:        "远程",
			Endpoint:    "https://mcp.notion.com/mcp",
			Description: "检索并更新 Notion 工作区页面与数据库。",
			SignIn:      true,
			Featured:    true,
			Inputs:      []apitypes.MarketInput{},
		},
		{
			ID:          "context7",
			ServerID:    "context7",
			Title:       "Context7",
			Publisher:   "Upstash",
			Transport:   "http",
			Runs:        "远程",
			Endpoint:    "https://mcp.context7.com/mcp",
			Description: "任意库的最新文档与代码示例。",
			Featured:    true,
			Inputs:      []apitypes.MarketInput{},
		},
		{
			ID:          "github",
			ServerID:    "github",
			Title:       "GitHub",
			Publisher:   "GitHub",
			Transport:   "http",
			Runs:        "远程",
			Endpoint:    "https://api.githubcopilot.com/mcp/",
			Description: "GitHub 仓库、Issue 与 PR。",
			Featured:    true,
			Installed:   true,
			Inputs: []apitypes.MarketInput{
				{Key: "Authorization", Where: "header", Label: "Personal Access Token", Secret: true, Required: true},
			},
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
			Description: "基于 Brave 独立索引的网页搜索。",
			Featured:    true,
			Inputs: []apitypes.MarketInput{
				{Key: "BRAVE_API_KEY", Where: "env", Label: "Brave Search API Key", Secret: true, Required: true},
			},
		},
	}
	if err := tester.Click("MCP 市场"); err != nil {
		t.Fatalf("nav market: %v", err)
	}
	if !tester.HasText("Notion") || !tester.HasText("需登录") || !tester.HasText("云端 SaaS") || !tester.HasText("Context7") || !tester.HasText("Brave Search") || !tester.HasText("需凭据") {
		t.Fatal("expected market SaaS/local curated cards and auth tags to render")
	}
	if err := tester.Click("添加..."); err != nil {
		t.Fatalf("open market install modal: %v", err)
	}
	if !tester.HasText("添加 Brave Search") || !tester.HasText("Brave Search API Key *") {
		t.Fatal("expected market install modal with required input")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close market install modal: %v", err)
	}

	// 3. Catalog & Tool Detail Inspector Panel.
	if err := tester.Click("工具目录"); err != nil {
		t.Fatalf("nav catalog: %v", err)
	}
	if !tester.HasText("list_prs") {
		t.Fatal("expected catalog to render list_prs")
	}
	if err := tester.Click("详情"); err != nil {
		t.Fatalf("click tool inspect button: %v", err)
	}
	if !tester.HasText("工具完整 ID") || !tester.HasText("功能描述") {
		t.Fatal("expected catalog tool detail inspector to render")
	}

	// 4. Playground.
	if err := tester.Click("路由沙盒"); err != nil {
		t.Fatalf("nav playground: %v", err)
	}
	if !tester.HasText("路由沙盒") || !tester.HasText("发起路由发现") {
		t.Fatal("expected playground controls to render")
	}

	// 5. Logs & Trace Detail Panel.
	if err := tester.Click("请求日志"); err != nil {
		t.Fatalf("nav logs: %v", err)
	}
	if !tester.HasText("refund customer invoice") {
		t.Fatal("expected logs timeline to render events")
	}
	if err := tester.Click("refund customer invoice"); err != nil {
		t.Fatalf("select log event: %v", err)
	}
	if !tester.HasText("完整事件 JSON") || !tester.HasText("Detail 详情载荷") {
		t.Fatal("expected logs trace inspector payload tabs")
	}
	if err := tester.Click("Detail 详情载荷"); err != nil {
		t.Fatalf("click logs payload tab: %v", err)
	}

	// 6. Settings.
	if err := tester.Click("访问与路由"); err != nil {
		t.Fatalf("nav settings: %v", err)
	}
	if !tester.HasText("调用身份与写操作护栏矩阵") || !tester.HasText("Decisions 语义路由模型") || !tester.HasText("已保存的模型") {
		t.Fatal("expected settings matrix, model config, and saved profiles list to render")
	}
	if !tester.HasText("测试连接") || !tester.HasText("保存配置") {
		t.Fatal("expected inline model ping and save buttons in model card")
	}
	if tester.HasText("清除内存中的 API Key") || tester.HasText("仅驻留内存不落盘") {
		t.Fatal("did not expect legacy clear-key checkbox or memory-only helper text")
	}
	if tester.HasText("运行路由校验") {
		t.Fatal("expected advanced routing policy & verification to be collapsed by default")
	}
	if err := tester.Click("▸ 高级设置 · 路由策略与校验"); err != nil {
		t.Fatalf("expand advanced settings: %v", err)
	}
	if !tester.HasText("单次返回上限 (1–12)") || !tester.HasText("运行路由校验") {
		t.Fatal("expected advanced routing fields after expanding")
	}

	// 7. Shared-gateway Login screen.
	uiApp.NeedsLogin = true
	tester.Frame()
	if !tester.HasText("登录") {
		t.Fatal("expected shared-gateway login screen to render")
	}
}

func TestDescriptionCleaningAndStructuredRendering(t *testing.T) {
	raw := "## search_issues\n\n搜索 GitHub Issues 与 Pull Requests，支持按状态和标签过滤。更多细节请参考上游文档。\n\n- 支持 `is:open` 与 `label:bug` 语法\n- 自动分页返回前 50 条\n\nArgs:\n  query (string): 搜索关键词表达式\n  limit (int): 最大返回条数\n"

	gotSummary := CleanDescriptionSummary(raw)
	wantSummary := "搜索 GitHub Issues 与 Pull Requests，支持按状态和标签过滤。"
	if gotSummary != wantSummary {
		t.Fatalf("CleanDescriptionSummary = %q, want %q", gotSummary, wantSummary)
	}

	inlineArgs := "Fetch repository commits from default branch. Args: owner (str), repo (str)"
	if got := CleanDescriptionSummary(inlineArgs); got != "Fetch repository commits from default branch." {
		t.Fatalf("inline Args tail not stripped: %q", got)
	}

	parsed := ParseStructuredDescription(raw)
	if parsed.Lead == "" || len(parsed.Bullets) != 2 || len(parsed.Params) != 2 || !parsed.HasMoreRaw {
		t.Fatalf("unexpected ParseStructuredDescription: %+v", parsed)
	}

	tester := ui.NewTester(func(c *ui.Context) {
		applySailTheme(c)
		SailDescriptionDetail(c, "test-desc", raw)
	}, 640, 480)

	if !tester.HasText("参数说明") || !tester.HasText("展开完整原始描述") {
		t.Fatal("expected structured params and raw disclosure toggle")
	}
	if err := tester.Click("展开完整原始描述"); err != nil {
		t.Fatalf("click raw toggle: %v", err)
	}
	if !tester.HasText("收起原始描述") {
		t.Fatal("expected raw description block to expand")
	}
}

func waitSettingsIdle(t *testing.T, tester *ui.Tester, uiApp *DesktopUI) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tester.Frame()
		done := func() bool {
			leave := uiApp.enterHeadlessFrame()
			defer leave()
			return uiApp.BusyCount == 0
		}()
		if done {
			tester.Frame()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for settings async work")
		}
		time.Sleep(2 * time.Millisecond)
		tester.Advance(10 * time.Millisecond)
	}
}

func TestSettingsSaveRequiresSuccessfulPing(t *testing.T) {
	var saveCalls, testCalls atomic.Int32
	var pingShouldPass atomic.Bool

	boot := apitypes.Bootstrap{
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{{ID: "github", Name: "GitHub", Enabled: true}},
			Tools:   []apitypes.Tool{{ID: "github.list", ServerID: "github", Name: "list", Enabled: true, ReadOnly: true}},
		},
		Settings: apitypes.Settings{
			BaseURL:        "https://tokenhub.infplacex.com/systemone",
			Model:          "jev",
			MaxTools:       4,
			TimeoutSeconds: 45,
		},
		KeyConfigured: true,
		Profiles: []apitypes.ModelProfile{{
			ID:             "default",
			BaseURL:        "https://tokenhub.infplacex.com/systemone",
			Model:          "jev",
			MaxTools:       4,
			TimeoutSeconds: 45,
			Active:         true,
			KeyConfigured:  true,
		}},
		Setup: apitypes.SetupStatus{
			Sources:         1,
			EnabledTools:    1,
			ToolsReady:      true,
			ModelConfigured: true,
			ModelReady:      false,
		},
	}

	var srvMu sync.Mutex
	srvBoot := boot
	srvBoot.Profiles = append([]apitypes.ModelProfile(nil), boot.Profiles...)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/bootstrap":
			srvMu.Lock()
			_ = json.NewEncoder(w).Encode(srvBoot)
			srvMu.Unlock()
		case "/api/auth/me":
			_ = json.NewEncoder(w).Encode(apitypes.AuthMe{Authenticated: true})
		case "/api/model/ping":
			if !pingShouldPass.Load() {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":  "模型连接测试失败",
					"detail": "请求返回了 HTML 页面而非 JSON",
				})
				return
			}
			srvMu.Lock()
			modelName := srvBoot.Settings.Model
			srvMu.Unlock()
			_ = json.NewEncoder(w).Encode(apitypes.PingModelResult{OK: true, Model: modelName, DurationMS: 42})
		case "/api/model-profiles":
			saveCalls.Add(1)
			var in apitypes.ModelProfile
			_ = json.NewDecoder(r.Body).Decode(&in)
			in.ID = "default"
			in.Active = true
			in.KeyConfigured = true
			srvMu.Lock()
			srvBoot.Settings = apitypes.Settings{
				BaseURL:        in.BaseURL,
				Model:          in.Model,
				MaxTools:       in.MaxTools,
				TimeoutSeconds: in.TimeoutSeconds,
			}
			srvBoot.Profiles[0] = in
			srvMu.Unlock()
			_ = json.NewEncoder(w).Encode(in)
		case "/api/setup/model":
			testCalls.Add(1)
			_ = json.NewEncoder(w).Encode(apitypes.DiscoverResult{Status: "selected", DurationMS: 80})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	rt := &gatewayRuntime{UIURL: srv.URL, MCPURL: srv.URL + "/mcp", Mode: modeAttached}
	uiApp := NewDesktopUI(rt)
	uiApp.State = &boot
	uiApp.Telemetry = ComputeGatewayTelemetry(boot)
	uiApp.Loading = false

	tester := ui.NewTester(uiApp.View, 1360, 1200)
	if err := tester.Click("访问与路由"); err != nil {
		t.Fatalf("nav settings: %v", err)
	}

	// 1. Before ping passes, save must be disabled and clicking it does nothing.
	if !tester.HasText("通过「测试连接」后才可保存配置") {
		t.Fatal("expected ping-required prompt before testing connection")
	}
	_ = tester.Click("保存配置")
	waitSettingsIdle(t, tester, uiApp)
	if saveCalls.Load() != 0 {
		t.Fatalf("expected save button to be disabled before ping, got %d save calls", saveCalls.Load())
	}

	// 2. When ping fails, save must remain disabled and running routing test must not save.
	if err := tester.Click("测试连接"); err != nil {
		t.Fatalf("click test connection: %v", err)
	}
	waitSettingsIdle(t, tester, uiApp)
	if !tester.HasText("模型连接测试失败") {
		t.Fatal("expected ping failure message")
	}
	_ = tester.Click("保存配置")
	waitSettingsIdle(t, tester, uiApp)
	if saveCalls.Load() != 0 {
		t.Fatalf("expected failed ping to block save, got %d save calls", saveCalls.Load())
	}
	if err := tester.Click("▸ 高级设置 · 路由策略与校验"); err != nil {
		t.Fatalf("expand advanced settings: %v", err)
	}
	if err := tester.Click("运行路由校验"); err != nil {
		t.Fatalf("click routing verify: %v", err)
	}
	waitSettingsIdle(t, tester, uiApp)
	if saveCalls.Load() != 0 || testCalls.Load() != 0 {
		t.Fatalf("expected failed ping inside routing verify to block save/test, got save=%d test=%d", saveCalls.Load(), testCalls.Load())
	}

	// 3. Once ping passes, save becomes enabled and persists the profile.
	pingShouldPass.Store(true)
	if err := tester.Click("测试连接"); err != nil {
		t.Fatalf("click test connection (passing): %v", err)
	}
	waitSettingsIdle(t, tester, uiApp)
	if !tester.HasText("模型测试通过") {
		t.Fatal("expected ping success status")
	}
	if err := tester.Click("保存配置"); err != nil {
		t.Fatalf("click save after ping: %v", err)
	}
	waitSettingsIdle(t, tester, uiApp)
	if saveCalls.Load() != 1 {
		t.Fatalf("expected 1 save call after passing ping, got %d", saveCalls.Load())
	}

	// 4. Editing a preset / connection field invalidates the passed ping and blocks save again.
	if err := tester.Click("OpenAI 预设"); err != nil {
		t.Fatalf("click OpenAI preset: %v", err)
	}
	tester.Frame()
	if !tester.HasText("通过「测试连接」后才可保存配置") {
		t.Fatal("expected preset edit to clear ping state and require re-test")
	}
	_ = tester.Click("保存配置")
	waitSettingsIdle(t, tester, uiApp)
	if saveCalls.Load() != 1 {
		t.Fatalf("expected save to be blocked after modifying config, got %d", saveCalls.Load())
	}
}
