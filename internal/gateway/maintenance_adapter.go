package gateway

import (
	"context"
	"iter"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
)

func (g *Gateway) acceptanceRoutingFingerprint(serverID string) string {
	g.publicationConfigMu.RLock()
	defer g.publicationConfigMu.RUnlock()
	return g.routingFingerprintForRegistryLocked(serverID, g.Snapshot())
}
func (g *Gateway) routingFingerprintForRegistryLocked(serverID string, reg registry.Registry) string {
	other := registry.Registry{Servers: []registry.Server{}, Tools: []registry.Tool{}}
	for _, server := range reg.Servers {
		if server.ID != serverID {
			other.Servers = append(other.Servers, server)
		}
	}
	for _, tool := range reg.Tools {
		if tool.ServerID != serverID {
			other.Tools = append(other.Tools, tool)
		}
	}
	var principals any
	if g.access != nil {
		principals = g.access.Principals()
	}
	settings, key := g.model.RuntimeConfig()
	return jsonutil.MustDigest([]any{routingProtocol, settings, key, other, principals, g.execution})
}

// maintenanceAdapter keeps orchestration independent of Gateway and its locks.
type maintenanceAdapter struct{ g *Gateway }

func newMaintainer(g *Gateway) (*maintenance.Maintainer, error) {
	adapter := maintenanceAdapter{g}
	return maintenance.New(maintenance.Options{DB: g.db, Dir: g.dir, Catalog: adapter, Router: adapter, Upstream: adapter, Recorder: adapter, Execution: g.execution, ImportOpenAPI: openapi.ImportOpenAPI})
}
func (g *Gateway) StartMaintenance(ctx context.Context)  { g.maintenance.Start(ctx) }
func (a maintenanceAdapter) Snapshot() registry.Registry { return a.g.Snapshot() }
func (a maintenanceAdapter) UpdateCatalog(serverID, fingerprint string, update func(registry.Registry) (registry.Registry, error), commit func(registry.Registry) error) error {
	a.g.publicationConfigMu.Lock()
	defer a.g.publicationConfigMu.Unlock()
	return a.g.registry.UpdateWithCommit(func(reg registry.Registry) (registry.Registry, error) {
		if fingerprint != "" && a.g.routingFingerprintForRegistryLocked(serverID, reg) != fingerprint {
			return registry.Registry{}, &maintenance.Error{Kind: maintenance.ErrConflict, Message: "路由配置或候选工具已变化，请重新验收"}
		}
		return update(reg)
	}, commit)
}
func (a maintenanceAdapter) RoutingFingerprint(id string) string {
	return a.g.acceptanceRoutingFingerprint(id)
}
func (a maintenanceAdapter) Discover(ctx context.Context, c maintenance.RoutingCase, reg registry.Registry) (maintenance.RoutingResult, error) {
	ctx, err := a.g.access.AcceptanceActor(ctx, c.Actor)
	if err != nil {
		return maintenance.RoutingResult{}, err
	}
	result, err := a.g.discoverRegistry(scope.WithInternalBypass(ctx), DiscoverRequest{Intent: c.Intent, Context: c.Context, Actor: c.Actor}, reg, false)
	out := maintenance.RoutingResult{Status: result.Status}
	for _, tool := range result.Tools {
		out.Tools = append(out.Tools, tool.ID)
	}
	return out, err
}
func (a maintenanceAdapter) NativeTools(ctx context.Context, s registry.Server) (iter.Seq2[*mcp.Tool, error], func(), error) {
	session, release, err := a.g.upstream.Session(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	return session.Tools(ctx, nil), release, nil
}
func (a maintenanceAdapter) CallMCP(ctx context.Context, s registry.Server, t registry.Tool, args map[string]any) (any, error) {
	return a.g.upstream.CallMCP(ctx, s, t, args)
}
func (a maintenanceAdapter) CallHTTP(ctx context.Context, s registry.Server, t registry.Tool, args map[string]any) (any, error) {
	return upstream.CallHTTPWithLookup(ctx, s, t, args, a.g.serverLookup(s))
}
func (a maintenanceAdapter) RedactResponse(result any, s registry.Server) any {
	return upstream.RedactResponseWithLookup(result, s, a.g.serverLookup(s))
}
func (a maintenanceAdapter) ErrorDetail(err error) string { return a.g.ErrorDetail(err) }
func (a maintenanceAdapter) Record(kind, actor, status, title string, start time.Time, detail any) {
	a.g.activity.Record(kind, actor, status, title, start, detail)
}
func (g *Gateway) callAdapter(ctx context.Context, s registry.Server, t registry.Tool, args map[string]any) (any, error) {
	if err := upstream.CheckEnvironment(s, g.serverLookup(s)); err != nil {
		return nil, err
	}
	p, err := g.maintenance.AdapterPath(t)
	if err != nil {
		return nil, err
	}
	return sandbox.CallAdapterFileWithLookup(ctx, g.execution, s, t, args, p, g.serverLookup(s))
}
