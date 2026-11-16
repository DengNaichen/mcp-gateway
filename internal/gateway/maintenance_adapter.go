package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"iter"
	"time"

	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (g *Gateway) acceptanceRoutingFingerprint(serverID string) string {
	g.publicationConfigMu.RLock()
	defer g.publicationConfigMu.RUnlock()
	return g.routingFingerprintForRegistryLocked(serverID, g.Snapshot())
}
func (g *Gateway) routingFingerprintForRegistryLocked(serverID string, reg Registry) string {
	other := Registry{Servers: []Server{}, Tools: []Tool{}}
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
		principals = g.access.principals
	}
	settings, key := g.model.RuntimeConfig()
	return maintenanceDigest([]any{routingProtocol, settings, key, other, principals, g.execution})
}

// maintenanceAdapter keeps orchestration independent of Gateway and its locks.
type maintenanceAdapter struct{ g *Gateway }

func newMaintainer(g *Gateway) (*Maintainer, error) {
	adapter := maintenanceAdapter{g}
	return maintenance.New(maintenance.Options{DB: g.db, Dir: g.dir, Catalog: adapter, Router: adapter, Upstream: adapter, Recorder: adapter, Execution: g.execution, ImportOpenAPI: openapi.ImportOpenAPI})
}
func (g *Gateway) StartMaintenance(ctx context.Context) { g.maintenance.Start(ctx) }
func (a maintenanceAdapter) Snapshot() Registry         { return a.g.Snapshot() }
func (a maintenanceAdapter) UpdateCatalog(serverID, fingerprint string, update func(Registry) (Registry, error), commit func(Registry) error) error {
	a.g.publicationConfigMu.Lock()
	defer a.g.publicationConfigMu.Unlock()
	return a.g.registry.UpdateWithCommit(func(reg Registry) (Registry, error) {
		if fingerprint != "" && a.g.routingFingerprintForRegistryLocked(serverID, reg) != fingerprint {
			return Registry{}, &maintenance.Error{Kind: maintenance.ErrConflict, Message: "路由配置或候选工具已变化，请重新验收"}
		}
		return update(reg)
	}, commit)
}
func (a maintenanceAdapter) RoutingFingerprint(id string) string {
	return a.g.acceptanceRoutingFingerprint(id)
}
func (a maintenanceAdapter) Discover(ctx context.Context, c maintenance.RoutingCase, reg Registry) (maintenance.RoutingResult, error) {
	ctx, err := a.g.acceptanceActor(ctx, c.Actor)
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
func (a maintenanceAdapter) NativeTools(ctx context.Context, s Server) (iter.Seq2[*mcp.Tool, error], func(), error) {
	session, release, err := a.g.upstream.Session(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	return session.Tools(ctx, nil), release, nil
}
func (a maintenanceAdapter) CallMCP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	return a.g.upstream.CallMCP(ctx, s, t, args)
}
func (a maintenanceAdapter) CallHTTP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	return upstream.CallHTTP(ctx, s, t, args)
}
func (a maintenanceAdapter) RedactResponse(result any, s Server) any {
	return upstream.RedactResponseWithLookup(result, s, a.g.lookupEnv)
}
func (a maintenanceAdapter) ErrorDetail(err error) string { return a.g.errorDetail(err) }
func (a maintenanceAdapter) Record(kind, actor, status, title string, start time.Time, detail any) {
	a.g.activity.Record(kind, actor, status, title, start, detail)
}
func (g *Gateway) callAdapter(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	if err := upstream.CheckEnvironment(s, nil); err != nil {
		return nil, err
	}
	p, err := g.maintenance.AdapterPath(t)
	if err != nil {
		return nil, err
	}
	return sandbox.CallAdapterFile(ctx, g.execution, s, t, args, p)
}

type executionPolicy = sandbox.Policy

var loadExecutionPolicy = sandbox.LoadPolicy

func maintenanceDigest(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("non-JSON maintenance value: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
