package model

import (
	"encoding/json"
	"fmt"
)

// SystemOne API (Jev / decision-model-preview): POST {base}/systemone with Bearer auth.
// Requests evaluate a "state" string against a map of typed questions ("choice", "noul"),
// using OpenAI-style "input_tokens" usage reporting.

type systemOneRequest struct {
	Model     string         `json:"model"`
	State     string         `json:"state"`
	Questions map[string]any `json:"questions"`
}

func systemOneCalls(endpoint, model, input string, instructions []string) ([]decisionsCall, error) {
	var calls []decisionsCall
	start := 0
	for len(calls) == 0 || start < len(instructions) {
		includeStatus := len(calls) == 0
		limit := SystemOneMaxQuestions
		if includeStatus {
			limit--
		}
		end := min(len(instructions), start+limit)
		body, err := json.Marshal(systemOneRequest{
			Model:     model,
			State:     input,
			Questions: buildMapQuestions(instructions, start, end, includeStatus),
		})
		if err != nil {
			return nil, fmt.Errorf("encode systemone request: %w", err)
		}
		s, e, inc := start, end, includeStatus
		calls = append(calls, decisionsCall{
			endpoint:  endpoint,
			keyHeader: "Authorization",
			keyPrefix: "Bearer ",
			body:      body,
			parse: func(b []byte) (Answers, error) {
				return parseMapAnswersRange(b, mapAnswersSpec{Start: s, End: e, IncludeStatus: inc})
			},
		})
		start = end
	}
	return calls, nil
}
