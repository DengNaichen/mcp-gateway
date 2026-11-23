package api

// Converters between domain types and the apitypes wire contract. Flat structs
// use Go struct conversion, which stops compiling as soon as the two field sets
// diverge; nested structs map field by field. Named one-line helpers remain here
// when passed as callbacks to mapSlice/mapPtr/mapValues, shared across callers,
// or passed directly to respond(...) so contract_test.go can resolve their
// apitypes return type; other single-caller conversions are inlined.
// wire_test.go checks that every domain field reaches the wire, so a new domain
// field cannot be dropped silently.

import (
	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
)

// mapSlice preserves nil so JSON keeps emitting null where the domain did.
func mapSlice[A, B any](in []A, f func(A) B) []B {
	if in == nil {
		return nil
	}
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func mapPtr[A, B any](in *A, f func(A) B) *B {
	if in == nil {
		return nil
	}
	out := f(*in)
	return &out
}

func mapValues[K comparable, A, B any](in map[K]A, f func(A) B) map[K]B {
	if in == nil {
		return nil
	}
	out := make(map[K]B, len(in))
	for k, v := range in {
		out[k] = f(v)
	}
	return out
}

// --- registry ---

func wireServer(s registry.Server) apitypes.Server   { return apitypes.Server(s) }
func domainServer(s apitypes.Server) registry.Server { return registry.Server(s) }
func wireParam(p registry.HTTPParameter) apitypes.HTTPParameter {
	return apitypes.HTTPParameter(p)
}
func domainParam(p apitypes.HTTPParameter) registry.HTTPParameter {
	return registry.HTTPParameter(p)
}
func wireHTTPResponse(r registry.HTTPResponse) apitypes.HTTPResponse {
	return apitypes.HTTPResponse(r)
}
func domainHTTPResponse(r apitypes.HTTPResponse) registry.HTTPResponse {
	return registry.HTTPResponse(r)
}

func wireHTTPMapping(m registry.HTTPMapping) apitypes.HTTPMapping {
	return apitypes.HTTPMapping{Parameters: mapSlice(m.Parameters, wireParam), Body: m.Body, Responses: mapValues(m.Responses, wireHTTPResponse)}
}

func domainHTTPMapping(m apitypes.HTTPMapping) registry.HTTPMapping {
	return registry.HTTPMapping{Parameters: mapSlice(m.Parameters, domainParam), Body: m.Body, Responses: mapValues(m.Responses, domainHTTPResponse)}
}

func wireTool(t registry.Tool) apitypes.Tool {
	return apitypes.Tool{
		ID: t.ID, ServerID: t.ServerID, Name: t.Name, Description: t.Description, Tags: t.Tags,
		InputSchema: t.InputSchema, Enabled: t.Enabled, ReadOnly: t.ReadOnly, Actors: t.Actors,
		Method: t.Method, Path: t.Path, Example: t.Example, Adapter: t.Adapter, Revision: t.Revision,
		HTTP: mapPtr(t.HTTP, wireHTTPMapping), OutputSchema: t.OutputSchema,
	}
}

func wireToolSchema(schema gateway.ToolSchema) apitypes.ToolSchema {
	return apitypes.ToolSchema{Tool: wireTool(schema.Tool), NeedsApproval: schema.NeedsApproval}
}

func domainTool(t apitypes.Tool) registry.Tool {
	return registry.Tool{
		ID: t.ID, ServerID: t.ServerID, Name: t.Name, Description: t.Description, Tags: t.Tags,
		InputSchema: t.InputSchema, Enabled: t.Enabled, ReadOnly: t.ReadOnly, Actors: t.Actors,
		Method: t.Method, Path: t.Path, Example: t.Example, Adapter: t.Adapter, Revision: t.Revision,
		HTTP: mapPtr(t.HTTP, domainHTTPMapping), OutputSchema: t.OutputSchema,
	}
}

func wireRegistry(r registry.Registry) apitypes.Registry {
	return apitypes.Registry{Servers: mapSlice(r.Servers, wireServer), Tools: mapSlice(r.Tools, wireTool)}
}

func wireCapability(c routing.Capability) apitypes.Capability { return apitypes.Capability(c) }
func wireCatalogServer(s gateway.CatalogServer) apitypes.CatalogServer {
	return apitypes.CatalogServer(s)
}

func wireCatalog(c gateway.Catalog) apitypes.Catalog {
	return apitypes.Catalog{Servers: mapSlice(c.Servers, wireCatalogServer), Tools: mapSlice(c.Tools, wireCapability)}
}

// --- discovery and invocation ---

func domainDiscoverRequest(in apitypes.DiscoverRequest) gateway.DiscoverRequest {
	return gateway.DiscoverRequest{Intent: in.Intent, Context: in.Context, Services: in.Services, Actor: in.Actor, IncludeDemo: in.IncludeDemo, SummaryOnly: in.SummaryOnly}
}

func domainInvokeRequest(in apitypes.InvokeRequest) gateway.InvokeRequest {
	return gateway.InvokeRequest{ApprovalID: in.ApprovalID, ToolID: in.ToolID, Arguments: in.Arguments, Fields: in.Fields, Actor: in.Actor}
}

func wireSelection(s gateway.Selection) apitypes.Selection {
	return apitypes.Selection{Tool: wireTool(s.Tool), Reason: s.Reason, NeedsApproval: s.NeedsApproval}
}

func wireDiscoverResult(r gateway.DiscoverResult) apitypes.DiscoverResult {
	return apitypes.DiscoverResult{
		ID: r.ID, Status: r.Status, Model: r.Model, Summary: r.Summary, Question: r.Question,
		Tools: mapSlice(r.Tools, wireSelection), Servers: r.Servers, Eligible: r.Eligible,
		DurationMS: r.DurationMS, PromptTokens: r.PromptTokens, OutputTokens: r.OutputTokens,
		Scope: mapPtr(r.Scope, wireEventScope),
	}
}

func wireDiscoverSummary(r gateway.DiscoverResult) apitypes.DiscoverSummaryResult {
	tools := make([]apitypes.Capability, 0, len(r.Tools))
	for _, t := range r.Tools {
		c := routing.Summarize(t.Tool)
		c.Reason = t.Reason
		c.NeedsApproval = t.NeedsApproval
		tools = append(tools, wireCapability(c))
	}
	return apitypes.DiscoverSummaryResult{
		ID: r.ID, Status: r.Status, Model: r.Model, Summary: r.Summary, Question: r.Question,
		Tools: tools, Servers: r.Servers, Eligible: r.Eligible,
		DurationMS: r.DurationMS, PromptTokens: r.PromptTokens, OutputTokens: r.OutputTokens,
		Scope: mapPtr(r.Scope, wireEventScope),
	}
}

func wireInvokeResult(r gateway.InvokeResult) apitypes.InvokeResult {
	return apitypes.InvokeResult{ID: r.ID, ToolID: r.ToolID, Protocol: r.Protocol, Data: r.Data, DurationMS: r.DurationMS,
		Status: r.Status, Approval: (*apitypes.ApprovalChallenge)(r.Approval)}
}

// --- model ---

func domainSettings(s apitypes.Settings) model.Settings { return model.Settings(s) }
func wireProfile(p model.ModelProfile) apitypes.ModelProfile {
	return apitypes.ModelProfile(p)
}
func domainProfile(p apitypes.ModelProfile) model.ModelProfile {
	return model.ModelProfile(p)
}
func wirePing(r model.PingModelResult) apitypes.PingModelResult { return apitypes.PingModelResult(r) }

// --- scope ---

func wireRepository(r scope.Repository) apitypes.Repository   { return apitypes.Repository(r) }
func wireObserved(r scope.ObservedRepo) apitypes.ObservedRepo { return apitypes.ObservedRepo(r) }
func wireGrant(g scope.ServiceGrant) apitypes.ServiceGrant    { return apitypes.ServiceGrant(g) }
func domainGrant(g apitypes.ServiceGrant) scope.ServiceGrant  { return scope.ServiceGrant(g) }
func wireEventScope(s scope.EventScope) apitypes.EventScope   { return apitypes.EventScope(s) }
func domainSimScope(s apitypes.SimScope) scope.SimScope       { return scope.SimScope(s) }
func wireSnippet(s scope.ScopeSnippetResponse) apitypes.ScopeSnippetResponse {
	return apitypes.ScopeSnippetResponse(s)
}

func wireRule(r scope.ScopeRule) apitypes.ScopeRule {
	return apitypes.ScopeRule{RepoKey: r.RepoKey, Grants: mapSlice(r.Grants, wireGrant), UpdatedAt: r.UpdatedAt}
}

func domainRule(r apitypes.ScopeRule) scope.ScopeRule {
	return scope.ScopeRule{RepoKey: r.RepoKey, Grants: mapSlice(r.Grants, domainGrant), UpdatedAt: r.UpdatedAt}
}

func wireScopeOverview(o scope.ScopeOverview) apitypes.ScopeOverview {
	return apitypes.ScopeOverview{
		Enforce: o.Enforce, HookBinPath: o.HookBinPath, Repositories: mapSlice(o.Repositories, wireRepository),
		Rules: mapSlice(o.Rules, wireRule), Observed: mapSlice(o.Observed, wireObserved),
	}
}

// --- market ---

func wireMarketInput(i market.MarketInput) apitypes.MarketInput   { return apitypes.MarketInput(i) }
func domainMarketInput(i apitypes.MarketInput) market.MarketInput { return market.MarketInput(i) }

func wireMarketServer(s market.MarketServer) apitypes.MarketServer {
	return apitypes.MarketServer{
		ID: s.ID, ServerID: s.ServerID, Title: s.Title, Publisher: s.Publisher, Icon: s.Icon, Homepage: s.Homepage,
		Description: s.Description, Transport: s.Transport, Runs: s.Runs, Endpoint: s.Endpoint, Command: s.Command,
		Args: s.Args, SignIn: s.SignIn, Featured: s.Featured, Installed: s.Installed, Inputs: mapSlice(s.Inputs, wireMarketInput),
	}
}

func domainMarketServer(s apitypes.MarketServer) market.MarketServer {
	return market.MarketServer{
		ID: s.ID, ServerID: s.ServerID, Title: s.Title, Publisher: s.Publisher, Icon: s.Icon, Homepage: s.Homepage,
		Description: s.Description, Transport: s.Transport, Runs: s.Runs, Endpoint: s.Endpoint, Command: s.Command,
		Args: s.Args, SignIn: s.SignIn, Featured: s.Featured, Installed: s.Installed, Inputs: mapSlice(s.Inputs, domainMarketInput),
	}
}

func wireMarketSearch(r market.MarketSearchResult) apitypes.MarketSearchResult {
	return apitypes.MarketSearchResult{Items: mapSlice(r.Items, wireMarketServer), RegistryError: r.RegistryError}
}

func domainInstallRequest(r apitypes.InstallMarketRequest) market.InstallMarketRequest {
	return market.InstallMarketRequest{ID: r.ID, Server: mapPtr(r.Server, domainMarketServer), Inputs: r.Inputs}
}

func wireInstallResult(r market.InstallMarketResult) apitypes.InstallMarketResult {
	return apitypes.InstallMarketResult{Server: wireServer(r.Server), ToolCount: r.ToolCount, SyncError: r.SyncError}
}

// --- maintenance ---

func wireRepositorySource(r maintenance.RepositorySource) apitypes.RepositorySource {
	return apitypes.RepositorySource(r)
}
func domainRepositorySource(r apitypes.RepositorySource) maintenance.RepositorySource {
	return maintenance.RepositorySource(r)
}

func wireSource(s maintenance.MaintenanceSource) apitypes.MaintenanceSource {
	return apitypes.MaintenanceSource{
		ServerID: s.ServerID, Files: s.Files, URLs: s.URLs, Instructions: s.Instructions, Enabled: s.Enabled,
		AutoPublish: s.AutoPublish, IntervalMinutes: s.IntervalMinutes, TimeoutSeconds: s.TimeoutSeconds,
		Provider: s.Provider, Model: s.Model, Format: s.Format, OperationIDs: s.OperationIDs,
		Repository: mapPtr(s.Repository, wireRepositorySource), NextRunAt: s.NextRunAt,
	}
}

func domainSource(s apitypes.MaintenanceSource) maintenance.MaintenanceSource {
	return maintenance.MaintenanceSource{
		ServerID: s.ServerID, Files: s.Files, URLs: s.URLs, Instructions: s.Instructions, Enabled: s.Enabled,
		AutoPublish: s.AutoPublish, IntervalMinutes: s.IntervalMinutes, TimeoutSeconds: s.TimeoutSeconds,
		Provider: s.Provider, Model: s.Model, Format: s.Format, OperationIDs: s.OperationIDs,
		Repository: mapPtr(s.Repository, domainRepositorySource), NextRunAt: s.NextRunAt,
	}
}

func wireExchange(e maintenance.HTTPExchange) apitypes.HTTPExchange { return apitypes.HTTPExchange(e) }
func domainExchange(e apitypes.HTTPExchange) maintenance.HTTPExchange {
	return maintenance.HTTPExchange(e)
}
func wireRoutingCase(c maintenance.RoutingCase) apitypes.RoutingCase { return apitypes.RoutingCase(c) }
func domainRoutingCase(c apitypes.RoutingCase) maintenance.RoutingCase {
	return maintenance.RoutingCase(c)
}

func wireContract(c maintenance.ContractCase) apitypes.ContractCase {
	return apitypes.ContractCase{
		Name: c.Name, ToolID: c.ToolID, Arguments: c.Arguments, Exchanges: mapSlice(c.Exchanges, wireExchange),
		Expected: c.Expected, ExpectError: c.ExpectError, MCPResult: c.MCPResult, MCPError: c.MCPError,
	}
}

func domainContract(c apitypes.ContractCase) maintenance.ContractCase {
	return maintenance.ContractCase{
		Name: c.Name, ToolID: c.ToolID, Arguments: c.Arguments, Exchanges: mapSlice(c.Exchanges, domainExchange),
		Expected: c.Expected, ExpectError: c.ExpectError, MCPResult: c.MCPResult, MCPError: c.MCPError,
	}
}

func wireSuite(s maintenance.AcceptanceSuite) apitypes.AcceptanceSuite {
	return apitypes.AcceptanceSuite{ServerID: s.ServerID, Contracts: mapSlice(s.Contracts, wireContract), Routing: mapSlice(s.Routing, wireRoutingCase)}
}

func domainSuite(s apitypes.AcceptanceSuite) maintenance.AcceptanceSuite {
	return maintenance.AcceptanceSuite{ServerID: s.ServerID, Contracts: mapSlice(s.Contracts, domainContract), Routing: mapSlice(s.Routing, domainRoutingCase)}
}

func wireAcceptanceResult(r maintenance.AcceptanceResult) apitypes.AcceptanceResult {
	return apitypes.AcceptanceResult(r)
}

func wireAcceptanceReport(r maintenance.AcceptanceReport) apitypes.AcceptanceReport {
	return apitypes.AcceptanceReport{
		Configured: r.Configured, Passed: r.Passed, Fingerprint: r.Fingerprint, RoutingFingerprint: r.RoutingFingerprint,
		Results: mapSlice(r.Results, wireAcceptanceResult), MissingCoverage: r.MissingCoverage,
	}
}

func wireReviewReason(r maintenance.ReviewReason) apitypes.ReviewReason {
	return apitypes.ReviewReason(r)
}

func wireReview(r maintenance.PublicationReview) apitypes.PublicationReview {
	return apitypes.PublicationReview{Required: r.Required, Reasons: mapSlice(r.Reasons, wireReviewReason)}
}

func wireRun(r maintenance.MaintenanceRun) apitypes.MaintenanceRun {
	return apitypes.MaintenanceRun{
		ID: r.ID, ServerID: r.ServerID, Status: r.Status, Stage: r.Stage, CreatedAt: r.CreatedAt, FinishedAt: r.FinishedAt,
		Fingerprint: r.Fingerprint, Baseline: r.Baseline, Source: wireSource(r.Source), Server: wireServer(r.Server),
		Before: mapSlice(r.Before, wireTool), Tools: mapSlice(r.Tools, wireTool), Summary: r.Summary, Error: r.Error,
		Events: r.Events, Artifacts: r.Artifacts, Force: r.Force, AcceptanceInput: mapPtr(r.AcceptanceInput, wireSuite),
		Acceptance: wireAcceptanceReport(r.Acceptance), Review: wireReview(r.Review),
		Trigger: apitypes.MaintenanceTrigger(r.Trigger), Provenance: apitypes.SourceProvenance(r.Provenance),
		PublishedBy: r.PublishedBy, PublishedAt: r.PublishedAt, RolledBackBy: r.RolledBackBy, RolledBackAt: r.RolledBackAt,
	}
}

func wireMaintenanceStatus(s maintenance.Status) apitypes.MaintenanceStatus {
	return apitypes.MaintenanceStatus{Runtime: s.Runtime, Sources: mapSlice(s.Sources, wireSource), Runs: mapSlice(s.Runs, wireRun)}
}

func wireToolChange(c maintenance.ToolChange) apitypes.ToolChange {
	return apitypes.ToolChange{ID: c.ID, Kind: c.Kind, Before: mapPtr(c.Before, wireTool), After: mapPtr(c.After, wireTool)}
}

func wireRunDetails(d maintenance.RunDetails) apitypes.MaintenanceRunDetails {
	return apitypes.MaintenanceRunDetails{Run: wireRun(d.Run), Changes: mapSlice(d.Changes, wireToolChange)}
}

// --- activity, setup, access ---

func wireEvent(e activity.Event) apitypes.Event { return apitypes.Event(e) }
func wirePulseBucket(b activity.PulseBucket) apitypes.PulseBucket {
	return apitypes.PulseBucket(b)
}
func wireCheck(c gateway.ConnectionCheck) apitypes.ConnectionCheck {
	return apitypes.ConnectionCheck(c)
}

func wireSetup(s gateway.SetupStatus) apitypes.SetupStatus {
	return apitypes.SetupStatus{
		Sources: s.Sources, EnabledTools: s.EnabledTools, ToolsReady: s.ToolsReady, ModelConfigured: s.ModelConfigured,
		ModelReady: s.ModelReady, ModelCheck: mapPtr(s.ModelCheck, wireCheck), SourceChecks: mapValues(s.SourceChecks, wireCheck),
		AgentLastRequest: mapPtr(s.AgentLastRequest, wireEvent),
	}
}

func wirePrincipal(p access.Principal) apitypes.Principal { return apitypes.Principal(p) }
