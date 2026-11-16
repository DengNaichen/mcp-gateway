package api

import (
	"net/http"

	"gatewaylab/internal/model"
)

func (g *server) registerModel(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			model.Settings
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveSettings(in.Settings, in.APIKey, in.ClearKey); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("PUT /api/model-profiles", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			model.ModelProfile
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		saved, err := g.SaveModelProfile(in.ModelProfile, in.APIKey, in.ClearKey)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, saved)
	})
	mux.HandleFunc("POST /api/model-profiles/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		activated, err := g.ActivateModelProfile(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, activated)
	})
	mux.HandleFunc("DELETE /api/model-profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteModelProfile(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"deleted": true})
	})
}
