package gateway

import (
	"bytes"
	"context"
	"fmt"
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
	tools, err := openapi.ImportOpenAPI(server.ID, spec, operationIDs)
	if err != nil {
		return 0, err
	}
	if err := g.registry.SaveServer(server); err != nil {
		return 0, err
	}
	for _, tool := range tools {
		if err := g.registry.SaveTool(tool); err != nil {
			return 0, fmt.Errorf("保存工具 %s 失败: %w", tool.ID, err)
		}
	}
	return len(tools), nil
}
