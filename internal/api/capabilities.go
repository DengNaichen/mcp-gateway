package api

import (
	"context"
	"net/http"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/scope"
)

// discoveryPresentation is the discovery body shared by POST /api/discover and
// the discover_tools MCP tool.
func discoveryPresentation(result gateway.DiscoverResult, summary bool) any {
	if summary {
		return wireDiscoverSummary(result)
	}
	return wireDiscoverResult(result)
}

func (g *server) registerCapabilities(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		ctx, err := g.resolveReadScope(r, "catalog")
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireCatalog(g.gw.Catalog(ctx, r.URL.Query().Get("actor"), r.URL.Query().Get("include_demo") == "true")))
	})
	mux.HandleFunc("GET /api/tools/{id}/schema", func(w http.ResponseWriter, r *http.Request) {
		ctx, err := g.resolveReadScope(r, "get_tool_schema")
		if err != nil {
			fail(w, err)
			return
		}
		tool, err := g.gw.ToolSchema(ctx, r.PathValue("id"), r.URL.Query().Get("actor"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireTool(tool))
	})
}

func (g *server) resolveReadScope(r *http.Request, op string) (context.Context, error) {
	started := time.Now()
	var sim *scope.SimScope
	query := r.URL.Query()
	if query.Has("sim_scope") {
		sim = &scope.SimScope{RepoKey: query.Get("sim_scope")}
	}
	ctx, err := g.resolveRESTScope(r.Context(), r, op, nil, sim)
	if err == nil && sim != nil {
		event := scope.EventForContext(ctx)
		if event.Status == "simulated" || event.Status == "bypass" {
			g.activity.Record(op, access.RequestActor(ctx, query.Get("actor")), event.Status, r.PathValue("id"), started, map[string]any{"scope": event})
		}
	}
	return ctx, err
}
