package gateway

import (
	"net/http"
)

func (g *Gateway) registerMaintenance(mux *http.ServeMux) {
	m := g.maintenance
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, m.Status())
	})
	mux.HandleFunc("PUT /api/maintenance/sources/{id}/acceptance", func(w http.ResponseWriter, r *http.Request) {
		var suite AcceptanceSuite
		if err := decode(w, r, &suite); err != nil {
			fail(w, err)
			return
		}
		suite.ServerID = r.PathValue("id")
		if err := m.SaveAcceptance(suite); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("GET /api/maintenance/sources/{id}/acceptance", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, m.Acceptance(r.PathValue("id")))
	})
	mux.HandleFunc("PUT /api/maintenance/sources", func(w http.ResponseWriter, r *http.Request) {
		var in MaintenanceSource
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := m.SaveSource(in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("POST /api/maintenance/sources/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Force bool `json:"force"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		run, err := m.Run(r.PathValue("id"), in.Force)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 202, run)
	})
	mux.HandleFunc("GET /api/maintenance/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		details, err := m.RunDetails(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, details)
	})
	mux.HandleFunc("GET /api/maintenance/runs/{id}/artifacts/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		b, err := m.Artifact(r.PathValue("id"), name)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]string{"name": name, "content": string(b)})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Cancel(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"cancelled": true})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		if err := m.PublishAs(r.PathValue("id"), false, true, requestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"published": true})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		if err := m.PublishAs(r.PathValue("id"), true, true, requestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"rolled_back": true})
	})
}
