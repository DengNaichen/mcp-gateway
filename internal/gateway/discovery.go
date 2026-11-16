package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/routing"
)

func (g *Gateway) Discover(ctx context.Context, req DiscoverRequest) (DiscoverResult, error) {
	return g.discoverRegistry(ctx, req, g.Snapshot(), true)
}

func (g *Gateway) discoverRegistry(ctx context.Context, req DiscoverRequest, registry Registry, record bool) (result DiscoverResult, retErr error) {
	req.Actor = requestActor(ctx, req.Actor)
	started := time.Now()
	scopeEv := scopeEventForContext(ctx)
	defer func() {
		result.DurationMS = time.Since(started).Milliseconds()
		if !record {
			return
		}
		if retErr != nil {
			g.record("discover", req.Actor, "error", req.Intent, started, map[string]any{"error": g.errorDetail(retErr), "scope": scopeEv})
		} else {
			result.Scope = &scopeEv
			g.record("discover", req.Actor, result.Status, req.Intent, started, result)
		}
	}()
	if err := g.checkExecutionScope(ctx); err != nil {
		return result, err
	}
	if strings.TrimSpace(req.Intent) == "" {
		return result, invalid("请描述你需要完成的任务")
	}
	if len(req.Intent)+len(req.Context) > 24000 {
		return result, invalid("任务和上下文合计不能超过 24 KB")
	}
	s, key := g.model.RuntimeConfig()
	r := registry
	servers := map[string]Server{}
	for _, server := range r.Servers {
		if server.Enabled && (!server.Demo || req.IncludeDemo) {
			servers[server.ID] = server
		}
	}
	eligible := []Tool{}
	for _, t := range r.Tools {
		if _, ok := servers[t.ServerID]; ok && g.authorizedTool(ctx, t, req.Actor) && (len(req.Services) == 0 || slices.Contains(req.Services, t.ServerID)) {
			eligible = append(eligible, t)
		}
	}
	result = DiscoverResult{ID: newID(), Status: "no_match", Tools: []Selection{}, Eligible: len(eligible)}
	if len(eligible) == 0 {
		result.Summary = "当前身份下没有已启用的可用工具"
		return result, nil
	}
	if strings.TrimSpace(s.Model) == "" {
		return result, problem(409, "请先在模型设置中填写模型名称", nil)
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
		return result, routingError(err)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

func noRedirect(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("close upstream response", "error", err)
	}
}
