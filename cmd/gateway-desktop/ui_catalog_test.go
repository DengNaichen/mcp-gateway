package main

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestBuildCatalogViewModelTableDriven(t *testing.T) {
	servers := []apitypes.Server{
		{ID: "gh", Name: "GitHub", Enabled: true},
		{ID: "db", Name: "Postgres", Enabled: true},
		{ID: "demo", Name: "DemoSvc", Enabled: true, Demo: true},
	}
	tools := []apitypes.Tool{
		{ID: "gh.list", ServerID: "gh", Name: "list_prs", Description: "List pull requests", Enabled: true, ReadOnly: true, Actors: []string{"developer", "observer"}, Tags: []string{"pr"}},
		{ID: "gh.merge", ServerID: "gh", Name: "merge_pr", Description: "Merge pull request", Enabled: true, ReadOnly: false, Actors: []string{"developer"}},
		{ID: "db.query", ServerID: "db", Name: "query", Description: "Run SQL query", Enabled: false, ReadOnly: true, Actors: []string{"*"}},
		{ID: "demo.ping", ServerID: "demo", Name: "ping", Description: "Demo ping", Enabled: true, ReadOnly: true, Actors: []string{"qa"}},
	}
	hitCounts := map[string]int{
		"gh.merge": 10,
		"gh.list":  3,
	}

	tests := []struct {
		name  string
		setup func() *catalogPageState
		check func(t *testing.T, st *catalogPageState, vm catalogViewModel)
	}{
		{
			name: "defaultExcludesDemoAndSortsByHits",
			setup: func() *catalogPageState {
				return &catalogPageState{serverFilter: "all", actorFilter: "all", page: 1}
			},
			check: func(t *testing.T, st *catalogPageState, vm catalogViewModel) {
				if vm.stats.realToolCount != 3 || vm.stats.enabledRealCount != 2 || vm.stats.closedRealCount != 1 {
					t.Fatalf("unexpected real stats: %+v", vm.stats)
				}
				if vm.stats.roEnabledCount != 1 || vm.stats.rwEnabledCount != 1 || vm.stats.hitRealToolCount != 2 {
					t.Fatalf("unexpected ro/rw/hit stats: %+v", vm.stats)
				}
				if len(vm.filtered) != 3 {
					t.Fatalf("filtered len = %d, want 3", len(vm.filtered))
				}
				if vm.filtered[0].ID != "gh.merge" || vm.filtered[1].ID != "gh.list" || vm.filtered[2].ID != "db.query" {
					t.Fatalf("unexpected hit sort order: %+v", []string{vm.filtered[0].ID, vm.filtered[1].ID, vm.filtered[2].ID})
				}
				if vm.selectedTool == nil || vm.selectedTool.ID != "gh.merge" || st.selectedToolID != "gh.merge" {
					t.Fatalf("expected auto-select first filtered tool, got %+v", vm.selectedTool)
				}
				if len(vm.serverOptions) != 3 { // all + 2 real
					t.Fatalf("serverOptions len = %d, want 3", len(vm.serverOptions))
				}
				if len(vm.actorOptions) != 3 { // all + developer + observer (qa is demo-only)
					t.Fatalf("actorOptions = %+v", vm.actorOptions)
				}
			},
		},
		{
			name: "showDemoEnabledIncludesDemoServerAndActor",
			setup: func() *catalogPageState {
				return &catalogPageState{serverFilter: "demo", actorFilter: "qa", showDemo: true, page: 1}
			},
			check: func(t *testing.T, st *catalogPageState, vm catalogViewModel) {
				if len(vm.filtered) != 1 || vm.filtered[0].ID != "demo.ping" {
					t.Fatalf("expected demo.ping, got %+v", vm.filtered)
				}
				if len(vm.serverOptions) != 4 {
					t.Fatalf("serverOptions with demo = %d, want 4", len(vm.serverOptions))
				}
			},
		},
		{
			name: "turningOffDemoResetsDemoServerAndDemoOnlyActor",
			setup: func() *catalogPageState {
				return &catalogPageState{serverFilter: "demo", actorFilter: "qa", showDemo: false, page: 3}
			},
			check: func(t *testing.T, st *catalogPageState, vm catalogViewModel) {
				if st.serverFilter != "all" || st.actorFilter != "all" {
					t.Fatalf("expected demo filters to reset, got srv=%q actor=%q", st.serverFilter, st.actorFilter)
				}
				if st.page != 1 {
					t.Fatalf("expected page reset to 1, got %d", st.page)
				}
			},
		},
		{
			name: "actorFilterMatchesWildcardToolToo",
			setup: func() *catalogPageState {
				return &catalogPageState{serverFilter: "all", actorFilter: "observer", sortIdx: 1, page: 1}
			},
			check: func(t *testing.T, st *catalogPageState, vm catalogViewModel) {
				if len(vm.filtered) != 2 {
					t.Fatalf("expected 2 tools (gh.list + wildcard db.query), got %d", len(vm.filtered))
				}
				// Sort by Name A-Z: list_prs < query
				if vm.filtered[0].ID != "gh.list" || vm.filtered[1].ID != "db.query" {
					t.Fatalf("unexpected A-Z order: %s, %s", vm.filtered[0].ID, vm.filtered[1].ID)
				}
			},
		},
		{
			name: "selectedToolFallsBackToRegistryWhenFilteredByStatus",
			setup: func() *catalogPageState {
				return &catalogPageState{
					serverFilter:   "all",
					actorFilter:    "all",
					statusIdx:      1, // enabled only (excludes db.query)
					selectedToolID: "db.query",
					page:           1,
				}
			},
			check: func(t *testing.T, st *catalogPageState, vm catalogViewModel) {
				if vm.selectedTool == nil || vm.selectedTool.ID != "db.query" {
					t.Fatalf("expected selectedTool fallback to db.query from registry, got %+v", vm.selectedTool)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.setup()
			vm := buildCatalogViewModel(servers, tools, hitCounts, st)
			tt.check(t, st, vm)
		})
	}
}

func TestCatalogPaginateToolsAndNavigationIntents(t *testing.T) {
	items := make([]apitypes.Tool, 45)
	for i := range items {
		items[i] = apitypes.Tool{ID: fmt.Sprintf("t.%02d", i)}
	}
	paged, clamped, total, start, end := catalogPaginateTools(items, 99, catalogPageSize)
	if clamped != 2 || total != 2 || start != 40 || end != 45 || len(paged) != 5 {
		t.Fatalf("unexpected page 2: clamped=%d total=%d start=%d end=%d len=%d", clamped, total, start, end, len(paged))
	}

	serverByID := map[string]apitypes.Server{
		"demo": {ID: "demo", Demo: true},
	}
	st := &catalogPageState{query: "old", statusIdx: 2, accessIdx: 1, actorFilter: "dev", panelCollapsed: true, detailTab: 2}
	st.focusTool("demo.ping", "demo", serverByID)
	if !st.showDemo || st.serverFilter != "demo" || st.selectedToolID != "demo.ping" || st.panelCollapsed || st.detailTab != 0 || st.query != "" {
		t.Fatalf("unexpected focusTool state: %+v", st)
	}

	st.applyFilterIntent("demo", "rw", serverByID)
	if st.accessIdx != 2 || st.page != 1 {
		t.Fatalf("unexpected applyFilterIntent state: %+v", st)
	}

	tool := apitypes.Tool{ID: "a.b", Revision: "1", Description: "desc", ReadOnly: true, Actors: []string{"developer"}, Tags: []string{"x"}}
	st.edit.sync(tool)
	st.edit.desc = "edited"
	st.edit.sync(tool) // same revision signature should keep user edits
	if st.edit.desc != "edited" {
		t.Fatalf("sync overwrote unsaved edit: %q", st.edit.desc)
	}
	st.edit.reset(tool)
	if st.edit.desc != "desc" {
		t.Fatalf("reset did not restore tool desc: %q", st.edit.desc)
	}
}

func TestToolMatchesApproveRule(t *testing.T) {
	merge := apitypes.Tool{ID: "gh.pr.merge", ServerID: "gh"}
	view := apitypes.Tool{ID: "gh.pr.view", ServerID: "gh", ReadOnly: true}
	rules := []apitypes.PolicyRule{
		{ID: "a", Effect: "approve", Enabled: true, RepoKey: "repo-1", ServiceID: "gh", Access: "rw"},
		{ID: "b", Effect: "deny", Enabled: true, RepoKey: "*", ServiceID: "*", Access: "*"},
	}
	if !toolMatchesApproveRule(merge, rules) || toolMatchesApproveRule(view, rules) {
		t.Fatal("rw approve rule must match writes only")
	}
	if toolMatchesApproveRule(merge, []apitypes.PolicyRule{{Effect: "approve", Enabled: false, ServiceID: "gh", Access: "*"}}) {
		t.Fatal("disabled rules must be ignored")
	}
	if !toolMatchesApproveRule(view, []apitypes.PolicyRule{{Effect: "approve", Enabled: true, ServiceID: "*", ToolIDs: []string{"gh.pr.view"}, Access: "*"}}) {
		t.Fatal("wildcard service + explicit tool must match")
	}
	if toolMatchesApproveRule(view, []apitypes.PolicyRule{{Effect: "approve", Enabled: true, ServiceID: "github", Access: "*"}}) {
		t.Fatal("other services must not match")
	}
}

func TestCatalogRendersControlledCLIToolsIsomorphically(t *testing.T) {
	app := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
	boot := apitypes.Bootstrap{MCPURL: "http://127.0.0.1:8788/mcp"}
	boot.Registry.Servers = []apitypes.Server{ghTestServer()}
	boot.Registry.Tools = ghTestTools()
	boot.Policies.Rules = []apitypes.PolicyRule{{ID: "pol-merge", Effect: "approve", Enabled: true, RepoKey: "*", ServiceID: "gh", ToolIDs: []string{"gh.pr.merge"}, Access: "rw"}}
	app.State = &boot
	app.Loading = false
	st := app.catalogState()
	st.selectedToolID = "gh.pr.merge"
	st.detailTab = 1
	render := func() *ui.Tester {
		return ui.NewTester(func(c *ui.Context) {
			defer app.enterHeadlessFrame()()
			app.renderCatalog(c)
		}, 1500, 1400)
	}
	tester := render()
	for _, want := range []string{"pr.view", "pr.list", "pr.merge", "需审批", "受控 CLI 工具契约由网关生成并锁定"} {
		if !tester.HasText(want) {
			t.Fatalf("catalog missing %q: %v", want, tester.Texts())
		}
	}
	if tester.HasText("保存契约") {
		t.Fatal("controlled CLI contracts must not be editable from the catalog")
	}
	st.detailTab = 0
	tester = render()
	if !tester.HasText("CLI 子命令") || !tester.HasText("gh pr merge") {
		t.Fatalf("catalog overview must show the CLI subcommand: %v", tester.Texts())
	}
}
