package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// Routing variants under evaluation, selected with GATEWAY_ROUTING as a
// comma-separated list. Both borrow from pi's tool discovery: pi tells the model
// which MCP servers exist with a one-line summary and lets scripts list a
// server's tool names (describeNamespace), and its tool ranker reads the schema
// property names next to the description.
type routingOptions struct {
	// hierarchical asks the model which services fit the task before asking about
	// their tools, so a call never carries more candidates than the largest service.
	hierarchical bool
	// params adds each tool's input parameter names to its candidate summary.
	params bool
	// refine asks one choice question over the tools that passed the yes/no stage,
	// so near-duplicates compete for one probability mass instead of each passing.
	refine bool
}

// loadRoutingOptions reads GATEWAY_ROUTING. Unset, routing is hierarchical with
// refinement, the configuration that measured best (see evals/routing/NOTES.md).
// `flat` turns both off; named options then add stages back.
func loadRoutingOptions() (routingOptions, error) {
	value, set := os.LookupEnv("GATEWAY_ROUTING")
	if !set || strings.TrimSpace(value) == "" {
		return routingOptions{hierarchical: true, refine: true}, nil
	}
	var o routingOptions
	for _, name := range strings.Split(value, ",") {
		switch strings.TrimSpace(name) {
		case "", "flat":
		case "hierarchical":
			o.hierarchical = true
		case "params":
			o.params = true
		case "refine":
			o.refine = true
		default:
			return o, fmt.Errorf("unknown GATEWAY_ROUTING option %q (use flat, hierarchical, refine, params)", name)
		}
	}
	return o, nil
}

// routingCandidate is what the model sees of a tool: the capability summary and,
// with the params option, the names of its input parameters.
type routingCandidate struct {
	Capability
	Params []string `json:"params,omitempty"`
}

func routingCandidates(tools []Tool, params bool) []routingCandidate {
	result := make([]routingCandidate, 0, len(tools))
	for _, t := range tools {
		c := routingCandidate{Capability: capability(t)}
		if params {
			c.Params = parameterNames(t.InputSchema)
		}
		result = append(result, c)
	}
	return result
}

// parameterNames lists the top-level property names of a JSON Schema object, sorted.
func parameterNames(schema map[string]any) []string {
	properties, _ := schema["properties"].(map[string]any)
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// At most this many services continue to the tool stage.
const maxRoutedServices = 3

type serviceSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools"`
}

func serviceInstructions(id string) string {
	return routeRules + fmt.Sprintf("Does the service %q offer a tool directly useful for the current task, including necessary identifier lookups? Judge by its description and its tool names.", id)
}

// selectServices is the first stage of hierarchical routing. It describes every
// service with its tool names and asks one yes/no question per service. Services
// at or above the threshold continue, best first and at most maxRoutedServices;
// when none reaches it, the best one continues so the tool stage still decides.
// Returns nil when fewer than two services are eligible, since nothing would
// be narrowed.
func selectServices(ctx context.Context, s Settings, key string, req DiscoverRequest, servers map[string]Server, tools []Tool) ([]string, modelUsage, error) {
	byServer := map[string][]string{}
	for _, t := range tools {
		byServer[t.ServerID] = append(byServer[t.ServerID], t.Name)
	}
	if len(byServer) < 2 {
		return nil, modelUsage{}, nil
	}
	ids := make([]string, 0, len(byServer))
	for id := range byServer {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	summaries := make([]serviceSummary, 0, len(ids))
	instructions := make([]string, 0, len(ids))
	for _, id := range ids {
		names := byServer[id]
		sort.Strings(names)
		summaries = append(summaries, serviceSummary{ID: id, Name: servers[id].Name, Description: servers[id].Description, Tools: names})
		instructions = append(instructions, serviceInstructions(id))
	}
	input, err := json.Marshal(struct {
		Intent   string           `json:"intent"`
		Context  string           `json:"context"`
		Services []serviceSummary `json:"services"`
	}{req.Intent, req.Context, summaries})
	if err != nil {
		return nil, modelUsage{}, fmt.Errorf("encode service routing context: %w", err)
	}
	answers, err := askDecisions(ctx, s, key, string(input), instructions)
	if err != nil {
		return nil, answers.usage, err
	}
	order := make([]int, len(ids))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return answers.relevance[order[a]] > answers.relevance[order[b]] })
	chosen := []string{ids[order[0]]}
	for _, i := range order[1:] {
		if answers.relevance[i] < decisionThreshold || len(chosen) >= maxRoutedServices {
			break
		}
		chosen = append(chosen, ids[i])
	}
	return chosen, answers.usage, nil
}

func toolsOfServices(tools []Tool, ids []string) []Tool {
	var result []Tool
	for _, t := range tools {
		if slices.Contains(ids, t.ServerID) {
			result = append(result, t)
		}
	}
	return result
}

