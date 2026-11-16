package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
)

func systemOneFixture(status string, start int, probabilities ...float64) map[string]any {
	answers := map[string]any{}
	if status != "" {
		answers["route_status"] = map[string]any{"type": "choice", "choice": status, "confidence": 0.9}
	}
	for i, p := range probabilities {
		answers[toolQuestionName(start+i)] = map[string]any{"type": "noul", "noul": p}
	}
	return map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 41, "output_tokens": 2}}
}

func TestSystemOneRouting(t *testing.T) {
	candidates := []Tool{{ID: "s.a"}, {ID: "s.b"}, {ID: "s.c"}}
	for _, tc := range []struct {
		name, suffix, model string
		status              string
		probabilities       []float64
		want                []string
		wantStatus          string
	}{
		{"model jev appends /systemone", "/v1", "jev", "selected", []float64{0.6, 0.95, 0.8}, []string{"s.b", "s.c"}, "selected"},
		{"model decision-model-preview appends /systemone", "/v1", "decision-model-preview", "no_match", []float64{0.1, 0.2, 0.3}, nil, "no_match"},
		{"scoped jev model appends /systemone", "/v1", "typesafe/jev-latest", "needs_context", []float64{0.1, 0.2, 0.3}, nil, "needs_context"},
		{"explicit /systemone works with any model ID", "/v1/systemone/", "custom-router", "needs_context", []float64{0.94, 0.1, 0.2}, []string{"s.a"}, "selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("x-goog-api-key") != "" {
					t.Errorf("unexpected request: %s %s auth=%q goog=%q", r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-goog-api-key"))
				}
				var payload struct {
					Model     string                    `json:"model"`
					State     string                    `json:"state"`
					Input     json.RawMessage           `json:"input"`
					Questions map[string]map[string]any `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				status := payload.Questions["route_status"]
				criteria, _ := status["criteria"].(map[string]any)
				intentAt, candidatesAt := strings.Index(payload.State, `"intent"`), strings.Index(payload.State, `"candidates"`)
				if payload.Model != tc.model || payload.Input != nil || intentAt < 0 || intentAt > candidatesAt ||
					len(payload.Questions) != 4 || status["type"] != "choice" || len(criteria) != 3 ||
					payload.Questions["tool_0"]["type"] != "noul" || !strings.Contains(fmt.Sprint(payload.Questions["tool_2"]["instructions"]), `"s.c"`) {
					t.Errorf("unexpected SystemOne payload: %+v", payload)
				}
				respond(w, 200, systemOneFixture(tc.status, 0, tc.probabilities...))
			}))
			defer server.Close()
			d, usage, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL + tc.suffix, Model: tc.model, MaxTools: 2, TimeoutSeconds: 5}, "test-key", DiscoverRequest{Intent: "inspect"}, candidates)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, tool := range d.Tools {
				ids = append(ids, tool.ID)
			}
			if d.Status != tc.wantStatus || !reflect.DeepEqual(ids, tc.want) || usage.PromptTokens != 41 || usage.CompletionTokens != 2 || (d.Status == "needs_context" && d.Question == "") {
				t.Fatalf("unexpected routing: %+v usage=%+v", d, usage)
			}
		})
	}
}

func TestSystemOneBatchesOverQuestionLimit(t *testing.T) {
	// 32 tools require 3 batches under the 16-question cap: 15 (with route_status), 16, 1.
	tools := make([]Tool, 32)
	for i := range tools {
		tools[i] = Tool{ID: fmt.Sprintf("s.t%02d", i)}
	}
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		batchSizes = append(batchSizes, len(payload.Questions))
		if len(payload.Questions) > systemOneMaxQuestions {
			t.Errorf("batch exceeded %d questions: %d", systemOneMaxQuestions, len(payload.Questions))
		}
		answers := make(map[string]any, len(payload.Questions))
		if _, ok := payload.Questions["route_status"]; ok {
			answers["route_status"] = map[string]any{"type": "choice", "choice": "selected"}
		}
		for name := range payload.Questions {
			if name == "route_status" {
				continue
			}
			score := 0.1
			if name == "tool_20" {
				score = 0.85
			} else if name == "tool_31" {
				score = 0.95
			}
			answers[name] = map[string]any{"type": "noul", "noul": score}
		}
		respond(w, 200, map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 10}})
	}))
	defer server.Close()

	d, usage, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL + "/v1/systemone", Model: "jev", MaxTools: 2, TimeoutSeconds: 5}, "k", DiscoverRequest{Intent: "find"}, tools)
	if err != nil {
		t.Fatal(err)
	}
	wantBatches := []int{16, 16, 1}
	if !reflect.DeepEqual(batchSizes, wantBatches) {
		t.Fatalf("batchSizes=%v want=%v", batchSizes, wantBatches)
	}
	if usage.PromptTokens != 30 {
		t.Fatalf("expected summed usage 30, got %+v", usage)
	}
	var ids []string
	for _, tool := range d.Tools {
		ids = append(ids, tool.ID)
	}
	if !reflect.DeepEqual(ids, []string{"s.t31", "s.t20"}) {
		t.Fatalf("unexpected tools across batches: %v", ids)
	}
}

func TestSystemOneRefineSelection(t *testing.T) {
	tools := []Tool{
		{ID: "s.errors", ServerID: "s", Name: "errors", Description: "Errors.\nMore."},
		{ID: "s.trace", ServerID: "s", Name: "trace", Description: "Trace."},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Model     string                    `json:"model"`
			State     string                    `json:"state"`
			Questions map[string]map[string]any `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if _, ok := payload.Questions["route_status"]; ok {
			respond(w, 200, systemOneFixture("selected", 0, 0.91, 0.76))
			return
		}
		if payload.State == "" || payload.Questions[choiceQuestionName]["type"] != "choice" {
			t.Errorf("unexpected choice payload: %+v", payload)
		}
		respond(w, 200, map[string]any{
			"answers": map[string]any{
				choiceQuestionName: map[string]any{
					"type":          "choice",
					"choice":        "s.trace",
					"probabilities": map[string]float64{"s.errors": 0.35, "s.trace": 0.6, "none": 0.05},
				},
			},
			"usage": map[string]int{"input_tokens": 9},
		})
	}))
	defer server.Close()

	d, usage, err := selectTools(context.Background(), Settings{BaseURL: server.URL + "/v1", Model: "jev", MaxTools: 4, TimeoutSeconds: 5}, "k", DiscoverRequest{Intent: "500 errors"}, tools, routingOptions{refine: true})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tool := range d.Tools {
		ids = append(ids, tool.ID)
	}
	if d.Status != "selected" || !reflect.DeepEqual(ids, []string{"s.trace", "s.errors"}) || usage.PromptTokens != 50 {
		t.Fatalf("unexpected refine result: d=%+v usage=%+v", d, usage)
	}
}

