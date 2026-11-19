package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestRepoMatrixSavesDefaults(t *testing.T) {
	var mu sync.Mutex
	boot := apitypes.Bootstrap{}
	boot.Registry.Servers = []apitypes.Server{
		{ID: "github", Name: "GitHub", Enabled: true},
		{ID: "linear", Name: "Linear", Enabled: false},
		{ID: "demo", Name: "Demo", Demo: true},
	}
	boot.Scopes.Observed = []apitypes.ObservedRepo{{Fingerprint: "observed", Name: "new-project", Root: "/work/new-project"}}
	boot.Scopes.Repositories = []apitypes.Repository{{ID: "repo", Name: "project"}}
	boot.Scopes.Rules = []apitypes.ScopeRule{
		{RepoKey: "*", Grants: []apitypes.ServiceGrant{{ServerID: "github", Access: "rw", ToolIDs: []string{"read", "write"}}}},
	}
	var writes []apitypes.ScopeRule
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/rules":
			if fail {
				http.Error(w, "save failed", http.StatusInternalServerError)
				return
			}
			var rule apitypes.ScopeRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
				t.Error(err)
			}
			writes = append(writes, rule)
			boot.Scopes.Rules = slices.DeleteFunc(boot.Scopes.Rules, func(old apitypes.ScopeRule) bool { return old.RepoKey == rule.RepoKey })
			boot.Scopes.Rules = append(boot.Scopes.Rules, rule)
			_ = json.NewEncoder(w).Encode(rule)
		case r.Method == http.MethodPut && r.URL.Path == "/api/scopes/repositories":
			var in apitypes.SaveRepositoryRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			repo := apitypes.Repository{ID: "new", Name: in.Name}
			boot.Scopes.Repositories = append(boot.Scopes.Repositories, repo)
			boot.Scopes.Observed = nil
			_ = json.NewEncoder(w).Encode(repo)
		case r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	app := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, Mode: modeAttached})
	copyBoot := boot
	app.State = &copyBoot
	app.Loading = false
	app.ViewMode = ViewRepos
	app.marketState().loaded = true
	tester := ui.NewTester(app.View, 1400, 920)
	waitAsyncIdle(t, tester, app)
	if !tester.HasText("全局默认 *") || !tester.HasText("Linear") || tester.HasText("Demo") || tester.HasText("六端生效预览") {
		t.Fatal("expected default matrix with real services, including disabled upstream and excluding preview")
	}
	click := func(label string) {
		t.Helper()
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
		waitAsyncIdle(t, tester, app)
	}
	// First edit materializes inherited RW/tool restrictions and adds only RO.
	click("project · Linear")
	mu.Lock()
	if len(writes) != 1 || writes[0].RepoKey != "repo" || len(writes[0].Grants) != 2 {
		t.Fatalf("unexpected saved rule: %+v", writes)
	}
	if writes[0].Grants[0].Access != "rw" || len(writes[0].Grants[0].ToolIDs) != 2 || writes[0].Grants[1].Access != "ro" {
		t.Fatalf("permissions changed: %+v", writes[0])
	}
	mu.Unlock()
	click("project · GitHub")
	click("project · Linear")
	mu.Lock()
	if len(writes) != 3 || len(writes[2].Grants) != 0 {
		t.Fatalf("last removal must persist an explicit empty default: %+v", writes)
	}
	fail = true
	mu.Unlock()
	click("project · GitHub")
	if app.reposState().ensureDraft(app.State.Scopes.Rules, "repo").saving {
		t.Fatal("failed save left row locked")
	}
	if len(repoMatrixGrants(app.State.Scopes, "repo")) != 0 {
		t.Fatal("failed save changed displayed grants")
	}
	mu.Lock()
	fail = false
	mu.Unlock()
	click("project · GitHub")
	mu.Lock()
	if len(writes) != 4 || writes[3].Grants[0].Access != "ro" {
		t.Fatal("retry did not save a new RO grant")
	}
	mu.Unlock()
	click("+ 绑定并加入矩阵")
	if !tester.HasText("new-project") || tester.HasText("最近请求 · 未绑定") || app.reposState().viewMode != 0 {
		t.Fatal("binding did not add observed repository to matrix")
	}
	click("全局默认 * · Linear")
	if len(repoMatrixGrants(app.State.Scopes, "new")) != 2 || len(repoMatrixGrants(app.State.Scopes, "repo")) != 1 {
		t.Fatal("global change must update inherited rows and preserve explicit defaults")
	}
	click("project 详情")
	if app.reposState().viewMode != 1 || app.reposState().selectedRepoKey != "repo" || !tester.HasText("仓库默认服务") {
		t.Fatal("details did not open selected repository")
	}
	if tester.HasText("客户端例外") || tester.HasText("六端生效预览") {
		t.Fatal("details should not render removed 客户端例外 or 六端生效预览")
	}
	// Uncommitted detail edits cannot be silently saved by a matrix click.
	draft := app.reposState().ensureDraft(app.State.Scopes.Rules, "repo")
	draft.dirty = true
	click("矩阵视图")
	tester.Frame()
	app.toggleRepoMatrixGrant(app.reposState(), "repo", "linear", true)
	if draft.saving {
		t.Fatal("matrix saved an unfinished detail draft")
	}
}

