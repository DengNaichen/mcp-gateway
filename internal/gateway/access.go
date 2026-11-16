package gateway

import (
	"context"

	"gatewaylab/internal/registry"
)

func (g *Gateway) identityAllowsTool(ctx context.Context, t registry.Tool, actor string) bool {
	return g.access.IdentityAllowsTool(ctx, t, actor)
}
func (g *Gateway) authorizedTool(ctx context.Context, t registry.Tool, actor string) bool {
	return g.identityAllowsTool(ctx, t, actor) && g.scope.AllowsTool(ctx, t)
}
