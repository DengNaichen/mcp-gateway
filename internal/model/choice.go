package model

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

type ChoiceAnswer struct {
	Choice        string
	Probabilities map[string]float64 // may be empty when the provider reports none
}

// AskChoice sends one choice question over input and returns the pick and, when
// the provider reports it, the probability of every option.
func AskChoice(ctx context.Context, s Settings, key, input, instructions string, choices []Choice) (ChoiceAnswer, Usage, error) {
	return defaultClient().askChoice(ctx, choiceRequest{
		Settings:     s,
		Key:          key,
		Input:        input,
		Instructions: instructions,
		Choices:      choices,
	})
}

type choiceRequest struct {
	Settings     Settings
	Key          string
	Input        string
	Instructions string
	Choices      []Choice
}

func (c decisionsClient) askChoice(ctx context.Context, req choiceRequest) (ChoiceAnswer, Usage, error) {
	proto, endpoint, err := resolveDecisionProtocol(req.Settings)
	if err != nil {
		return ChoiceAnswer{}, Usage{}, err
	}
	var call decisionsCall
	var parse func([]byte) (ChoiceAnswer, Usage, error)
	switch proto {
	case protocolGemini:
		call, err = geminiChoiceCall(endpoint, req.Settings.Model, req.Input, req.Instructions, req.Choices)
		parse = func(b []byte) (ChoiceAnswer, Usage, error) { return parseMapChoice(b, true) }
	case protocolSystemOne:
		call, err = systemOneChoiceCall(endpoint, req.Settings.Model, req.Input, req.Instructions, req.Choices)
		parse = func(b []byte) (ChoiceAnswer, Usage, error) { return parseMapChoice(b, false) }
	default:
		call, err = openAIChoiceCall(endpoint, req.Settings.Model, req.Input, req.Instructions, req.Choices)
		parse = parseOpenAIChoice
	}
	if err != nil {
		return ChoiceAnswer{}, Usage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(req.Settings.TimeoutSeconds)*time.Second)
	defer cancel()
	b, err := c.post(ctx, call, req.Key)
	if err != nil {
		return ChoiceAnswer{}, Usage{}, err
	}
	answer, usage, err := parse(b)
	if err != nil {
		return answer, usage, err
	}
	if !slices.ContainsFunc(req.Choices, func(ch Choice) bool { return ch.Value == answer.Choice }) {
		return answer, usage, badResponse("Decisions 精选返回了候选之外的选项", nil)
	}
	return answer, usage, nil
}

const ChoiceQuestionName = "best"

func buildChoiceQuestions(instructions string, choices []Choice) map[string]any {
	return map[string]any{
		ChoiceQuestionName: map[string]any{
			"type":         "choice",
			"instructions": instructions,
			"criteria":     buildChoiceCriteria(choices),
		},
	}
}

func geminiChoiceCall(endpoint, model, input, instructions string, choices []Choice) (decisionsCall, error) {
	body, err := json.Marshal(geminiRequest{
		Model:     model,
		Input:     []map[string]string{{"type": "text", "text": input}},
		Questions: buildChoiceQuestions(instructions, choices),
	})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "x-goog-api-key", body: body}, nil
}

func systemOneChoiceCall(endpoint, model, input, instructions string, choices []Choice) (decisionsCall, error) {
	body, err := json.Marshal(systemOneRequest{
		Model:     model,
		State:     input,
		Questions: buildChoiceQuestions(instructions, choices),
	})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode systemone request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "Authorization", keyPrefix: "Bearer ", body: body}, nil
}

func parseMapChoice(b []byte, geminiUsage bool) (ChoiceAnswer, Usage, error) {
	var response struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
		Usage mapUsage `json:"usage"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return ChoiceAnswer{}, Usage{}, badResponse("Decisions 响应格式无效", err)
	}
	usage := response.Usage.toModelUsage(geminiUsage)
	answer, ok := response.Answers[ChoiceQuestionName]
	if answer.Type == "refusal" {
		return ChoiceAnswer{}, usage, refused()
	}
	if !ok || answer.Type != "choice" {
		return ChoiceAnswer{}, usage, badResponse("Decisions 回答名称、顺序或类型不匹配", nil)
	}
	return ChoiceAnswer{Choice: answer.Choice, Probabilities: answer.Probabilities}, usage, nil
}

func openAIChoiceCall(endpoint, model, input, instructions string, choices []Choice) (decisionsCall, error) {
	body, err := json.Marshal(map[string]any{"model": model, "input": input, "questions": []Question{{Type: "choice", Name: ChoiceQuestionName, Instructions: instructions, Choices: choices}}})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "Authorization", keyPrefix: "Bearer ", body: body}, nil
}

func parseOpenAIChoice(b []byte) (ChoiceAnswer, Usage, error) {
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
		return ChoiceAnswer{}, Usage{}, badResponse("Decisions 响应格式无效", err)
	}
	usage := Usage{PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens}
	if len(response.Answers) == 1 && response.Answers[0].Type == "refusal" {
		return ChoiceAnswer{}, usage, refused()
	}
	if len(response.Answers) != 1 || response.Answers[0].Name != ChoiceQuestionName || response.Answers[0].Type != "choice" {
		return ChoiceAnswer{}, usage, badResponse("Decisions 回答名称、顺序或类型不匹配", nil)
	}
	return ChoiceAnswer{Choice: response.Answers[0].Choice, Probabilities: response.Answers[0].Probabilities}, usage, nil
}
