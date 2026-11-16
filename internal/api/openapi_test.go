package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/input"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
	upstreamclient "gatewaylab/internal/upstream"
)

const openAPIFixture = `openapi: 3.0.3
info: {title: Fixture, version: v1}
paths:
  /builds/{project}:
    parameters:
      - {$ref: '#/components/parameters/Project'}
    post:
      operationId: create_build
      summary: Create a build
      parameters:
        - {name: labels, in: query, schema: {type: array, items: {type: string}}}
        - {name: verbose, in: header, schema: {type: boolean}}
      requestBody:
        required: true
        content:
          application/json:
            schema: {$ref: '#/components/schemas/Input'}
      responses:
        '200':
          description: Build
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Build'}
  /logs:
    get:
      operationId: logs
      parameters:
        - {name: filter, in: query, style: deepObject, explode: true, schema: {type: object, properties: {service: {type: string}}}}
      responses:
        '200': {description: Logs, content: {text/plain: {schema: {type: string}}}}
components:
  parameters:
    Project: {name: project, in: path, required: true, schema: {type: string}}
  schemas:
    Input:
      type: object
      required: [branch]
      properties:
        branch: {type: string}
        note: {type: string, nullable: true}
    Build:
      type: object
      required: [id]
      properties:
        id: {type: integer}
        parent: {$ref: '#/components/schemas/Build'}
`

func TestOpenAPIWireContract(t *testing.T) {
	tools, err := openapi.ImportOpenAPI("ci", []byte(openAPIFixture), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatalf("unexpected tools: %+v", tools)
	}
	tool := tools[0]
	if tool.Name != "create_build" || tool.ReadOnly {
		t.Fatalf("incorrect operation: %+v", tool)
	}
	args := map[string]any{"path": map[string]any{"project": "team/api"}, "query": map[string]any{"labels": []any{"urgent", "A B"}}, "headers": map[string]any{"verbose": true}, "body": map[string]any{"branch": "main", "note": nil}}
	schema, err := registry.CompileSchema(tool.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(args); err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.EscapedPath() != "/builds/team%2Fapi" || r.Header.Get("verbose") != "true" {
			t.Errorf("incorrect wire request: %s %s %v", r.Method, r.URL, r.Header)
		}
		if got := r.URL.Query()["labels"]; len(got) != 2 || got[1] != "A B" {
			t.Errorf("incorrect query: %v", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["branch"] != "main" || body["note"] != nil {
			t.Errorf("incorrect body: %v", body)
		}
		respond(w, 200, map[string]any{"id": 12, "parent": map[string]any{"id": 9}})
	}))
	defer upstream.Close()
	result, err := upstreamclient.CallHTTP(context.Background(), registry.Server{Endpoint: upstream.URL}, tool, args)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["id"] != float64(12) {
		t.Fatal(result)
	}
	if _, err := upstreamclient.MappedResponse(tool, 200, []byte(`{"id":"wrong"}`)); err == nil {
		t.Fatal("response schema violation accepted")
	}
	if _, err := upstreamclient.MappedResponse(tool, 201, []byte(`{"id":12}`)); err == nil {
		t.Fatal("undeclared status accepted")
	}
	logs := tools[1]
	request, err := upstreamclient.BuildMappedRequest(context.Background(), registry.Server{Endpoint: upstream.URL}, logs, map[string]any{"query": map[string]any{"filter": map[string]any{"service": "billing"}}})
	if err != nil {
		t.Fatal(err)
	}
	if request.URL.Query().Get("filter[service]") != "billing" {
		t.Fatal(request.URL)
	}
	text, err := upstreamclient.MappedResponse(logs, 200, []byte("hello logs"))
	if err != nil || text.(map[string]any)["text"] != "hello logs" {
		t.Fatalf("text response: %v %v", text, err)
	}
}
func TestOpenAPIMaintenanceWithoutPi(t *testing.T) {
	g, reference := testGatewayOptions(t, "nonexistent-pi-binary", nil)
	if err := os.WriteFile(reference, []byte(openAPIFixture), 0600); err != nil {
		t.Fatal(err)
	}
	m := g.Maintenance()
	setLogsAcceptance(t, g, "v1", "openapi")
	source, _ := m.Source("service")
	source.Format = "openapi"
	source.OperationIDs = []string{"logs"}
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	r := approveRun(t, m, enqueueTest(t, m, false).ID)
	if len(r.Tools) != 1 || r.Tools[0].HTTP == nil || !strings.Contains(r.Summary, "未调用模型") {
		t.Fatalf("wrong deterministic result: %+v", r)
	}
}

func TestLiveBackendOpenAPI(t *testing.T) {
	endpoint := os.Getenv("GATEWAY_TEST_BACKEND_URL")
	if endpoint == "" {
		t.Skip("set GATEWAY_TEST_BACKEND_URL to verify a live backend")
	}
	resp, err := http.Get(strings.TrimRight(endpoint, "/") + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := input.ReadBounded(resp.Body, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	tools, err := openapi.ImportOpenAPI("backend", b, []string{"healthz", "readyz"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 2 {
		t.Fatal("health/readiness operations missing")
	}
	for _, tool := range tools {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, err := upstreamclient.CallHTTP(ctx, registry.Server{Endpoint: endpoint}, tool, map[string]any{})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: response validated (%T)", tool.ID, result)
	}
}
