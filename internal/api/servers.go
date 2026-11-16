package api

import (
	"net/http"

	"gatewaylab/internal/registry"
)

func (g *server) registerServers(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/servers", func(w http.ResponseWriter, r *http.Request) {
		var in registry.Server
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.Registry().SaveServer(in); err != nil {
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
		respond(w, 200, map[string]int{"imported": n})
	})
	mux.HandleFunc("PUT /api/tools", func(w http.ResponseWriter, r *http.Request) {
		var in registry.Tool
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.Registry().SaveTool(in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
}
