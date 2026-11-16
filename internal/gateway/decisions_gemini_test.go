package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func geminiFixture(status string, probabilities ...float64) map[string]any {
	answers := map[string]any{"route_status": map[string]any{"type": "choice", "choice": status, "confidence": 0.9}}
	for i, p := range probabilities {
		answers[toolQuestionName(i)] = map[string]any{"type": "noul", "noul": p, "confidence": p}
	}
	return map[string]any{"answers": answers, "usage": map[string]int{"total_input_tokens": 77, "total_output_tokens": 0}}
}

func TestGeminiDecisionsRouting(t *testing.T) {
	candidates := []Tool{{ID: "s.a"}, {ID: "s.b"}, {ID: "s.c"}}
	for _, tc := range []struct {
		name, status  string
		probabilities []float64
		want          []string
		wantStatus    string
	}{
		{"rank and limit", "selected", []float64{0.6, 0.95, 0.8}, []string{"s.b", "s.c"}, "selected"},
		{"below threshold", "selected", []float64{0.1, 0.2, 0.3}, nil, "no_match"},
		{"no match", "no_match", []float64{0.1, 0.2, 0.3}, nil, "no_match"},
		{"clarification", "needs_context", []float64{0.1, 0.2, 0.3}, nil, "needs_context"},
		{"status cannot veto a confident tool", "needs_context", []float64{0.94, 0.1, 0.2}, []string{"s.a"}, "selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1beta/decisions" || r.Header.Get("x-goog-api-key") != "test-key" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var payload struct {
					Model     string                    `json:"model"`
					Input     []map[string]string       `json:"input"`
					Questions map[string]map[string]any `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				status := payload.Questions["route_status"]
				criteria, _ := status["criteria"].(map[string]any)
				if payload.Model != "gemini-decision-1" || len(payload.Input) != 1 || payload.Input[0]["type"] != "text" || !strings.Contains(payload.Input[0]["text"], "inspect") ||
					len(payload.Questions) != 4 || status["type"] != "choice" || len(criteria) != 3 || payload.Questions["tool_0"]["type"] != "noul" || payload.Questions["tool_2"]["instructions"] == nil || !strings.Contains(fmt.Sprint(payload.Questions["tool_2"]["instructions"]), `"s.c"`) || strings.Contains(fmt.Sprint(payload.Questions["tool_0"]["instructions"]), "candidates[") {
					t.Errorf("unexpected Gemini payload: %+v", payload)
				}
				respond(w, 200, geminiFixture(tc.status, tc.probabilities...))
			}))
			defer server.Close()
			for _, suffix := range []string{"/v1beta", "/v1beta/decisions/"} {
				d, usage, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL + suffix, Model: "gemini-decision-1", MaxTools: 2, TimeoutSeconds: 5}, "test-key", DiscoverRequest{Intent: "inspect"}, candidates)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, tool := range d.Tools {
					ids = append(ids, tool.ID)
				}
				if d.Status != tc.wantStatus || !reflect.DeepEqual(ids, tc.want) || usage.PromptTokens != 77 || (d.Status == "needs_context" && d.Question == "") {
					t.Fatalf("unexpected routing: %+v usage=%+v", d, usage)
				}
			}
		})
	}
}

func TestGeminiDecisionsRejectsInvalidAnswers(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing", `{}`},
		{"openai shape", `{"answers":[{"type":"choice","name":"route_status","choice":"selected"},{"type":"predicate","name":"tool_0","probability":0.9}]}`},
		{"missing tool", `{"answers":{"route_status":{"type":"choice","choice":"selected"},"other":{"type":"noul","noul":0.9}}}`},
		{"wrong type", `{"answers":{"route_status":{"type":"choice","choice":"selected"},"tool_0":{"type":"score","noul":0.9}}}`},
		{"missing probability", `{"answers":{"route_status":{"type":"choice","choice":"selected"},"tool_0":{"type":"noul"}}}`},
		{"invalid probability", `{"answers":{"route_status":{"type":"choice","choice":"selected"},"tool_0":{"type":"noul","noul":1.1}}}`},
		{"unknown status", `{"answers":{"route_status":{"type":"choice","choice":"execute"},"tool_0":{"type":"noul","noul":0.9}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			_, _, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL, Model: "gemini-decision-1", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{}, []Tool{{ID: "s.a"}})
			if err == nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
}
