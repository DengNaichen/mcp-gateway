package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestLogsParseEventDetail(t *testing.T) {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	toolByID := map[string]apitypes.Tool{
		"srv.read": {
			ID:          "srv.read",
			Name:        "read",
			ServerID:    "srv",
			Description: "Read item",
			ReadOnly:    true,
		},
		"srv.write": {
			ID:          "srv.write",
			Name:        "write",
			ServerID:    "srv",
			Description: "Write item",
			ReadOnly:    false,
		},
	}

	tests := []struct {
		name  string
		ev    apitypes.Event
		check func(t *testing.T, got logsParsedDetail)
	}{
		{
			name: "emptyDetail",
			ev:   apitypes.Event{ID: "ev-1", Time: now, Kind: "discover", Status: "selected"},
			check: func(t *testing.T, got logsParsedDetail) {
				if got.DetailJSON != "{}" {
					t.Fatalf("DetailJSON = %q, want {}", got.DetailJSON)
				}
				if got.Scope != nil || len(got.Tools) != 0 || got.FullEventJSON == "" {
					t.Fatalf("unexpected parsed fields: %+v", got)
				}
			},
		},
		{
			name: "invalidJSONDetailPreservedRaw",
			ev:   apitypes.Event{ID: "ev-2", Time: now, Detail: json.RawMessage(`not-json`)},
			check: func(t *testing.T, got logsParsedDetail) {
				if got.DetailJSON != "not-json" {
					t.Fatalf("DetailJSON = %q, want not-json", got.DetailJSON)
				}
				if got.Scope != nil || got.Model != "" {
					t.Fatalf("unexpected structured fields on invalid JSON: %+v", got)
				}
			},
		},
		{
			name: "fullStructuredPayloadAndScopeFallbacks",
			ev: apitypes.Event{
				ID:     "ev-3",
				Time:   now,
				Kind:   "discover",
				Status: "selected",
				Detail: json.RawMessage(`{
					"model": " gpt-5 ",
					"summary": " routed ",
					"question": " which repo? ",
					"error": " boom ",
					"eligible": 12,
					"prompt_tokens": 100,
					"output_tokens": 25,
					"tool_id": "srv.write",
					"client": "claude",
					"method": "tools/call",
					"tool": "discover_tools",
					"servers": [" srv ", "", "other"],
					"fields": [" id ", "  "],
					"scope": {
						"agent": "codex",
						"cwd": "/repo/app",
						"context_status": "scope_required"
					},
					"tools": [
						"srv.read",
						"unknown.ping",
						{"tool_id": "srv.write", "reason": "matched write", "read_only": false},
						{"id": "extra.fn", "name": "fn", "reason": "custom"}
					]
				}`),
			},
			check: func(t *testing.T, got logsParsedDetail) {
				if got.Model != "gpt-5" || got.Summary != "routed" || got.Question != "which repo?" || got.Error != "boom" {
					t.Fatalf("unexpected scalar strings: %+v", got)
				}
				if got.Eligible == nil || *got.Eligible != 12 ||
					got.PromptTokens == nil || *got.PromptTokens != 100 ||
					got.OutputTokens == nil || *got.OutputTokens != 25 {
					t.Fatalf("unexpected numeric fields: %+v", got)
				}
				if got.ToolID != "srv.write" || got.Client != "claude" || got.Method != "tools/call" || got.AgentToolName != "discover_tools" {
					t.Fatalf("unexpected protocol fields: %+v", got)
				}
				if len(got.Servers) != 2 || got.Servers[0] != "srv" || got.Servers[1] != "other" {
					t.Fatalf("Servers = %v", got.Servers)
				}
				if len(got.Fields) != 1 || got.Fields[0] != "id" {
					t.Fatalf("Fields = %v", got.Fields)
				}
				if got.Scope == nil || got.Scope.Agent != "codex" || got.Scope.Root != "/repo/app" || got.Scope.Status != "scope_required" {
					t.Fatalf("Scope = %+v", got.Scope)
				}
				if len(got.Tools) != 4 {
					t.Fatalf("Tools len = %d, want 4", len(got.Tools))
				}
				if got.Tools[0].ServerID != "srv" || !got.Tools[0].HasReadOnly || !got.Tools[0].ReadOnly {
					t.Fatalf("Tools[0] = %+v", got.Tools[0])
				}
				if got.Tools[1].ServerID != "unknown" || got.Tools[1].Name != "ping" {
					t.Fatalf("Tools[1] = %+v", got.Tools[1])
				}
				if got.Tools[2].ID != "srv.write" || got.Tools[2].Description != "Write item" || got.Tools[2].ReadOnly {
					t.Fatalf("Tools[2] = %+v", got.Tools[2])
				}
				if got.Tools[3].ServerID != "extra" || got.Tools[3].Reason != "custom" {
					t.Fatalf("Tools[3] = %+v", got.Tools[3])
				}
			},
		},
		{
			name: "emptyScopeFieldsReturnsNilScope",
			ev:   apitypes.Event{ID: "ev-4", Time: now, Detail: json.RawMessage(`{"scope":{"kind":"git"}}`)},
			check: func(t *testing.T, got logsParsedDetail) {
				if got.Scope != nil {
					t.Fatalf("expected nil Scope when only kind is set, got %+v", got.Scope)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logsParseEventDetail(tt.ev, toolByID)
			tt.check(t, got)
		})
	}
}

func TestLogsMatchedBoundRepoAndReplayKey(t *testing.T) {
	state := &apitypes.Bootstrap{
		Scopes: apitypes.ScopeOverview{
			Observed: []apitypes.ObservedRepo{
				{Root: "/ws/obs-wt", Kind: "git", GitCommonDir: "/ws/obs/.git"},
			},
			Repositories: []apitypes.Repository{
				{ID: "repo-id", Name: "ByID", RootPaths: []string{"/ws/byid"}},
				{ID: "repo-git", Name: "ObsGit", GitCommonDirs: []string{"/ws/obs/.git"}},
				{ID: "repo-dotgit", Name: "DotGit", GitCommonDirs: []string{"/ws/dotgit/.git"}},
				{ID: "repo-short", Name: "Short", RootPaths: []string{"/ws"}},
				{ID: "repo-long", Name: "Long", RootPaths: []string{"/ws/app"}},
			},
		},
	}

	tests := []struct {
		name      string
		state     *apitypes.Bootstrap
		scope     logsParsedScope
		wantID    string
		wantReply string
	}{
		{
			name:      "nilState",
			state:     nil,
			scope:     logsParsedScope{RepoID: "repo-id"},
			wantID:    "",
			wantReply: "repo-id",
		},
		{
			name:      "exactRepoID",
			state:     state,
			scope:     logsParsedScope{RepoID: "repo-id"},
			wantID:    "repo-id",
			wantReply: "repo-id",
		},
		{
			name:      "observedGitCommonDirMatch",
			state:     state,
			scope:     logsParsedScope{RepoID: "*", Root: "/ws/obs-wt"},
			wantID:    "repo-git",
			wantReply: "repo-git",
		},
		{
			name:      "dotGitFallbackMatch",
			state:     state,
			scope:     logsParsedScope{Kind: "git", Root: "/ws/dotgit"},
			wantID:    "repo-dotgit",
			wantReply: "repo-dotgit",
		},
		{
			name:      "longestRootPrefixMatch",
			state:     state,
			scope:     logsParsedScope{Root: "/ws/app/sub"},
			wantID:    "repo-long",
			wantReply: "repo-long",
		},
		{
			name:      "unmatchedWildcardFallsBackToStar",
			state:     state,
			scope:     logsParsedScope{Root: "/elsewhere"},
			wantID:    "",
			wantReply: "*",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := logsMatchedBoundRepo(tt.state, tt.scope)
			gotID := ""
			if got != nil {
				gotID = got.ID
			}
			if gotID != tt.wantID {
				t.Fatalf("logsMatchedBoundRepo() ID = %q, want %q", gotID, tt.wantID)
			}
			if reply := logsResolveReplayRepoKey(tt.state, tt.scope); reply != tt.wantReply {
				t.Fatalf("logsResolveReplayRepoKey() = %q, want %q", reply, tt.wantReply)
			}
		})
	}
}

