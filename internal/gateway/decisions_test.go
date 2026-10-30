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

func decisionFixture(status string, probabilities ...float64) map[string]any {
	answers := []any{map[string]any{"type": "choice", "name": "route_status", "choice": status}}
	for i, probability := range probabilities {
		answers = append(answers, map[string]any{"type": "predicate", "name": fmt.Sprintf("tool_%d", i), "probability": probability})
	}
	return map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 123, "output_tokens": 0}}
}

func TestDecisionsRouting(t *testing.T) {
	candidates := []Candidate{{Tool: Tool{ID: "s.a"}}, {Tool: Tool{ID: "s.b"}}, {Tool: Tool{ID: "s.c"}}}
	for _, tc := range []struct {
		name, status  string
		probabilities []float64
		want          []string
		wantStatus    string
	}{
		{"rank and limit", "selected", []float64{0.6, 0.95, 0.8}, []string{"s.b", "s.c"}, "selected"},
		{"stable ties", "selected", []float64{0.8, 0.8, 0.1}, []string{"s.a", "s.b"}, "selected"},
		{"threshold", "selected", []float64{0.49, 0.5, 0.1}, []string{"s.b"}, "selected"},
		{"below threshold", "selected", []float64{0.1, 0.2, 0.3}, nil, "no_match"},
		{"no match", "no_match", []float64{0.9, 0.9, 0.9}, nil, "no_match"},
		{"clarification", "needs_context", []float64{0.9, 0.9, 0.9}, nil, "needs_context"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/decisions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				var payload struct {
					Model          string             `json:"model"`
					Input          string             `json:"input"`
					Questions      []decisionQuestion `json:"questions"`
					Messages       json.RawMessage    `json:"messages"`
					ResponseFormat json.RawMessage    `json:"response_format"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				if payload.Model != "gpt-6-luna" || !strings.Contains(payload.Input, "inspect") || len(payload.Questions) != 4 || payload.Questions[0].Type != "choice" || payload.Questions[1].Name != "tool_0" || payload.Messages != nil || payload.ResponseFormat != nil {
					t.Errorf("unexpected Decisions payload: %+v", payload)
				}
				respond(w, 200, decisionFixture(tc.status, tc.probabilities...))
			}))
			defer server.Close()
			// Full endpoints and base URLs must both work.
			for _, suffix := range []string{"/v1", "/v1/decisions/"} {
				d, usage, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL + suffix, Model: "gpt-6-luna", MaxTools: 2, TimeoutSeconds: 5}, "test-key", DiscoverRequest{Intent: "inspect"}, candidates)
				if err != nil {
					t.Fatal(err)
				}
				var ids []string
				for _, tool := range d.Tools {
					ids = append(ids, tool.ID)
				}
				if d.Status != tc.wantStatus || !reflect.DeepEqual(ids, tc.want) || usage.PromptTokens != 123 || usage.CompletionTokens != 0 || (d.Status == "needs_context" && d.Question == "") {
					t.Fatalf("unexpected routing: %+v usage=%+v", d, usage)
				}
			}
		})
	}
}

func TestDecisionsRejectsInvalidAnswers(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"missing", `{}`},
		{"old protocol", `{"choices":[{"message":{"content":"{}"}}]}`},
		{"refusal", `{"answers":[{"type":"refusal","name":"route_status"},{"type":"predicate","name":"tool_0","probability":0.9}]}`},
		{"wrong name", `{"answers":[{"type":"choice","name":"route_status","choice":"selected"},{"type":"predicate","name":"s.unauthorized","probability":0.9}]}`},
		{"wrong type", `{"answers":[{"type":"choice","name":"route_status","choice":"selected"},{"type":"score","name":"tool_0","probability":0.9}]}`},
		{"missing probability", `{"answers":[{"type":"choice","name":"route_status","choice":"selected"},{"type":"predicate","name":"tool_0"}]}`},
		{"invalid probability", `{"answers":[{"type":"choice","name":"route_status","choice":"selected"},{"type":"predicate","name":"tool_0","probability":1.1}]}`},
		{"unknown status", `{"answers":[{"type":"choice","name":"route_status","choice":"execute"},{"type":"predicate","name":"tool_0","probability":0.9}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			_, _, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{}, []Candidate{{Tool: Tool{ID: "s.a"}}})
			if err == nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
}

func TestDecisionsErrorsAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 401, map[string]string{"error": "secret-test-key"})
	}))
	defer server.Close()
	s := Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}
	candidates := []Candidate{{Tool: Tool{ID: "s.a"}}}
	_, _, err := selectWithModel(context.Background(), s, "secret-test-key", DiscoverRequest{}, candidates)
	if err == nil || strings.Contains(err.Error(), "secret-test-key") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error did not redact key: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := selectWithModel(ctx, s, "", DiscoverRequest{}, candidates); err == nil {
		t.Fatal("cancellation ignored")
	}
	s.BaseURL += "/chat/completions"
	if _, _, err := selectWithModel(context.Background(), s, "", DiscoverRequest{}, candidates); err == nil {
		t.Fatal("legacy endpoint accepted")
	}
}
