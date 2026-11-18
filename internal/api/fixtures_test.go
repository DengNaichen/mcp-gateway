package api

import (
	"fmt"
	"testing"

	"gatewaylab/internal/model"
	"gatewaylab/internal/workspace"
)

type decisionQuestion = model.Question

func scopeKey(t *testing.T, dataDir string) []byte {
	t.Helper()
	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
func decisionFixture(status string, probabilities ...float64) map[string]any {
	answers := []any{map[string]any{"type": "choice", "name": "route_status", "choice": status}}
	for i, probability := range probabilities {
		answers = append(answers, map[string]any{"type": "predicate", "name": fmt.Sprintf("tool_%d", i), "probability": probability})
	}
	return map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 123, "output_tokens": 0}}
}
func objectSchema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}
