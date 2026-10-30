package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingModelReceivesSummariesOnly(t *testing.T) {
	schema := objectSchema(map[string]any{"payload": map[string]any{"type": "string", "description": strings.Repeat("large schema ", 5000)}})
	candidates := []Candidate{{Tool: Tool{ID: "s.large", ServerID: "s", Name: "large", Description: "Inspect payload", InputSchema: schema, Adapter: "private.mjs", Revision: "private"}, Score: 1}}
	full := mustJSON(t, candidates)
	small := mustJSON(t, summarizeCandidates(candidates))
	if len(small)*20 >= len(full) {
		t.Fatal("summary did not materially reduce payload")
	}
	t.Logf("candidate payload: %d -> %d bytes", len(full), len(small))
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(body.Messages) != 2 || strings.Contains(body.Messages[1].Content, "input_schema") || strings.Contains(body.Messages[1].Content, "private.mjs") {
			t.Error("schema or execution details reached model")
		}
		respond(w, 200, map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"status":"selected","tools":[{"id":"s.large","reason":"match"}]}`}}}})
	}))
	defer model.Close()
	_, _, err := selectWithModel(context.Background(), Settings{BaseURL: model.URL, Model: "fixture", MaxTools: 1, TimeoutSeconds: 5}, "", DiscoverRequest{Intent: "payload"}, candidates)
	if err != nil {
		t.Fatal(err)
	}
}
func TestSummaryDiscoveryAndSchemaAuthorization(t *testing.T) {
	g, _ := testGateway(t)
	g.StartMaintenance(context.Background())
	approveRun(t, g.maintenance, enqueueTest(t, g.maintenance, false).ID)
	result, err := g.Discover(context.Background(), DiscoverRequest{Intent: "logs", Mode: "retrieval"})
	if err != nil {
		t.Fatal(err)
	}
	b := mustJSON(t, discoveryPresentation(result, true))
	if strings.Contains(string(b), "input_schema") {
		t.Fatal("summary discovery contained schema")
	}
	tool, err := g.ToolSchema(context.Background(), "service.logs", "developer")
	if err != nil || tool.InputSchema == nil {
		t.Fatalf("schema retrieval: %+v %v", tool, err)
	}
	if _, err := g.ToolSchema(context.Background(), "service.logs", "observer"); err == nil {
		t.Fatal("schema leaked to unauthorized actor")
	}
}
