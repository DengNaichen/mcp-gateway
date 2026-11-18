package scope

import "testing"

func TestRulePrecedenceReplacesWholeRule(t *testing.T) {
	rules := []ScopeRule{
		{RepoKey: "*", Grants: []ServiceGrant{{ServerID: "global", Access: "rw"}}},
		{RepoKey: "repo", Grants: []ServiceGrant{{ServerID: "repo", Access: "rw"}}},
		{RepoKey: "empty-repo", Grants: []ServiceGrant{}},
	}
	for _, tc := range []struct {
		repo, wantRepo string
		matched        bool
		wantGrants     int
	}{
		{"repo", "repo", true, 1},
		{"empty-repo", "empty-repo", true, 0},
		{"unbound-rule", "*", true, 1},
		{"other", "*", false, 1},
	} {
		got := resolvePrecedenceRule(rules, tc.matched, tc.repo)
		if got.RepoKey != tc.wantRepo || len(got.Grants) != tc.wantGrants {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	if got := resolvePrecedenceRule(nil, true, "repo"); got.Grants == nil || len(got.Grants) != 0 {
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
