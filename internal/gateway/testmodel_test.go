package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// useKeywordModel points the gateway at a local Decisions server that rates a
// tool relevant when any word of the intent appears in its ID, name,
// description or tags. It stands in for a real routing model in tests whose
// subject is not routing quality.
func useKeywordModel(t *testing.T, g *Gateway) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Input string `json:"input"`
		}
		var input struct {
			Intent     string       `json:"intent"`
			Candidates []Capability `json:"candidates"`
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
		status := "no_match"
		probabilities := make([]float64, len(input.Candidates))
		for i, c := range input.Candidates {
			text := strings.ToLower(c.ID + " " + c.Name + " " + c.Description + " " + strings.Join(c.Tags, " "))
			probabilities[i] = 0.1
			for _, word := range words {
				if strings.Contains(text, word) {
					probabilities[i], status = 0.9, "selected"
					break
				}
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
