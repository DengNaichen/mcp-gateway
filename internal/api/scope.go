package api

import (
	"context"
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/scope"
)

func (g *server) registerScopes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scopes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, wireScopeOverview(g.scope.ScopeOverview()))
	})
	mux.HandleFunc("PUT /api/scopes/mode", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ScopeModeRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.scope.SetScopeMode(in.Enforce); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, in)
	})
	mux.HandleFunc("PUT /api/scopes/repositories", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.SaveRepositoryRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		repo, err := g.scope.SaveRepository(r.Context(), scope.SaveRepositoryRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireRepository(repo))
	})
	mux.HandleFunc("DELETE /api/scopes/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.scope.DeleteRepository(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, apitypes.Deleted{Deleted: true})
	})
	mux.HandleFunc("PUT /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ScopeRule
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		rule, err := g.scope.SaveScopeRule(domainRule(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireRule(rule))
	})
	mux.HandleFunc("DELETE /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		if err := g.scope.DeleteScopeRule(r.URL.Query().Get("repo_key")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, apitypes.Deleted{Deleted: true})
	})
	mux.HandleFunc("GET /api/scopes/snippets", func(w http.ResponseWriter, r *http.Request) {
		out, err := g.scope.ScopeSnippet(r.URL.Query().Get("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireSnippet(out))
	})
}

func (g *server) resolveRESTScope(ctx context.Context, r *http.Request, op string, rawBody []byte, sim *scope.SimScope) (context.Context, error) {
	next, err := g.scope.ResolveRESTScope(ctx, scope.RESTRequest{Token: r.Header.Get("X-Gateway-Scope"), Agent: r.URL.Query().Get("agent"), ToolID: r.PathValue("id")}, op, rawBody, sim)
	return next, err
}
