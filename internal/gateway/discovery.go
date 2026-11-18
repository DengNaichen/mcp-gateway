package gateway

import (
	"context"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
)

func (g *Gateway) Discover(ctx context.Context, req DiscoverRequest) (DiscoverResult, error) {
	return g.discoverRegistry(ctx, req, g.registry.Snapshot(), true)
}

func (g *Gateway) discoverRegistry(ctx context.Context, req DiscoverRequest, catalog registry.Registry, record bool) (result DiscoverResult, retErr error) {
	req.Actor = access.RequestActor(ctx, req.Actor)
	started := time.Now()
	scopeEv := scope.EventForContext(ctx)
	defer func() {
		result.DurationMS = time.Since(started).Milliseconds()
		if !record {
			return
		}
		if retErr != nil {
			g.activity.Record("discover", req.Actor, "error", req.Intent, started, map[string]any{"error": g.ErrorDetail(retErr), "scope": scopeEv})
		} else {
			result.Scope = &scopeEv
			g.activity.Record("discover", req.Actor, result.Status, req.Intent, started, result)
		}
	}()
	if err := g.scope.CheckExecutionScope(ctx); err != nil {
		return result, err
	}
	if strings.TrimSpace(req.Intent) == "" {
		return result, invalid("请描述你需要完成的任务")
	}
	if len(req.Intent)+len(req.Context) > 24000 {
		return result, invalid("任务和上下文合计不能超过 24 KB")
	}
	s, key := g.model.RuntimeConfig()
	servers, eligible := g.eligibleTools(ctx, catalog, req)
	result = DiscoverResult{ID: newID(), Status: "no_match", Tools: []Selection{}, Eligible: len(eligible)}
	if len(eligible) == 0 {
		result.Summary = "当前身份下没有已启用的可用工具"
		return result, nil
	}
	if strings.TrimSpace(s.Model) == "" {
		return result, fault.New(fault.Conflict, "请先在模型设置中填写模型名称", nil)
	}
	matched, err := routing.Discover(ctx, routingModel{s, key}, routingRequest(req), servers, eligible, s.MaxTools, g.routing)
	result.Status, result.Summary, result.Question = matched.Status, matched.Summary, matched.Question
	result.Servers = matched.Servers
	result.PromptTokens, result.OutputTokens = matched.Usage.PromptTokens, matched.Usage.CompletionTokens
	for _, selection := range matched.Tools {
		result.Tools = append(result.Tools, Selection(selection))
	}
	// As before, model identity is added only after a successful model decision.
	if matched.Decided {
		result.Model = s.Model
	}
	if err != nil {
		return result, err
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func (g *Gateway) eligibleTools(ctx context.Context, catalog registry.Registry, req DiscoverRequest) (map[string]registry.Server, []registry.Tool) {
	servers := map[string]registry.Server{}
	for _, server := range catalog.Servers {
		if server.Enabled && (!server.Demo || req.IncludeDemo) {
			servers[server.ID] = server
		}
	}
	eligible := []registry.Tool{}
	for _, t := range catalog.Tools {
		if _, ok := servers[t.ServerID]; ok && g.authorizedTool(ctx, t, req.Actor) && (len(req.Services) == 0 || slices.Contains(req.Services, t.ServerID)) {
			eligible = append(eligible, t)
		}
	}
	return servers, eligible
}
