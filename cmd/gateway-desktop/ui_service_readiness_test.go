package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestIssue63MissingCredentialLifecycleAndRecovery(t *testing.T) {
	historicalCheckAt := time.Date(2026, 11, 14, 17, 34, 16, 0, time.Local)
	invokeFailAt := time.Date(2026, 11, 17, 19, 10, 0, 0, time.Local)
	recoveredCheckAt := time.Date(2026, 11, 18, 15, 5, 0, 0, time.Local)

	invokeErrDetail, _ := json.Marshal(map[string]any{
		"tool_id": "github.list_pull_requests",
		"error":   "下游工具调用失败: gateway process environment is missing: GITHUB_TOKEN; export the variables and restart the gateway",
	})

	const secretToken = "ghp_super_secret_dogfood_token_value"

	var (
		mu          sync.Mutex
		savedToken  string
		probeCalled int
	)

	makeBootstrap := func() apitypes.Bootstrap {
		mu.Lock()
		defer mu.Unlock()
		hasToken := savedToken != ""
		chk := apitypes.ConnectionCheck{
			Status:    "success",
			Message:   "MCP 已连接，读取到 1 个工具。",
			CheckedAt: historicalCheckAt,
			ToolCount: 1,
			Stale:     !hasToken,
		}
		if probeCalled > 0 && hasToken {
			chk.CheckedAt = recoveredCheckAt
			chk.Stale = false
		}
		return apitypes.Bootstrap{
			MCPURL:                "http://127.0.0.1:8789/mcp",
			KeyConfigured:         true,
			CredentialsPersistent: true,
			Registry: apitypes.Registry{
				Servers: []apitypes.Server{
					{
						ID:        "github",
						Name:      "GitHub",
						Kind:      "mcp",
						Transport: "http",
						Endpoint:  "https://api.githubcopilot.com/mcp/",
						Headers:   map[string]string{"Authorization": "Bearer ${GITHUB_TOKEN}"},
						Enabled:   true,
					},
				},
				Tools: []apitypes.Tool{
					{
						ID:          "github.list_pull_requests",
						ServerID:    "github",
						Name:        "list_pull_requests",
						Description: "List pull requests in a repository.",
						Enabled:     true,
						ReadOnly:    true,
						Actors:      []string{"developer"},
						InputSchema: map[string]any{"type": "object"},
					},
				},
			},
			Setup: apitypes.SetupStatus{
				Sources:         1,
				EnabledTools:    1,
				ToolsReady:      true,
				ModelConfigured: true,
				ModelReady:      true,
				SourceChecks: map[string]apitypes.ConnectionCheck{
					"github": chk,
				},
			},
			Events: []apitypes.Event{
				{
					ID:         "ev-gh-502",
					Time:       invokeFailAt,
					Kind:       "invoke",
					Actor:      "developer",
					Status:     "error",
					Title:      "github.list_pull_requests",
					DurationMS: 12,
					Detail:     invokeErrDetail,
				},
			},
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(makeBootstrap())
		case r.Method == http.MethodGet && r.URL.Path == "/api/servers/github/credentials":
			mu.Lock()
			configured := savedToken != ""
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(apitypes.ServerCredentials{
				Persistent: true,
				Credentials: []apitypes.CredentialStatus{
					{Name: "GITHUB_TOKEN", Configured: configured, Saved: configured, Environment: false},
				},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/servers/github/credentials":
			var req apitypes.SaveCredentialsRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode credentials request: %v", err)
			}
			mu.Lock()
			savedToken = req.Values["GITHUB_TOKEN"]
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(apitypes.Saved{Saved: true})
		case r.Method == http.MethodPost && r.URL.Path == "/api/setup/source":
			mu.Lock()
			probeCalled++
			ok := savedToken != ""
			mu.Unlock()
			if !ok {
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(apitypes.Error{
					Error:  "missing credential",
					Detail: "gateway process environment is missing: GITHUB_TOKEN",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(apitypes.ConnectionCheck{
				Status:    "success",
				Message:   "MCP 已连接，读取到 1 个工具。",
				CheckedAt: recoveredCheckAt,
				ToolCount: 1,
				Stale:     false,
			})
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	app := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: "http://127.0.0.1:8789/mcp", Mode: modeAttached})
	if err := app.RefreshSync(); err != nil {
		t.Fatalf("initial RefreshSync: %v", err)
	}

	tester := ui.NewTester(app.View, 1360, 860)
	waitAsyncIdle(t, tester, app)

	// 1. Overview KPI & service list must NOT claim GitHub is ready/green.
	if !tester.HasText("1 异常") || !tester.HasText("缺少凭据") {
		t.Fatal("expected Overview to surface missing credential status instead of healthy online")
	}

	// 2. Navigate to Services -> GitHub Detail (Subtab 0 by default).
	app.OpenServiceDetail("github")
	tester.Advance(20 * time.Millisecond)
	waitAsyncIdle(t, tester, app)

	if !tester.HasText("当前缺少凭据：GITHUB_TOKEN") {
		t.Fatal("expected Service Detail first screen to highlight missing GITHUB_TOKEN")
	}
	if !tester.HasText("历史通过 · " + FormatTime(historicalCheckAt)) {
		t.Fatalf("expected historical check timestamp preserved as non-current history, want %q", "历史通过 · "+FormatTime(historicalCheckAt))
	}
	if tester.HasText("就绪") {
		t.Fatal("must not display 就绪 while GITHUB_TOKEN is missing")
	}

	// 3. Click "填写服务凭据" on the first-screen alert banner -> switches to Subtab 1.
	if err := tester.Click("填写服务凭据"); err != nil {
		t.Fatalf("click 填写服务凭据: %v", err)
	}
	waitAsyncIdle(t, tester, app)
	if app.servicesState().detailSubtab != 1 {
		t.Fatalf("expected detailSubtab=1 (连接与认证配置), got %d", app.servicesState().detailSubtab)
	}

	// Stage secret value in credential input; verify plaintext secret is never exposed in UI text.
	app.servicesState().credentialValues["GITHUB_TOKEN"] = secretToken
	tester.Advance(20 * time.Millisecond)
	if tester.HasText(secretToken) {
		t.Fatal("secret token leaked into rendered UI text")
	}

	// 4. Click "保存凭据并测试连接" -> saves to credential store, runs TestSource, and restores healthy Ready state.
	if err := tester.Click("保存凭据并测试连接"); err != nil {
		t.Fatalf("click 保存凭据并测试连接: %v", err)
	}
	waitAsyncIdle(t, tester, app)

	mu.Lock()
	if savedToken != secretToken || probeCalled == 0 {
		mu.Unlock()
		t.Fatalf("expected credential saved and TestSource called, got saved=%v probeCalled=%d", savedToken != "", probeCalled)
	}
	mu.Unlock()

	if tester.HasText(secretToken) {
		t.Fatal("secret token leaked after save")
	}
	if !tester.HasText("就绪") || tester.HasText("缺少凭据") {
		t.Fatal("expected service status to recover to 就绪 after credential save and connection test")
	}
	if !tester.HasText(FormatTime(recoveredCheckAt)) {
		t.Fatalf("expected updated check timestamp %q after recovery", FormatTime(recoveredCheckAt))
	}
}

func TestEvaluateServiceReadinessStates(t *testing.T) {
	baseTime := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	srv := apitypes.Server{
		ID:      "linear",
		Name:    "Linear",
		Kind:    "mcp",
		Enabled: true,
	}
	openTools := []apitypes.Tool{
		{ID: "linear.list_issues", ServerID: "linear", Name: "list_issues", Enabled: true, ReadOnly: true},
	}
	closedTools := []apitypes.Tool{
		{ID: "linear.list_issues", ServerID: "linear", Name: "list_issues", Enabled: false, ReadOnly: true},
	}

	// 1. Never checked -> readinessUnverified ("待验证"), never green.
	r := evaluateServiceReadiness(srv, openTools, apitypes.ConnectionCheck{}, false, nil, nil)
	if r.Kind != readinessUnverified || r.BadgeLabel != "待验证" {
		t.Fatalf("expected unverified when no check exists, got kind=%v badge=%q", r.Kind, r.BadgeLabel)
	}

	// 2. Stale check -> readinessUnverified ("待复测 · ...").
	staleCheck := apitypes.ConnectionCheck{Status: "success", CheckedAt: baseTime, Stale: true}
	r = evaluateServiceReadiness(srv, openTools, staleCheck, true, nil, nil)
	if r.Kind != readinessUnverified || r.CheckDisplay != checkDisplayWarn {
		t.Fatalf("expected stale check to be unverified warning, got kind=%v disp=%v", r.Kind, r.CheckDisplay)
	}

	// 3. Verified check, but 0 tools enabled -> readinessNoOpenTools ("未开放工具").
	freshCheck := apitypes.ConnectionCheck{Status: "success", CheckedAt: baseTime, Stale: false}
	r = evaluateServiceReadiness(srv, closedTools, freshCheck, true, nil, nil)
	if r.Kind != readinessNoOpenTools || r.BadgeLabel != "未开放工具" {
		t.Fatalf("expected no-open-tools warning, got kind=%v badge=%q", r.Kind, r.BadgeLabel)
	}

	// 4. Caller-side schema error after fresh check must NOT mark upstream service unhealthy.
	schemaErrDetail, _ := json.Marshal(map[string]any{"error": "参数不符合工具 schema: missing required field"})
	callerEvents := []apitypes.Event{
		{Kind: "invoke", Status: "error", Title: "linear.list_issues", Time: baseTime.Add(time.Minute), Detail: schemaErrDetail},
	}
	r = evaluateServiceReadiness(srv, openTools, freshCheck, true, nil, callerEvents)
	if !r.IsHealthyReady() {
		t.Fatalf("expected caller schema validation error to keep service Ready, got kind=%v", r.Kind)
	}

	// 5. Upstream invoke 502 after fresh check -> readinessInvokeError while preserving historical CheckedAt.
	upErrDetail, _ := json.Marshal(map[string]any{"error": "下游工具调用失败: HTTP 401 unauthorized"})
	failEvents := []apitypes.Event{
		{Kind: "invoke", Status: "error", Title: "linear.list_issues", Time: baseTime.Add(2 * time.Minute), Detail: upErrDetail},
	}
	r = evaluateServiceReadiness(srv, openTools, freshCheck, true, nil, failEvents)
	if r.Kind != readinessInvokeError || r.BadgeLabel != "调用失败" || r.CheckDisplay == checkDisplaySuccess {
		t.Fatalf("expected post-check upstream invoke error to mark 调用失败 without green check, got %+v", r)
	}

	// 6. Newer successful invoke clears the previous invoke failure -> Ready again!
	recoveredEvents := append(failEvents, apitypes.Event{
		Kind: "invoke", Status: "success", Title: "linear.list_issues", Time: baseTime.Add(3 * time.Minute),
	})
	r = evaluateServiceReadiness(srv, openTools, freshCheck, true, nil, recoveredEvents)
	if !r.IsHealthyReady() {
		t.Fatalf("expected subsequent successful invoke to restore Ready, got kind=%v", r.Kind)
	}
}
