package gateway

import (
	"context"
	"slices"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
)

func (g *Gateway) ToolSchema(ctx context.Context, id, actor string) (registry.Tool, error) {
	if err := g.scope.CheckExecutionScope(ctx); err != nil {
		return registry.Tool{}, err
	}
	reg := g.Snapshot()
	i := slices.IndexFunc(reg.Tools, func(t registry.Tool) bool { return t.ID == id })
	if i < 0 {
		return registry.Tool{}, fault.New(fault.NotFound, "工具不存在", nil)
	}
	tool := reg.Tools[i]
	server := slices.IndexFunc(reg.Servers, func(s registry.Server) bool { return s.ID == tool.ServerID && s.Enabled })
	if server < 0 || !g.authorizedTool(ctx, tool, actor) {
		return registry.Tool{}, fault.New(fault.Forbidden, "工具已禁用或没有访问权限", nil)
	}
	return tool, nil
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
	reg := g.Snapshot()
	out := Catalog{Servers: []CatalogServer{}, Tools: []routing.Capability{}}
	for _, server := range reg.Servers {
		if !server.Enabled || server.Demo && !includeDemo {
			continue
		}
		count := 0
		for _, tool := range reg.Tools {
			if tool.ServerID == server.ID && g.authorizedTool(ctx, tool, actor) {
				out.Tools = append(out.Tools, routing.Summarize(tool))
				count++
			}
		}
		if count > 0 {
			out.Servers = append(out.Servers, CatalogServer{server.ID, server.Name, server.Description, count})
		}
	}
	return out
}
