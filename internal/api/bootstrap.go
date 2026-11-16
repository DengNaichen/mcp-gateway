package api

import (
	"net/http"

	"gatewaylab/internal/access"
	"gatewaylab/internal/buildinfo"
)

func (g *server) registerBootstrap(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"status": "ok", "version": buildinfo.Current().Version, "channel": buildinfo.Current().Channel, "application": buildinfo.Current().Identifier, "instance_id": g.InstanceID(), "data_identity": buildinfo.DataIdentity(g.DataDir())})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.Model().Config()
		setup, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		events := g.Activity().Recent()
		respond(w, 200, map[string]any{"registry": g.Snapshot(), "settings": settings, "key_configured": key, "profiles": g.Model().ModelProfiles(), "events": events, "mcp_url": g.BaseURL() + "/mcp", "setup": setup, "scopes": g.Scope().ScopeOverview(), "access": map[string]any{"shared": g.Access() != nil, "actor": access.RequestActor(r.Context(), "")}})
	})
}
