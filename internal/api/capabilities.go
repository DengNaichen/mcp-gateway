package api

import (
	"net/http"

	"gatewaylab/internal/gateway"
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
		ctx, err := g.resolveRESTScope(r.Context(), r, "catalog", nil, nil)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireCatalog(g.Catalog(ctx, r.URL.Query().Get("actor"), r.URL.Query().Get("include_demo") == "true")))
	})
	mux.HandleFunc("GET /api/tools/{id}/schema", func(w http.ResponseWriter, r *http.Request) {
		ctx, err := g.resolveRESTScope(r.Context(), r, "get_tool_schema", nil, nil)
		if err != nil {
			fail(w, err)
			return
		}
		tool, err := g.ToolSchema(ctx, r.PathValue("id"), r.URL.Query().Get("actor"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireTool(tool))
	})
}
