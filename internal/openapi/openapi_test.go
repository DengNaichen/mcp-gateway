package openapi

import (
	"reflect"
	"strings"
	"testing"

	"gatewaylab/internal/registry"
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

func TestOpenAPISelectionAndFailures(t *testing.T) {
	selected, err := ImportOpenAPI("ci", []byte(openAPIFixture), []string{"logs"})
	if err != nil || len(selected) != 1 || selected[0].ID != "ci.logs" {
		t.Fatalf("selection: %v %v", selected, err)
	}
	if _, err := ImportOpenAPI("ci", []byte(openAPIFixture), []string{"missing"}); err == nil {
		t.Fatal("missing selected operation ignored")
	}
	for _, bad := range []string{
		strings.ReplaceAll(openAPIFixture, "#/components/schemas/Input", "https://example.invalid/schema.json"),
		strings.ReplaceAll(openAPIFixture, "application/json:", "multipart/form-data:"),
		strings.ReplaceAll(openAPIFixture, "style: deepObject", "style: matrix"),
		strings.ReplaceAll(openAPIFixture, "operationId: logs", "operationId: create_build"),
	} {
		if _, err := ImportOpenAPI("ci", []byte(bad), nil); err == nil {
			t.Fatal("unsupported contract was silently imported")
		}
	}
}

func TestOpenAPIStableIDsAnd31(t *testing.T) {
	document := strings.ReplaceAll(openAPIFixture, "3.0.3", "3.1.1")
	a, err := ImportOpenAPI("ci", []byte(document), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ImportOpenAPI("ci", []byte(document), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("import is not deterministic")
	}
}

func TestOpenAPIRejectsForbiddenHeadersAndMapsGroups(t *testing.T) {
	tools, err := ImportOpenAPI("ci", []byte(openAPIFixture), []string{"create_build"})
	if err != nil || len(tools) != 1 {
		t.Fatalf("import failed: %v", err)
	}
	props := tools[0].InputSchema["properties"].(map[string]any)
	if _, ok := props["headers"]; !ok {
		t.Fatalf("expected header parameter under headers group: %+v", props)
	}

	for _, header := range []string{"Authorization", "Content-Type", "Cookie", "Host"} {
		bad := strings.ReplaceAll(openAPIFixture, "name: verbose, in: header", "name: "+header+", in: header")
		if _, err := ImportOpenAPI("ci", []byte(bad), nil); err == nil {
			t.Fatalf("forbidden header %q was imported", header)
		}
	}
}

func TestOpenAPIOperationCharacterization(t *testing.T) {
	doc := `{
  "openapi": "3.0.3",
  "paths": {
    "/items/{id}": {
      "parameters": [
        {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
        {"name": "q", "in": "query", "schema": {"type": "string"}}
      ],
      "get": {
        "tags": ["items", 123],
        "parameters": [
          {"name": "q", "in": "query", "required": true, "description": "override", "schema": {"type": "integer"}},
          {"name": "sid", "in": "cookie", "schema": {"type": "string"}}
        ],
        "responses": {
          "404": {"description": "not found"},
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Node"}}}},
          "default": {"description": "fallback", "content": {"text/plain": {"schema": {"type": "string"}}}}
        }
      },
      "delete": {
        "operationId": "delete_item",
        "responses": {
          "204": {"description": "deleted"}
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Node": {
        "type": "object",
        "properties": {
          "score": {"type": "number", "minimum": 0, "exclusiveMinimum": true},
          "tags": {"type": "array", "items": {"type": "string"}},
          "meta": {"type": "object", "additionalProperties": {"type": "string"}},
          "alt": {"oneOf": [{"type": "string"}, {"type": "integer"}]}
        }
      }
    }
  }
}`
	tools, err := ImportOpenAPI("svc", []byte(doc), nil)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	var getTool, delTool Tool
	for _, tool := range tools {
		if tool.Method == "GET" {
			getTool = tool
		} else {
			delTool = tool
		}
	}
	if getTool.ID != "svc.get_items_id" || !getTool.ReadOnly || getTool.Description != "GET /items/{id}" {
		t.Fatalf("unexpected GET tool metadata: %+v", getTool)
	}
	if !reflect.DeepEqual(getTool.Tags, []string{"items"}) {
		t.Fatalf("unexpected tags: %+v", getTool.Tags)
	}
	props := getTool.InputSchema["properties"].(map[string]any)
	queryProps := props["query"].(map[string]any)["properties"].(map[string]any)
	if queryProps["q"].(map[string]any)["type"] != "integer" || queryProps["q"].(map[string]any)["description"] != "override" {
		t.Fatalf("expected operation query parameter to override path parameter: %+v", queryProps["q"])
	}
	if _, ok := props["cookies"].(map[string]any)["properties"].(map[string]any)["sid"]; !ok {
		t.Fatalf("expected cookie parameter under cookies group: %+v", props)
	}
	if getTool.HTTP.Responses["200"].MediaType != "application/json" || getTool.HTTP.Responses["default"].MediaType != "text/plain" {
		t.Fatalf("unexpected responses: %+v", getTool.HTTP.Responses)
	}
	if _, has404 := getTool.HTTP.Responses["404"]; has404 {
		t.Fatalf("unexpected 404 response mapped: %+v", getTool.HTTP.Responses)
	}
	scoreProp := getTool.OutputSchema["$defs"].(map[string]any)["Node"].(map[string]any)["properties"].(map[string]any)["score"].(map[string]any)
	if scoreProp["exclusiveMinimum"] != float64(0) {
		t.Fatalf("expected OpenAPI 3.0 boolean exclusiveMinimum to convert to numeric 0, got %+v", scoreProp)
	}

	if delTool.ID != "svc.delete_item" || delTool.ReadOnly {
		t.Fatalf("unexpected DELETE tool metadata: %+v", delTool)
	}
	if delTool.HTTP.Responses["204"].MediaType != "empty" || delTool.OutputSchema != nil {
		t.Fatalf("unexpected 204 response mapping: %+v", delTool.HTTP.Responses)
	}
}

func TestOpenAPIErrorAndSchemaEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{
			name: "callbacks unsupported",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"post":{"callbacks":{"cb":{}},"responses":{"200":{"description":"ok"}}}}}}`,
		},
		{
			name: "path param not required",
			doc:  `{"openapi":"3.0.3","paths":{"/a/{id}":{"get":{"parameters":[{"name":"id","in":"path","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`,
		},
		{
			name: "missing path template param",
			doc:  `{"openapi":"3.0.3","paths":{"/a/{id}":{"get":{"responses":{"200":{"description":"ok"}}}}}}`,
		},
		{
			name: "extra unused path param",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`,
		},
		{
			name: "parameter content unsupported",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"parameters":[{"name":"q","in":"query","content":{"application/json":{}}}],"responses":{"200":{"description":"ok"}}}}}}`,
		},
		{
			name: "missing responses",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{}}}}`,
		},
		{
			name: "unsupported response media type",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/xml":{}}}}}}}}`,
		},
		{
			name: "subpath ref unsupported",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/A/properties/x"}}}}}}}}}`,
		},
		{
			name: "invalid properties map",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":[]}}}}}}}}}`,
		},
		{
			name: "invalid anyOf array",
			doc:  `{"openapi":"3.0.3","paths":{"/a":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"anyOf":{}}}}}}}}}`,
		},
	}
	for _, tc := range cases {
		if _, err := ImportOpenAPI("svc", []byte(tc.doc), nil); err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

// FuzzImportOpenAPI feeds arbitrary JSON/YAML to the importer. Errors are
// expected; panics are not. Accepted documents must import deterministically
// and every path template variable must have a path parameter mapping.
func FuzzImportOpenAPI(f *testing.F) {
	f.Add([]byte(openAPIFixture))
	f.Add([]byte(strings.ReplaceAll(openAPIFixture, "3.0.3", "3.1.1")))
	f.Add([]byte(`{"openapi":"3.0.0","paths":{"/a/{id}":{"get":{"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`))
	f.Add([]byte(`{"openapi":"3.1.0","paths":{"/a":{"post":{"requestBody":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/N"}}}},"responses":{"204":{"description":"x"}}}}},"components":{"schemas":{"N":{"$ref":"#/components/schemas/N"}}}}`))
	f.Add([]byte("openapi: 3.0.1\npaths:\n  /x: &a\n    get: {responses: {default: {description: d}}}\n  /y: *a\n"))
	f.Add([]byte(`{"openapi":"3.0.0","paths":{"//evil":{},"/q?x":{}}}`))
	f.Add([]byte(`{"openapi":"3.0.0","paths":{"/a":{"get":{"parameters":[{"$ref":"https://example.invalid/p.json"}]}}}}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`null`))
	f.Add([]byte("{"))
	f.Fuzz(func(t *testing.T, data []byte) {
		tools, err := ImportOpenAPI("svc", data, nil)
		if err != nil {
			return
		}
		again, err := ImportOpenAPI("svc", data, nil)
		if err != nil || !reflect.DeepEqual(tools, again) {
			t.Fatalf("import is not deterministic: %v", err)
		}
		for _, tool := range tools {
			if tool.ServerID != "svc" || !strings.HasPrefix(tool.Path, "/") || strings.HasPrefix(tool.Path, "//") || strings.ContainsAny(tool.Path, "?#") {
				t.Fatalf("malformed imported tool: %+v", tool)
			}
			mapped := map[string]bool{}
			if tool.HTTP != nil {
				for _, p := range tool.HTTP.Parameters {
					mapped[p.In+":"+p.Name] = true
				}
			}
			for _, match := range registry.PathVariable.FindAllStringSubmatch(tool.Path, -1) {
				if !mapped["path:"+match[1]] {
					t.Fatalf("tool %s path %q has unmapped variable %q", tool.ID, tool.Path, match[1])
				}
			}
		}
	})
}
