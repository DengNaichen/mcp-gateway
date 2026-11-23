package maintenance

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
)

func maintenanceFormat(s MaintenanceSource, server Server) string {
	switch server.Kind {
	case "mcp":
		if s.Format == "" && s.Instructions == "" {
			return "native"
		}
	case "http":
	default:
		return ""
	}
	if s.Format != "" {
		return s.Format
	}
	return "pi"
}
func (m *Maintainer) generateDefinitions(r MaintenanceRun, docs []sourceDocument, dir string) error {
	var tools []Tool
	var err error
	format := maintenanceFormat(r.Source, r.Server)
	switch format {
	case "openapi":
		if len(docs) == 0 {
			return invalid("OpenAPI 需要一个参考文档")
		}
		tools, err = m.importOpenAPI(r.ServerID, []byte(docs[0].Content), r.Source.OperationIDs)
		if err != nil {
			return err
		}
	case "native":
		var raw string
		for _, doc := range docs {
			if doc.Name == "native-tools.json" {
				raw = doc.Content
			}
		}
		var definitions []struct {
			Name         string         `json:"name"`
			Description  string         `json:"description"`
			InputSchema  map[string]any `json:"inputSchema"`
			OutputSchema map[string]any `json:"outputSchema"`
			Annotations  *struct {
				ReadOnly bool `json:"readOnlyHint"`
			} `json:"annotations"`
		}
		if err := json.Unmarshal([]byte(raw), &definitions); err != nil {
			return fmt.Errorf("decode native definitions: %w", err)
		}
		tools = []Tool{}
		matched := map[string]bool{}
		for _, d := range definitions {
			if len(r.Source.OperationIDs) > 0 && !slices.Contains(r.Source.OperationIDs, d.Name) {
				continue
			}
			matched[d.Name] = true
			description := d.Description
			if description == "" {
				description = d.Name
			}
			t := Tool{ID: r.ServerID + "." + d.Name, ServerID: r.ServerID, Name: d.Name, Description: description, InputSchema: d.InputSchema, OutputSchema: d.OutputSchema, Actors: []string{"developer"}, Tags: []string{}}
			if d.Annotations != nil {
				t.ReadOnly = d.Annotations.ReadOnly
			}
			tools = append(tools, t)
		}
		for _, name := range r.Source.OperationIDs {
			if !matched[name] {
				return invalid("未找到所选 MCP 工具：" + name)
			}
		}
	default:
		return invalid("不支持的确定性导入格式")
	}
	return writeJSON(filepath.Join(dir, "output", "manifest.json"), maintenanceBundle{Summary: fmt.Sprintf("从 %s 定义同步 %d 个工具，未调用模型", format, len(tools)), Tools: tools})
}
