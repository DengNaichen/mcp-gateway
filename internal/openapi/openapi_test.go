package openapi

import (
	"reflect"
	"strings"
	"testing"
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
