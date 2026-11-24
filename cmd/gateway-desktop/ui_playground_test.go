package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestBuildPlaygroundViewModelAndReplayHandoff(t *testing.T) {
	state := &apitypes.Bootstrap{
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "gh", Name: "GitHub", Enabled: true},
				{ID: "demo", Name: "Demo", Enabled: true, Demo: true},
				{ID: "off", Name: "Disabled", Enabled: false},
			},
			Tools: []apitypes.Tool{
				{ID: "gh.list", ServerID: "gh", Name: "list", Enabled: true, Actors: []string{"developer", "observer"}},
				{ID: "gh.merge", ServerID: "gh", Name: "merge", Enabled: true, Actors: []string{"developer"}},
				{ID: "demo.ping", ServerID: "demo", Name: "ping", Enabled: true, Actors: []string{"*"}},
				{ID: "off.fn", ServerID: "off", Name: "fn", Enabled: true, Actors: []string{"*"}},
			},
		},
		Scopes: apitypes.ScopeOverview{
			Repositories: []apitypes.Repository{
				{ID: "r1", Name: "MyRepo"},
				{ID: "r2", Name: "MyRepo"},
			},
			Rules: []apitypes.ScopeRule{
				{
					RepoKey: "r1",
					Grants: []apitypes.ServiceGrant{
						{ServerID: "gh", Access: "rw", ToolIDs: []string{"gh.list"}},
					},
				},
			},
		},
	}

	st := &playgroundState{
		serviceScopeLabel: "全部已启用服务",
		simRepoLabel:      "管理员全量",
	}

	vm := buildPlaygroundViewModel(state, st)
	if vm.effectiveActor != "developer" {
		t.Fatalf("effectiveActor = %q, want developer", vm.effectiveActor)
	}
	if vm.eligibleCount != 2 {
		t.Fatalf("eligibleCount = %d, want 2 (gh.list + gh.merge)", vm.eligibleCount)
	}
	if len(vm.scopeOptions) != 2 {
		t.Fatalf("scopeOptions = %v, want 2 (all + GitHub)", vm.scopeOptions)
	}
	if len(vm.repoOptions) != 4 {
		t.Fatalf("repoOptions = %v, want 4 with disambiguated duplicate name", vm.repoOptions)
	}

	// Simulate repo scope r1 restricting gh to only gh.list.
	st.simRepoKey = "r1"
	vmScoped := buildPlaygroundViewModel(state, st)
	if vmScoped.eligibleCount != 1 {
		t.Fatalf("scoped eligibleCount = %d, want 1", vmScoped.eligibleCount)
	}

	// Replay handoff resets demo and result state.
	st.includeDemo = true
	st.result = &apitypes.DiscoverResult{Status: "selected"}
	pgApplyReplayHandoff(st, state, playgroundReplayIntent{
		intent:   "list issues",
		actor:    "observer",
		repoKey:  "r2",
		hasScope: true,
	})
	if st.intent != "list issues" || st.actor != "observer" || st.simRepoKey != "r2" || st.includeDemo || st.result != nil {
		t.Fatalf("unexpected state after replay handoff: %+v", st)
	}
}

func TestPlaygroundPolicySimulation(t *testing.T) {
	var (
		mu   sync.Mutex
		reqs []apitypes.PolicyEvaluateRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost || r.URL.Path != "/api/policies/evaluate" {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var in apitypes.PolicyEvaluateRequest
		_ = json.Unmarshal(raw, &in)
		mu.Lock()
		reqs = append(reqs, in)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(apitypes.PolicyEvaluateResult{
			Effect: "approve", RuleID: "policy:pol-2", Reason: "merge needs review", Trust: "declared",
			Trace: []apitypes.PolicyTraceStep{
				{Stage: "internal"},
				{Stage: "identity", Matched: true, Effect: "allow", RuleID: "identity"},
				{Stage: "deny"},
				{Stage: "baseline", Matched: true, Effect: "allow", RuleID: "scope:repo-1"},
				{Stage: "approve", Matched: true, Effect: "approve", RuleID: "policy:pol-2", Reason: "merge needs review"},
			},
		})
	}))
	defer srv.Close()

	boot := apitypes.Bootstrap{
		Access: apitypes.AccessInfo{Actor: "developer", SecurityMode: apitypes.SecurityModeLocalGuardrail},
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{{ID: "gh", Name: "gh CLI", Enabled: true}, {ID: "demo", Name: "Demo", Enabled: true, Demo: true}},
			Tools: []apitypes.Tool{
				{ID: "gh.pr.merge", ServerID: "gh", Enabled: true, Actors: []string{"*"}},
				{ID: "demo.echo", ServerID: "demo", Enabled: true, Actors: []string{"*"}},
			},
		},
		Scopes: apitypes.ScopeOverview{Repositories: []apitypes.Repository{{ID: "repo-1", Name: "alpha-repo"}}},
	}
	u := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, Mode: modeAttached})
	u.State = &boot
	u.Loading = false
	ps := u.playgroundState().policyState()
	ps.repoLabel = "alpha-repo"
	ps.actor = " developer "
	tester := ui.NewTester(func(c *ui.Context) {
		defer u.enterHeadlessFrame()()
		u.renderPlayground(c)
	}, 1400, 1600)
	tester.Frame()

	if ps.toolID != "gh.pr.merge" {
		t.Fatalf("toolID = %q, want first real enabled tool", ps.toolID)
	}
	if err := tester.Click("推演裁决"); err != nil {
		t.Fatal(err)
	}
	waitAsyncIdle(t, tester, u)

	mu.Lock()
	if len(reqs) != 1 || reqs[0] != (apitypes.PolicyEvaluateRequest{ToolID: "gh.pr.merge", RepoID: "repo-1", Actor: "developer"}) {
		t.Fatalf("unexpected evaluate requests: %+v", reqs)
	}
	mu.Unlock()
	for _, want := range []string{"approve", "policy:pol-2", "trust: declared", "身份上限", "显式 Deny", "仓库基线", "显式 Approve", "未命中，继续", "approve · policy:pol-2 · merge needs review"} {
		if !tester.HasText(want) {
			t.Fatalf("policy result missing %q: %v", want, tester.Texts())
		}
	}
	if tester.HasText("内部请求") {
		t.Fatal("unmatched internal stage must stay hidden")
	}
}
