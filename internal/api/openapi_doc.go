package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
)

// OpenAPIDocument renders apiRoutes as the OpenAPI 3.1 description of /api.
// The committed copy lives at docs/api/openapi.json; regenerate it with
// go test ./internal/api -run TestOpenAPIDocumentIsCurrent -update.
func OpenAPIDocument() ([]byte, error) {
	g := docGenerator{schemas: map[string]any{}}
	paths := map[string]any{}
	for _, r := range apiRoutes {
		item, _ := paths[r.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			paths[r.Path] = item
		}
		op, err := g.operation(r)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", r.Method, r.Path, err)
		}
		item[strings.ToLower(r.Method)] = op
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   "MCP Gateway control plane API",
			"version": "1",
			"description": "The HTTP contract between the gateway backend and its control-plane clients (desktop app, web console). " +
				"In local mode the backend listens on loopback only and requires no authentication. In shared mode every route " +
				"except the public ones needs a bearer access token or the session cookie from /api/auth/login; routes that are " +
				"not marked as invocation or authenticated routes additionally need an admin principal. Every error response has the Error body.",
		},
		"paths": paths,
		"components": map[string]any{
			"schemas": g.schemas,
			"securitySchemes": map[string]any{
				"bearerAuth":    map[string]any{"type": "http", "scheme": "bearer"},
				"sessionCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": "gateway_session"},
			},
		},
		"security": []any{map[string]any{"bearerAuth": []any{}}, map[string]any{"sessionCookie": []any{}}},
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

type docGenerator struct{ schemas map[string]any }

var pathParam = regexp.MustCompile(`\{([^{}]+)\}`)

func (g docGenerator) operation(r route) (map[string]any, error) {
	segments := strings.Split(strings.TrimPrefix(r.Path, "/api/"), "/")
	op := map[string]any{
		"operationId": operationID(r.Method, r.Path),
		"summary":     r.Summary,
		"tags":        []any{segments[0]},
	}
	applyOperationSecurity(op, r)
	if params := operationParameters(r); len(params) > 0 {
		op["parameters"] = params
	}
	reqBody, err := g.operationRequestBody(r.Request)
	if err != nil {
		return nil, err
	}
	if reqBody != nil {
		op["requestBody"] = reqBody
	}
	responses, err := g.operationResponses(r.Responses)
	if err != nil {
		return nil, err
	}
	op["responses"] = responses
	return op, nil
}

func applyOperationSecurity(op map[string]any, r route) {
	switch {
	case r.Public:
		op["security"] = []any{}
	case r.Invocation:
		op["description"] = "Invocation route: open to any authenticated principal in shared mode. Results are limited to what the principal (or, in local mode, the simulated actor) may use."
	case r.Authenticated:
		op["description"] = "Authenticated route: open to any authenticated principal in shared mode."
	default:
		op["description"] = "Management route: requires an admin principal in shared mode."
	}
}

