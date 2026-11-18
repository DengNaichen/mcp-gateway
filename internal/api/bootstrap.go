package api

import (
	"net/http"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
)

func (g *server) registerBootstrap(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		build := buildinfo.Current()
		respond(w, 200, apitypes.Health{Status: "ok", Version: build.Version, Channel: build.Channel, Application: build.Identifier, InstanceID: g.instanceID, DataIdentity: buildinfo.DataIdentity(g.dataDir)})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.model.Config()
		setup, err := g.gw.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Bootstrap{
			Registry:              wireRegistry(g.registry.Snapshot()),
			Settings:              apitypes.Settings(settings),
			KeyConfigured:         key,
			CredentialsPersistent: g.credentialsPersistent,
			Profiles:              mapSlice(g.model.ModelProfiles(), wireProfile),
			Events:                mapSlice(g.activity.Recent(), wireEvent),
			Pulse:                 mapSlice(g.activity.Pulse(time.Now()), wirePulseBucket),
			MCPURL:                g.baseURL + "/mcp",
			Setup:                 wireSetup(setup),
			Scopes:                wireScopeOverview(g.scope.ScopeOverview()),
			Access:                apitypes.AccessInfo{Shared: g.access != nil, Actor: access.RequestActor(r.Context(), "")},
		})
	})
}
