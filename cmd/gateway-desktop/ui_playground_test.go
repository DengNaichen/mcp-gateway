package main

import (
	"testing"

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
