package scope

import (
	"context"
	"slices"
	"strings"

	"gatewaylab/internal/workspace"
)

func (g *Service) PreviewScope(ctx context.Context, repoKey, actor string) (ScopePreviewResponse, error) {
	repoKey = strings.TrimSpace(repoKey)
	if repoKey == "" {
		repoKey = "*"
	}

	g.mu.RLock()
	if repoKey != "*" && !slices.ContainsFunc(g.scope.Repositories, func(r Repository) bool { return r.ID == repoKey }) {
		g.mu.RUnlock()
		return ScopePreviewResponse{}, invalid("指定的仓库不存在")
	}
	rules := slices.Clone(g.scope.Rules)
	reg := g.snapshot()
	g.mu.RUnlock()

	enabledServers := map[string]Server{}
	for _, s := range reg.Servers {
		if s.Enabled && !s.Demo {
			enabledServers[s.ID] = s
		}
	}

	previews := make([]ScopeAgentPreview, 0, len(workspace.KnownAgents))
	for _, agent := range workspace.KnownAgents {
		matched := resolvePrecedenceRule(rules, repoKey != "*", repoKey, agent)
		srvSummaries := make([]ScopeServerSummary, 0, len(matched.Grants))
		totalTools := 0
		hasWrite := false

		for _, gr := range matched.Grants {
			if _, ok := enabledServers[gr.ServerID]; !ok {
				continue
			}
			tCount := 0
			for _, tool := range reg.Tools {
				if tool.ServerID != gr.ServerID {
					continue
				}
				if !g.identityAllowsTool(ctx, tool, actor) {
					continue
				}
				if !grantAllowsTool(matched, tool) {
					continue
				}
				tCount++
				if !tool.ReadOnly {
					hasWrite = true
				}
			}
			if tCount > 0 {
				srvSummaries = append(srvSummaries, ScopeServerSummary{
					ServerID:  gr.ServerID,
					Access:    gr.Access,
					ToolCount: tCount,
				})
				totalTools += tCount
			}
		}

		previews = append(previews, ScopeAgentPreview{
			Agent:           agent,
			MatchedRepoKey:  matched.RepoKey,
			MatchedAgentKey: matched.AgentKey,
			SourceLabel:     formatRuleSourceLabel(matched),
			ServerCount:     len(srvSummaries),
			ToolCount:       totalTools,
			HasWrite:        hasWrite,
			Servers:         srvSummaries,
		})
	}

	return ScopePreviewResponse{
		RepoKey: repoKey,
		Agents:  previews,
	}, nil
}

func formatRuleSourceLabel(rule ScopeRule) string {
	if len(rule.Grants) == 0 {
		if rule.AgentKey != "*" {
			return "客户端例外"
		}
		if rule.RepoKey != "*" {
			return "仓库默认"
		}
		return "无权限"
	}
	if rule.AgentKey != "*" {
		return "客户端例外"
	}
	if rule.RepoKey != "*" {
		return "仓库默认"
	}
	return "全局默认"
}

func (g *Service) ScopeSnippet(agent string) (ScopeSnippetResponse, error) {
	spec, err := workspace.BuildSnippet(workspace.SnippetOptions{
		Agent:    agent,
		Endpoint: g.baseURL + "/mcp",
		BinPath:  g.HookBinPath(),
		DataDir:  g.dir,
	})
	if err != nil {
		return ScopeSnippetResponse{}, invalid(err.Error())
	}
	return ScopeSnippetResponse{
		Agent:        spec.Agent,
		ConfigPath:   spec.ConfigPath,
		MCPURL:       spec.MCPURL,
		HookBinPath:  spec.HookBinPath,
		Instructions: spec.Instructions,
		Snippet:      spec.Snippet,
	}, nil
}
