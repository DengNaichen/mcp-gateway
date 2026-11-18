package gateway

import (
	"context"
	"iter"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/activity"
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
	return g.routingFingerprintForRegistryLocked(serverID, g.registry.Snapshot())
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

// Focused port adapters keep maintenance independent of Gateway and its locks
// while reusing existing service methods via embedding rather than forwarding.
type maintenanceCatalog struct {
	*registry.Service
	g *Gateway
}

type maintenanceRouter struct{ g *Gateway }

type maintenanceUpstream struct {
	*upstream.Service
	g *Gateway
}

type maintenanceRecorder struct {
	*Gateway
	*activity.Service
}

func newMaintainer(g *Gateway) (*maintenance.Maintainer, error) {
	return maintenance.New(maintenance.Options{
		DB:            g.db,
		Dir:           g.dir,
		Catalog:       maintenanceCatalog{Service: g.registry, g: g},
		Router:        maintenanceRouter{g: g},
		Upstream:      maintenanceUpstream{Service: g.upstream, g: g},
		Recorder:      maintenanceRecorder{Gateway: g, Service: g.activity},
		Execution:     g.execution,
		ImportOpenAPI: openapi.ImportOpenAPI,
	})
}
func (g *Gateway) StartMaintenance(ctx context.Context) { g.maintenance.Start(ctx) }
func (c maintenanceCatalog) UpdateCatalog(serverID, fingerprint string, update func(registry.Registry) (registry.Registry, error), commit func(registry.Registry) error) error {
	c.g.publicationConfigMu.Lock()
	defer c.g.publicationConfigMu.Unlock()
	return c.g.registry.UpdateWithCommit(func(reg registry.Registry) (registry.Registry, error) {
		if fingerprint != "" && c.g.routingFingerprintForRegistryLocked(serverID, reg) != fingerprint {
			return registry.Registry{}, &maintenance.Error{Kind: maintenance.ErrConflict, Message: "路由配置或候选工具已变化，请重新验收"}
		}
		return update(reg)
	}, commit)
}
func (r maintenanceRouter) RoutingFingerprint(id string) string {
	return r.g.acceptanceRoutingFingerprint(id)
}
func (r maintenanceRouter) Discover(ctx context.Context, c maintenance.RoutingCase, reg registry.Registry) (maintenance.RoutingResult, error) {
	ctx, err := r.g.access.AcceptanceActor(ctx, c.Actor)
	if err != nil {
		return maintenance.RoutingResult{}, err
	}
	result, err := r.g.discoverRegistry(scope.WithInternalBypass(ctx), DiscoverRequest{Intent: c.Intent, Context: c.Context, Actor: c.Actor}, reg, false)
	out := maintenance.RoutingResult{Status: result.Status}
	for _, tool := range result.Tools {
		out.Tools = append(out.Tools, tool.ID)
	}
	return out, err
}
func (u maintenanceUpstream) NativeTools(ctx context.Context, s registry.Server) (iter.Seq2[*mcp.Tool, error], func(), error) {
	session, release, err := u.Session(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	return session.Tools(ctx, nil), release, nil
}
func (u maintenanceUpstream) CallHTTP(ctx context.Context, s registry.Server, t registry.Tool, args map[string]any) (any, error) {
	return upstream.CallHTTPWithLookup(ctx, s, t, args, u.g.serverLookup(s))
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