// Refinement: the yes/no stage judges every tool on its own, so a tool that is
// merely adjacent to the right one (a trace tool next to an error tool, a schema
// tool next to a query tool) passes the threshold too. A choice question over the
// survivors makes them compete: the model distributes one probability mass over
// them and a "none" option, like pi's main model picking from tool_search results.

// At most this many survivors of the yes/no stage enter the choice question.
const refineCandidates = 8

// Survivors whose share of the choice distribution is at least this fraction of
// the winner's share are returned next to it, so two equally direct tools can both
// be returned while a distant runner-up is not.
const refineShare = 0.5

// "none" wins only when no survivor reached this relevance in the yes/no stage.
// The choice question asks for a single tool, so it answers "none" for a task
// that needs two tools or that several services serve equally; a survivor the
// yes/no stage was this sure about is kept instead.
const refineOverride = 0.9

const refineNone = "none"

func refineInstructions() string {
	return routeRules + "Which single candidate tool most directly performs the current task? Choose " + refineNone + " when no candidate performs it, even if some only look up or prepare data for it."
}

type scoredTool struct {
	Tool
	relevance float64
}

// survivors lists the tools at or above the threshold, best first, at most limit.
func survivors(a routeAnswers, tools []Tool, limit int) []scoredTool {
	order := make([]int, len(tools))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return a.relevance[order[i]] > a.relevance[order[j]] })
	var result []scoredTool
	for _, i := range order {
		if a.relevance[i] < decisionThreshold || len(result) >= limit {
			break
		}
		result = append(result, scoredTool{tools[i], a.relevance[i]})
	}
	return result
}

// refineSelection asks the choice question over the survivors and keeps the
// winner plus survivors with at least refineShare of its share. When "none"
// wins and no survivor is confidently relevant, nothing is selected.
func refineSelection(ctx context.Context, s Settings, key string, req DiscoverRequest, candidates []scoredTool, opts routingOptions) (modelDecision, modelUsage, error) {
	var decision modelDecision
	tools := make([]Tool, len(candidates))
	for i, c := range candidates {
		tools[i] = c.Tool
	}
	input, err := json.Marshal(struct {
		Intent     string             `json:"intent"`
		Context    string             `json:"context"`
		Candidates []routingCandidate `json:"candidates"`
	}{req.Intent, req.Context, routingCandidates(tools, opts.params)})
	if err != nil {
		return decision, modelUsage{}, fmt.Errorf("encode refinement context: %w", err)
	}
	criteria := make([]decisionChoice, 0, len(candidates)+1)
	for _, c := range candidates {
		criteria = append(criteria, decisionChoice{Value: c.ID, Description: strings.SplitN(strings.TrimSpace(c.Description), "\n", 2)[0]})
	}
	criteria = append(criteria, decisionChoice{Value: refineNone, Description: "No candidate directly performs the task; a candidate that only looks up or prepares data for it does not count."})
	answer, usage, err := askChoice(ctx, s, key, string(input), refineInstructions(), criteria)
	if err != nil {
		return decision, usage, err
	}
	winner, winnerShare := answer.choice, answer.probabilities[answer.choice]
	if winner == refineNone {
		winner, winnerShare = "", 0
		for _, c := range candidates {
			if share := answer.probabilities[c.ID]; c.relevance >= refineOverride && share > winnerShare {
				winner, winnerShare = c.ID, share
			}
		}
		if winner == "" {
			// Survivors from several services and no winner: the task fits each service
			// about equally, so the caller has to say which one it means.
			services := []string{}
			for _, c := range candidates {
				if !slices.Contains(services, c.ServerID) {
					services = append(services, c.ServerID)
				}
			}
			if len(services) > 1 {
				decision.Status = "needs_context"
				decision.Summary = "Decisions 判断任务同时适用于多个服务"
				decision.Question = fmt.Sprintf("任务可能涉及 %s，请说明要操作哪个服务，或在 services 参数中指定。", strings.Join(services, "、"))
				return decision, usage, nil
			}
			decision.Status = "no_match"
			decision.Summary = "Decisions 精选后判断没有直接服务任务的工具"
			return decision, usage, nil
		}
	}
	for _, c := range candidates {
		share, known := answer.probabilities[c.ID]
		if c.ID != winner && (!known || share < refineShare*winnerShare) {
			continue
		}
		if len(decision.Tools) >= s.MaxTools {
			break
		}
		reason := fmt.Sprintf("Decisions 相关性概率 %.1f%%", c.relevance*100)
		if known {
			reason += fmt.Sprintf("，精选占比 %.1f%%", share*100)
		}
		decision.Tools = append(decision.Tools, struct {
			ID     string `json:"id"`
			Reason string `json:"reason"`
		}{ID: c.ID, Reason: reason})
	}
	// The winner comes first even when a higher-relevance survivor shares the distribution.
	sort.SliceStable(decision.Tools, func(i, j int) bool {
		return decision.Tools[i].ID == winner && decision.Tools[j].ID != winner
	})
	if len(decision.Tools) == 0 {
		return decision, usage, problem(502, "Decisions 精选结果不在候选集内", nil)
	}
	decision.Status = "selected"
	decision.Summary = fmt.Sprintf("Decisions 精选出 %d 个工具", len(decision.Tools))
	return decision, usage, nil
}

