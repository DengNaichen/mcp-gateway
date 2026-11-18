package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo/ui"
)

func TestScopeAPIClientContract(t *testing.T) {
	var (
		mu       sync.Mutex
		recorded []struct {
			Method string
			URI    string
			Body   string
		}
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		recorded = append(recorded, struct {
			Method string
			URI    string
			Body   string
		}{
			Method: r.Method,
			URI:    r.URL.RequestURI(),
			Body:   strings.TrimSpace(string(raw)),
		})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_, _ = w.Write([]byte(`{
				"mcp_url": "http://127.0.0.1:8788/mcp",
				"scopes": {
					"enforce": true,
					"hook_bin_path": "/Users/test/.mcp-gateway/bin/mcp-gateway-hook",
					"repositories": [
						{
							"id": "repo-1",
							"name": "mcp-gateway",
							"git_common_dirs": ["/work/mcp-gateway/.git"],
							"root_paths": ["/work/mcp-gateway"],
							"remote_hint": "https://github.com/DengNaichen/mcp-gateway.git",
							"created_at": "2026-11-16T10:00:00Z"
						}
					],
					"rules": [
						{"repo_key": "*", "grants": []},
						{"repo_key": "repo-1", "grants": [{"server_id": "github", "access": "ro"}]}
					],
					"observed": [
						{
							"fingerprint": "fp-1",
							"name": "demo-app",
							"kind": "git",
							"root": "/work/demo-app",
							"git_common_dir": "/work/demo-app/.git",
							"remote_hint": "github.com/acme/demo-app",
							"last_agent": "claude",
							"last_seen": "2026-11-16T10:05:00Z",
							"request_count": 3
						}
					]
				}
			}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/scopes":
			_, _ = w.Write([]byte(`{"enforce":true,"hook_bin_path":"/bin/hook","repositories":[],"rules":[],"observed":[]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/mode":
			_, _ = w.Write([]byte(`{"enforce":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/repositories":
			_, _ = w.Write([]byte(`{"id":"repo-2","name":"new-repo","git_common_dirs":["/tmp/new/.git"],"root_paths":["/tmp/new"],"remote_hint":"","created_at":"2026-11-16T10:10:00Z"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/scopes/repositories/repo-2":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/rules":
			_, _ = w.Write(raw)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/scopes/rules":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/scopes/snippets":
			_, _ = w.Write([]byte(`{
				"agent": "opencode",
				"config_path": "/Users/test/.config/opencode/plugins/mcp-gateway.js",
				"mcp_url": "http://127.0.0.1:8788/mcp?agent=opencode",
				"hook_bin_path": "/Users/test/.mcp-gateway/bin/mcp-gateway-hook",
				"instructions": "Save plugin and restart OpenCode.",
				"snippet": "export default async function plugin(ctx) {}"
			}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/discover":
			_, _ = w.Write([]byte(`{"id":"d1","status":"selected","summary":"ok","tools":[],"eligible":1}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/invoke":
			_, _ = w.Write([]byte(`{"id":"i1","tool_id":"github.list_prs","protocol":"mcp","data":{"ok":true},"duration_ms":12}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	c := apiclient.New(srv.URL)

	boot, err := c.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("apitypes.Bootstrap: %v", err)
	}
	if !boot.Scopes.Enforce || len(boot.Scopes.Repositories) != 1 || len(boot.Scopes.Observed) != 1 {
		t.Fatalf("unexpected apitypes.Bootstrap.Scopes: %+v", boot.Scopes)
	}
	if boot.Scopes.Repositories[0].ID != "repo-1" || boot.Scopes.Observed[0].Fingerprint != "fp-1" {
		t.Fatalf("unexpected repo/observed IDs: %+v", boot.Scopes)
	}

	if _, err := c.GetScopes(ctx); err != nil {
		t.Fatalf("GetScopes: %v", err)
	}
	if err := c.SetScopeMode(ctx, true); err != nil {
		t.Fatalf("SetScopeMode: %v", err)
	}
	savedRepo, err := c.SaveRepository(ctx, apitypes.SaveRepositoryRequest{Name: "new-repo", Path: "/tmp/new"})
	if err != nil || savedRepo.ID != "repo-2" {
		t.Fatalf("SaveRepository: repo=%+v err=%v", savedRepo, err)
	}
	if err := c.DeleteRepository(ctx, "repo-2"); err != nil {
		t.Fatalf("DeleteRepository: %v", err)
	}

	// Nil Grants must marshal as `"grants":[]` rather than null.
	emptyRule, err := c.SaveScopeRule(ctx, apitypes.ScopeRule{RepoKey: "*"})
	if err != nil || emptyRule.Grants == nil {
		t.Fatalf("SaveScopeRule empty: rule=%+v err=%v", emptyRule, err)
	}
	if err := c.DeleteScopeRule(ctx, "repo-1"); err != nil {
		t.Fatalf("DeleteScopeRule: %v", err)
	}

	snip, err := c.GetHookSnippet(ctx, "opencode")
	if err != nil || snip.Agent != "opencode" || !strings.Contains(snip.Snippet, "plugin") {
		t.Fatalf("GetHookSnippet: snip=%+v err=%v", snip, err)
	}

	// Discover without sim_scope omits sim_scope; DiscoverScoped includes sim_scope.
	if _, err := c.Discover(ctx, apitypes.DiscoverRequest{Intent: "check prs"}); err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if _, err := c.DiscoverScoped(ctx, apitypes.DiscoverRequest{Intent: "check prs"}, &apitypes.SimScope{RepoKey: "repo-1"}); err != nil {
		t.Fatalf("DiscoverScoped: %v", err)
	}
	if _, err := c.InvokeScoped(ctx, apitypes.InvokeRequest{ToolID: "github.list_prs", Arguments: map[string]any{}}, &apitypes.SimScope{RepoKey: "*"}); err != nil {
		t.Fatalf("InvokeScoped: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	var (
		sawEmptyGrants   bool
		sawDeleteRuleURI bool
		sawNoSimScope    bool
		sawDiscoverScope bool
		sawInvokeScope   bool
	)
	for _, rec := range recorded {
		if rec.Method == http.MethodPut && rec.URI == "/api/scopes/rules" && strings.Contains(rec.Body, `"grants":[]`) {
			sawEmptyGrants = true
		}
		if rec.Method == http.MethodDelete && rec.URI == "/api/scopes/rules?repo_key=repo-1" {
			sawDeleteRuleURI = true
		}
		if rec.Method == http.MethodPost && rec.URI == "/api/discover" && !strings.Contains(rec.Body, `"sim_scope"`) {
			sawNoSimScope = true
		}
		if rec.Method == http.MethodPost && rec.URI == "/api/discover" && strings.Contains(rec.Body, `"sim_scope":{"repo_key":"repo-1"}`) {
			sawDiscoverScope = true
		}
		if rec.Method == http.MethodPost && rec.URI == "/api/invoke" && strings.Contains(rec.Body, `"sim_scope":{"repo_key":"*"}`) {
			sawInvokeScope = true
		}
	}
	if !sawEmptyGrants || !sawDeleteRuleURI || !sawNoSimScope || !sawDiscoverScope || !sawInvokeScope {
		t.Fatalf("wire contract mismatch: emptyGrants=%v delRule=%v noSim=%v discSim=%v invSim=%v",
			sawEmptyGrants, sawDeleteRuleURI, sawNoSimScope, sawDiscoverScope, sawInvokeScope)
	}
}

func TestReposPageMasterDetailAndCrossViewIntegration(t *testing.T) {
	now := time.Now()
	scopeLogDetail, _ := json.Marshal(map[string]any{
		"eligible": 1,
		"summary":  "Scoped discovery in observed repo",
		"scope": map[string]any{
			"agent":          "claude",
			"repo_id":        "*",
			"repo_name":      "unconfigured-svc",
			"root":           "/tmp/wt-unconfigured-svc",
			"git_common_dir": "/work/unconfigured-svc/.git",
			"remote_hint":    "github.com/DengNaichen/unconfigured-svc",
			"matched_rule":   "*",
			"status":         "verified_unconfigured",
		},
		"tools": []map[string]any{
			{"id": "github.list_prs", "reason": "Read-only PR lookup"},
		},
	})

	var stateMu sync.Mutex
	boot := apitypes.Bootstrap{
		MCPURL:        "http://127.0.0.1:8788/mcp",
		KeyConfigured: true,
		Access:        apitypes.AccessInfo{Shared: false, Actor: "developer"},
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{
					ID:          "github",
					Name:        "GitHub MCP",
					Description: "Pull request and issue operations",
					Kind:        "mcp",
					Transport:   "stdio",
					Command:     "npx",
					Enabled:     true,
				},
				{
					ID:          "roster",
					Name:        "Roster Tasks",
					Description: "Task and Spec management",
					Kind:        "mcp",
					Transport:   "http",
					Endpoint:    "https://example.com/mcp",
					Enabled:     true,
				},
			},
			Tools: []apitypes.Tool{
				{
					ID:          "github.list_prs",
					ServerID:    "github",
					Name:        "list_prs",
					Description: "List open pull requests.",
					Enabled:     true,
					ReadOnly:    true,
					Actors:      []string{"developer", "observer"},
				},
				{
					ID:          "github.merge_pr",
					ServerID:    "github",
					Name:        "merge_pr",
					Description: "Merge a pull request.",
					Enabled:     true,
					ReadOnly:    false,
					Actors:      []string{"developer"},
				},
				{
					ID:          "roster.list_tasks",
					ServerID:    "roster",
					Name:        "list_tasks",
					Description: "List team tasks.",
					Enabled:     true,
					ReadOnly:    true,
					Actors:      []string{"developer"},
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
			EnabledTools:    3,
			ToolsReady:      true,
			ModelConfigured: true,
			ModelReady:      true,
		},
		Scopes: apitypes.ScopeOverview{
			Enforce:     false,
			HookBinPath: "/Users/test/.mcp-gateway/bin/mcp-gateway-hook",
			Repositories: []apitypes.Repository{
				{
					ID:            "repo-gw",
					Name:          "mcp-gateway",
					GitCommonDirs: []string{"/work/mcp-gateway/.git"},
					RootPaths:     []string{"/work/mcp-gateway"},
					RemoteHint:    "https://github.com/DengNaichen/mcp-gateway.git",
					CreatedAt:     "2026-11-16T10:00:00Z",
				},
			},
			Rules: []apitypes.ScopeRule{
				{
					RepoKey: "*",
					Grants:  []apitypes.ServiceGrant{},
				},
				{
					RepoKey: "repo-gw",
					Grants: []apitypes.ServiceGrant{
						{ServerID: "github", Access: "rw"},
						{ServerID: "roster", Access: "ro"},
					},
				},
			},
			Observed: []apitypes.ObservedRepo{
				{
					Fingerprint:  "fp-obs-1",
					Name:         "new-service",
					Kind:         "git",
					Root:         "/tmp/wt-new-service",
					GitCommonDir: "/work/new-service/.git",
					RemoteHint:   "github.com/DengNaichen/new-service",
					LastAgent:    "cursor",
					RequestCount: 2,
				},
			},
		},
		Events: []apitypes.Event{
			{
				ID:         "ev-scope-1",
				Time:       now,
				Kind:       "discover",
				Actor:      "developer",
				Status:     "selected",
				Title:      "check open PRs in unconfigured repo",
				DurationMS: 29,
				Detail:     scopeLogDetail,
			},
		},
	}

	var (
		lastDiscoverBody string
		savedRepoBodies  []string
	)
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")

		stateMu.Lock()
		defer stateMu.Unlock()

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)

		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/mode":
			var in struct {
				Enforce bool `json:"enforce"`
			}
			_ = json.Unmarshal(raw, &in)
			boot.Scopes.Enforce = in.Enforce
			_ = json.NewEncoder(w).Encode(in)

		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/repositories":
			savedRepoBodies = append(savedRepoBodies, strings.TrimSpace(string(raw)))
			var in apitypes.SaveRepositoryRequest
			_ = json.Unmarshal(raw, &in)
			newID := "repo-" + in.Name
			repo := apitypes.Repository{
				ID:            newID,
				Name:          in.Name,
				GitCommonDirs: in.GitCommonDirs,
				RootPaths:     in.RootPaths,
				RemoteHint:    in.RemoteHint,
			}
			if in.Path != "" && len(repo.RootPaths) == 0 {
				repo.RootPaths = []string{in.Path}
			}
			boot.Scopes.Repositories = append(boot.Scopes.Repositories, repo)
			boot.Scopes.Observed = slicesDeleteObserved(boot.Scopes.Observed, in.Name)
			_ = json.NewEncoder(w).Encode(repo)

		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/rules":
			var rule apitypes.ScopeRule
			_ = json.Unmarshal(raw, &rule)
			replaced := false
			for i := range boot.Scopes.Rules {
				if boot.Scopes.Rules[i].RepoKey == rule.RepoKey {
					boot.Scopes.Rules[i] = rule
					replaced = true
					break
				}
			}
			if !replaced {
				boot.Scopes.Rules = append(boot.Scopes.Rules, rule)
			}
			_ = json.NewEncoder(w).Encode(rule)

		case r.Method == http.MethodDelete && r.URL.Path == "/api/scopes/rules":
			rk := r.URL.Query().Get("repo_key")
			var kept []apitypes.ScopeRule
			for _, item := range boot.Scopes.Rules {
				if item.RepoKey == rk {
					continue
				}
				kept = append(kept, item)
			}
			boot.Scopes.Rules = kept
			_, _ = w.Write([]byte(`{"deleted":true}`))

		case r.Method == http.MethodGet && r.URL.Path == "/api/scopes/snippets":
			ag := r.URL.Query().Get("agent")
			_ = json.NewEncoder(w).Encode(apitypes.ScopeSnippetResponse{
				Agent:        ag,
				ConfigPath:   "/Users/test/." + ag + "/hooks.json",
				MCPURL:       "http://127.0.0.1:8788/mcp?agent=" + ag,
				HookBinPath:  boot.Scopes.HookBinPath,
				Instructions: "将以下片段合并至客户端配置文件：",
				Snippet:      `{"hooks":{"PreToolUse":[{"matcher":"mcp__mcp_gateway__.*"}]}}`,
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/discover":
			lastDiscoverBody = string(raw)
			_ = json.NewEncoder(w).Encode(apitypes.DiscoverResult{
				ID:       "disc-1",
				Status:   "selected",
				Summary:  "Matched scoped tools",
				Eligible: 3,
				Tools: []apitypes.Selection{
					{
						Tool:   boot.Registry.Tools[0],
						Reason: "Scoped match",
					},
				},
			})

		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer mockSrv.Close()

	cfgRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgRoot, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_CLIENT_CONFIG_HOME", cfgRoot)

	rt := &gatewayRuntime{
		UIURL:  mockSrv.URL,
		MCPURL: boot.MCPURL,
		Mode:   modeAttached,
	}
	uiApp := NewDesktopUI(rt)
	stateMu.Lock()
	bootCopy := boot
	stateMu.Unlock()
	uiApp.State = &bootCopy
	uiApp.Telemetry = ComputeGatewayTelemetry(bootCopy)
	uiApp.Loading = false

	tester := ui.NewTester(uiApp.View, 1400, 920)

	// 1. Navigate to 仓库绑定 in sidebar.
	if !tester.HasText("仓库绑定") {
		t.Fatal("expected sidebar to contain 仓库绑定")
	}
	if err := tester.Click("仓库绑定"); err != nil {
		t.Fatalf("click 仓库绑定: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)

	if err := tester.Click("单仓库详情"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()

	// Verify Global Default initial state (`*` empty -> 默认无服务, and no legacy client overrides or 6-client preview).
	if !tester.HasText("全局默认") || !tester.HasText("默认无服务") {
		t.Fatal("expected global default card to render")
	}
	if tester.HasText("客户端例外") || tester.HasText("六端生效预览") {
		t.Fatal("did not expect removed 客户端例外 or 六端生效预览 cards")
	}
	if !tester.HasText("最近请求 · 未绑定") || !tester.HasText("new-service") {
		t.Fatal("expected observed unbound repo section to render")
	}

	// 2. Select configured repo `mcp-gateway` and inspect default services only.
	if err := tester.Click("mcp-gateway"); err != nil {
		t.Fatalf("select repo mcp-gateway: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)

	if !tester.HasText("/work/mcp-gateway/.git") {
		t.Fatal("expected CommonDir in repo detail header")
	}
	if !tester.HasText("仓库默认服务") {
		t.Fatal("expected default services card")
	}
	if tester.HasText("客户端例外") || tester.HasText("六端生效预览") {
		t.Fatal("did not expect removed 客户端例外 or 六端生效预览 cards on repo detail")
	}

	// Expand tool subset drawer on repo default service (`全部工具 (2)`).
	if err := tester.Click("全部工具 (2)"); err != nil {
		t.Fatalf("expand tool subset drawer: %v", err)
	}
	if !tester.HasText("仅选只读工具") || !tester.HasText("merge_pr") {
		t.Fatal("expected tool subset drawer with tools and quick filter")
	}

	// 3. Test Manual Hook Snippet Modal from 仓库绑定.
	if err := tester.Click("手动配置 Hook"); err != nil {
		t.Fatalf("open hook modal: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	if !tester.HasText("PreToolUse") || !tester.HasText("复制配置片段") || !tester.HasText("Antigravity") {
		t.Fatal("expected hook snippet content and Antigravity tab in modal")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close hook modal: %v", err)
	}

	// 4. One-click bind observed Git repo `new-service` (observed from linked worktree `/tmp/wt-new-service`).
	if err := tester.Click("绑定"); err != nil {
		t.Fatalf("click bind observed repo: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	stateMu.Lock()
	repoCountAfterBind := len(boot.Scopes.Repositories)
	var boundNewSvc apitypes.Repository
	if repoCountAfterBind >= 2 {
		boundNewSvc = boot.Scopes.Repositories[1]
	}
	var firstSavedRepoBody string
	if len(savedRepoBodies) > 0 {
		firstSavedRepoBody = savedRepoBodies[0]
	}
	stateMu.Unlock()
	if repoCountAfterBind != 2 {
		t.Fatalf("expected 2 bound repositories after clicking 绑定, got %d", repoCountAfterBind)
	}
	if !strings.Contains(firstSavedRepoBody, `"git_common_dirs":["/work/new-service/.git"]`) ||
		strings.Contains(firstSavedRepoBody, `"root_paths"`) ||
		strings.Contains(firstSavedRepoBody, `"path"`) {
		t.Fatalf("binding Git ObservedRepo must send git_common_dirs without root_paths/path, got: %s", firstSavedRepoBody)
	}
	if gotPrimary := primaryRepoPath(boundNewSvc); gotPrimary != "/work/new-service" {
		t.Fatalf("expected primaryRepoPath to resolve to canonical repo dir /work/new-service, got %q", gotPrimary)
	}

	// Navigate to Playground, select repository `mcp-gateway`, and verify repo-only sim_scope.
	if err := tester.Click("路由沙盒"); err != nil {
		t.Fatalf("nav playground: %v", err)
	}
	tester.Frame()
	if tester.HasText("不限") {
		t.Fatal("did not expect removed simulated client selector in Playground")
	}
	if err := tester.Click("管理员全量"); err != nil {
		t.Fatalf("open sim repo selector: %v", err)
	}
	if err := tester.Click("mcp-gateway"); err != nil {
		t.Fatalf("choose mcp-gateway in sim repo selector: %v", err)
	}
	tester.Frame()
	// For repo-gw: github (rw: 2 tools) + roster (ro: 1 tool) = 3 eligible tools!
	if !tester.HasText("候选 3") {
		t.Fatal("expected Playground candidate count to align with repo-gw scope (候选 3)")
	}

	// Run discovery in Playground and verify repo-only sim_scope is transmitted.
	pgSt := uiApp.playgroundState()
	pgSt.intent = "查看待合并 PR"
	tester.Frame()
	if err := tester.Click("发起路由发现"); err != nil {
		t.Fatalf("click 发起路由发现: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	stateMu.Lock()
	gotDiscBody := lastDiscoverBody
	stateMu.Unlock()
	if !strings.Contains(gotDiscBody, `"sim_scope":{"repo_key":"repo-gw"}`) {
		t.Fatalf("expected repo-only sim_scope in /api/discover request, got: %s", gotDiscBody)
	}

	// 5. Verify ViewClients Hook snippet entry.
	if err := tester.Click("连接客户端"); err != nil {
		t.Fatalf("nav 连接客户端: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	if err := tester.Click("Cursor 更多操作"); err != nil {
		t.Fatalf("open Cursor 更多操作: %v", err)
	}
	if err := tester.ChooseMenuItem("查看 Hook 配置"); err != nil {
		t.Fatalf("choose 查看 Hook 配置: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	if !tester.HasText("/Users/test/.cursor/hooks.json") {
		t.Fatal("expected Cursor hook config path in modal")
	}
	if err := tester.Click("关闭"); err != nil {
		t.Fatalf("close client hook modal: %v", err)
	}

	// 6. Verify ViewLogs detail.scope rendering (retains agent for audit), one-click 绑定此仓库, and repo-only 沙盒重放 handoff.
	if err := tester.Click("请求日志"); err != nil {
		t.Fatalf("nav 请求日志: %v", err)
	}
	tester.Frame()
	if !tester.HasText("仓库作用域") || !tester.HasText("/tmp/wt-unconfigured-svc") || !tester.HasText("Claude Code (claude)") || !tester.HasText("命中规则") {
		t.Fatal("expected Logs inspector to display detail.scope fields including client agent")
	}
	if !tester.HasText("绑定此仓库") {
		t.Fatal("expected 绑定此仓库 action on unconfigured repo log event")
	}
	if err := tester.Click("绑定此仓库"); err != nil {
		t.Fatalf("click 绑定此仓库 in logs: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)

	stateMu.Lock()
	var logSavedRepoBody string
	if len(savedRepoBodies) >= 2 {
		logSavedRepoBody = savedRepoBodies[1]
	}
	stateMu.Unlock()
	if !strings.Contains(logSavedRepoBody, `"git_common_dirs":["/work/unconfigured-svc/.git"]`) ||
		strings.Contains(logSavedRepoBody, `"root_paths"`) ||
		strings.Contains(logSavedRepoBody, `"path"`) {
		t.Fatalf("binding Git repo from Logs must send git_common_dirs without root_paths/path, got: %s", logSavedRepoBody)
	}
	if tester.HasText("绑定此仓库") || !tester.HasText("仓库规则") {
		t.Fatal("expected Logs scope card to switch from 绑定此仓库 to 仓库规则 after binding via git_common_dirs")
	}

	// Replay from logs should carry ONLY the newly bound repo ID (`repo-unconfigured-svc`) into Playground!
	if err := tester.Click("沙盒重放"); err != nil {
		t.Fatalf("click 沙盒重放 in logs: %v", err)
	}
	if uiApp.CurrentView() != ViewPlayground {
		t.Fatalf("expected ViewPlayground after logs replay, got %s", uiApp.CurrentView())
	}
	if pgSt.simRepoKey != "repo-unconfigured-svc" {
		t.Fatalf("expected Playground replay scope repo-unconfigured-svc, got %q", pgSt.simRepoKey)
	}
}

func TestReposRuleEditingAndEnforceToggle(t *testing.T) {
	var (
		mu          sync.Mutex
		savedRules  []apitypes.ScopeRule
		deletedRule string
		deletedRepo string
		enforceSet  []bool
	)

	boot := apitypes.Bootstrap{
		MCPURL: "http://127.0.0.1:8788/mcp",
		Access: apitypes.AccessInfo{Shared: false, Actor: "developer"},
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "github", Name: "GitHub MCP", Enabled: true},
			},
			Tools: []apitypes.Tool{
				{ID: "github.list_prs", ServerID: "github", Name: "list_prs", Enabled: true, ReadOnly: true, Actors: []string{"developer"}},
				{ID: "github.merge_pr", ServerID: "github", Name: "merge_pr", Enabled: true, ReadOnly: false, Actors: []string{"developer"}},
			},
		},
		Scopes: apitypes.ScopeOverview{
			Enforce: false,
			Repositories: []apitypes.Repository{
				{
					ID:            "repo-1",
					Name:          "alpha-repo",
					GitCommonDirs: []string{"/work/alpha/.git"},
					RootPaths:     []string{"/work/alpha"},
				},
			},
			Rules: []apitypes.ScopeRule{
				{RepoKey: "*", Grants: []apitypes.ServiceGrant{}},
				{RepoKey: "repo-1", Grants: []apitypes.ServiceGrant{{ServerID: "github", Access: "ro"}}},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/mode":
			var in struct {
				Enforce bool `json:"enforce"`
			}
			_ = json.Unmarshal(raw, &in)
			boot.Scopes.Enforce = in.Enforce
			enforceSet = append(enforceSet, in.Enforce)
			_ = json.NewEncoder(w).Encode(in)
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/rules":
			var rule apitypes.ScopeRule
			_ = json.Unmarshal(raw, &rule)
			savedRules = append(savedRules, rule)
			replaced := false
			for i := range boot.Scopes.Rules {
				if boot.Scopes.Rules[i].RepoKey == rule.RepoKey {
					boot.Scopes.Rules[i] = rule
					replaced = true
					break
				}
			}
			if !replaced {
				boot.Scopes.Rules = append(boot.Scopes.Rules, rule)
			}
			_ = json.NewEncoder(w).Encode(rule)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/scopes/rules":
			deletedRule = r.URL.Query().Get("repo_key")
			var kept []apitypes.ScopeRule
			for _, item := range boot.Scopes.Rules {
				if item.RepoKey == deletedRule {
					continue
				}
				kept = append(kept, item)
			}
			boot.Scopes.Rules = kept
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/scopes/repositories/"):
			deletedRepo = strings.TrimPrefix(r.URL.Path, "/api/scopes/repositories/")
			boot.Scopes.Repositories = nil
			_, _ = w.Write([]byte(`{"deleted":true}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	uiApp := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	mu.Lock()
	bootCopy := boot
	mu.Unlock()
	uiApp.State = &bootCopy
	uiApp.Loading = false
	uiApp.ViewMode = ViewRepos

	tester := ui.NewTester(uiApp.View, 1400, 920)
	waitAsyncIdle(t, tester, uiApp)

	if err := tester.Click("单仓库详情"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()

	// Toggle Enforce switch.
	if err := tester.Click("启用仓库隔离拦截"); err != nil {
		t.Fatalf("toggle enforce switch: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	mu.Lock()
	gotEnforce := len(enforceSet) > 0 && enforceSet[0]
	mu.Unlock()
	if !gotEnforce {
		t.Fatal("expected SetScopeMode(true) to be called")
	}

	// Select alpha-repo (`本地仓库` subtitle since RemoteHint is empty).
	if !tester.HasText("本地仓库") {
		t.Fatal("expected 本地仓库 fallback when RemoteHint is empty")
	}
	if err := tester.Click("alpha-repo"); err != nil {
		t.Fatalf("select alpha-repo: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)

	// Select tool subset (`仅选只读工具`) and save default rule.
	if err := tester.Click("全部工具 (2)"); err != nil {
		t.Fatalf("expand subset: %v", err)
	}
	if err := tester.Click("仅选只读工具"); err != nil {
		t.Fatalf("click 仅选只读工具: %v", err)
	}
	if err := tester.Click("保存"); err != nil {
		t.Fatalf("click 保存: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)

	mu.Lock()
	gotSavedSubset := len(savedRules) == 1 && len(savedRules[0].Grants) == 1 && len(savedRules[0].Grants[0].ToolIDs) == 1 && savedRules[0].Grants[0].ToolIDs[0] == "github.list_prs"
	mu.Unlock()
	if !gotSavedSubset {
		t.Fatalf("expected tool_ids subset [github.list_prs] to be saved, got %+v", savedRules)
	}

	// Restore inheritance (`恢复继承全局` -> DELETE /api/scopes/rules?repo_key=repo-1).
	if err := tester.Click("恢复继承全局"); err != nil {
		t.Fatalf("click 恢复继承全局: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	mu.Lock()
	gotDeletedRule := deletedRule
	mu.Unlock()
	if gotDeletedRule != "repo-1" {
		t.Fatalf("expected deletedRule repo-1, got %q", gotDeletedRule)
	}

	// Delete repository via confirmation dialog (`移除绑定`).
	if err := tester.Click("移除绑定"); err != nil {
		t.Fatalf("click 移除绑定: %v", err)
	}
	tester.Frame()
	// Confirm alert button ("移除绑定").
	if err := tester.Click("移除绑定"); err != nil {
		t.Fatalf("confirm 移除绑定 alert: %v", err)
	}
	waitAsyncIdle(t, tester, uiApp)
	mu.Lock()
	gotDeletedRepo := deletedRepo
	mu.Unlock()
	if gotDeletedRepo != "repo-1" {
		t.Fatalf("expected DeleteRepository(repo-1), got %q", gotDeletedRepo)
	}
}

func waitAsyncIdle(t *testing.T, tester *ui.Tester, uiApp *DesktopUI) {
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
			t.Fatal("timed out waiting for async UI work to settle")
		}
		time.Sleep(2 * time.Millisecond)
		tester.Advance(10 * time.Millisecond)
	}
}

func slicesDeleteObserved(in []apitypes.ObservedRepo, name string) []apitypes.ObservedRepo {
	var out []apitypes.ObservedRepo
	for _, o := range in {
		if o.Name != name {
			out = append(out, o)
		}
	}
	return out
}
