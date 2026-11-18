package api

import "gatewaylab/internal/apitypes"

// apiRoutes documents every /api route registered by handler(): its request
// body and its success responses, as apitypes values. docs/api/openapi.json is
// generated from this table, and contract_test.go keeps the table, the mux and
// each handler's decode/respond types in agreement. Error responses are always
// apitypes.Error and are added by the generator.
var apiRoutes = []route{
	{Method: "GET", Path: "/api/health", Summary: "Liveness and instance identity", Public: true, Responses: ok(apitypes.Health{})},
	{Method: "GET", Path: "/api/bootstrap", Summary: "Everything the control plane needs for its first screen", Responses: ok(apitypes.Bootstrap{})},
	{Method: "GET", Path: "/api/events", Summary: "Recent activity", Responses: ok([]apitypes.Event{})},

	{Method: "GET", Path: "/api/auth/me", Summary: "The caller's identity", Authenticated: true, Responses: ok(apitypes.AuthMe{})},
	{Method: "POST", Path: "/api/auth/login", Summary: "Exchange an access token for a session cookie (shared mode)", Public: true, Request: apitypes.LoginRequest{}, Responses: ok(apitypes.LoginResult{})},
	{Method: "POST", Path: "/api/auth/logout", Summary: "End the session", Authenticated: true, Responses: ok(apitypes.SignedOut{})},

	{Method: "GET", Path: "/api/catalog", Summary: "Authorized services and tool summaries", Invocation: true, Query: []query{actorQuery, {"include_demo", "boolean", "Include demo services"}}, Responses: ok(apitypes.Catalog{})},
	{Method: "GET", Path: "/api/tools/{id}/schema", Summary: "Full definition of one authorized tool", Invocation: true, Query: []query{actorQuery}, Responses: ok(apitypes.Tool{})},
	{Method: "POST", Path: "/api/discover", Summary: "Route an intent to a small set of authorized tools", Invocation: true, Request: apitypes.DiscoverRequest{}, Responses: ok(oneOf{apitypes.DiscoverResult{}, apitypes.DiscoverSummaryResult{}})},
	{Method: "POST", Path: "/api/invoke", Summary: "Invoke a registered tool", Invocation: true, Request: apitypes.InvokeRequest{}, Responses: ok(apitypes.InvokeResult{})},

	{Method: "PUT", Path: "/api/servers", Summary: "Create or replace a tool source", Request: apitypes.Server{}, Responses: ok(apitypes.Server{})},
	{Method: "POST", Path: "/api/servers/openapi", Summary: "Register an HTTP source and the tools generated from its OpenAPI document", Request: apitypes.ImportOpenAPIRequest{}, Responses: ok(apitypes.ImportOpenAPIResult{})},
	{Method: "GET", Path: "/api/servers/{id}/credentials", Summary: "Read credential availability without secret values", Responses: ok(apitypes.ServerCredentials{})},
	{Method: "PUT", Path: "/api/servers/{id}/credentials", Summary: "Save or clear service credentials", Request: apitypes.SaveCredentialsRequest{}, Responses: ok(apitypes.Saved{})},
	{Method: "DELETE", Path: "/api/servers/{id}/oauth", Summary: "Clear a service's cached and saved OAuth session", Responses: ok(apitypes.Deleted{})},
	{Method: "DELETE", Path: "/api/servers/{id}", Summary: "Delete a service, its tools and saved credentials", Responses: ok(apitypes.Deleted{})},

	{Method: "POST", Path: "/api/servers/{id}/sync", Summary: "Re-read an MCP source's tools/list", Responses: ok(apitypes.SyncServerResult{})},
	{Method: "POST", Path: "/api/servers/{id}/tools", Summary: "Choose which of a source's tools are enabled", Request: apitypes.EnableToolsRequest{}, Responses: ok(apitypes.Saved{})},
	{Method: "PUT", Path: "/api/tools", Summary: "Create or replace a tool", Request: apitypes.Tool{}, Responses: ok(apitypes.Tool{})},

	{Method: "GET", Path: "/api/setup/status", Summary: "Onboarding progress", Responses: ok(apitypes.SetupStatus{})},
	{Method: "POST", Path: "/api/setup/source", Summary: "Save a source and test its connection", Request: apitypes.Server{}, Responses: map[int]any{200: apitypes.ConnectionCheck{}, 400: apitypes.Error{}}},
	{Method: "POST", Path: "/api/setup/model", Summary: "Save model settings and try a routing request", Request: apitypes.SetupModelRequest{}, Responses: ok(apitypes.DiscoverResult{})},
	{Method: "POST", Path: "/api/model/ping", Summary: "Test model connectivity without saving", Request: apitypes.PingModelRequest{}, Responses: ok(apitypes.PingModelResult{})},
	{Method: "PUT", Path: "/api/settings", Summary: "Save the active model settings", Request: apitypes.SaveSettingsRequest{}, Responses: ok(apitypes.Saved{})},
	{Method: "PUT", Path: "/api/model-profiles", Summary: "Create or update a model profile after a passing connectivity check", Request: apitypes.SaveModelProfileRequest{}, Responses: ok(apitypes.ModelProfile{})},
	{Method: "POST", Path: "/api/model-profiles/{id}/activate", Summary: "Make a model profile active", Responses: ok(apitypes.ModelProfile{})},
	{Method: "DELETE", Path: "/api/model-profiles/{id}", Summary: "Delete a model profile", Responses: ok(apitypes.Deleted{})},

	{Method: "GET", Path: "/api/market", Summary: "Search installable MCP servers", Query: []query{{"q", "string", "Search text"}}, Responses: ok(apitypes.MarketSearchResult{})},
	{Method: "GET", Path: "/api/market/icon", Summary: "Proxied market icon", Query: []query{{"id", "string", "Market server ID"}, {"url", "string", "Icon URL"}}, Responses: ok(binary{"image/*"})},
	{Method: "POST", Path: "/api/market/install", Summary: "Install a market server", Request: apitypes.InstallMarketRequest{}, Responses: ok(apitypes.InstallMarketResult{})},

	{Method: "GET", Path: "/api/scopes", Summary: "Repository scopes, rules and observed repositories", Responses: ok(apitypes.ScopeOverview{})},
	{Method: "PUT", Path: "/api/scopes/mode", Summary: "Turn scope enforcement on or off", Request: apitypes.ScopeModeRequest{}, Responses: ok(apitypes.ScopeModeRequest{})},
	{Method: "PUT", Path: "/api/scopes/repositories", Summary: "Create or update a repository", Request: apitypes.SaveRepositoryRequest{}, Responses: ok(apitypes.Repository{})},
	{Method: "DELETE", Path: "/api/scopes/repositories/{id}", Summary: "Delete a repository", Responses: ok(apitypes.Deleted{})},
	{Method: "PUT", Path: "/api/scopes/rules", Summary: "Create or replace a scope rule", Request: apitypes.ScopeRule{}, Responses: ok(apitypes.ScopeRule{})},
	{Method: "DELETE", Path: "/api/scopes/rules", Summary: "Delete a scope rule", Query: []query{{"repo_key", "string", "Rule repository key"}}, Responses: ok(apitypes.Deleted{})},
	{Method: "GET", Path: "/api/scopes/snippets", Summary: "Hook configuration snippet for an agent", Query: []query{{"agent", "string", "Agent key"}}, Responses: ok(apitypes.ScopeSnippetResponse{})},

	{Method: "GET", Path: "/api/maintenance", Summary: "Maintenance runtime, sources and recent runs", Responses: ok(apitypes.MaintenanceStatus{})},
	{Method: "PUT", Path: "/api/maintenance/sources", Summary: "Create or update a maintenance source", Request: apitypes.MaintenanceSource{}, Responses: ok(apitypes.Saved{})},
	{Method: "GET", Path: "/api/maintenance/sources/{id}/acceptance", Summary: "A source's acceptance suite", Responses: ok(apitypes.AcceptanceSuite{})},
	{Method: "PUT", Path: "/api/maintenance/sources/{id}/acceptance", Summary: "Replace a source's acceptance suite", Request: apitypes.AcceptanceSuite{}, Responses: ok(apitypes.Saved{})},
	{Method: "POST", Path: "/api/maintenance/sources/{id}/run", Summary: "Start a maintenance run", Request: apitypes.RunMaintenanceRequest{}, Responses: map[int]any{202: apitypes.MaintenanceRun{}}},
	{Method: "POST", Path: "/api/maintenance/sources/{id}/webhook/{provider}", Summary: "Repository event webhook (authenticated by the provider signature)", Public: true, Request: rawBody{"application/json"}, Responses: map[int]any{200: apitypes.WebhookResult{}, 202: apitypes.WebhookResult{}}},
	{Method: "GET", Path: "/api/maintenance/runs/{id}", Summary: "A run and its tool changes", Responses: ok(apitypes.MaintenanceRunDetails{})},
	{Method: "GET", Path: "/api/maintenance/runs/{id}/artifacts/{name}", Summary: "One run artifact", Responses: ok(apitypes.MaintenanceArtifact{})},
	{Method: "POST", Path: "/api/maintenance/runs/{id}/cancel", Summary: "Cancel a run", Responses: ok(apitypes.Cancelled{})},
	{Method: "POST", Path: "/api/maintenance/runs/{id}/publish", Summary: "Publish a run's tools", Responses: ok(apitypes.Published{})},
	{Method: "POST", Path: "/api/maintenance/runs/{id}/rollback", Summary: "Roll back to a run's baseline", Responses: ok(apitypes.RolledBack{})},
}

type route struct {
	Method, Path, Summary string
	// Public routes skip shared-mode authentication.
	Public bool
	// Authenticated routes are open to any authenticated principal (including non-admin) in shared mode.
	Authenticated bool
	// Invocation routes are open to non-admin principals and honor repository
	// scope (?agent= and X-Gateway-Scope).
	Invocation bool
	Query      []query
	// Request is the JSON body type; nil when the route reads no body.
	Request   any
	Responses map[int]any
}

type query struct{ Name, Type, Description string }

// oneOf documents a response whose shape depends on the request.
type oneOf []any

// binary documents a non-JSON response body.
type binary struct{ MediaType string }

// rawBody documents a request body passed through unparsed (webhook payloads).
type rawBody struct{ MediaType string }

var actorQuery = query{"actor", "string", "Simulated identity in local mode; ignored when the caller is authenticated"}

func ok(body any) map[int]any { return map[int]any{200: body} }
