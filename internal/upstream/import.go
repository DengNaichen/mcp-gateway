package upstream

import (
	"context"
	"encoding/json"
	"fmt"

	"gatewaylab/internal/registry"
)

// ImportTools reads and validates definitions. Enabling and persisting them is
// the caller's responsibility; freshly imported definitions remain disabled.
func (u *Service) ImportTools(ctx context.Context, server Server) ([]Tool, error) {
	id := server.ID
	session, release, err := u.Session(ctx, server)
	if err != nil {
		return nil, failure("连接 MCP 服务失败", err)
	}
	defer release()
	imported := []Tool{}
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			u.ForgetClosedSession(id, session, err)
			return nil, failure("读取下游 tools/list 失败", err)
		}
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return nil, fmt.Errorf("encode imported schema: %w", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil {
			return nil, failure("下游工具的 inputSchema 无效", err)
		}
		if _, err := registry.CompileSchema(schema); err != nil {
			return nil, failure("下游工具 schema 无法编译", err)
		}
		tool := Tool{ID: id + "." + t.Name, ServerID: id, Name: t.Name, Description: t.Description, InputSchema: schema, Tags: []string{}, Actors: []string{"developer"}}
		if t.Annotations != nil {
			tool.ReadOnly = t.Annotations.ReadOnlyHint
		}
		if !registry.ValidID(tool.ID) {
			return nil, invalid("下游工具名称组合后不是有效工具 ID")
		}
		imported = append(imported, tool)
	}
	return imported, nil
}
