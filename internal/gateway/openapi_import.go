package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/upstream"
)

// maxOpenAPIDocument bounds both pasted and downloaded specs.
const maxOpenAPIDocument = 4 << 20

// ImportOpenAPI registers an HTTP server and the tools generated from its
// OpenAPI 3.0/3.1 document. The document is spec when given, otherwise it is
// downloaded from specURL. operationIDs optionally limits the operations.
func (g *Gateway) ImportOpenAPI(ctx context.Context, server registry.Server, spec []byte, specURL string, operationIDs []string) (int, error) {
	spec = bytes.TrimSpace(spec)
	if len(spec) == 0 && strings.TrimSpace(specURL) != "" {
		fetched, err := upstream.FetchDocument(ctx, strings.TrimSpace(specURL), maxOpenAPIDocument)
		if err != nil {
			return 0, err
		}
		spec = fetched
	}
	if len(spec) == 0 {
		return 0, invalid("请提供 OpenAPI JSON/YAML 内容或文档 URL")
	}
	if len(spec) > maxOpenAPIDocument {
		return 0, invalid("OpenAPI 文档超过 4 MB 上限")
	}
	server.Kind = "http"
	if err := validateHTTPServer(server); err != nil {
		return 0, err
	}
	tools, err := openapi.ImportOpenAPI(server.ID, spec, operationIDs)
	if err != nil {
		return 0, err
	}
	for _, tool := range tools {
		if err := validateImportedTool(server.ID, tool); err != nil {
			return 0, fmt.Errorf("保存工具 %s 失败: %w", tool.ID, err)
		}
	}
	if err := g.registry.Update(func(current registry.Registry) (registry.Registry, error) {
		updated := registry.Registry{
			Servers: slices.Clone(current.Servers),
			Tools:   slices.Clone(current.Tools),
		}
		if i := slices.IndexFunc(updated.Servers, func(old registry.Server) bool { return old.ID == server.ID }); i >= 0 {
			server.Demo = updated.Servers[i].Demo
			updated.Servers[i] = server
		} else {
			updated.Servers = append(updated.Servers, server)
		}
		for _, tool := range tools {
			if i := slices.IndexFunc(updated.Tools, func(old registry.Tool) bool { return old.ID == tool.ID }); i >= 0 {
				updated.Tools[i] = tool
			} else {
				updated.Tools = append(updated.Tools, tool)
			}
		}
		return updated, nil
	}); err != nil {
		return 0, err
	}
	return len(tools), nil
}

func validateHTTPServer(s registry.Server) error {
	if !registry.ValidID(s.ID) || s.Name == "" || s.Kind != "http" {
		return &registry.ValidationError{Message: "请提供合法 ID、名称及协议类型 http/mcp"}
	}
	if s.Transport != "" && s.Transport != "http" {
		return &registry.ValidationError{Message: "支持的 MCP 传输为 http 或 stdio"}
	}
	if err := registry.ValidateURL(s.Endpoint); err != nil {
		return err
	}
	for _, value := range s.Headers {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return &registry.ValidationError{Message: "请求头的凭据请使用 ${ENV_VAR} 环境变量引用"}
		}
	}
	for _, value := range s.Env {
		if !strings.Contains(value, "${") || !strings.Contains(value, "}") {
			return &registry.ValidationError{Message: "环境变量值请使用 ${ENV_VAR} 引用，由网关启动环境提供"}
		}
	}
	return nil
}

func validateImportedTool(serverID string, t registry.Tool) error {
	if t.ServerID != serverID {
		return &registry.ValidationError{Message: "server_id 不存在"}
	}
	if err := registry.ValidateMapping(t); err != nil {
		return err
	}
	if !registry.ValidID(t.ID) || t.Name == "" || t.Description == "" || len(t.Actors) == 0 {
		return &registry.ValidationError{Message: "工具需要合法 ID、名称、描述及至少一个模拟身份"}
	}
	if _, err := registry.CompileSchema(t.InputSchema); err != nil {
		return &registry.ValidationError{Message: "input_schema 无效", Cause: err}
	}
	if _, err := json.Marshal(t); err != nil {
		return fmt.Errorf("encode tool: %w", err)
	}
	return nil
}
