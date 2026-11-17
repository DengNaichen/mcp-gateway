package api

import (
	"net/http"

	"gatewaylab/internal/access"
	"gatewaylab/internal/apitypes"
)

func (g *server) registerMaintenance(mux *http.ServeMux) {
	m := g.Maintenance()
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, wireMaintenanceStatus(m.Status()))
	})
	mux.HandleFunc("PUT /api/maintenance/sources/{id}/acceptance", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.AcceptanceSuite
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		suite := domainSuite(in)
		suite.ServerID = r.PathValue("id")
		if err := m.SaveAcceptance(suite); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Saved{Saved: true})
	})
	mux.HandleFunc("GET /api/maintenance/sources/{id}/acceptance", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, wireSuite(m.Acceptance(r.PathValue("id"))))
	})
	mux.HandleFunc("PUT /api/maintenance/sources", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.MaintenanceSource
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := m.SaveSource(domainSource(in)); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Saved{Saved: true})
	})
	mux.HandleFunc("POST /api/maintenance/sources/{id}/run", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.RunMaintenanceRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		run, err := m.Run(r.PathValue("id"), in.Force)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 202, wireRun(run))
	})
	mux.HandleFunc("GET /api/maintenance/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		details, err := m.RunDetails(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireRunDetails(details))
	})
	mux.HandleFunc("GET /api/maintenance/runs/{id}/artifacts/{name}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		b, err := m.Artifact(r.PathValue("id"), name)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.MaintenanceArtifact{Name: name, Content: string(b)})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Cancel(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Cancelled{Cancelled: true})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/publish", func(w http.ResponseWriter, r *http.Request) {
		if err := m.PublishAs(r.PathValue("id"), false, true, access.RequestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Published{Published: true})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		if err := m.PublishAs(r.PathValue("id"), true, true, access.RequestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.RolledBack{RolledBack: true})
	})
}
