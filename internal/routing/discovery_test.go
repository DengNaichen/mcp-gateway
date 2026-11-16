package routing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverPurePipeline(t *testing.T) {
	tools := []Tool{{ID: "a.one", ServerID: "a", Name: "one"}, {ID: "b.two", ServerID: "b", Name: "two"}, {ID: "c.three", ServerID: "c", Name: "three"}}
	servers := map[string]Server{"a": {ID: "a"}, "b": {ID: "b"}, "c": {ID: "c"}}
	calls := 0
	client := stubClient{decisions: func(_ context.Context, input string, instructions []string) (Answers, error) {
		calls++
		if calls == 1 {
			return Answers{Status: "selected", Relevance: []float64{0.1, 0.9, 0.2}, Usage: Usage{PromptTokens: 10, CompletionTokens: 2}}, nil
		}
		if len(instructions) != 1 || !strings.Contains(input, `"id":"b.two"`) || strings.Contains(input, `"id":"a.one"`) {
			t.Fatalf("hierarchy did not narrow candidate set: %s", input)
		}
		return Answers{Status: "no_match", Relevance: []float64{0.9}, Usage: Usage{PromptTokens: 20, CompletionTokens: 3}}, nil
	}}
	result, err := Discover(context.Background(), client, Request{Intent: "do it"}, servers, tools, 4, Options{Hierarchical: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.Status != "selected" || !result.Decided || result.Eligible != 3 || !reflect.DeepEqual(result.Servers, []string{"b"}) || len(result.Tools) != 1 || result.Tools[0].ID != "b.two" || result.Usage != (Usage{PromptTokens: 30, CompletionTokens: 5}) {
		t.Fatalf("result=%+v calls=%d", result, calls)
	}
}

func TestDiscoverNoToolsValidationAndErrors(t *testing.T) {
	client := stubClient{decisions: func(context.Context, string, []string) (Answers, error) {
		t.Fatal("unexpected model call")
		return Answers{}, nil
	}}
	result, err := Discover(context.Background(), client, Request{Intent: "x"}, nil, nil, 4, Options{})
	if err != nil || result.Tools == nil || result.Decided || result.Status != "no_match" || result.Summary != "当前身份下没有已启用的可用工具" {
		t.Fatalf("empty=%+v err=%v", result, err)
	}
	for _, req := range []Request{{}, {Intent: strings.Repeat("a", 24001)}} {
		if _, err := Discover(context.Background(), client, req, nil, nil, 4, Options{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("validation: %v", err)
		}
	}
	sentinel := errors.New("model offline")
	client.decisions = func(context.Context, string, []string) (Answers, error) { return Answers{}, sentinel }
	result, err = Discover(context.Background(), client, Request{Intent: "x"}, nil, []Tool{{ID: "s.one"}}, 4, Options{})
	if !errors.Is(err, sentinel) || result.Decided || result.Status != "no_match" {
		t.Fatalf("model error: %+v %v", result, err)
	}
}

func TestFlatSelectionPolicy(t *testing.T) {
	tools := []Tool{{ID: "s.one"}, {ID: "s.two"}, {ID: "s.three"}}
	for _, tc := range []struct {
		status     string
		relevance  []float64
		max        int
		wantStatus string
		want       []string
		invalid    bool
	}{
		{"no_match", []float64{0.7, 0.9, 0.9}, 2, "selected", []string{"s.two", "s.three"}, false},
		{"selected", []float64{0.1, 0.49, 0.5}, 3, "selected", []string{"s.three"}, false},
		{"selected", []float64{0.1, 0.2, 0.3}, 3, "no_match", nil, false},
		{"needs_context", []float64{0.1, 0.2, 0.3}, 3, "needs_context", nil, false},
		{"unknown", []float64{0.1, 0.2, 0.3}, 3, "", nil, true},
	} {
		t.Run(tc.status+tc.wantStatus, func(t *testing.T) {
			client := stubClient{decisions: func(context.Context, string, []string) (Answers, error) {
				return Answers{Status: tc.status, Relevance: tc.relevance}, nil
			}}
			result, err := Discover(context.Background(), client, Request{Intent: "task", Services: []string{"s"}}, nil, tools, tc.max, Options{Hierarchical: true})
			if tc.invalid {
				if !errors.Is(err, ErrModelResponse) || err.Error() != "Decisions 返回了未知路由状态" {
					t.Fatalf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, tool := range result.Tools {
				ids = append(ids, tool.ID)
			}
			if result.Status != tc.wantStatus || !reflect.DeepEqual(ids, tc.want) || (result.Status == "needs_context" && result.Question == "") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