type choiceAnswer struct {
	choice        string
	probabilities map[string]float64 // may be empty when the provider reports none
}

// askChoice sends one choice question over input and returns the pick and, when
// the provider reports it, the probability of every option.
func askChoice(ctx context.Context, s Settings, key, input, instructions string, choices []decisionChoice) (choiceAnswer, modelUsage, error) {
	var call decisionsCall
	var parse func([]byte) (choiceAnswer, modelUsage, error)
	var err error
	if isGeminiModel(s.Model) {
		call, err = geminiChoiceCall(s, input, instructions, choices)
		parse = parseGeminiChoice
	} else {
		call, err = openAIChoiceCall(s, input, instructions, choices)
		parse = parseOpenAIChoice
	}
	if err != nil {
		return choiceAnswer{}, modelUsage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.TimeoutSeconds)*time.Second)
	defer cancel()
	b, err := postDecisions(ctx, call, key)
	if err != nil {
		return choiceAnswer{}, modelUsage{}, err
	}
	answer, usage, err := parse(b)
	if err != nil {
		return answer, usage, err
	}
	if !slices.ContainsFunc(choices, func(c decisionChoice) bool { return c.Value == answer.choice }) {
		return answer, usage, problem(502, "Decisions 精选返回了候选之外的选项", nil)
	}
	return answer, usage, nil
}

const choiceQuestionName = "best"

func geminiChoiceCall(s Settings, input, instructions string, choices []decisionChoice) (decisionsCall, error) {
	endpoint := strings.TrimRight(s.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/decisions") {
		endpoint += "/decisions"
	}
	criteria := make(map[string]string, len(choices))
	for _, c := range choices {
		criteria[c.Value] = c.Description
	}
	body, err := json.Marshal(map[string]any{
		"model":     s.Model,
		"input":     []map[string]string{{"type": "text", "text": input}},
		"questions": map[string]any{choiceQuestionName: map[string]any{"type": "choice", "instructions": instructions, "criteria": criteria}},
	})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "x-goog-api-key", maxTools: geminiMaxTools, body: body}, nil
}

func parseGeminiChoice(b []byte) (choiceAnswer, modelUsage, error) {
	var response struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
		Usage struct {
			InputTokens  int `json:"total_input_tokens"`
			OutputTokens int `json:"total_output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return choiceAnswer{}, modelUsage{}, problem(502, "Decisions 响应格式无效", err)
	}
	usage := modelUsage{PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens}
	answer, ok := response.Answers[choiceQuestionName]
	if !ok || answer.Type != "choice" {
		return choiceAnswer{}, usage, problem(502, "Decisions 回答名称、顺序或类型不匹配", nil)
	}
	return choiceAnswer{choice: answer.Choice, probabilities: answer.Probabilities}, usage, nil
}

func openAIChoiceCall(s Settings, input, instructions string, choices []decisionChoice) (decisionsCall, error) {
	endpoint := strings.TrimRight(s.BaseURL, "/")
	if strings.HasSuffix(endpoint, "/chat/completions") || strings.HasSuffix(endpoint, "/responses") {
		return decisionsCall{}, invalid("当前使用 Decisions API，请将 Base URL 改为 https://api.openai.com/v1 或完整 /decisions 地址")
	}
	if !strings.HasSuffix(endpoint, "/decisions") {
		endpoint += "/decisions"
	}
	body, err := json.Marshal(map[string]any{"model": s.Model, "input": input, "questions": []decisionQuestion{{Type: "choice", Name: choiceQuestionName, Instructions: instructions, Choices: choices}}})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "Authorization", keyPrefix: "Bearer ", maxTools: openAIMaxTools, body: body}, nil
}

func parseOpenAIChoice(b []byte) (choiceAnswer, modelUsage, error) {
	var response struct {
		Answers []struct {
			Type          string             `json:"type"`
			Name          string             `json:"name"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return choiceAnswer{}, modelUsage{}, problem(502, "Decisions 响应格式无效", err)
	}
	usage := modelUsage{PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens}
	if len(response.Answers) != 1 || response.Answers[0].Name != choiceQuestionName || response.Answers[0].Type != "choice" {
		return choiceAnswer{}, usage, problem(502, "Decisions 回答名称、顺序或类型不匹配", nil)
	}
	return choiceAnswer{choice: response.Answers[0].Choice, probabilities: response.Answers[0].Probabilities}, usage, nil
}
