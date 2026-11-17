package api

import (
	"context"
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/scope"
)

func (g *server) registerScopes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scopes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, wireScopeOverview(g.gw.Scope().ScopeOverview()))
	})
	mux.HandleFunc("PUT /api/scopes/mode", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ScopeModeRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.gw.Scope().SetScopeMode(in.Enforce); err != nil {
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
		repo, err := g.gw.Scope().SaveRepository(r.Context(), domainSaveRepository(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireRepository(repo))
	})
	mux.HandleFunc("DELETE /api/scopes/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.Scope().DeleteRepository(r.PathValue("id")); err != nil {
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
		rule, err := g.gw.Scope().SaveScopeRule(domainRule(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireRule(rule))
	})
	mux.HandleFunc("DELETE /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := g.gw.Scope().DeleteScopeRule(q.Get("repo_key"), q.Get("agent_key")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, apitypes.Deleted{Deleted: true})
	})
	mux.HandleFunc("POST /api/scopes/preview", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ScopePreviewRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.gw.PreviewScope(r.Context(), in.RepoKey, in.Actor)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireScopePreview(out))
	})
	mux.HandleFunc("GET /api/scopes/snippets", func(w http.ResponseWriter, r *http.Request) {
		out, err := g.gw.Scope().ScopeSnippet(r.URL.Query().Get("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, wireSnippet(out))
	})
}

func (g *server) resolveRESTScope(ctx context.Context, r *http.Request, op string, rawBody []byte, sim *scope.SimScope) (context.Context, error) {
	next, err := g.gw.Scope().ResolveRESTScope(ctx, scope.RESTRequest{Token: r.Header.Get("X-Gateway-Scope"), Agent: r.URL.Query().Get("agent"), ToolID: r.PathValue("id")}, op, rawBody, sim)
	return next, err
}
