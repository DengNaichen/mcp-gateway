package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Gemini Decisions API: POST {base}/decisions with the key in x-goog-api-key.
// Questions are an object keyed by name; yes/no questions have type "noul" and
// answer with a calibrated probability in the "noul" field.

type geminiAnswer struct {
	Type   string   `json:"type"`
	Choice string   `json:"choice"`
	Noul   *float64 `json:"noul"`
}

func geminiCall(s Settings, input string, tools []Tool) (decisionsCall, error) {
	endpoint := strings.TrimRight(s.BaseURL, "/")
	if !strings.HasSuffix(endpoint, "/decisions") {
		endpoint += "/decisions"
	}
	criteria := make(map[string]string, len(routeStatusChoices))
	for _, c := range routeStatusChoices {
		criteria[c.Value] = c.Description
	}
	questions := map[string]any{
		"route_status": map[string]any{"type": "choice", "instructions": routeStatusInstructions, "criteria": criteria},
	}
	for i, t := range tools {
		questions[toolQuestionName(i)] = map[string]any{"type": "noul", "instructions": relevanceInstructions(t.ID)}
	}
	body, err := json.Marshal(map[string]any{
		"model":     s.Model,
		"input":     []map[string]string{{"type": "text", "text": input}},
		"questions": questions,
	})
	if err != nil {
		return decisionsCall{}, fmt.Errorf("encode decisions request: %w", err)
	}
	return decisionsCall{endpoint: endpoint, keyHeader: "x-goog-api-key", maxTools: geminiMaxTools, body: body, parse: parseGeminiAnswers}, nil
}

func parseGeminiAnswers(b []byte, n int) (routeAnswers, error) {
	var out routeAnswers
	var response struct {
		Answers map[string]geminiAnswer `json:"answers"`
		Usage   struct {
			InputTokens  int `json:"total_input_tokens"`
			OutputTokens int `json:"total_output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &response); err != nil {
		return out, problem(502, "Decisions 响应格式无效", err)
	}
	out.usage = modelUsage{PromptTokens: response.Usage.InputTokens, CompletionTokens: response.Usage.OutputTokens}
	if len(response.Answers) != n+1 {
		return out, problem(502, "Decisions 回答数量与问题不一致", nil)
	}
	status, ok := response.Answers["route_status"]
	if !ok || status.Type != "choice" {
		return out, problem(502, "Decisions 回答名称、顺序或类型不匹配", nil)
	}
	out.status = status.Choice
	for i := 0; i < n; i++ {
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