func TestRepoMatrixHorizontalScrollKeepsColumnsAligned(t *testing.T) {
	app := &DesktopUI{State: &apitypes.Bootstrap{}}
	for i := range 8 {
		app.State.Registry.Servers = append(app.State.Registry.Servers, apitypes.Server{ID: fmt.Sprint(i), Name: fmt.Sprintf("Service %d", i)})
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().FillHeight().Padding(20).Children(func() { app.renderReposMatrix(c, app.reposState(), app.State) })
	}, 800, 600)
	tester.Frame()
	before, ok := tester.Find("Service 7")
	if !ok || before.X < 800 {
		t.Fatal("wide matrix must preserve column width beyond viewport")
	}
	tester.Scroll(400, 80, 1200, 0)
	tester.Frame()
	after, ok := tester.Find("Service 7")
	cell, cellOK := tester.Find("全局默认 * · Service 7")
	if !ok || !cellOK || after.X >= before.X || cell.X < 20 || cell.X+cell.W > 780 {
		t.Fatalf("last column not reachable: header=%+v cell=%+v", after, cell)
	}
	first, _ := tester.Find("全局默认 * · Service 6")
	if cell.X-first.X != repoMatrixMinServiceWidth {
		t.Fatalf("checkbox columns changed width: %+v %+v", first, cell)
	}
	if cell.X < after.X-24 || cell.X > after.X+after.W {
		t.Fatalf("header and checkbox drifted: %+v %+v", after, cell)
	}
}

func TestRepoMatrixCentersHeadersAndFitsDetailsButton(t *testing.T) {
	app := &DesktopUI{State: &apitypes.Bootstrap{}}
	app.State.Registry.Servers = []apitypes.Server{
		{ID: "github", Name: "GitHub"},
		{ID: "vercel", Name: "Vercel"},
		{ID: "supabase", Name: "Supabase"},
		{ID: "notion", Name: "Notion"},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).FillWidth().FillHeight().Padding(20).Children(func() { app.renderReposMatrix(c, app.reposState(), app.State) })
	}, 920, 600)
	// First frame measures scroll viewport width; second frame lays out with adaptive columns.
	tester.Frame()
	tester.Frame()

	details, ok := tester.Find("全局默认 * 详情")
	if !ok || details.X+details.W > 900 {
		t.Fatalf("details button clipped outside viewport: %+v", details)
	}
	for _, name := range []string{"GitHub", "Vercel", "Supabase", "Notion"} {
		hdr, hdrOK := tester.Find(name)
		cell, cellOK := tester.Find("全局默认 * · " + name)
		if !hdrOK || !cellOK {
			t.Fatalf("missing header or cell for %s: hdr=%v cell=%v", name, hdrOK, cellOK)
		}
		// Header text sits to the right of its 16px icon + 7px gap, so group center is (hdr.X - 23 + hdr.X + hdr.W) / 2.
		groupCenter := (hdr.X - 23 + hdr.X + hdr.W) / 2
		cellCenter := cell.X + cell.W/2
		if diff := groupCenter - cellCenter; diff < -2 || diff > 2 {
			t.Fatalf("%s header center (%.1f) and checkbox center (%.1f) misaligned: hdr=%+v cell=%+v", name, groupCenter, cellCenter, hdr, cell)
		}
	}
}
