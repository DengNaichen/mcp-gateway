package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"gatewaylab/internal/model"
)

const routingProtocol = model.RoutingProtocol
const decisionThreshold = 0.5
const routeRules = model.RouteRules
const systemOneMaxQuestions = model.SystemOneMaxQuestions
const openAIMaxTools = model.OpenAIMaxTools
const choiceQuestionName = model.ChoiceQuestionName

type routeAnswers struct {
	status    string
	relevance []float64
	usage     modelUsage
}

func selectWithModel(ctx context.Context, s Settings, key string, req DiscoverRequest, tools []Tool) (modelDecision, modelUsage, error) {
	return selectTools(ctx, s, key, req, tools, routingOptions{})
}

// selectTools asks the model one relevance question per tool over the task and
// the candidate summaries, then applies the fixed selection policy.
func selectTools(ctx context.Context, s Settings, key string, req DiscoverRequest, tools []Tool, opts routingOptions) (modelDecision, modelUsage, error) {
	var decision modelDecision
	var usage modelUsage
	if len(tools) == 0 || s.MaxTools < 1 {
		return decision, usage, invalid("Decisions 路由需要至少 1 个可用工具和正数工具上限")
	}
	// A struct, not a map: json.Marshal sorts map keys, which would put the task after
	// thousands of tokens of tools. Measured on gemini-decision-1, that dropped one
	// tool's relevance from 0.93 to 0.46.
	input, err := json.Marshal(struct {
		Intent     string             `json:"intent"`
		Context    string             `json:"context"`
		Candidates []routingCandidate `json:"candidates"`
	}{req.Intent, req.Context, routingCandidates(tools, opts.params)})
	if err != nil {
		return decision, usage, fmt.Errorf("encode routing context: %w", err)
	}
	instructions := make([]string, len(tools))
	for i, t := range tools {
		instructions[i] = relevanceInstructions(t.ID)
	}
	answers, err := askDecisions(ctx, s, key, string(input), instructions)
	usage = answers.usage
	if err != nil {
		return decision, usage, err
	}
	if opts.refine {
		if candidates := survivors(answers, tools, refineCandidates); len(candidates) > 0 {
			decision, refineUsage, err := refineSelection(ctx, s, key, req, candidates, opts)
			usage.PromptTokens += refineUsage.PromptTokens
			usage.CompletionTokens += refineUsage.CompletionTokens
			return decision, usage, err
		}
	}
	decision, err = chooseTools(answers, tools, s.MaxTools)
	return decision, usage, err
}

// chooseTools applies the fixed policy: tools whose relevance reaches the
// threshold are selected, ranked by probability and capped at maxTools. The
// route_status answer never vetoes a confident tool; it only explains an empty
// result, distinguishing a task that needs clarification from one with no match.
func chooseTools(a routeAnswers, tools []Tool, maxTools int) (modelDecision, error) {
	var decision modelDecision
	decision.Status = a.status
	if !slices.Contains([]string{"selected", "no_match", "needs_context"}, a.status) {
		return decision, problem(502, "Decisions 返回了未知路由状态", nil)
	}
	indices := make([]int, len(tools))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		return a.relevance[indices[i]] > a.relevance[indices[j]]
	})
	for _, i := range indices {
		probability := a.relevance[i]
		if probability < decisionThreshold || len(decision.Tools) >= maxTools {
			break
		}
		decision.Tools = append(decision.Tools, struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}{ID: tools[i].ID, Reason: fmt.Sprintf("Decisions 相关性概率 %.1f%%", probability*100)})
	}
	if len(decision.Tools) > 0 {
		decision.Status = "selected"
		decision.Summary = fmt.Sprintf("Decisions 按相关性选出 %d 个工具", len(decision.Tools))
		return decision, nil
	}
	decision.Summary = "Decisions 未找到匹配的工具"
	if a.status == "needs_context" {
		decision.Summary = "Decisions 判断需要补充任务上下文"
		decision.Question = "请补充要操作的项目、服务或具体目标，以便选择工具。"
	} else {
		decision.Status = "no_match"
	}
	return decision, nil
}

// relevanceInstructions names the tool by ID rather than by its position in the
// candidates array. Measured on gemini-decision-1 with 151 tools, "candidates[17]"
// is not resolved reliably: irrelevant tools scored 0.97-0.99 and the right one
// ranked 26th to 54th. Naming the tool gave the right one 0.98 and the rest below 0.25.
func relevanceInstructions(toolID string) string {
	return routeRules + fmt.Sprintf("Is the candidate tool %q directly useful for the current task, including necessary identifier lookups? Prefer the smallest useful set of capabilities.", toolID)
}

type decisionChoice = model.Choice
type decisionQuestion = model.Question

func toolQuestionName(i int) string { return fmt.Sprintf("tool_%d", i) }
func askDecisions(ctx context.Context, s Settings, key, input string, instructions []string) (routeAnswers, error) {
	a, err := model.AskDecisions(ctx, s, key, input, instructions)
	return routeAnswers{status: a.Status, relevance: a.Relevance, usage: modelUsage(a.Usage)}, modelError(err)
}
