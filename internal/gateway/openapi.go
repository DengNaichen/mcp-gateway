package gateway

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ImportOpenAPI supports JSON and YAML OpenAPI 3.0/3.1. External references and
// unsupported wire formats are errors, never silently guessed or dropped.
func ImportOpenAPI(serverID string, data []byte, operationIDs []string) ([]Tool, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi: %w", err)
	}
	// YAML numbers/maps must have JSON semantics before generating JSON Schema.
	encoded, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("normalize openapi: %w", err)
	}
	if err := json.Unmarshal(encoded, &doc); err != nil {
		return nil, fmt.Errorf("decode openapi document: %w", err)
	}
	version, _ := doc["openapi"].(string)
	if !strings.HasPrefix(version, "3.0.") && !strings.HasPrefix(version, "3.1.") {
		return nil, invalid("支持 OpenAPI 3.0 / 3.1 文档")
	}
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		return nil, invalid("OpenAPI 缺少 paths")
	}
	importer := openAPIImporter{doc: doc, version: version}
	names := sortedKeys(paths)
	tools := []Tool{}
	seen := map[string]bool{}
	selected := map[string]bool{}
	for _, path := range names {
		item, err := importer.object(paths[path])
		if err != nil {
			return nil, fmt.Errorf("path %s: %w", path, err)
		}
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#") {
			return nil, invalid("OpenAPI 路径必须是相对路径且不能包含 query/fragment")
		}
		for _, method := range []string{"get", "head", "post", "put", "patch", "delete", "options", "trace"} {
			raw, exists := item[method]
			if !exists {
				continue
			}
			op, err := importer.object(raw)
			if err != nil {
				return nil, err
			}
			operationID, _ := op["operationId"].(string)
			if operationID == "" {
				operationID = method + "_" + strings.Trim(nonID.ReplaceAllString(path, "_"), "_")
			}
			if len(operationIDs) > 0 && !slices.Contains(operationIDs, operationID) {
				continue
			}
			selected[operationID] = true
			id := serverID + "." + operationID
			if !validID(id) || seen[id] {
				return nil, invalid("OpenAPI operationId 重复或不能作为稳定工具 ID：" + operationID)
			}
			seen[id] = true
			if method == "trace" {
				return nil, invalid("TRACE 不支持暴露为工具：" + operationID)
			}
			t, err := importer.operation(serverID, id, operationID, strings.ToUpper(method), path, item, op)
			if err != nil {
				return nil, fmt.Errorf("operation %s: %w", operationID, err)
			}
			tools = append(tools, t)
		}
	}
	for _, id := range operationIDs {
		if !selected[id] {
			return nil, invalid("未找到所选 operationId：" + id)
		}
	}
	if len(tools) > 200 {
		return nil, invalid("OpenAPI 超过 200 个操作，请选择需要的 operationId")
	}
	return tools, nil
}

var nonID = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

