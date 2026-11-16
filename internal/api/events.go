package api

import (
	"net/http"
)

func (g *server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		events := g.Activity().Recent()
		respond(w, 200, events)
	})
}
