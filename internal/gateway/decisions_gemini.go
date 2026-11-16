package gateway

import (
	"encoding/json"
	"fmt"
)

// Gemini Decisions API: POST {base}/decisions with the key in x-goog-api-key.
// Questions are an object keyed by name; yes/no questions have type "noul" and
// answer with a calibrated probability in the "noul" field.

type mapAnswer struct {
	Type   string   `json:"type"`
	Choice string   `json:"choice"`
	Noul   *float64 `json:"noul"`
}

type mapUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	TotalInputTokens  int `json:"total_input_tokens"`
	TotalOutputTokens int `json:"total_output_tokens"`
}

func (u mapUsage) toModelUsage(gemini bool) modelUsage {
	if gemini {
		return modelUsage{PromptTokens: u.TotalInputTokens, CompletionTokens: u.TotalOutputTokens}
	}
	return modelUsage{PromptTokens: u.InputTokens, CompletionTokens: u.OutputTokens}
}

type geminiRequest struct {
	Model     string              `json:"model"`
	Input     []map[string]string `json:"input"`
	Questions map[string]any      `json:"questions"`
}

func buildChoiceCriteria(choices []decisionChoice) map[string]string {
	criteria := make(map[string]string, len(choices))
	for _, c := range choices {
		criteria[c.Value] = c.Description
	}
	return criteria
}

func buildMapQuestions(instructions []string, start, end int, includeStatus bool) map[string]any {
	questions := make(map[string]any, (end-start)+1)
	if includeStatus {
		questions["route_status"] = map[string]any{
			"type":         "choice",
			"instructions": routeStatusInstructions,
			"criteria":     buildChoiceCriteria(routeStatusChoices),
		}
	}
	for i := start; i < end; i++ {
		questions[toolQuestionName(i)] = map[string]any{"type": "noul", "instructions": instructions[i]}
	}
	return questions
}

func geminiCall(endpoint, model, input string, instructions []string) (decisionsCall, error) {
	body, err := json.Marshal(geminiRequest{
		Model:     model,
		Input:     []map[string]string{{"type": "text", "text": input}},
		Questions: buildMapQuestions(instructions, 0, len(instructions), true),
	})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{
		endpoint:  endpoint,
		keyHeader: "x-goog-api-key",
		body:      body,
		parse:     func(b []byte) (routeAnswers, error) { return parseMapAnswersRange(b, 0, len(instructions), true, true) },
	}, nil
}

func parseMapAnswersRange(b []byte, start, end int, includeStatus, geminiUsage bool) (routeAnswers, error) {
	var out routeAnswers
	var response struct {
		Answers map[string]mapAnswer `json:"answers"`
		Usage   mapUsage             `json:"usage"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return out, problem(502, "Decisions 响应格式无效", err)
	}
	out.usage = response.Usage.toModelUsage(geminiUsage)
	wantCount := end - start
	if includeStatus {
		wantCount++
	}
	if len(response.Answers) != wantCount {
		return out, problem(502, "Decisions 回答数量与问题不一致", nil)
	}
	if includeStatus {
		status, ok := response.Answers["route_status"]
		if !ok || status.Type != "choice" {
			return out, problem(502, "Decisions 回答名称、顺序或类型不匹配", nil)
		}
		out.status = status.Choice
	}
	for i := start; i < end; i++ {
		answer, ok := response.Answers[toolQuestionName(i)]
		if !ok || answer.Type != "noul" {
			return out, problem(502, "Decisions 回答名称、顺序或类型不匹配", nil)
		}
		if !validProbability(answer.Noul) {
			return out, problem(502, "Decisions 返回的相关性概率无效", nil)
		}
		out.relevance = append(out.relevance, *answer.Noul)
	}
	return out, nil
}
