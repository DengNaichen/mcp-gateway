package main

import (
	"encoding/json"
	"testing"
	"time"

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
	evDiscoverErr := apitypes.Event{ID: "1", Kind: "discover", Status: "error", Title: "search issues", Actor: "dev"}
	evAgent := apitypes.Event{ID: "2", Kind: "agent", Status: "selected", Title: "invoke"}
	evNoMatch := apitypes.Event{ID: "3", Kind: "discover", Status: "no_match", Title: "unknown"}

	if !logsMatchesFilters(evDiscoverErr, &logsPageState{kindIdx: 1, statusIdx: 3, query: "ISSUES"}) {
		t.Fatal("expected discover error to match filter")
	}
	if !logsMatchesFilters(evAgent, &logsPageState{kindIdx: 3, statusIdx: 1}) {
		t.Fatal("expected agent kind to match kindIdx=3 and statusIdx=1")
	}
	if !logsMatchesFilters(evNoMatch, &logsPageState{kindIdx: 1, statusIdx: 2}) {
		t.Fatal("expected no_match to match statusIdx=2")
	}
	if logsMatchesFilters(evNoMatch, &logsPageState{statusIdx: 1}) {
		t.Fatal("unmatched event must not match success filter")
	}
}

func TestLogsPrepareEventsViewAndPendingEvent(t *testing.T) {
	events := []apitypes.Event{
		{ID: "e1", Kind: "discover", Status: "selected", Title: "first"},
		{ID: "e2", Kind: "invoke", Status: "error", Title: "second"},
		{ID: "e3", Kind: "discover", Status: "failed", Title: "third"},
	}
	st := &logsPageState{kindIdx: 1, statusIdx: 1, timelineLimit: 1}
	logsApplyPendingEvent(events, st, "e3")
	if st.selectedID != "e3" {
		t.Fatalf("selectedID = %q, want e3", st.selectedID)
	}
	if st.kindIdx != 0 || st.statusIdx != 0 {
		t.Fatalf("expected incompatible filters to reset, got kind=%d status=%d", st.kindIdx, st.statusIdx)
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
