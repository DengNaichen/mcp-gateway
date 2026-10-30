package gateway

import "slices"

type ReviewReason struct {
	ToolID  string `json:"tool_id"`
	Code    string `json:"code"`
	Message string `json:"message"`
}
type PublicationReview struct {
	Required bool           `json:"required"`
	Reasons  []ReviewReason `json:"reasons"`
}

func publicationReview(before, after []Tool) PublicationReview {
	review := PublicationReview{Reasons: []ReviewReason{}}
	add := func(id, code, message string) {
		review.Required = true
		review.Reasons = append(review.Reasons, ReviewReason{id, code, message})
	}
	for _, tool := range after {
		i := slices.IndexFunc(before, func(old Tool) bool { return old.ID == tool.ID })
		if i < 0 {
			add(tool.ID, "new_capability", "新增能力，需要负责人确认启用")
			continue
		}
		old := before[i]
		if old.ReadOnly && !tool.ReadOnly {
			add(tool.ID, "write_expansion", "只读操作变为写操作")
		}
		if !inputCompatible(old.InputSchema, tool.InputSchema) {
			add(tool.ID, "input_breaking", "输入 schema 不兼容或无法证明兼容")
		}
		if maintenanceDigest(schemaSemantics(old.OutputSchema)) != maintenanceDigest(schemaSemantics(tool.OutputSchema)) {
			add(tool.ID, "output_changed", "输出 schema 变化，需要检查调用方影响")
		}
		for _, actor := range tool.Actors {
			if !slices.Contains(old.Actors, actor) {
				add(tool.ID, "permission_expansion", "工具访问身份扩大")
				break
			}
		}
		if tool.Enabled && !old.Enabled {
			add(tool.ID, "enablement", "启用之前禁用的能力")
		}
		if old.Method != tool.Method || old.Path != tool.Path || old.Name != tool.Name {
			add(tool.ID, "destination_changed", "操作名、方法或请求路径变化")
		}
		if (old.Adapter == "") != (tool.Adapter == "") || (old.HTTP == nil) != (tool.HTTP == nil) {
			add(tool.ID, "execution_changed", "工具执行方式变化")
		}
		if old.HTTP != nil && tool.HTTP != nil && maintenanceDigest(httpContract(old.HTTP)) != maintenanceDigest(httpContract(tool.HTTP)) {
			add(tool.ID, "http_contract_changed", "HTTP 参数编码或状态响应契约变化，需要检查兼容性")
		}
	}
	for _, old := range before {
		if !slices.ContainsFunc(after, func(t Tool) bool { return t.ID == old.ID }) {
			add(old.ID, "removed", "工具移除，现有调用方可能受影响")
		}
	}
	return review
}

func httpContract(mapping *HTTPMapping) any {
	responses := map[string]any{}
	for status, response := range mapping.Responses {
		responses[status] = []any{response.MediaType, schemaSemantics(response.Schema)}
	}
	return []any{mapping.Parameters, mapping.Body, responses}
}

// Compatibility is intentionally conservative: prove equivalent contracts or an
// optional-property extension; otherwise require review instead of guessing.
func inputCompatible(old, new map[string]any) bool {
	a, _ := schemaSemantics(old).(map[string]any)
	b, _ := schemaSemantics(new).(map[string]any)
	if maintenanceDigest(a) == maintenanceDigest(b) {
		return true
	}
	if a["type"] != "object" || b["type"] != "object" || a["additionalProperties"] != false || b["additionalProperties"] != false {
		return false
	}
	oldProps, _ := a["properties"].(map[string]any)
	newProps, _ := b["properties"].(map[string]any)
	oldRequired := jsonStringList(a["required"])
	newRequired := jsonStringList(b["required"])
	for _, name := range newRequired {
		if !slices.Contains(oldRequired, name) {
			return false
		}
	}
	for name, value := range oldProps {
		next, ok := newProps[name]
		if !ok {
			return false
		}
		av, ok := value.(map[string]any)
		if !ok {
			return false
		}
		bv, ok := next.(map[string]any)
		if !ok || !inputCompatible(av, bv) {
			return false
		}
	}
	delete(a, "properties")
	delete(b, "properties")
	delete(a, "required")
	delete(b, "required")
	return maintenanceDigest(a) == maintenanceDigest(b)
}
func jsonStringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := []string{}
		for _, value := range x {
			if s, ok := value.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
func schemaSemantics(v any) any {
	x, ok := v.(map[string]any)
	if !ok {
		return v
	}
	out := map[string]any{}
	for key, value := range x {
		if slices.Contains([]string{"description", "title", "examples", "example", "$comment"}, key) {
			continue
		}
		switch key {
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			if fields, ok := value.(map[string]any); ok {
				mapped := map[string]any{}
				for name, child := range fields {
					mapped[name] = schemaSemantics(child)
				}
				out[key] = mapped
			} else {
				out[key] = value
			}
		case "items", "additionalProperties", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties":
			out[key] = schemaSemantics(value)
		case "allOf", "anyOf", "oneOf", "prefixItems":
			if values, ok := value.([]any); ok {
				mapped := make([]any, len(values))
				for i, child := range values {
					mapped[i] = schemaSemantics(child)
				}
				out[key] = mapped
			} else {
				out[key] = value
			}
		default:
			out[key] = value
		}
	}
	return out
}

// Executable compatibility cannot be proved from a matching JSON schema alone.
func (m *Maintainer) reviewArtifacts(r MaintenanceRun, review *PublicationReview) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tool := range r.Tools {
		if tool.Adapter == "" {
			continue
		}
		index := slices.IndexFunc(r.Before, func(old Tool) bool { return old.ID == tool.ID })
		if index < 0 || r.Before[index].Adapter == "" {
			continue
		}
		old := r.Before[index]
		prior := m.runs[old.Revision]
		if old.Adapter != tool.Adapter || prior.Artifacts[old.Adapter] == "" || prior.Artifacts[old.Adapter] != r.Artifacts[tool.Adapter] {
			review.Required = true
			review.Reasons = append(review.Reasons, ReviewReason{ToolID: tool.ID, Code: "adapter_code_changed", Message: "适配器代码变化，需确认实际行为与读写范围"})
		}
	}
}
