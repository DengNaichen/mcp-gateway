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
