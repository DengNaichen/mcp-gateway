package gateway

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"
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
	var serviceUsage modelUsage
	if g.routing.hierarchical && len(req.Services) == 0 {
		chosen, usage, err := selectServices(ctx, s, key, req, servers, eligible)
		if err != nil {
			return result, err
		}
		serviceUsage = usage
		if len(chosen) > 0 {
			result.Servers = chosen
			eligible = toolsOfServices(eligible, chosen)
		}
	}
	decision, usage, err := selectTools(ctx, s, key, req, eligible, g.routing)
	if err != nil {
		return result, err
	}
	result.Model, result.Status, result.Summary, result.Question = s.Model, decision.Status, decision.Summary, decision.Question
	result.PromptTokens, result.OutputTokens = serviceUsage.PromptTokens+usage.PromptTokens, serviceUsage.CompletionTokens+usage.CompletionTokens
	if !slices.Contains([]string{"selected", "no_match", "needs_context"}, decision.Status) {
		return result, problem(502, "模型返回了不支持的 status", nil)
	}
	if len(decision.Tools) > s.MaxTools {
		return result, problem(502, "模型返回的工具数量超过配置上限", nil)
	}
	seen := map[string]bool{}
	for _, choice := range decision.Tools {
		i := slices.IndexFunc(eligible, func(t Tool) bool { return t.ID == choice.ID })
		if i < 0 || seen[choice.ID] {
			return result, problem(502, "模型返回了候选集之外或重复的工具 ID", nil)
		}
		seen[choice.ID] = true
		result.Tools = append(result.Tools, Selection{Tool: eligible[i], Reason: choice.Reason})
	}
	if result.Status == "selected" && len(result.Tools) == 0 {
		return result, problem(502, "模型选择状态为 selected，但未返回工具", nil)
	}
	if result.Status != "selected" && len(result.Tools) != 0 {
		return result, problem(502, "模型的 status 与工具列表不一致", nil)
	}
	if result.Status == "needs_context" && result.Question == "" {
		return result, problem(502, "模型请求补充上下文，但未返回问题", nil)
	}
	result.DurationMS = time.Since(started).Milliseconds()
	return result, nil
}

type modelDecision struct {
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Question string `json:"question"`
	Tools    []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"tools"`
}

type modelUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func noRedirect(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }

func closeBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		slog.Debug("close upstream response", "error", err)
	}
}
