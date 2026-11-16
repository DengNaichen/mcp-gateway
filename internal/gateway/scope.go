package gateway

import (
	"gatewaylab/internal/access"

	"context"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"time"
)

func (g *Gateway) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

func (g *Gateway) isAdminCaller(ctx context.Context) bool {
	if g.access == nil {
		return true
	}
	p, ok := access.PrincipalFrom(ctx)
	return ok && p.Admin
}

func withRequestScope(ctx context.Context, sc scope.ResolvedRequestScope) context.Context {
	return scope.WithRequestScope(ctx, sc)
}
func requestScopeFrom(ctx context.Context) (scope.ResolvedRequestScope, bool) {
	return scope.RequestScopeFrom(ctx)
}
func withInternalScopeBypass(ctx context.Context) context.Context {
	return scope.WithInternalBypass(ctx)
}
func hasInternalScopeBypass(ctx context.Context) bool           { return scope.HasInternalBypass(ctx) }
func scopeEventForContext(ctx context.Context) scope.EventScope { return scope.EventForContext(ctx) }
func (g *Gateway) HookBinPath() string                          { return g.scope.HookBinPath() }
func (g *Gateway) isScopeEnforced() bool                        { return g.scope.IsEnforced() }
func (g *Gateway) ScopeOverview() scope.ScopeOverview           { return g.scope.ScopeOverview() }
func (g *Gateway) SetScopeMode(enforce bool) error              { return g.scope.SetScopeMode(enforce) }
func (g *Gateway) SaveRepository(ctx context.Context, in scope.SaveRepositoryRequest) (scope.Repository, error) {
	out, err := g.scope.SaveRepository(ctx, in)
	return out, err
}
func (g *Gateway) DeleteRepository(id string) error { return g.scope.DeleteRepository(id) }
func (g *Gateway) SaveScopeRule(in scope.ScopeRule) (scope.ScopeRule, error) {
	out, err := g.scope.SaveScopeRule(in)
	return out, err
}
func (g *Gateway) DeleteScopeRule(repoKey, agentKey string) error {
	return g.scope.DeleteScopeRule(repoKey, agentKey)
}
func (g *Gateway) PreviewScope(ctx context.Context, repoKey, actor string) (scope.ScopePreviewResponse, error) {
	out, err := g.scope.PreviewScope(ctx, repoKey, access.RequestActor(ctx, actor))
	return out, err
}
func (g *Gateway) ScopeSnippet(agent string) (scope.ScopeSnippetResponse, error) {
	out, err := g.scope.ScopeSnippet(agent)
	return out, err
}
func (g *Gateway) resolveMCPToolCallScope(ctx context.Context, urlAgent, toolName string, rawArgs []byte) (context.Context, []byte, error) {
	next, args, err := g.scope.ResolveMCPToolCallScope(ctx, urlAgent, toolName, rawArgs)
	return next, args, err
}

func (g *Gateway) evaluateSimulatedScope(sim scope.SimScope) (scope.ResolvedRequestScope, error) {
	out, err := g.scope.EvaluateSimulatedScope(sim)
	return out, err
}
func (g *Gateway) checkExecutionScope(ctx context.Context) error {
	return g.scope.CheckExecutionScope(ctx)
}
func (g *Gateway) scopeAllowsTool(ctx context.Context, t registry.Tool) bool {
	return g.scope.AllowsTool(ctx, t)
}