func TestLogsMatchesFilters(t *testing.T) {
	evDeny := apitypes.Event{ID: "1", Kind: "invoke", Protocol: "cli", Decision: "deny", RuleID: "policy:pol-1", ServiceID: "gh", RepoID: "repo-1", Title: "gh pr merge"}
	evAllow := apitypes.Event{ID: "2", Kind: "invoke", Protocol: "mcp", Decision: "allow", ServiceID: "github", Title: "list_issues"}
	evApprove := apitypes.Event{ID: "3", Kind: "invoke", Protocol: "http", Decision: "approve", ApprovalID: "apv-7f3a21", ServiceID: "gh", RepoID: "repo-1"}

	cases := []struct {
		name string
		ev   apitypes.Event
		st   logsPageState
		want bool
	}{
		{"protocolAndDecision", evDeny, logsPageState{protocolIdx: 3, decisionIdx: 3}, true},
		{"protocolMismatch", evDeny, logsPageState{protocolIdx: 1}, false},
		{"decisionMismatch", evAllow, logsPageState{decisionIdx: 2}, false},
		{"repoMatch", evApprove, logsPageState{repoKey: "repo-1"}, true},
		{"repoMismatch", evAllow, logsPageState{repoKey: "repo-1"}, false},
		// Unbound means RepoID == "", independent of any scope status in Detail.
		{"unboundMatchesEmptyRepoID", evAllow, logsPageState{repoKey: logsRepoUnbound}, true},
		{"unboundRejectsBoundRepo", evDeny, logsPageState{repoKey: logsRepoUnbound}, false},
		{"serviceMatch", evDeny, logsPageState{serviceID: "gh"}, true},
		{"serviceMismatch", evAllow, logsPageState{serviceID: "gh"}, false},
		{"searchRuleID", evDeny, logsPageState{query: "POL-1"}, true},
		{"searchApprovalID", evApprove, logsPageState{query: "apv-7f3a"}, true},
		{"searchMiss", evAllow, logsPageState{query: "nothing"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logsMatchesFilters(tc.ev, &tc.st); got != tc.want {
				t.Fatalf("logsMatchesFilters() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLogsServerFilter(t *testing.T) {
	if _, active := logsServerFilter(&logsPageState{}); active {
		t.Fatal("empty filters must render Bootstrap.Events without querying")
	}
	f, active := logsServerFilter(&logsPageState{protocolIdx: 3, decisionIdx: 2, repoKey: "repo-1", serviceID: "gh", query: " merge "})
	if !active || f.Protocol != "cli" || f.Decision != "approve" || f.RepoID != "repo-1" || f.ServiceID != "gh" || f.Query != "merge" || f.Limit != logsQueryPageSize {
		t.Fatalf("unexpected filter: %+v active=%v", f, active)
	}
	f, active = logsServerFilter(&logsPageState{repoKey: logsRepoUnbound})
	if !active || f.RepoID != "" {
		t.Fatalf("unbound filter must not be sent as repo_id: %+v active=%v", f, active)
	}
}

func TestLogsPrepareEventsViewAndPendingEvent(t *testing.T) {
	events := []apitypes.Event{
		{ID: "e1", Kind: "discover", Status: "selected", Title: "first"},
		{ID: "e2", Kind: "invoke", Status: "error", Title: "second"},
		{ID: "e3", Kind: "discover", Status: "failed", Title: "third"},
	}
	st := &logsPageState{protocolIdx: 1, decisionIdx: 1, timelineLimit: 1}
	logsApplyPendingEvent(events, st, "e3")
	if st.selectedID != "e3" {
		t.Fatalf("selectedID = %q, want e3", st.selectedID)
	}
	if st.protocolIdx != 0 || st.decisionIdx != 0 {
		t.Fatalf("expected incompatible filters to reset, got protocol=%d decision=%d", st.protocolIdx, st.decisionIdx)
	}
	if st.timelineLimit < 3 {
		t.Fatalf("expected timelineLimit to expand for e3, got %d", st.timelineLimit)
	}

	filtered, errCount, active := logsPrepareEventsView(events, st)
	if len(filtered) != 3 || errCount != 2 {
		t.Fatalf("filtered=%d errCount=%d, want 3, 2", len(filtered), errCount)
	}
	if active == nil || active.ID != "e3" {
		t.Fatalf("active = %+v, want e3", active)
	}

	st.selectedID = "missing"
	_, _, fallback := logsPrepareEventsView(events, st)
	if fallback == nil || fallback.ID != "e1" || st.selectedID != "e1" {
		t.Fatalf("expected fallback to first event e1, got %+v (selected=%q)", fallback, st.selectedID)
	}
}

func waitForUI(t *testing.T, tester *ui.Tester, u *DesktopUI, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		tester.Frame()
		ok := func() bool {
			leave := u.enterHeadlessFrame()
			defer leave()
			return cond()
		}()
		if ok {
			tester.Frame()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for UI condition")
		}
		time.Sleep(2 * time.Millisecond)
		tester.Advance(10 * time.Millisecond)
	}
}

func logsLedgerBootstrap() apitypes.Bootstrap {
	now := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	return apitypes.Bootstrap{
		Access: apitypes.AccessInfo{Actor: "developer", SecurityMode: apitypes.SecurityModeLocalGuardrail},
		Registry: apitypes.Registry{Servers: []apitypes.Server{
			{ID: "gh", Name: "gh CLI", Enabled: true},
			{ID: "github", Name: "GitHub MCP", Enabled: true},
		}},
		Scopes: apitypes.ScopeOverview{Repositories: []apitypes.Repository{{ID: "repo-1", Name: "alpha-repo"}}},
		Events: []apitypes.Event{
			{ID: "ev-local-allow", Time: now, Kind: "invoke", Status: "ok", Title: "github.list_issues", Protocol: "mcp", Decision: "allow", ServiceID: "github"},
			{ID: "ev-local-deny", Time: now.Add(-time.Minute), Kind: "invoke", Status: "denied", Title: "gh pr merge 1", Protocol: "cli", Decision: "deny", ServiceID: "gh", RepoID: "repo-1", RuleID: "policy:pol-1"},
		},
	}
}

func TestLogsUnifiedLedgerQueriesBackend(t *testing.T) {
	var (
		mu      sync.Mutex
		queries []url.Values
	)
	boot := logsLedgerBootstrap()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/api/events/query" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(apitypes.EventPage{Items: []apitypes.Event{{
			ID: "ev-remote-approve", Time: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC), Kind: "invoke", Status: "pending",
			Title: "gh pr merge 482", Protocol: "cli", Decision: "approve", ServiceID: "gh", Action: "gh.pr.merge",
			RuleID: "policy:pol-2", ApprovalID: "apv-7f3a21", RepoID: "repo-1",
		}}})
	}))
	defer srv.Close()

	u := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, Mode: modeAttached})
	u.State = &boot
	u.Loading = false
	tester := ui.NewTester(func(c *ui.Context) {
		defer u.enterHeadlessFrame()()
		u.renderLogs(c)
	}, 1400, 1200)

	// No filter: Bootstrap.Events render directly, deny gets a red badge.
	if !tester.HasText("github.list_issues") || !tester.HasText("gh pr merge 1") {
		t.Fatalf("expected local events without filters: %v", tester.Texts())
	}
	mu.Lock()
	if len(queries) != 0 {
		t.Fatalf("unfiltered view must not query: %v", queries)
	}
	mu.Unlock()

	if err := tester.Click("CLI"); err != nil {
		t.Fatal(err)
	}
	waitForUI(t, tester, u, func() bool { return u.logsState().remote.loaded })
	if !tester.HasText("gh pr merge 482") || tester.HasText("github.list_issues") {
		t.Fatalf("filtered view should render backend page: %v", tester.Texts())
	}
	mu.Lock()
	q := queries[len(queries)-1]
	mu.Unlock()
	if q.Get("protocol") != "cli" || q.Get("limit") != "200" || q.Has("repo_id") {
		t.Fatalf("unexpected query: %v", q)
	}

	// Inspector shows the audit metadata card.
	for _, want := range []string{"审计元信息", "rule_id", "policy:pol-2", "approval_id", "apv-7f3a21", "action", "gh.pr.merge"} {
		if !tester.HasText(want) {
			t.Fatalf("inspector missing %q", want)
		}
	}
}

func TestLogsUnboundFilterAndOfflineFallback(t *testing.T) {
	boot := logsLedgerBootstrap()
	// Unreachable backend: filtered views fall back to Bootstrap.Events.
	u := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
	u.State = &boot
	u.Loading = false
	st := u.logsState()
	st.repoLabel = "未绑定仓库"
	tester := ui.NewTester(func(c *ui.Context) {
		defer u.enterHeadlessFrame()()
		u.renderLogs(c)
	}, 1400, 1200)
	waitForUI(t, tester, u, func() bool { return !st.remote.loading && st.remote.err != "" })
	if st.repoKey != logsRepoUnbound {
		t.Fatalf("repoKey = %q, want unbound sentinel", st.repoKey)
	}
	if !tester.HasText("github.list_issues") || tester.HasText("gh pr merge 1") {
		t.Fatalf("unbound filter must keep only RepoID==\"\" events: %v", tester.Texts())
	}
}
