package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
)

func TestRoutingModelReceivesSummariesOnly(t *testing.T) {
	schema := objectSchema(map[string]any{"payload": map[string]any{"type": "string", "description": strings.Repeat("large schema ", 5000)}})
	candidates := []registry.Tool{{ID: "s.large", ServerID: "s", Name: "large", Description: "Inspect payload", InputSchema: schema, Adapter: "private.mjs", Revision: "private"}}
	full := mustJSON(t, candidates)
	small := mustJSON(t, routing.SummarizeTools(candidates))
	if len(small)*20 >= len(full) {
		t.Fatal("summary did not materially reduce payload")
	}
	t.Logf("candidate payload: %d -> %d bytes", len(full), len(small))
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if body.Input == "" || strings.Contains(body.Input, "input_schema") || strings.Contains(body.Input, "private.mjs") {
			t.Error("schema or execution details reached model")
		}
		respond(w, 200, decisionFixture("selected", 0.9))
	}))
	defer modelServer.Close()
	_, _, err := selectWithModel(context.Background(), model.Settings{BaseURL: modelServer.URL, Model: "fixture", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "payload"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
}
