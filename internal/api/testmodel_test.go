package api

import (
	"encoding/json"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/routing"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// useKeywordModel points the gateway at a local Decisions server that rates a
// tool relevant when any word of the intent appears in its ID, name,
// description or tags. It stands in for a real routing model in tests whose
// subject is not routing quality.
func useKeywordModel(t *testing.T, g *gateway.Gateway) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input     string             `json:"input"`
			Questions []decisionQuestion `json:"questions"`
		}
		// The yes/no stage sends candidates (tools) or services; the refine stage
		// sends the survivors and one choice question.
		var input struct {
			Intent     string               `json:"intent"`
			Candidates []routing.Capability `json:"candidates"`
			Services   []struct {
				ID          string   `json:"id"`
				Name        string   `json:"name"`
				Description string   `json:"description"`
				Tools       []string `json:"tools"`
			} `json:"services"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if err := json.Unmarshal([]byte(payload.Input), &input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		words := strings.Fields(strings.ToLower(input.Intent))
		matches := func(text string) bool {
			text = strings.ToLower(text)
			for _, word := range words {
				if strings.Contains(text, word) {
					return true
				}
			}
			return false
		}
		if len(payload.Questions) == 1 && payload.Questions[0].Type == "choice" {
			// Pick the first matching survivor; survivors arrive best first.
			choice := "none"
			for _, c := range input.Candidates {
				if matches(c.ID + " " + c.Name + " " + c.Description + " " + strings.Join(c.Tags, " ")) {
					choice = c.ID
					break
				}
			}
			respond(w, 200, map[string]any{"answers": []any{map[string]any{"type": "choice", "name": payload.Questions[0].Name, "choice": choice, "probabilities": map[string]float64{choice: 1}}}, "usage": map[string]int{"input_tokens": 50, "output_tokens": 0}})
			return
		}
		texts := make([]string, 0, len(input.Candidates)+len(input.Services))
		for _, c := range input.Candidates {
			texts = append(texts, c.ID+" "+c.Name+" "+c.Description+" "+strings.Join(c.Tags, " "))
		}
		for _, service := range input.Services {
			texts = append(texts, service.ID+" "+service.Name+" "+service.Description+" "+strings.Join(service.Tools, " "))
		}
		status := "no_match"
		probabilities := make([]float64, len(texts))
		for i, text := range texts {
			probabilities[i] = 0.1
			if matches(text) {
				probabilities[i], status = 0.9, "selected"
			}
		}
		respond(w, 200, decisionFixture(status, probabilities...))
	}))
	t.Cleanup(server.Close)
	settings, _ := g.Config()
	settings.BaseURL, settings.Model = server.URL, "fixture"
	if err := g.SaveSettings(settings, "", false); err != nil {
		t.Fatal(err)
	}
}
