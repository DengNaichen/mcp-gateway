package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/workspace"
)

type trackingWriter struct {
	http.ResponseWriter
	status int
}

func (w *trackingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *trackingWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(b)
}

func (w *trackingWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

type mcpRPCInput struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ClientInfo struct {
			Name string `json:"name"`
		} `json:"clientInfo"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"params"`
}

func rewriteMCPToolArgs(body, cleanArgs []byte) []byte {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil {
		return body
	}
	var paramsMap map[string]json.RawMessage
	if json.Unmarshal(envelope["params"], &paramsMap) != nil {
		return body
	}
	paramsMap["arguments"] = cleanArgs
	newParams, err := json.Marshal(paramsMap)
	if err != nil {
		return body
	}
	envelope["params"] = newParams
	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return body
	}
	return rewritten
}

func (g *server) observeMCPLifecycle(r *http.Request, agent string, input mcpRPCInput, start time.Time) {
	if !slices.Contains([]string{"initialize", "tools/list"}, input.Method) {
		return
	}
	client := input.Params.ClientInfo.Name
	if client == "" {
		client = agent
	}
	if client == "" {
		client = r.UserAgent()
	}
	if client == "" {
		client = "MCP client"
	}
	g.activity.Record("agent", access.RequestActor(r.Context(), "developer"), "observed", input.Method, start, map[string]any{"client": client, "agent": agent, "method": input.Method})
}

// Observe only real requests arriving at the MCP endpoint. Browser status checks
// never create an Agent connection or complete the third onboarding step.
func (g *server) trackMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("agent")))
		r.Header.Del("X-Gateway-Agent")
		if agent != "" {
			r.Header.Set("X-Gateway-Agent", agent)
		}
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			fail(w, problem(400, "MCP 请求过大或无法读取", err))
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		var input mcpRPCInput
		if err := json.Unmarshal(body, &input); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		if input.Method == "tools/call" && workspace.IsMetaTool(input.Params.Name) {
			scopedCtx, cleanArgs, scopeErr := g.scope.ResolveMCPToolCallScope(r.Context(), agent, input.Params.Name, input.Params.Arguments)
			if scopeErr != nil {
				g.recordRejectedMCPScope(r.Context(), rejectedMCPScope{
					agent: agent, toolName: input.Params.Name, rawArgs: input.Params.Arguments, start: start, err: scopeErr,
				})
				writeJSONRPCScopeError(w, input.ID, scopeErr)
				return
			}
			r = r.WithContext(scopedCtx)
			body = rewriteMCPToolArgs(body, cleanArgs)
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		tracked := &trackingWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		if tracked.status == http.StatusOK {
			g.observeMCPLifecycle(r, agent, input, start)
		}
	})
}

type rejectedMCPScope struct {
	agent    string
	toolName string
	rawArgs  []byte
	start    time.Time
	err      error
}

func (g *server) recordRejectedMCPScope(ctx context.Context, rej rejectedMCPScope) {
	actor := access.RequestActor(ctx, "")
	status := "invalid"
	var app *scope.Error
	if errors.As(rej.err, &app) {
		switch app.Code {
		case "scope_required":
			status = "missing"
		case "ambiguous_workspace":
			status = "ambiguous"
		}
	}
	evScope := scope.EventScope{Agent: rej.agent, Status: status}
	switch rej.toolName {
	case "discover_tools":
		var in struct {
			Intent string `json:"intent"`
		}
		_ = json.Unmarshal(rej.rawArgs, &in)
		title := in.Intent
		if title == "" {
			title = "discover_tools"
		}
		g.activity.Record("discover", actor, "error", title, rej.start, map[string]any{"error": g.gw.ErrorDetail(rej.err), "scope": evScope})
	case "invoke_tool":
		var in struct {
			ToolID string `json:"tool_id"`
		}
		_ = json.Unmarshal(rej.rawArgs, &in)
		title := in.ToolID
		if title == "" {
			title = "invoke_tool"
		}
		g.activity.Record("invoke", actor, "error", title, rej.start, map[string]any{"tool_id": in.ToolID, "error": g.gw.ErrorDetail(rej.err), "scope": evScope})
	}
	g.gw.RecordAgentTool(rej.toolName, actor, rej.start, rej.err)
}

func writeJSONRPCScopeError(w http.ResponseWriter, rawID json.RawMessage, err error) {
	httpStatus := http.StatusForbidden
	code := "scope_invalid"
	msg := err.Error()
	app := httpError(err)
	if app != nil {
		httpStatus = app.status
		if app.code != "" {
			code = app.code
		}
		msg = app.message
	}
	var id any
	if len(bytes.TrimSpace(rawID)) > 0 {
		id = rawID
	}
	respond(w, httpStatus, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32003,
			"message": msg,
			"data": map[string]any{
				"code":  code,
				"error": msg,
			},
		},
	})
}
