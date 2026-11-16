package api

import (
	"context"
	"net/http"

	"gatewaylab/internal/scope"
)

func (g *server) registerScopes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scopes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, g.Scope().ScopeOverview())
	})
	mux.HandleFunc("PUT /api/scopes/mode", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enforce bool `json:"enforce"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.Scope().SetScopeMode(in.Enforce); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"enforce": in.Enforce})
	})
	mux.HandleFunc("PUT /api/scopes/repositories", func(w http.ResponseWriter, r *http.Request) {
		var in scope.SaveRepositoryRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		repo, err := g.Scope().SaveRepository(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, repo)
	})
	mux.HandleFunc("DELETE /api/scopes/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.Scope().DeleteRepository(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("PUT /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		var in scope.ScopeRule
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		rule, err := g.Scope().SaveScopeRule(in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, rule)
	})
	mux.HandleFunc("DELETE /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := g.Scope().DeleteScopeRule(q.Get("repo_key"), q.Get("agent_key")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("POST /api/scopes/preview", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RepoKey string `json:"repo_key"`
			Actor   string `json:"actor"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.PreviewScope(r.Context(), in.RepoKey, in.Actor)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/scopes/snippets", func(w http.ResponseWriter, r *http.Request) {
		out, err := g.Scope().ScopeSnippet(r.URL.Query().Get("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
}

func (g *server) resolveRESTScope(ctx context.Context, r *http.Request, op string, rawBody []byte, sim *scope.SimScope) (context.Context, error) {
	next, err := g.Scope().ResolveRESTScope(ctx, scope.RESTRequest{Token: r.Header.Get("X-Gateway-Scope"), Agent: r.URL.Query().Get("agent"), ToolID: r.PathValue("id")}, op, rawBody, sim)
	return next, err
}
