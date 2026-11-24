package scope

import (
	"slices"

	"gatewaylab/internal/workspace"
)

// resolvePrecedenceRule evaluates whole-rule replacement precedence:
// - Matched repo R: R > *
// - Unconfigured repo: *
func resolvePrecedenceRule(rules []ScopeRule, matchedRepo bool, repoID string) ScopeRule {
	var candidates []string
	if matchedRepo && repoID != "" && repoID != "*" {
		candidates = append(candidates, repoID)
	}
	candidates = append(candidates, "*")

	for _, repoKey := range candidates {
		for _, r := range rules {
			if r.RepoKey == repoKey {
				return r
			}
		}
	}
	return ScopeRule{RepoKey: "*", Grants: []ServiceGrant{}}
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
