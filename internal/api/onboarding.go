package api

import (
	"bytes"
	"encoding/json"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/workspace"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
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
		var envelope map[string]json.RawMessage
		var input struct {
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
		if err := json.Unmarshal(body, &input); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		if input.Method == "tools/call" && workspace.IsMetaTool(input.Params.Name) {
			scopedCtx, cleanArgs, scopeErr := g.Scope().ResolveMCPToolCallScope(r.Context(), agent, input.Params.Name, input.Params.Arguments)
			if scopeErr != nil {
				g.RecordRejectedMCPScope(r.Context(), agent, input.Params.Name, input.Params.Arguments, start, scopeErr)
				writeJSONRPCScopeError(w, input.ID, scopeErr)
				return
			}
			r = r.WithContext(scopedCtx)
			if json.Unmarshal(body, &envelope) == nil {
				var paramsMap map[string]json.RawMessage
				if json.Unmarshal(envelope["params"], &paramsMap) == nil {
					paramsMap["arguments"] = cleanArgs
					if newParams, pErr := json.Marshal(paramsMap); pErr == nil {
						envelope["params"] = newParams
						if rewritten, mErr := json.Marshal(envelope); mErr == nil {
							body = rewritten
						}
					}
				}
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
		}
		tracked := &trackingWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		if slices.Contains([]string{"initialize", "tools/list"}, input.Method) && tracked.status == 200 {
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
			g.Activity().Record("agent", "developer", "observed", input.Method, start, map[string]any{"client": client, "agent": agent, "method": input.Method})
		}
	})
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

func (g *server) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, status)
	})
	mux.HandleFunc("POST /api/setup/source", func(w http.ResponseWriter, r *http.Request) {
		var source registry.Server
		if err := decode(w, r, &source); err != nil {
			fail(w, err)
			return
		}
		check, err := g.TestSource(r.Context(), source)
		if err != nil {
			if check.Message == "" {
				check.Message = "工具来源配置有误"
			}
			respond(w, 400, map[string]any{"error": check.Message, "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, check)
	})
	mux.HandleFunc("POST /api/servers/{id}/tools", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IDs []string `json:"ids"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if err := g.EnableTools(r.PathValue("id"), input.IDs); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("POST /api/model/ping", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			model.Settings
			ProfileID string `json:"profile_id"`
			APIKey    string `json:"api_key"`
			ClearKey  bool   `json:"clear_key"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		result, err := g.PingModel(r.Context(), input.Settings, input.ProfileID, input.APIKey, input.ClearKey)
		if err != nil {
			respond(w, 400, map[string]any{"error": "模型连接测试失败", "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, result)
	})
	mux.HandleFunc("POST /api/setup/model", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			model.Settings
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
			Intent   string `json:"intent"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveSettings(input.Settings, input.APIKey, input.ClearKey); err != nil {
			fail(w, err)
			return
		}
		result, err := g.TestModel(r.Context(), input.Intent)
		if err != nil {
			respond(w, 400, map[string]any{"error": "配置已保存，模型路由尚未试通", "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, result)
	})
}
