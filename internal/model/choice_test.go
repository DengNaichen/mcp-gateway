package model

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Provider wire coverage stays beside the shared implementation; routing policy
// tests use an independent model stub.
func TestChoiceProviderWireContract(t *testing.T) {
	for _, model := range []string{"fixture", "gemini-decision-1"} {
		t.Run(model, func(t *testing.T) {
			host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/decisions" {
					t.Errorf("path=%s", r.URL.Path)
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if model == "fixture" {
					if r.Header.Get("Authorization") != "Bearer secret" {
						t.Error("missing bearer auth")
					}
					var questions []Question
					if err := json.Unmarshal(body["questions"], &questions); err != nil {
						t.Error(err)
					}
					if len(questions) != 1 || questions[0].Type != "choice" || questions[0].Name != "best" || len(questions[0].Choices) != 2 {
						t.Errorf("questions=%+v", questions)
					}
					respond(w, 200, map[string]any{"answers": []any{map[string]any{"type": "choice", "name": "best", "choice": "s.one", "probabilities": map[string]float64{"s.one": 0.9, "none": 0.1}}}, "usage": map[string]int{"input_tokens": 5, "output_tokens": 2}})
				} else {
					if r.Header.Get("x-goog-api-key") != "secret" {
						t.Error("missing gemini auth")
					}
					var questions map[string]struct {
						Type     string
						Criteria map[string]string
					}
					if err := json.Unmarshal(body["questions"], &questions); err != nil {
						t.Error(err)
					}
					if questions["best"].Type != "choice" || len(questions["best"].Criteria) != 2 {
						t.Errorf("questions=%+v", questions)
					}
					respond(w, 200, map[string]any{"answers": map[string]any{"best": map[string]any{"type": "choice", "choice": "s.one", "probabilities": map[string]float64{"s.one": 0.9, "none": 0.1}}}, "usage": map[string]int{"total_input_tokens": 5, "total_output_tokens": 2}})
				}
			}))
			defer host.Close()
			answer, usage, err := AskChoice(context.Background(), Settings{BaseURL: host.URL, Model: model, TimeoutSeconds: 5}, "secret", "task", "pick", []Choice{{Value: "s.one", Description: "One"}, {Value: "none", Description: "None"}})
			if err != nil || answer.Choice != "s.one" || answer.Probabilities["s.one"] != 0.9 || usage.PromptTokens != 5 || usage.CompletionTokens != 2 {
				t.Fatalf("answer=%+v usage=%+v err=%v", answer, usage, err)
			}
		})
	}
}
