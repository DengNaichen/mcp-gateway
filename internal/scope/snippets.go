package scope

import "gatewaylab/internal/workspace"

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
