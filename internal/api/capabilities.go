package api

import (
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/routing"
	"net/http"
)

func discoveryPresentation(result gateway.DiscoverResult, summary bool) any {
	if !summary {
		return result
	}
	out := struct {
		gateway.DiscoverResult
		Tools []routing.Capability `json:"tools"`
	}{DiscoverResult: result, Tools: []routing.Capability{}}
	for _, t := range result.Tools {
		c := routing.Summarize(t.Tool)
		c.Reason = t.Reason
		out.Tools = append(out.Tools, c)
	}
	return out
}

func (g *server) registerCapabilities(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/catalog", func(w http.ResponseWriter, r *http.Request) {
		ctx, err := g.resolveRESTScope(r.Context(), r, "catalog", nil, nil)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, g.Catalog(ctx, r.URL.Query().Get("actor"), r.URL.Query().Get("include_demo") == "true"))
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
		respond(w, 200, tool)
	})
}
