package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gatewaylab/internal/access"
	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/market"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func respond(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Debug("write http response", "error", err)
	}
}

func decodeRaw(w http.ResponseWriter, r *http.Request, out any) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, problem(400, "请求过大或无法读取", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(out); err != nil {
		return raw, problem(400, "请求不是有效的 JSON", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return raw, invalid("请求必须只有一个 JSON 对象")
	}
	return raw, nil
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	_, err := decodeRaw(w, r, out)
	return err
}

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

func toolResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: data}, nil
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
		return toolResult(discoveryPresentation(out, in.SummaryOnly))
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
		return toolResult(tool)
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
		return toolResult(out)
	})
	return s
}

func (g *server) handler() http.Handler {
	mux := http.NewServeMux()
	g.registerSetup(mux)
	g.registerMaintenance(mux)
	g.registerCapabilities(mux)
	g.registerSourceEvents(mux)
	g.registerAccess(mux)
	g.registerScopes(mux)
	mcpServer := g.mcpServer()
	mux.Handle("/mcp", g.trackMCP(mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})))
	demo := g.demoServer()
	mux.Handle("/demo/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return demo }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	mux.HandleFunc("GET /demo/api/{name}", g.demoHTTP)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"status": "ok", "version": buildinfo.Current().Version, "channel": buildinfo.Current().Channel, "application": buildinfo.Current().Identifier, "instance_id": g.InstanceID(), "data_identity": buildinfo.DataIdentity(g.DataDir())})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.Config()
		setup, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		events := g.Activity().Recent()
		respond(w, 200, map[string]any{"registry": g.Snapshot(), "settings": settings, "key_configured": key, "profiles": g.ModelProfiles(), "events": events, "mcp_url": g.BaseURL() + "/mcp", "setup": setup, "scopes": g.ScopeOverview(), "access": map[string]any{"shared": g.Access() != nil, "actor": access.RequestActor(r.Context(), "")}})
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			model.Settings
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveSettings(in.Settings, in.APIKey, in.ClearKey); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("PUT /api/model-profiles", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			model.ModelProfile
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		saved, err := g.SaveModelProfile(in.ModelProfile, in.APIKey, in.ClearKey)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, saved)
	})
	mux.HandleFunc("POST /api/model-profiles/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		activated, err := g.ActivateModelProfile(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, activated)
	})
	mux.HandleFunc("DELETE /api/model-profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.DeleteModelProfile(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"deleted": true})
	})
	mux.HandleFunc("GET /api/market", func(w http.ResponseWriter, r *http.Request) {
		res := g.Market().MarketServers(r.Context(), r.URL.Query().Get("q"))
		respond(w, 200, res)
	})
	mux.HandleFunc("GET /api/market/icon", func(w http.ResponseWriter, r *http.Request) {
		data, ct, err := g.Market().MarketIcon(r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("url"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("POST /api/market/install", func(w http.ResponseWriter, r *http.Request) {
		var in market.InstallMarketRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.Market().InstallMarketServer(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("PUT /api/servers", func(w http.ResponseWriter, r *http.Request) {
		var in registry.Server
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveServer(in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
	mux.HandleFunc("POST /api/servers/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		n, err := g.ImportMCP(r.Context(), r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]int{"imported": n})
	})
	mux.HandleFunc("PUT /api/tools", func(w http.ResponseWriter, r *http.Request) {
		var in registry.Tool
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveTool(in); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, in)
	})
	mux.HandleFunc("POST /api/discover", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			gateway.DiscoverRequest
			SimScope *scope.SimScope `json:"sim_scope,omitempty"`
		}
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "discover_tools", rawBody, in.SimScope)
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Discover(scopedCtx, in.DiscoverRequest)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, discoveryPresentation(out, in.SummaryOnly))
	})
	mux.HandleFunc("POST /api/invoke", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			gateway.InvokeRequest
			SimScope *scope.SimScope `json:"sim_scope,omitempty"`
		}
		rawBody, err := decodeRaw(w, r, &in)
		if err != nil {
			fail(w, err)
			return
		}
		scopedCtx, err := g.resolveRESTScope(r.Context(), r, "invoke_tool", rawBody, in.SimScope)
		if err != nil {
			fail(w, err)
			return
		}
		out, err := g.Invoke(scopedCtx, in.InvokeRequest)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		events := g.Activity().Recent()
		respond(w, 200, events)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "API endpoint not found"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "not found", "mcp": "/mcp", "health": "/api/health"})
	})
	protection := http.NewCrossOriginProtection()
	origins := []string{}
	if configured := os.Getenv("GATEWAY_TRUSTED_ORIGINS"); configured != "" {
		origins = append(origins, strings.Split(configured, ",")...)
	}
	for _, origin := range origins {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			slog.Error("configure trusted origin", "error", err)
		}
	}
	return protection.Handler(g.protectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		mux.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})))
}
