package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestSelectServicesNarrowsToConfidentServices(t *testing.T) {
	servers := map[string]Server{"a": {ID: "a", Name: "Alpha", Description: "alpha things"}, "b": {ID: "b", Name: "Beta"}, "c": {ID: "c", Name: "Gamma"}}
	tools := []Tool{{ID: "a.one", ServerID: "a", Name: "one"}, {ID: "b.two", ServerID: "b", Name: "two"}, {ID: "a.zero", ServerID: "a", Name: "zero"}, {ID: "c.three", ServerID: "c", Name: "three"}}
	for _, tc := range []struct {
		name          string
		probabilities []float64 // for a, b, c in ID order
		want          []string
	}{
		{"confident services continue, best first", []float64{0.9, 0.3, 0.6}, []string{"a", "c"}},
		{"nothing confident keeps the best one", []float64{0.2, 0.4, 0.1}, []string{"b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var input string
			var questions int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Input     []map[string]string `json:"input"`
					Questions map[string]any      `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				input = payload.Input[0]["text"]
				questions = len(payload.Questions) - 1
				respond(w, 200, geminiFixture("selected", tc.probabilities...))
			}))
			defer server.Close()
			chosen, usage, err := selectServices(context.Background(), Settings{BaseURL: server.URL, Model: "gemini-decision-1", MaxTools: 4, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "do it", Context: "known"}, servers, tools)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(chosen, tc.want) || usage.PromptTokens != 77 || questions != 3 {
				t.Fatalf("chosen=%v usage=%+v questions=%d", chosen, usage, questions)
			}
			intentAt, servicesAt := strings.Index(input, `"intent"`), strings.Index(input, `"services"`)
			if intentAt < 0 || intentAt > servicesAt || !strings.Contains(input, `"tools":["one","zero"]`) || !strings.Contains(input, `"description":"alpha things"`) {
				t.Fatalf("service summaries must follow the intent and list tool names: %s", input)
			}
		})
	}
}

func TestSelectServicesSkipsSingleService(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer server.Close()
	chosen, _, err := selectServices(context.Background(), Settings{BaseURL: server.URL, Model: "gemini-decision-1", MaxTools: 4, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "x"}, map[string]Server{"a": {ID: "a"}}, []Tool{{ID: "a.one", ServerID: "a"}, {ID: "a.two", ServerID: "a"}})
	if err != nil || chosen != nil || calls != 0 {
		t.Fatalf("one service must not be routed: chosen=%v calls=%d err=%v", chosen, calls, err)
	}
}

func TestRoutingCandidatesCarryParameterNamesOnlyWhenEnabled(t *testing.T) {
	tool := Tool{ID: "s.a", Name: "a", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"repo": map[string]any{}, "owner": map[string]any{}}, "required": []any{"owner"}}}
	with, err := json.Marshal(routingCandidates([]Tool{tool}, true))
	if err != nil {
		t.Fatal(err)
	}
	without, err := json.Marshal(routingCandidates([]Tool{tool}, false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"params":["owner","repo"]`) || strings.Contains(string(without), "params") || strings.Contains(string(with), "input_schema") {
		t.Fatalf("with=%s without=%s", with, without)
	}
}

func TestLoadRoutingOptions(t *testing.T) {
	t.Setenv("GATEWAY_ROUTING", "")
	o, err := loadRoutingOptions()
	if err != nil || !o.hierarchical || !o.refine || o.params {
		t.Fatalf("default must be hierarchical with refine: options=%+v err=%v", o, err)
	}
	t.Setenv("GATEWAY_ROUTING", "flat")
	if o, err = loadRoutingOptions(); err != nil || o != (routingOptions{}) {
		t.Fatalf("flat must disable every stage: options=%+v err=%v", o, err)
	}
	t.Setenv("GATEWAY_ROUTING", "hierarchical, params")
	o, err = loadRoutingOptions()
	if err != nil || !o.hierarchical || !o.params || o.refine {
		t.Fatalf("options=%+v err=%v", o, err)
	}
	t.Setenv("GATEWAY_ROUTING", "bm25")
	if _, err := loadRoutingOptions(); err == nil {
		t.Fatal("unknown option must be rejected")
	}
}

// With refine, survivors of the yes/no stage compete in one choice question; the
// winner and survivors with a large share are returned, and "none" clears the result.
func TestRefineSelectionMakesSurvivorsCompete(t *testing.T) {
	tools := []Tool{{ID: "s.errors", ServerID: "s", Name: "errors", Description: "Errors.\nMore."}, {ID: "s.trace", ServerID: "s", Name: "trace"}, {ID: "s.projects", ServerID: "s", Name: "projects"}, {ID: "t.other", ServerID: "t", Name: "other"}}
	for _, tc := range []struct {
		name          string
		relevance     []float64 // yes/no stage, for errors, trace, projects, other
		choice        string
		probabilities map[string]float64
		want          []string
		wantStatus    string
	}{
		{"winner plus a strong runner-up", []float64{0.91, 0.76, 0.6, 0.2}, "s.trace", map[string]float64{"s.errors": 0.35, "s.trace": 0.6, "s.projects": 0.04, "none": 0.01}, []string{"s.trace", "s.errors"}, "selected"},
		{"a distant runner-up is left out", []float64{0.91, 0.76, 0.6, 0.2}, "s.errors", map[string]float64{"s.errors": 0.7, "s.trace": 0.27, "s.projects": 0.02, "none": 0.01}, []string{"s.errors"}, "selected"},
		{"none wins over unsure survivors", []float64{0.65, 0.6, 0.55, 0.2}, "none", map[string]float64{"s.errors": 0.3, "s.trace": 0.05, "s.projects": 0.05, "none": 0.6}, nil, "no_match"},
		{"none over unsure survivors of several services asks which one", []float64{0.65, 0.2, 0.2, 0.6}, "none", map[string]float64{"s.errors": 0.2, "t.other": 0.2, "none": 0.6}, nil, "needs_context"},
		{"none cannot veto a confident survivor", []float64{0.95, 0.93, 0.6, 0.2}, "none", map[string]float64{"s.errors": 0.25, "s.trace": 0.2, "s.projects": 0.05, "none": 0.5}, []string{"s.errors", "s.trace"}, "selected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var choiceInput string
			var criteria map[string]string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var payload struct {
					Input     []map[string]string       `json:"input"`
					Questions map[string]map[string]any `json:"questions"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if _, ok := payload.Questions["route_status"]; ok {
					respond(w, 200, geminiFixture("selected", tc.relevance...))
					return
				}
				choiceInput = payload.Input[0]["text"]
				raw, _ := json.Marshal(payload.Questions[choiceQuestionName]["criteria"])
				_ = json.Unmarshal(raw, &criteria)
				respond(w, 200, map[string]any{"answers": map[string]any{choiceQuestionName: map[string]any{"type": "choice", "choice": tc.choice, "probabilities": tc.probabilities}}, "usage": map[string]int{"total_input_tokens": 5}})
			}))
			defer server.Close()
			d, usage, err := selectTools(context.Background(), Settings{BaseURL: server.URL, Model: "gemini-decision-1", MaxTools: 4, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "500 errors"}, tools, routingOptions{refine: true})
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, tool := range d.Tools {
				ids = append(ids, tool.ID)
			}
			if d.Status != tc.wantStatus || !reflect.DeepEqual(ids, tc.want) || usage.PromptTokens != 77+5 || (d.Status == "needs_context") != strings.Contains(d.Question, "s、t") {
				t.Fatalf("decision=%+v usage=%+v", d, usage)
			}
			// Only the survivors and "none" are offered; descriptions are their first line.
			survivors := 0
			for _, p := range tc.relevance {
				if p >= decisionThreshold {
					survivors++
				}
			}
			if len(criteria) != survivors+1 || criteria["s.errors"] != "Errors." || criteria["none"] == "" {
				t.Fatalf("criteria=%v input=%s", criteria, choiceInput)
			}
		})
	}
}
