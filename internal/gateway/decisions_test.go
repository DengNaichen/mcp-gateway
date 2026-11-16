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

	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
)

func decisionFixture(status string, probabilities ...float64) map[string]any {
	answers := []any{map[string]any{"type": "choice", "name": "route_status", "choice": status}}
	for i, probability := range probabilities {
		answers = append(answers, map[string]any{"type": "predicate", "name": fmt.Sprintf("tool_%d", i), "probability": probability})
	}
	return map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 123, "output_tokens": 0}}
}

func TestDecisionsRouting(t *testing.T) {
	candidates := []registry.Tool{{ID: "s.a"}, {ID: "s.b"}, {ID: "s.c"}}
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
		{"no match", "no_match", []float64{0.1, 0.2, 0.3}, nil, "no_match"},
		{"clarification", "needs_context", []float64{0.1, 0.2, 0.3}, nil, "needs_context"},
		{"status cannot veto a confident tool", "needs_context", []float64{0.94, 0.1, 0.2}, []string{"s.a"}, "selected"},
		{"no_match status cannot veto either", "no_match", []float64{0.1, 0.9, 0.2}, []string{"s.b"}, "selected"},
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
				if payload.Model != "gpt-6-luna" || !strings.Contains(payload.Input, "inspect") || len(payload.Questions) != 4 || payload.Questions[0].Type != "choice" || payload.Questions[1].Name != "tool_0" || !strings.Contains(payload.Questions[1].Instructions, `"s.a"`) || strings.Contains(payload.Questions[1].Instructions, "candidates[") || payload.Messages != nil || payload.ResponseFormat != nil {
					t.Errorf("unexpected Decisions payload: %+v", payload)
				}
				respond(w, 200, decisionFixture(tc.status, tc.probabilities...))
			}))
			defer server.Close()
			// Full endpoints and base URLs must both work.
			for _, suffix := range []string{"/v1", "/v1/decisions/"} {
				d, usage, err := selectWithModel(context.Background(), model.Settings{BaseURL: server.URL + suffix, Model: "gpt-6-luna", MaxTools: 2, TimeoutSeconds: 5}, "test-key", DiscoverRequest{Intent: "inspect"}, candidates)
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
			_, _, err := selectWithModel(context.Background(), model.Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{}, []registry.Tool{{ID: "s.a"}})
			if err == nil {
				t.Fatal("invalid answer accepted")
			}
		})
	}
}

// The task must come before the tool list in the model input; see selectWithModel.
func TestRoutingInputPutsIntentBeforeCandidates(t *testing.T) {
	var input string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		input = payload.Input
		respond(w, 200, decisionFixture("selected", 0.9))
	}))
	defer server.Close()
	tools := []registry.Tool{{ID: "s.a", Name: "a", Description: "first"}}
	if _, _, err := selectWithModel(context.Background(), model.Settings{BaseURL: server.URL, MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "find it", Context: "known"}, tools); err != nil {
		t.Fatal(err)
	}
	intentAt, contextAt, candidatesAt := strings.Index(input, `"intent"`), strings.Index(input, `"context"`), strings.Index(input, `"candidates"`)
	if intentAt < 0 || !(intentAt < contextAt && contextAt < candidatesAt) {
		t.Fatalf("model input must list intent, context, then candidates: %s", input)
	}
}

// The per-request tool limit is per provider: OpenAI keeps its original 100, Gemini allows more.
func TestRoutingToolLimitIsPerProvider(t *testing.T) {
	tools := make([]registry.Tool, openAIMaxTools+1)
	for i := range tools {
		tools[i] = registry.Tool{ID: fmt.Sprintf("s.t%03d", i)}
	}
	probabilities := make([]float64, len(tools))
	var openAICalls int
	openAI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openAICalls++
		respond(w, 200, decisionFixture("no_match", probabilities...))
	}))
	defer openAI.Close()
	_, _, err := selectWithModel(context.Background(), model.Settings{BaseURL: openAI.URL, Model: "gpt-6-luna", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "x"}, tools)
	if err == nil || !strings.Contains(err.Error(), "100") || openAICalls != 0 {
		t.Fatalf("OpenAI path must reject %d tools before calling the model: err=%v calls=%d", len(tools), err, openAICalls)
	}
	gemini := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, geminiFixture("no_match", probabilities...))
	}))
	defer gemini.Close()
	if _, _, err := selectWithModel(context.Background(), model.Settings{BaseURL: gemini.URL, Model: "gemini-decision-1", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "x"}, tools); err != nil {
		t.Fatalf("Gemini path must accept %d tools: %v", len(tools), err)
	}
}
