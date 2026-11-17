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
		respond(w, 200, apitypes.Health{Status: "ok", Version: build.Version, Channel: build.Channel, Application: build.Identifier, InstanceID: g.gw.InstanceID(), DataIdentity: buildinfo.DataIdentity(g.gw.DataDir())})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.gw.Model().Config()
		setup, err := g.gw.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Bootstrap{
			Registry:              wireRegistry(g.gw.Snapshot()),
			Settings:              wireSettings(settings),
			KeyConfigured:         key,
			CredentialsPersistent: g.gw.CredentialsPersistent(),
			Profiles:              mapSlice(g.gw.Model().ModelProfiles(), wireProfile),
			Events:                mapSlice(g.gw.Activity().Recent(), wireEvent),
			Pulse:                 mapSlice(g.gw.Activity().Pulse(time.Now()), wirePulseBucket),
			MCPURL:                g.gw.BaseURL() + "/mcp",
			Setup:                 wireSetup(setup),
			Scopes:                wireScopeOverview(g.gw.Scope().ScopeOverview()),
			Access:                apitypes.AccessInfo{Shared: g.gw.Access() != nil, Actor: access.RequestActor(r.Context(), "")},
		})
	})
}
