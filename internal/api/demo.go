package api

import (
	"context"
	"encoding/json"
	"fmt"
	"gatewaylab/internal/demo"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (g *server) demoServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "observability-demo", Version: "0.1.0"}, nil)
	for _, tool := range demo.Registry(g.BaseURL()).Tools {
		if tool.ServerID != "observability" {
			continue
		}
		s.AddTool(&mcp.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: tool.ReadOnly}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]any{}
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return nil, fmt.Errorf("decode demo arguments: %w", err)
			}
			return toolResult(demo.Data(tool.Name, args))
		})
	}
	return s
}

func (g *server) demoHTTP(w http.ResponseWriter, r *http.Request) {
	names := map[string]string{"services": "list_services", "owner": "get_service_owner", "runbooks": "search_runbooks"}
	name, ok := names[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	args := map[string]any{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			args[k] = v[0]
		}
	}
	respond(w, 200, demo.Data(name, args))
}
