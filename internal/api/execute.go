package api

import (
	"net/http"

	"gatewaylab/internal/gateway"
	"gatewaylab/internal/scope"
)

func (g *server) registerExecute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/discover", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			gateway.DiscoverRequest
			SimScope *scope.SimScope `json:"sim_scope,omitempty"`
		}
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "discover_tools", rawBody, in.SimScope)
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Discover(scopedCtx, in.DiscoverRequest)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, discoveryPresentation(out, in.SummaryOnly))
	})
	mux.HandleFunc("POST /api/invoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			gateway.InvokeRequest
			SimScope *scope.SimScope `json:"sim_scope,omitempty"`
		}
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "invoke_tool", rawBody, in.SimScope)
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Invoke(scopedCtx, in.InvokeRequest)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})
}
