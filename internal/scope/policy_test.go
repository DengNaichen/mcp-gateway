package scope

import "testing"

func TestRulePrecedenceReplacesWholeRule(t *testing.T) {
	rules := []ScopeRule{
		{RepoKey: "*", AgentKey: "*", Grants: []ServiceGrant{{ServerID: "global", Access: "rw"}}},
		{RepoKey: "*", AgentKey: "codex", Grants: []ServiceGrant{{ServerID: "agent", Access: "rw"}}},
		{RepoKey: "repo", AgentKey: "*", Grants: []ServiceGrant{{ServerID: "repo", Access: "rw"}}},
		{RepoKey: "repo", AgentKey: "codex", Grants: []ServiceGrant{}},
	}
	for _, tc := range []struct {
		repo, agent, wantRepo, wantAgent string
		matched                          bool
	}{
		{"repo", "codex", "repo", "codex", true},
		{"repo", "claude", "repo", "*", true},
		{"other", "codex", "*", "codex", false},
		{"other", "claude", "*", "*", false},
	} {
		got := resolvePrecedenceRule(rules, tc.matched, tc.repo, tc.agent)
		if got.RepoKey != tc.wantRepo || got.AgentKey != tc.wantAgent {
			t.Fatalf("%+v: %+v", tc, got)
		}
		if got.RepoKey == "repo" && got.AgentKey == "codex" && len(got.Grants) != 0 {
			t.Fatal("empty rule inherited parent grants")
		}
	}
	if got := resolvePrecedenceRule(nil, true, "repo", "codex"); got.Grants == nil || len(got.Grants) != 0 {
		t.Fatal("missing default must deny with empty grants")
	}
}
func TestGrantReadOnlyAndToolAllowlist(t *testing.T) {
	rule := ScopeRule{Grants: []ServiceGrant{{ServerID: "svc", Access: "ro", ToolIDs: []string{"svc.read", "svc.write"}}}}
	for _, tc := range []struct {
		tool Tool
		want bool
	}{
		{Tool{ID: "svc.read", ServerID: "svc", ReadOnly: true}, true},
		{Tool{ID: "svc.write", ServerID: "svc", ReadOnly: false}, false},
		{Tool{ID: "svc.other", ServerID: "svc", ReadOnly: true}, false},
		{Tool{ID: "other.read", ServerID: "other", ReadOnly: true}, false},
	} {
		if got := grantAllowsTool(rule, tc.tool); got != tc.want {
			t.Fatalf("%s allowed=%v", tc.tool.ID, got)
		}
	}
}
func TestAnchorMatchingDoesNotAuthorizeByRemoteOrPathPrefix(t *testing.T) {
	repos := []Repository{{ID: "root", RootPaths: []string{"/projects"}, RemoteHint: "github.com/org/repo"}, {ID: "child", RootPaths: []string{"/projects/app"}}, {ID: "git", GitCommonDirs: []string{"/git/repo/.git"}}}
	for _, tc := range []struct{ kind, common, root, want string }{
		{"dir", "", "/projects/app/sub", "child"},
		{"dir", "", "/projects/apple", "root"},
		{"dir", "", "/projects-other", ""},
		{"git", "/unbound/.git", "/projects/app", ""},
		{"git", "/git/repo/.git", "/worktrees/repo", "git"},
	} {
		got := matchConfiguredRepoByAnchors(repos, tc.kind, tc.common, tc.root)
		id := ""
		if got != nil {
			id = got.ID
		}
		if id != tc.want {
			t.Fatalf("%+v: %q", tc, id)
		}
	}
}
