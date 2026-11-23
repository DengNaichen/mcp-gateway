package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/registry"
)

func (g *server) registerSkills(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/skills", func(w http.ResponseWriter, _ *http.Request) {
		skills, err := g.gw.Skills()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, mapSlice(skills, wireSkill))
	})
	mux.HandleFunc("PUT /api/skills", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.Skill
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		skill, err := g.gw.SaveSkill(registry.Skill(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireSkill(skill))
	})
	mux.HandleFunc("DELETE /api/skills/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.DeleteSkill(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, apitypes.Deleted{Deleted: true})
	})
}

func wireSkill(skill registry.Skill) apitypes.Skill { return apitypes.Skill(skill) }
