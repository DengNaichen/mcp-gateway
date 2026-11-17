package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
)

func (g *server) registerExecute(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/discover", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.DiscoverRequest
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "discover_tools", rawBody, mapPtr(in.SimScope, domainSimScope))
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Discover(scopedCtx, domainDiscoverRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, discoveryPresentation(out, in.SummaryOnly))
	})
	mux.HandleFunc("POST /api/invoke", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.InvokeRequest
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "invoke_tool", rawBody, mapPtr(in.SimScope, domainSimScope))
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Invoke(scopedCtx, domainInvokeRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireInvokeResult(out))
	})
}
