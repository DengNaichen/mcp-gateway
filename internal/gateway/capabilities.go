package gateway

import (
	"context"
	"slices"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
)

type ToolSchema struct {
	registry.Tool
	NeedsApproval bool `json:"needs_approval,omitempty"`
}

func (g *Gateway) ToolSchema(ctx context.Context, id, actor string) (ToolSchema, error) {
	if err := g.scope.CheckExecutionScope(ctx); err != nil {
		return ToolSchema{}, err
	}
	reg := g.registry.Snapshot()
	i := slices.IndexFunc(reg.Tools, func(t registry.Tool) bool { return t.ID == id })
	if i < 0 {
		return ToolSchema{}, fault.New(fault.NotFound, "工具不存在", nil)
	}
	tool := reg.Tools[i]
	server := slices.IndexFunc(reg.Servers, func(s registry.Server) bool { return s.ID == tool.ServerID && s.Enabled })
	decision := g.authorize(ctx, tool, actor)
	if server < 0 || decision.Effect == policy.EffectDeny {
		return ToolSchema{}, fault.WithCode(fault.New(fault.Forbidden, "工具已禁用或没有访问权限", nil), "policy_denied")
	}
	return ToolSchema{Tool: tool, NeedsApproval: decision.Effect == policy.EffectApprove}, nil
}

type CatalogServer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ToolCount   int    `json:"tool_count"`
}
type Catalog struct {
	Servers []CatalogServer      `json:"servers"`
	Tools   []routing.Capability `json:"tools"`
}

func (g *Gateway) Catalog(ctx context.Context, actor string, includeDemo bool) Catalog {
	reg := g.registry.Snapshot()
	out := Catalog{Servers: []CatalogServer{}, Tools: []routing.Capability{}}
	for _, server := range reg.Servers {
		if !server.Enabled || server.Demo && !includeDemo {
			continue
		}
		count := 0
		for _, tool := range reg.Tools {
			if tool.ServerID != server.ID {
				continue
			}
			decision := g.authorize(ctx, tool, actor)
			if decision.Effect != policy.EffectDeny {
				capability := routing.Summarize(tool)
				capability.NeedsApproval = decision.Effect == policy.EffectApprove
				out.Tools = append(out.Tools, capability)
				count++
			}
		}
		if count > 0 {
			out.Servers = append(out.Servers, CatalogServer{server.ID, server.Name, server.Description, count})
		}
	}
	return out
}
