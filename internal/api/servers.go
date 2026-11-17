package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
)

func (g *server) registerServers(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/servers", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.Server
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.Registry().SaveServer(domainServer(in)); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
	mux.HandleFunc("POST /api/servers/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		n, err := g.ImportMCP(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.SyncServerResult{Imported: n})
	})
	mux.HandleFunc("POST /api/servers/openapi", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ImportOpenAPIRequest
		// A 4 MB document can grow when JSON-escaped; the gateway enforces 4 MB itself.
		if _, err := decodeRawLimit(w, r, &in, 8<<20); err != nil {
			fail(w, err)
			return
		}
		n, err := g.ImportOpenAPI(r.Context(), domainServer(in.Server), []byte(in.Spec), in.SpecURL, in.OperationIDs)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.ImportOpenAPIResult{Imported: n})
	})
	mux.HandleFunc("PUT /api/tools", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.Tool
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.Registry().SaveTool(domainTool(in)); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
}
