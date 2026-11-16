package gateway

import (
	"context"

	"gatewaylab/internal/access"
	"gatewaylab/internal/scope"
)

func (g *Gateway) isAdminCaller(ctx context.Context) bool {
	if g.access == nil {
		return true
	}
	p, ok := access.PrincipalFrom(ctx)
	return ok && p.Admin
}

func (g *Gateway) PreviewScope(ctx context.Context, repoKey, actor string) (scope.ScopePreviewResponse, error) {
	out, err := g.scope.PreviewScope(ctx, repoKey, access.RequestActor(ctx, actor))
	return out, err
}
