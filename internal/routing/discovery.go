package routing

import (
	"context"
	"slices"
	"strings"
)

// Discover routes an already authorized candidate set. It does not read process
// configuration, take locks, assign IDs or record activity.
func Discover(ctx context.Context, client Client, req Request, servers map[string]Server, eligible []Tool, maxTools int, opts Options) (Result, error) {
	result := Result{Status: "no_match", Tools: []Selection{}, Eligible: len(eligible)}
	if strings.TrimSpace(req.Intent) == "" {
		return result, invalid("请描述你需要完成的任务")
	}
	if len(req.Intent)+len(req.Context) > 24000 {
		return result, invalid("任务和上下文合计不能超过 24 KB")
	}
	if len(eligible) == 0 {
		result.Summary = "当前身份下没有已启用的可用工具"
		return result, nil
	}
	var serviceUsage Usage
	if opts.Hierarchical && len(req.Services) == 0 {
		chosen, usage, err := SelectServices(ctx, client, req, servers, eligible)
		if err != nil {
			return result, err
		}
		serviceUsage = usage
		if len(chosen) > 0 {
			result.Servers = chosen
			eligible = toolsOfServices(eligible, chosen)
		}
	}
	decision, usage, err := SelectTools(ctx, client, maxTools, req, eligible, opts)
	if err != nil {
		return result, err
	}
	result.Decided = true
	result.Status, result.Summary, result.Question = decision.Status, decision.Summary, decision.Question
	result.Usage.PromptTokens, result.Usage.CompletionTokens = serviceUsage.PromptTokens+usage.PromptTokens, serviceUsage.CompletionTokens+usage.CompletionTokens
	if !slices.Contains([]string{"selected", "no_match", "needs_context"}, decision.Status) {
		return result, badResponse("模型返回了不支持的 status", nil)
	}
	if len(decision.Tools) > maxTools {
		return result, badResponse("模型返回的工具数量超过配置上限", nil)
	}
	seen := map[string]bool{}
	for _, choice := range decision.Tools {
		i := slices.IndexFunc(eligible, func(t Tool) bool { return t.ID == choice.ID })
		if i < 0 || seen[choice.ID] {
			return result, badResponse("模型返回了候选集之外或重复的工具 ID", nil)
		}
		seen[choice.ID] = true
		result.Tools = append(result.Tools, Selection{Tool: eligible[i], Reason: choice.Reason})
	}
	if result.Status == "selected" && len(result.Tools) == 0 {
		return result, badResponse("模型选择状态为 selected，但未返回工具", nil)
	}
	if result.Status != "selected" && len(result.Tools) != 0 {
		return result, badResponse("模型的 status 与工具列表不一致", nil)
	}
	if result.Status == "needs_context" && result.Question == "" {
		return result, badResponse("模型请求补充上下文，但未返回问题", nil)
	}

	return result, nil
}