type openAPIImporter struct {
	doc     map[string]any
	version string
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
func (p openAPIImporter) object(v any) (map[string]any, error) {
	seen := map[string]bool{}
	for depth := 0; depth < 64; depth++ {
		obj, ok := v.(map[string]any)
		if !ok {
			return nil, invalid("OpenAPI 引用目标必须是对象")
		}
		ref, _ := obj["$ref"].(string)
		if ref == "" {
			return obj, nil
		}
		if !strings.HasPrefix(ref, "#/") || seen[ref] {
			return nil, invalid("不支持外部或循环结构引用：" + ref)
		}
		seen[ref] = true
		var current any = p.doc
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			key := strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
			m, ok := current.(map[string]any)
			if !ok {
				return nil, invalid("无法解析引用：" + ref)
			}
			current, ok = m[key]
			if !ok {
				return nil, invalid("无法解析引用：" + ref)
			}
		}
		v = current
	}
	return nil, invalid("OpenAPI 引用层数过深")
}
func (p openAPIImporter) schema(raw any) (map[string]any, error) {
	schema, ok := raw.(map[string]any)
	if !ok {
		return nil, invalid("需要对象形式的 JSON Schema")
	}
	defs := map[string]any{}
	converted, err := p.convertSchema(schema, defs, 0)
	if err != nil {
		return nil, err
	}
	out := converted.(map[string]any)
	if len(defs) > 0 {
		out["$defs"] = defs
	}
	if _, err := compileSchema(out); err != nil {
		return nil, err
	}
	return out, nil
}
func (p openAPIImporter) convertSchema(v any, defs map[string]any, depth int) (any, error) {
	if depth > 80 {
		return nil, invalid("schema 嵌套过深")
	}
	switch value := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, x := range value {
			if key == "$ref" {
				ref, ok := x.(string)
				if !ok || !strings.HasPrefix(ref, "#/components/schemas/") {
					return nil, invalid("schema 只支持本地 components/schemas 引用")
				}
				encodedName := strings.TrimPrefix(ref, "#/components/schemas/")
				name := decodePointer(encodedName)
				if strings.Contains(encodedName, "/") {
					return nil, invalid("不支持 schema 子路径引用")
				}
				if _, exists := defs[name]; !exists {
					defs[name] = map[string]any{} // Reserve recursive definitions before descent.
					target, err := p.object(map[string]any{"$ref": ref})
					if err != nil {
						return nil, err
					}
					converted, err := p.convertSchema(target, defs, depth+1)
					if err != nil {
						return nil, err
					}
					defs[name] = converted
				}
				out[key] = "#/$defs/" + encodePointer(name)
				continue
			}
			if key == "nullable" || key == "discriminator" || key == "xml" || key == "example" {
				continue
			}
			if key == "exclusiveMinimum" || key == "exclusiveMaximum" {
				if flag, ok := x.(bool); ok {
					bound := strings.TrimPrefix(key, "exclusive")
					bound = strings.ToLower(bound[:1]) + bound[1:]
					if flag {
						if number, ok := value[bound].(float64); ok {
							out[key] = number
						}
					}
					continue
				}
			}
			// Only descend through schema-valued keywords. Enum/default/example values
			// may themselves contain a literal "$ref" and must remain ordinary data.
			switch key {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
				m, ok := x.(map[string]any)
				if !ok {
					return nil, invalid("schema map 无效：" + key)
				}
				mapped := map[string]any{}
				for name, item := range m {
					next, err := p.convertSchema(item, defs, depth+1)
					if err != nil {
						return nil, err
					}
					mapped[name] = next
				}
				out[key] = mapped
			case "items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties":
				next, err := p.convertSchema(x, defs, depth+1)
				if err != nil {
					return nil, err
				}
				out[key] = next
			case "allOf", "anyOf", "oneOf", "prefixItems":
				arr, ok := x.([]any)
				if !ok {
					return nil, invalid("schema array 无效：" + key)
				}
				mapped := []any{}
				for _, item := range arr {
					next, err := p.convertSchema(item, defs, depth+1)
					if err != nil {
						return nil, err
					}
					mapped = append(mapped, next)
				}
				out[key] = mapped
			default:
				out[key] = x
			}
		}
		if nullable, _ := value["nullable"].(bool); nullable {
			out = map[string]any{"anyOf": []any{out, map[string]any{"type": "null"}}}
		}
		return out, nil
	default:
		return value, nil
	}
}
func (p openAPIImporter) operation(serverID, id, name, method, path string, item, op map[string]any) (Tool, error) {
	t := Tool{ID: id, ServerID: serverID, Name: name, Method: method, Path: path, Actors: []string{"developer"}, Tags: []string{}, ReadOnly: method == "GET" || method == "HEAD" || method == "OPTIONS", HTTP: &HTTPMapping{Parameters: []HTTPParameter{}, Responses: map[string]HTTPResponse{}}}
	summary, _ := op["summary"].(string)
	description, _ := op["description"].(string)
	t.Description = strings.TrimSpace(summary + "\n" + description)
	if t.Description == "" {
		t.Description = method + " " + path
	}
	if tags, ok := op["tags"].([]any); ok {
		for _, tag := range tags {
			if s, ok := tag.(string); ok {
				t.Tags = append(t.Tags, s)
			}
		}
	}
	if callbacks, ok := op["callbacks"].(map[string]any); ok && len(callbacks) > 0 {
		return t, invalid("callbacks 需要自定义适配器")
	}
	params := map[string]map[string]any{}
	for _, owner := range []map[string]any{item, op} {
		if owner["parameters"] == nil {
			continue
		}
		list, ok := owner["parameters"].([]any)
		if !ok {
			return t, invalid("parameters 必须是数组")
		}
		for _, raw := range list {
			param, err := p.object(raw)
			if err != nil {
				return t, err
			}
			in, _ := param["in"].(string)
			name, _ := param["name"].(string)
			params[in+":"+name] = param
		}
	}
	groups := map[string]map[string]any{}
	requiredGroups := []string{}
	for _, key := range sortedKeys(params) {
		param := params[key]
		in, _ := param["in"].(string)
		name, _ := param["name"].(string)
		if name == "" || !slices.Contains([]string{"path", "query", "header", "cookie"}, in) {
			return t, invalid("参数位置或名称无效")
		}
		if in == "header" && slices.Contains([]string{"authorization", "proxy-authorization", "host", "cookie", "connection", "content-length", "content-type"}, strings.ToLower(name)) {
			return t, invalid("认证和协议请求头请通过服务配置设置：" + name)
		}
		if param["content"] != nil || param["allowReserved"] == true {
			return t, invalid("parameter content / allowReserved 需要自定义适配器")
		}
		schema, err := p.schema(param["schema"])
		if err != nil {
			return t, err
		}
		if desc, ok := param["description"].(string); ok {
			schema["description"] = desc
		}
		style, _ := param["style"].(string)
		if style == "" {
			style = "form"
			if in == "path" || in == "header" {
				style = "simple"
			}
		}
		explode := style == "form"
		if b, ok := param["explode"].(bool); ok {
			explode = b
		}
		if !supportedParameterStyle(in, style, explode) {
			return t, invalid("不支持的参数序列化：" + in + "/" + style)
		}
		group := in
		if in == "header" {
			group = "headers"
		}
		if in == "cookie" {
			group = "cookies"
		}
		if groups[group] == nil {
			groups[group] = objectSchema(map[string]any{})
		}
		groups[group]["properties"].(map[string]any)[name] = schema
		required, _ := param["required"].(bool)
		if in == "path" && !required {
			return t, invalid("path 参数必须 required")
		}
		if required {
			groups[group]["required"] = append(groups[group]["required"].([]string), name)
			if !slices.Contains(requiredGroups, group) {
				requiredGroups = append(requiredGroups, group)
			}
		}
		t.HTTP.Parameters = append(t.HTTP.Parameters, HTTPParameter{Name: name, In: in, Style: style, Explode: explode})
	}
	for _, match := range pathVariable.FindAllStringSubmatch(path, -1) {
		if _, ok := params["path:"+match[1]]; !ok {
			return t, invalid("路径模板缺少参数定义：" + match[1])
		}
	}
	for _, param := range t.HTTP.Parameters {
		if param.In == "path" && !strings.Contains(path, "{"+param.Name+"}") {
			return t, invalid("多余的 path 参数：" + param.Name)
		}
	}
	properties := map[string]any{}
	for key, group := range groups {
		properties[key] = group
	}
	if raw := op["requestBody"]; raw != nil {
		body, err := p.object(raw)
		if err != nil {
			return t, err
		}
		content, ok := body["content"].(map[string]any)
		if !ok {
			return t, invalid("requestBody 缺少 content")
		}
		media, ok := content["application/json"].(map[string]any)
		if !ok {
			return t, invalid("目前支持 application/json 请求体；其他格式请使用适配器")
		}
		schema, err := p.schema(media["schema"])
		if err != nil {
			return t, err
		}
		properties["body"] = schema
		t.HTTP.Body = true
		if required, _ := body["required"].(bool); required {
			requiredGroups = append(requiredGroups, "body")
		}
	}
	t.InputSchema = objectSchema(properties, requiredGroups...)
	// Nested schema documents need their own $id so their local $defs references
	// resolve at the group property rather than at the combined argument root.
	rebaseDefinitions(t.InputSchema)
	responses, ok := op["responses"].(map[string]any)
	if !ok {
		return t, invalid("operation 缺少 responses")
	}
	for _, status := range sortedKeys(responses) {
		if !strings.HasPrefix(status, "2") && status != "default" {
			continue
		}
		response, err := p.object(responses[status])
		if err != nil {
			return t, err
		}
		content, _ := response["content"].(map[string]any)
		if len(content) == 0 {
			t.HTTP.Responses[status] = HTTPResponse{MediaType: "empty"}
			continue
		}
		mediaType := "application/json"
		media, ok := content[mediaType].(map[string]any)
		if !ok {
			mediaType = "text/plain"
			media, ok = content[mediaType].(map[string]any)
		}
		if !ok {
			return t, invalid("响应只支持 application/json 或 text/plain")
		}
		var schema map[string]any
		if raw, exists := media["schema"]; exists {
			schema, err = p.schema(raw)
			if err != nil {
				return t, err
			}
		}
		t.HTTP.Responses[status] = HTTPResponse{MediaType: mediaType, Schema: schema}
		if t.OutputSchema == nil && strings.HasPrefix(status, "2") {
			if mediaType == "text/plain" {
				t.OutputSchema = objectSchema(map[string]any{"text": map[string]any{"type": "string"}}, "text")
			} else {
				t.OutputSchema = schema
			}
		}
	}
	if _, err := compileSchema(t.InputSchema); err != nil {
		return t, err
	}
	return t, nil
}