func TestSystemOneRejectsChatEndpointsAndInvalidAnswers(t *testing.T) {
	for _, suffix := range []string{"/v1/chat/completions", "/v1/responses"} {
		_, _, err := selectWithModel(context.Background(), Settings{BaseURL: "https://example.com" + suffix, Model: "jev", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{}, []Tool{{ID: "s.a"}})
		if err == nil || !strings.Contains(err.Error(), "/systemone") {
			t.Fatalf("expected /systemone error for %s, got %v", suffix, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"answers":{"route_status":{"type":"choice","choice":"selected"},"tool_0":{"type":"predicate","noul":0.9}}}`))
	}))
	defer server.Close()
	if _, _, err := selectWithModel(context.Background(), Settings{BaseURL: server.URL + "/v1/systemone", Model: "jev", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{}, []Tool{{ID: "s.a"}}); err == nil {
		t.Fatal("invalid noul type accepted")
	}
}

// Explicit opt-in live test against a real SystemOne endpoint. Keys are never logged.
func TestLiveSystemOneRouting(t *testing.T) {
	key := os.Getenv("GATEWAY_TEST_SYSTEMONE_KEY")
	baseURL := os.Getenv("GATEWAY_TEST_SYSTEMONE_URL")
	model := os.Getenv("GATEWAY_TEST_SYSTEMONE_MODEL")
	if key == "" || baseURL == "" {
		t.Skip("set GATEWAY_TEST_SYSTEMONE_URL and GATEWAY_TEST_SYSTEMONE_KEY for live SystemOne integration")
	}
	if model == "" {
		model = "jev"
	}
	tools := make([]Tool, 18)
	for i := range tools {
		tools[i] = Tool{ID: fmt.Sprintf("svc.tool_%02d", i), ServerID: "svc", Name: fmt.Sprintf("tool_%02d", i), Description: fmt.Sprintf("Unrelated helper %d", i)}
	}
	tools[16] = Tool{ID: "svc.list_issues", ServerID: "svc", Name: "list_issues", Description: "List open GitHub issues in a repository."}

	d, usage, err := selectTools(context.Background(), Settings{
		BaseURL:        baseURL,
		Model:          model,
		MaxTools:       4,
		TimeoutSeconds: 30,
	}, key, DiscoverRequest{Intent: "list open GitHub issues in mcp-gateway"}, tools, routingOptions{hierarchical: true, refine: true})
	if err != nil {
		t.Fatalf("live SystemOne selectTools failed: %v", err)
	}
	if d.Status != "selected" || len(d.Tools) == 0 || d.Tools[0].ID != "svc.list_issues" || usage.PromptTokens == 0 {
		t.Fatalf("unexpected live SystemOne decision: %+v usage=%+v", d, usage)
	}
	t.Logf("live SystemOne routed %d tools -> %s (tokens=%d)", len(tools), d.Tools[0].ID, usage.PromptTokens)
}
