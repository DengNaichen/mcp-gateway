package api

import (
	"net/http"

	"gatewaylab/internal/access"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
)

func (g *server) registerBootstrap(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		build := buildinfo.Current()
		respond(w, 200, apitypes.Health{Status: "ok", Version: build.Version, Channel: build.Channel, Application: build.Identifier, InstanceID: g.InstanceID(), DataIdentity: buildinfo.DataIdentity(g.DataDir())})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.Model().Config()
		setup, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Bootstrap{
			Registry:      wireRegistry(g.Snapshot()),
			Settings:      wireSettings(settings),
			KeyConfigured: key,
			Profiles:      mapSlice(g.Model().ModelProfiles(), wireProfile),
			Events:        mapSlice(g.Activity().Recent(), wireEvent),
			MCPURL:        g.BaseURL() + "/mcp",
			Setup:         wireSetup(setup),
			Scopes:        wireScopeOverview(g.Scope().ScopeOverview()),
			Access:        apitypes.AccessInfo{Shared: g.Access() != nil, Actor: access.RequestActor(r.Context(), "")},
		})
	})
}
