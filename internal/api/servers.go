package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
)

func (g *server) registerServers(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/servers", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.Server
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		saved, err := g.gw.SaveServerDefinition(domainServer(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireServer(saved))
	})
	mux.HandleFunc("GET /api/servers/{id}/credentials", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		statuses, err := g.gw.ServerCredentials(id)
		if err != nil {
			fail(w, err)
			return
		}
		oauth, upgrade, err := g.gw.ServerOAuthInfo(id)
		if err != nil {
			fail(w, err)
			return
		}
		var out apitypes.ServerCredentials
		out.Persistent = g.credentialsPersistent
		out.Credentials = []apitypes.CredentialStatus{}
		for _, status := range statuses {
			out.Credentials = append(out.Credentials, apitypes.CredentialStatus{Name: status.Name, Configured: status.Configured, Saved: status.Saved, Environment: status.Environment})
		}
		if oauth != nil {
			out.OAuth = &apitypes.OAuthStatus{Enabled: oauth.Enabled, Authorized: oauth.Authorized}
		}
		if upgrade != nil {
			out.OAuthUpgrade = &apitypes.OAuthUpgradeInfo{Title: upgrade.Title, Endpoint: upgrade.Endpoint, Homepage: upgrade.Homepage}
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("PUT /api/servers/{id}/credentials", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.SaveCredentialsRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.gw.SetServerCredentials(r.PathValue("id"), in.Values); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Saved{Saved: true})
	})
	mux.HandleFunc("DELETE /api/servers/{id}/oauth", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.ClearServerOAuth(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Deleted{Deleted: true})
	})
	mux.HandleFunc("DELETE /api/servers/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.DeleteServer(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Deleted{Deleted: true})
	})

	mux.HandleFunc("POST /api/servers/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		n, err := g.gw.ImportMCP(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.SyncServerResult{Imported: n})
	})
	mux.HandleFunc("POST /api/servers/openapi", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.ImportOpenAPIRequest
		// A 4 MB document can grow when JSON-escaped; the gateway enforces 4 MB itself.
		if _, err := decodeRawLimit(w, r, &in, 8<<20); err != nil {
			fail(w, err)
			return
		}
		n, err := g.gw.ImportOpenAPI(r.Context(), domainServer(in.Server), []byte(in.Spec), in.SpecURL, in.OperationIDs)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.ImportOpenAPIResult{Imported: n})
	})
	mux.HandleFunc("PUT /api/tools", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.Tool
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.registry.SaveTool(domainTool(in)); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
}
