package api

import (
	"net/http"
)

func (g *server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, mapSlice(g.gw.Activity().Recent(), wireEvent))
	})
}
