package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
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

func fail(w http.ResponseWriter, err error) {
	var app *appError
	if errors.As(err, &app) {
		if app.cause != nil {
			slog.Warn("gateway operation failed", "status", app.status, "error", err)
		}
		respond(w, app.status, map[string]any{"error": app.message})
		return
	}
	slog.Error("gateway operation failed", "error", err)
	respond(w, 500, map[string]string{"error": "操作失败，请查看后端日志"})
}

func decode(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		return problem(400, "请求不是有效的 JSON", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalid("请求必须只有一个 JSON 对象")
	}
	return nil
}

func toolResult(data any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode tool result: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: data}, nil
}

func (g *Gateway) mcpServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gateway-lab", Version: "0.1.0"}, nil)
	s.AddTool(&mcp.Tool{Name: "discover_tools", Description: "Describe your current task to discover a small set of authorized tools. Returns real tool IDs, schemas and usage reasons. Use invoke_tool to execute them. Rediscover as the task changes.", InputSchema: objectSchema(map[string]any{
		"intent": map[string]any{"type": "string", "description": "The capability needed now, in natural language"}, "context": map[string]any{"type": "string", "description": "Relevant known facts and previous attempts"}, "actor": map[string]any{"type": "string", "enum": []string{"developer", "observer"}, "description": "Simulated identity only"}, "mode": map[string]any{"type": "string", "enum": []string{"model", "retrieval"}},
	}, "intent")}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		var in DiscoverRequest
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return nil, fmt.Errorf("decode discovery arguments: %w", err)
		}
		if req.Extra != nil && req.Extra.Header.Get("X-Gateway-Actor") != "" {
			in.Actor = req.Extra.Header.Get("X-Gateway-Actor")
		}
		out, err := g.Discover(ctx, in)
		g.recordAgentTool("discover_tools", in.Actor, started, err)
		if err != nil {
			return nil, err
		}
		out.Candidates = nil // Keep retrieval diagnostics out of the local agent context.
		return toolResult(out)
	})
	s.AddTool(&mcp.Tool{Name: "invoke_tool", Description: "Execute a registered tool ID returned by discover_tools using arguments matching its input_schema. Authorization, enablement and schema are checked again. Optional fields projects nested response paths.", InputSchema: objectSchema(map[string]any{
		"tool_id": map[string]any{"type": "string"}, "arguments": map[string]any{"type": "object"}, "fields": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "actor": map[string]any{"type": "string", "enum": []string{"developer", "observer"}},
	}, "tool_id", "arguments")}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		started := time.Now()
		var in InvokeRequest
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return nil, fmt.Errorf("decode invocation arguments: %w", err)
		}
		if req.Extra != nil && req.Extra.Header.Get("X-Gateway-Actor") != "" {
			in.Actor = req.Extra.Header.Get("X-Gateway-Actor")
		}
		out, err := g.Invoke(ctx, in)
		g.recordAgentTool("invoke_tool", in.Actor, started, err)
		if err != nil {
			return nil, err
		}
		return toolResult(out)
	})
	return s
}

func (g *Gateway) Handler(assets fs.FS) http.Handler {
	mux := http.NewServeMux()
	g.registerSetup(mux)
	mcpServer := g.mcpServer()
	mux.Handle("/mcp", g.trackMCP(mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})))
	demo := g.demoServer()
	mux.Handle("/demo/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return demo }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	mux.HandleFunc("GET /demo/api/{name}", g.demoHTTP)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"status": "ok", "version": "0.1.0"})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		settings, key := g.Config()
		setup, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		g.mu.RLock()
		events := slices.Clone(g.events)
		g.mu.RUnlock()
		respond(w, 200, map[string]any{"registry": g.Snapshot(), "settings": settings, "key_configured": key, "events": events, "mcp_url": g.baseURL + "/mcp", "setup": setup})
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Settings
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
	mux.HandleFunc("PUT /api/servers", func(w http.ResponseWriter, r *http.Request) {
		var in Server
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
		var in Tool
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
		var in DiscoverRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.Discover(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		if r.URL.Query().Get("debug") != "1" {
			out.Candidates = nil
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("POST /api/invoke", func(w http.ResponseWriter, r *http.Request) {
		var in InvokeRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.Invoke(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		g.mu.RLock()
		events := slices.Clone(g.events)
		g.mu.RUnlock()
		respond(w, 200, events)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "API endpoint not found"})
	})
	files := http.FileServerFS(assets)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(assets, strings.TrimPrefix(r.URL.Path, "/")); err != nil && r.URL.Path != "/" {
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Debug("inspect frontend asset", "error", err)
			}
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
	protection := http.NewCrossOriginProtection()
	for _, origin := range []string{"http://127.0.0.1:5173", "http://localhost:5173"} {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			slog.Error("configure local development origin", "error", err)
		}
	}
	return protection.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		mux.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/assets/") {
			slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
		}
	}))
}
