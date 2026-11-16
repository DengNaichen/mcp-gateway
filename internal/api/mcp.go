package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/access"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/mcputil"
	"gatewaylab/internal/scope"
)

func metaToolSchema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": true}
}

func mcpHeader(req *mcp.CallToolRequest, name string) string {
	if req != nil && req.Extra != nil {
		return req.Extra.Header.Get(name)
	}
	return ""
}

func (g *server) ensureMCPCallScope(ctx context.Context, req *mcp.CallToolRequest, toolName string) (context.Context, []byte, error) {
	raw := []byte(req.Params.Arguments)
	if _, ok := scope.RequestScopeFrom(ctx); ok {
		return ctx, raw, nil
	}
	return g.Scope().ResolveMCPToolCallScope(ctx, mcpHeader(req, "X-Gateway-Agent"), toolName, raw)
}

func (g *server) mcpServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gateway-lab", Version: "0.0.1"}, nil)
	s.AddTool(&mcp.Tool{Name: "discover_tools", Description: "Describe your current task to discover a small set of authorized tools. Returns real tool IDs, schemas and usage reasons. Use invoke_tool to execute them. Rediscover as the task changes.", InputSchema: metaToolSchema(map[string]any{
		"summary_only": map[string]any{"type": "boolean", "description": "Return capability summaries; use get_tool_schema for the selected tool"},
		"intent":       map[string]any{"type": "string", "description": "The capability needed now, in natural language"}, "context": map[string]any{"type": "string", "description": "Relevant known facts and previous attempts"}, "actor": map[string]any{"type": "string", "enum": []string{"developer", "observer"}, "description": "Simulated identity only"},
		"services": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Service IDs to search within when the task names a service (for example [\"github\"]); omit to search every service"},
	}, "intent")}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		scopedCtx, cleanArgs, scopeErr := g.ensureMCPCallScope(ctx, req, "discover_tools")
		if scopeErr != nil {
			g.RecordAgentTool("discover_tools", access.RequestActor(ctx, mcpHeader(req, "X-Gateway-Actor")), started, scopeErr)
			return nil, scopeErr
		}
		var in gateway.DiscoverRequest
		if err := json.Unmarshal(cleanArgs, &in); err != nil {
			return nil, fmt.Errorf("decode discovery arguments: %w", err)
		}
		if hdrActor := mcpHeader(req, "X-Gateway-Actor"); hdrActor != "" {
			in.Actor = hdrActor
		}
		out, err := g.Discover(scopedCtx, in)
		g.RecordAgentTool("discover_tools", access.RequestActor(scopedCtx, in.Actor), started, err)
		if err != nil {
			return nil, err
		}
		return mcputil.Result(discoveryPresentation(out, in.SummaryOnly))
	})
	s.AddTool(&mcp.Tool{Name: "get_tool_schema", Description: "Read the current schema of an enabled authorized tool after summary discovery.", InputSchema: metaToolSchema(map[string]any{"tool_id": map[string]any{"type": "string"}, "actor": map[string]any{"type": "string"}}, "tool_id")}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		scopedCtx, cleanArgs, scopeErr := g.ensureMCPCallScope(ctx, req, "get_tool_schema")
		if scopeErr != nil {
			return nil, scopeErr
		}
		var in struct {
			ToolID string `json:"tool_id"`
			Actor  string `json:"actor"`
		}
		if err := json.Unmarshal(cleanArgs, &in); err != nil {
			return nil, invalid("schema 请求参数无效")
		}
		if hdrActor := mcpHeader(req, "X-Gateway-Actor"); hdrActor != "" {
			in.Actor = hdrActor
		}
		tool, err := g.ToolSchema(scopedCtx, in.ToolID, in.Actor)
		if err != nil {
			return nil, err
		}
		return mcputil.Result(tool)
	})
	s.AddTool(&mcp.Tool{Name: "invoke_tool", Description: "Execute a registered tool ID returned by discover_tools using arguments matching its input_schema. Authorization, enablement and schema are checked again. Optional fields projects nested response paths.", InputSchema: metaToolSchema(map[string]any{
		"tool_id": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}, "fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "actor": map[string]any{"type": "string", "enum": []string{"developer", "observer"}},
	}, "tool_id", "arguments")}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		scopedCtx, cleanArgs, scopeErr := g.ensureMCPCallScope(ctx, req, "invoke_tool")
		if scopeErr != nil {
			g.RecordAgentTool("invoke_tool", access.RequestActor(ctx, mcpHeader(req, "X-Gateway-Actor")), started, scopeErr)
			return nil, scopeErr
		}
		var in gateway.InvokeRequest
		if err := json.Unmarshal(cleanArgs, &in); err != nil {
			return nil, fmt.Errorf("decode invocation arguments: %w", err)
		}
		if hdrActor := mcpHeader(req, "X-Gateway-Actor"); hdrActor != "" {
			in.Actor = hdrActor
		}
		out, err := g.Invoke(scopedCtx, in)
		g.RecordAgentTool("invoke_tool", access.RequestActor(scopedCtx, in.Actor), started, err)
		if err != nil {
			return nil, err
		}
		return mcputil.Result(out)
	})
	return s
}
