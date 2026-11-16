package scope

import (
	"slices"

	"gatewaylab/internal/workspace"
)

// resolvePrecedenceRule evaluates whole-rule replacement precedence:
// - Matched repo R: (R, agent) > (R, *) > (*, agent) > (*, *)
// - Unconfigured repo: (*, agent) > (*, *)
func resolvePrecedenceRule(rules []ScopeRule, matchedRepo bool, repoID, agent string) ScopeRule {
	if agent == "" {
		agent = "*"
	}
	type coord struct{ repo, agent string }
	var candidates []coord
	if matchedRepo && repoID != "" && repoID != "*" {
		if agent != "*" {
			candidates = append(candidates, coord{repoID, agent})
		}
		candidates = append(candidates, coord{repoID, "*"})
	}
	if agent != "*" {
		candidates = append(candidates, coord{"*", agent})
	}
	candidates = append(candidates, coord{"*", "*"})

	for _, c := range candidates {
		for _, r := range rules {
			if r.RepoKey == c.repo && r.AgentKey == c.agent {
				return r
			}
		}
	}
	return ScopeRule{RepoKey: "*", AgentKey: "*", Grants: []ServiceGrant{}}
}

func grantAllowsTool(rule ScopeRule, t Tool) bool {
	for _, gr := range rule.Grants {
		if gr.ServerID != t.ServerID {
			continue
		}
		if gr.Access == "ro" && !t.ReadOnly {
			return false
		}
		if len(gr.ToolIDs) > 0 && !slices.Contains(gr.ToolIDs, t.ID) {
			return false
		}
		return true
	}
	return false
}

// matchConfiguredRepoByAnchors matches a workspace strictly by local CommonDir
// for Git repositories, or longest segment-boundary RootPath for non-Git
// directories. An unbound Git repository never falls through to inherit a
// parent non-Git RootPath anchor, and RemoteHint never authorizes access.
func matchConfiguredRepoByAnchors(repos []Repository, kind, commonDir, root string) *Repository {
	if kind == string(workspace.KindGit) && commonDir != "" {
		for i := range repos {
			if slices.Contains(repos[i].GitCommonDirs, commonDir) {
				return &repos[i]
			}
		}
		return nil
	}
	if root != "" {
		var best *Repository
		bestLen := -1
		for i := range repos {
			for _, rp := range repos[i].RootPaths {
				if workspace.HasPathPrefix(root, rp) && len(rp) > bestLen {
					best = &repos[i]
					bestLen = len(rp)
				}
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}
