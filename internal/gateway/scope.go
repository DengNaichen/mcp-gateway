package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"gatewaylab/internal/scope"
)

func (g *Gateway) now() time.Time {
	if g.clock != nil {
		return g.clock()
	}
	return time.Now()
}

func (g *Gateway) isAdminCaller(ctx context.Context) bool {
	if g.access == nil {
		return true
	}
	p, ok := principalFrom(ctx)
	return ok && p.Admin
}

func (g *Gateway) registerScopes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/scopes", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, g.ScopeOverview())
	})
	mux.HandleFunc("PUT /api/scopes/mode", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Enforce bool `json:"enforce"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SetScopeMode(in.Enforce); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"enforce": in.Enforce})
	})
	mux.HandleFunc("PUT /api/scopes/repositories", func(w http.ResponseWriter, r *http.Request) {
		var in SaveRepositoryRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		repo, err := g.SaveRepository(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, repo)
	})
	mux.HandleFunc("DELETE /api/scopes/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteRepository(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("PUT /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		var in ScopeRule
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		rule, err := g.SaveScopeRule(in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, rule)
	})
	mux.HandleFunc("DELETE /api/scopes/rules", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if err := g.DeleteScopeRule(q.Get("repo_key"), q.Get("agent_key")); err != nil {
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
		out, err := g.ScopeSnippet(r.URL.Query().Get("agent"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, http.StatusOK, out)
	})
}

type ScopeOverview = scope.ScopeOverview
type Repository = scope.Repository
type ServiceGrant = scope.ServiceGrant
type ScopeRule = scope.ScopeRule
type ObservedRepo = scope.ObservedRepo
type ScopeServerSummary = scope.ScopeServerSummary
type ScopeAgentPreview = scope.ScopeAgentPreview
type ScopePreviewResponse = scope.ScopePreviewResponse
type ScopeSnippetResponse = scope.ScopeSnippetResponse
type SimScope = scope.SimScope
type EventScope = scope.EventScope
type SaveRepositoryRequest = scope.SaveRepositoryRequest

type resolvedRequestScope = scope.ResolvedRequestScope

func withRequestScope(ctx context.Context, sc resolvedRequestScope) context.Context {
	return scope.WithRequestScope(ctx, sc)
}
func requestScopeFrom(ctx context.Context) (resolvedRequestScope, bool) {
	return scope.RequestScopeFrom(ctx)
}
func withInternalScopeBypass(ctx context.Context) context.Context {
	return scope.WithInternalBypass(ctx)
}
func hasInternalScopeBypass(ctx context.Context) bool     { return scope.HasInternalBypass(ctx) }
func scopeEventForContext(ctx context.Context) EventScope { return scope.EventForContext(ctx) }
func (g *Gateway) HookBinPath() string                    { return g.scope.HookBinPath() }
func (g *Gateway) isScopeEnforced() bool                  { return g.scope.IsEnforced() }
func (g *Gateway) ScopeOverview() ScopeOverview           { return g.scope.ScopeOverview() }
func (g *Gateway) SetScopeMode(enforce bool) error        { return scopeError(g.scope.SetScopeMode(enforce)) }
func (g *Gateway) SaveRepository(ctx context.Context, in SaveRepositoryRequest) (Repository, error) {
	out, err := g.scope.SaveRepository(ctx, in)
	return out, scopeError(err)
}
func (g *Gateway) DeleteRepository(id string) error { return scopeError(g.scope.DeleteRepository(id)) }
func (g *Gateway) SaveScopeRule(in ScopeRule) (ScopeRule, error) {
	out, err := g.scope.SaveScopeRule(in)
	return out, scopeError(err)
}
func (g *Gateway) DeleteScopeRule(repoKey, agentKey string) error {
	return scopeError(g.scope.DeleteScopeRule(repoKey, agentKey))
}
func (g *Gateway) PreviewScope(ctx context.Context, repoKey, actor string) (ScopePreviewResponse, error) {
	out, err := g.scope.PreviewScope(ctx, repoKey, requestActor(ctx, actor))
	return out, scopeError(err)
}
func (g *Gateway) ScopeSnippet(agent string) (ScopeSnippetResponse, error) {
	out, err := g.scope.ScopeSnippet(agent)
	return out, scopeError(err)
}
func (g *Gateway) resolveMCPToolCallScope(ctx context.Context, urlAgent, toolName string, rawArgs []byte) (context.Context, []byte, error) {
	next, args, err := g.scope.ResolveMCPToolCallScope(ctx, urlAgent, toolName, rawArgs)
	return next, args, scopeError(err)
}
func (g *Gateway) resolveRESTScope(ctx context.Context, r *http.Request, op string, rawBody []byte, sim *SimScope) (context.Context, error) {
	next, err := g.scope.ResolveRESTScope(ctx, scope.RESTRequest{Token: r.Header.Get("X-Gateway-Scope"), Agent: r.URL.Query().Get("agent"), ToolID: r.PathValue("id")}, op, rawBody, sim)
	return next, scopeError(err)
}
func (g *Gateway) evaluateSimulatedScope(sim SimScope) (resolvedRequestScope, error) {
	out, err := g.scope.EvaluateSimulatedScope(sim)
	return out, scopeError(err)
}
func (g *Gateway) checkExecutionScope(ctx context.Context) error {
	return scopeError(g.scope.CheckExecutionScope(ctx))
}
func (g *Gateway) scopeAllowsTool(ctx context.Context, t Tool) bool {
	return g.scope.AllowsTool(ctx, t)
}
func scopeError(err error) error {
	var e *scope.Error
	if errors.As(err, &e) {
		status := 400
		if errors.Is(e, scope.ErrForbidden) {
			status = 403
		}
		if errors.Is(e, scope.ErrConflict) {
			status = 409
		}
		return &appError{status: status, code: e.Code, message: e.Message, cause: e.Cause}
	}
	return err
}