func operationParameters(r route) []any {
	params := []any{}
	for _, m := range pathParam.FindAllStringSubmatch(r.Path, -1) {
		params = append(params, map[string]any{"name": m[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	for _, q := range r.Query {
		params = append(params, map[string]any{"name": q.Name, "in": "query", "description": q.Description, "schema": map[string]any{"type": q.Type}})
	}
	if r.Invocation {
		params = append(params,
			map[string]any{"name": "agent", "in": "query", "description": "Agent key identifying the calling client", "schema": map[string]any{"type": "string"}},
			map[string]any{"name": "X-Gateway-Scope", "in": "header", "description": "Signed repository scope token issued by the scope hook", "schema": map[string]any{"type": "string"}},
		)
	}
	return params
}

func (g docGenerator) operationRequestBody(req any) (map[string]any, error) {
	switch body := req.(type) {
	case nil:
		return nil, nil
	case rawBody:
		return map[string]any{"required": true, "content": map[string]any{body.MediaType: map[string]any{"schema": map[string]any{}}}}, nil
	default:
		schema, err := g.schema(reflect.TypeOf(body))
		if err != nil {
			return nil, err
		}
		return map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}, nil
	}
}

func (g docGenerator) operationResponses(respMap map[int]any) (map[string]any, error) {
	errorSchema, err := g.schema(reflect.TypeFor[apitypes.Error]())
	if err != nil {
		return nil, err
	}
	responses := map[string]any{"default": map[string]any{"description": "Error", "content": jsonContent(errorSchema)}}
	for status, body := range respMap {
		content, err := g.responseContent(body)
		if err != nil {
			return nil, err
		}
		responses[strconv.Itoa(status)] = map[string]any{"description": httpStatusText(status), "content": content}
	}
	return responses, nil
}

func (g docGenerator) responseContent(body any) (map[string]any, error) {
	switch body := body.(type) {
	case binary:
		return map[string]any{body.MediaType: map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}, nil
	case oneOf:
		variants := make([]any, 0, len(body))
		for _, v := range body {
			s, err := g.schema(reflect.TypeOf(v))
			if err != nil {
				return nil, err
			}
			variants = append(variants, s)
		}
		return jsonContent(map[string]any{"oneOf": variants}), nil
	default:
		s, err := g.schema(reflect.TypeOf(body))
		if err != nil {
			return nil, err
		}
		return jsonContent(s), nil
	}
}

func operationID(method, path string) string {
	parts := []string{strings.ToLower(method)}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/api/"), "/") {
		parts = append(parts, strings.NewReplacer("{", "by_", "}", "", "-", "_").Replace(segment))
	}
	return strings.Join(parts, "_")
}

func jsonContent(schema any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

func ref(name string) map[string]any { return map[string]any{"$ref": "#/components/schemas/" + name} }

func httpStatusText(status int) string {
	switch status {
	case 200:
		return "OK"
	case 202:
		return "Accepted"
	case 400:
		return "Rejected; see error and detail"
	}
	return strconv.Itoa(status)
}

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// schema returns the JSON Schema of t, registering named structs as components.
// Slices, maps and pointers are nullable because Go encodes nil as null.
func (g docGenerator) schema(t reflect.Type) (map[string]any, error) {
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}, nil
	case rawType:
		return map[string]any{}, nil
	}
	switch t.Kind() {
	case reflect.Pointer:
		inner, err := g.schema(t.Elem())
		return nullable(inner), err
	case reflect.Slice:
		items, err := g.schema(t.Elem())
		return nullable(map[string]any{"type": "array", "items": items}), err
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map key %s is not a string", t.Key())
		}
		out := map[string]any{"type": "object"}
		if t.Elem().Kind() != reflect.Interface {
			values, err := g.schema(t.Elem())
			if err != nil {
				return nil, err
			}
			out["additionalProperties"] = values
		}
		return nullable(out), nil
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Struct:
		return g.component(t)
	}
	return nil, fmt.Errorf("no JSON schema mapping for %s", t)
}

func (g docGenerator) component(t reflect.Type) (map[string]any, error) {
	name := t.Name()
	if name == "" || t.PkgPath() != reflect.TypeFor[apitypes.Error]().PkgPath() {
		return nil, fmt.Errorf("%s is not a named apitypes struct", t)
	}
	if _, ok := g.schemas[name]; ok {
		return ref(name), nil
	}
	g.schemas[name] = map[string]any{} // break recursion before visiting fields
	properties := map[string]any{}
	required := []string{}
	if err := g.fields(t, properties, &required); err != nil {
		return nil, err
	}
	slices.Sort(required)
	g.schemas[name] = map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
	return ref(name), nil
}

func (g docGenerator) fields(t reflect.Type, properties map[string]any, required *[]string) error {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		if f.Anonymous && name == "" {
			if err := g.fields(f.Type, properties, required); err != nil {
				return err
			}
			continue
		}
		if name == "" {
			name = f.Name
		}
		s, err := g.schema(f.Type)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
		properties[name] = s
		if !strings.Contains(opts, "omitempty") {
			*required = append(*required, name)
		}
	}
	return nil
}

func nullable(s map[string]any) map[string]any {
	if typ, ok := s["type"].(string); ok {
		out := map[string]any{}
		for k, v := range s {
			out[k] = v
		}
		out["type"] = []any{typ, "null"}
		return out
	}
	if _, ok := s["$ref"]; ok {
		return map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
	}
	return s
}
