package gateway

import (
	"net/http"
	"slices"
	"strings"
)

func (g *Gateway) registerMaintenance(mux *http.ServeMux) {
	m := g.maintenance
	mux.HandleFunc("GET /api/maintenance", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		sources := []MaintenanceSource{}
		runs := []MaintenanceRun{}
		for _, s := range m.sources {
			sources = append(sources, s)
		}
		slices.SortFunc(sources, func(a, b MaintenanceSource) int { return strings.Compare(a.ServerID, b.ServerID) })
		for _, run := range m.runs {
			runs = append(runs, run)
		}
		slices.SortFunc(runs, func(a, b MaintenanceRun) int { return b.CreatedAt.Compare(a.CreatedAt) })
		if len(runs) > 50 {
			runs = runs[:50]
		}
		respond(w, 200, map[string]any{"runtime": m.runtimeStatus(), "sources": sources, "runs": runs})
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
		m.mu.Lock()
		defer m.mu.Unlock()
		suite, ok := m.suites[r.PathValue("id")]
		if !ok {
			suite = AcceptanceSuite{ServerID: r.PathValue("id"), Contracts: []ContractCase{}, Routing: []RoutingCase{}}
		}
		respond(w, 200, suite)
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
		m.mu.Lock()
		defer m.mu.Unlock()
		run, ok := m.runs[r.PathValue("id")]
		if !ok {
			fail(w, problem(404, "任务不存在", nil))
			return
		}
		diff := []ToolChange{}
		if run.Tools != nil {
			diff = changes(run)
		}
		respond(w, 200, map[string]any{"run": run, "changes": diff})
	})
	mux.HandleFunc("GET /api/maintenance/runs/{id}/artifacts/{name}", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		run, ok := m.runs[r.PathValue("id")]
		m.mu.Unlock()
		if !ok {
			fail(w, problem(404, "任务不存在", nil))
			return
		}
		name := r.PathValue("name")
		if _, ok := run.Artifacts[name]; !ok {
			fail(w, problem(404, "产物不存在", nil))
			return
		}
		b, err := regularArtifact(m.directory(run.ID)+"/revision", name)
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
		if err := m.publishAs(r.PathValue("id"), false, true, requestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"published": true})
	})
	mux.HandleFunc("POST /api/maintenance/runs/{id}/rollback", func(w http.ResponseWriter, r *http.Request) {
		if err := m.publishAs(r.PathValue("id"), true, true, requestActor(r.Context(), "")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"rolled_back": true})
	})
}