// Hoist each independently resolved schema's definitions to unique root names.
func rebaseDefinitions(root map[string]any) {
	merged := map[string]any{}
	counter := 0
	var visit func(any, map[string]string)
	visit = func(v any, names map[string]string) {
		switch value := v.(type) {
		case map[string]any:
			local := names
			if defs, ok := value["$defs"].(map[string]any); ok {
				local = map[string]string{}
				for key, val := range names {
					local[key] = val
				}
				counter++
				prefix := fmt.Sprintf("s%d_", counter)
				for _, key := range sortedKeys(defs) {
					local[key] = prefix + key
				}
				delete(value, "$defs")
				for _, key := range sortedKeys(defs) {
					visit(defs[key], local)
					merged[local[key]] = defs[key]
				}
			}
			if ref, ok := value["$ref"].(string); ok && strings.HasPrefix(ref, "#/$defs/") {
				name := decodePointer(strings.TrimPrefix(ref, "#/$defs/"))
				if mapped, ok := local[name]; ok {
					value["$ref"] = "#/$defs/" + encodePointer(mapped)
				}
			}
			for _, key := range sortedKeys(value) {
				switch key {
				case "properties", "patternProperties", "definitions", "dependentSchemas":
					if fields, ok := value[key].(map[string]any); ok {
						for _, name := range sortedKeys(fields) {
							visit(fields[name], local)
						}
					}
				case "items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "allOf", "anyOf", "oneOf", "prefixItems":
					visit(value[key], local)
				}
			}
		case []any:
			for _, child := range value {
				visit(child, names)
			}
		}
	}
	visit(root, nil)
	if len(merged) > 0 {
		root["$defs"] = merged
	}
}

var pathVariable = regexp.MustCompile(`\{([^{}]+)\}`)

func supportedParameterStyle(in, style string, explode bool) bool {
	switch in {
	case "path", "header":
		return style == "simple"
	case "query":
		return style == "form" || (style == "deepObject" && explode) || ((style == "spaceDelimited" || style == "pipeDelimited") && !explode)
	case "cookie":
		return style == "form"
	}
	return false
}

func decodePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
}
func encodePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
