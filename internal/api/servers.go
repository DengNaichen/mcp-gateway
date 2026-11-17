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
