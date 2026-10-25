package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type ConnectionCheck struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Detail    string    `json:"detail,omitempty"`
	CheckedAt time.Time `json:"checked_at"`
	ToolCount int       `json:"tool_count,omitempty"`
	Stale     bool      `json:"stale"`
}

type SetupStatus struct {
	Sources          int                        `json:"sources"`
	EnabledTools     int                        `json:"enabled_tools"`
	ToolsReady       bool                       `json:"tools_ready"`
	ModelConfigured  bool                       `json:"model_configured"`
	ModelReady       bool                       `json:"model_ready"`
	ModelCheck       *ConnectionCheck           `json:"model_check"`
	SourceChecks     map[string]ConnectionCheck `json:"source_checks"`
	AgentLastRequest *Event                     `json:"agent_last_request"`
}

func digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode connection fingerprint: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func sourceFingerprint(s Server) (string, error) {
	values := map[string]string{}
	for k, v := range s.Headers {
		values["header:"+k] = os.ExpandEnv(v)
	}
	for k, v := range s.Env {
		values["env:"+k] = os.ExpandEnv(v)
	}
	return digest([]any{s, values})
}

func (g *Gateway) modelFingerprint() (string, error) {
	g.mu.RLock()
	settings, key := g.settings, g.apiKey
	g.mu.RUnlock()
	return digest([]any{settings, key, g.realTools()})
}

func (g *Gateway) realTools() []Tool {
	r := g.Snapshot()
	out := []Tool{}
	for _, tool := range r.Tools {
		if allowed(tool, "developer") && slices.ContainsFunc(r.Servers, func(s Server) bool { return s.ID == tool.ServerID && s.Enabled && !s.Demo }) {
			out = append(out, tool)
		}
	}
	return out
}

func checkEnvironment(s Server) error {
	for _, values := range []map[string]string{s.Headers, s.Env} {
		for _, value := range values {
			missing := []string{}
			os.Expand(value, func(key string) string {
				v := os.Getenv(key)
				if v == "" {
					missing = append(missing, key)
				}
				return v
			})
			if len(missing) > 0 {
				return fmt.Errorf("gateway process environment is missing: %s; export the variables and restart the gateway", strings.Join(missing, ", "))
			}
		}
	}
	return nil
}

func (g *Gateway) saveCheck(id, fingerprint string, check ConnectionCheck) error {
	b, err := json.Marshal(check)
	if err != nil {
		return fmt.Errorf("encode connection check: %w", err)
	}
	if _, err := g.db.Exec("INSERT INTO connection_checks(id,fingerprint,definition) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET fingerprint=excluded.fingerprint,definition=excluded.definition", id, fingerprint, string(b)); err != nil {
		return fmt.Errorf("save connection check: %w", err)
	}
	return nil
}

func (g *Gateway) loadCheck(id, fingerprint string) (*ConnectionCheck, error) {
	var stored, raw string
	if err := g.db.QueryRow("SELECT fingerprint,definition FROM connection_checks WHERE id=?", id).Scan(&stored, &raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read connection check: %w", err)
	}
	var check ConnectionCheck
	if err := json.Unmarshal([]byte(raw), &check); err != nil {
		return nil, fmt.Errorf("decode connection check: %w", err)
	}
	check.Stale = stored != fingerprint
	return &check, nil
}

func (g *Gateway) Setup() (SetupStatus, error) {
	out := SetupStatus{SourceChecks: map[string]ConnectionCheck{}, EnabledTools: len(g.realTools())}
	out.ToolsReady = out.EnabledTools > 0
	for _, s := range g.Snapshot().Servers {
		if s.Demo {
			continue
		}
		out.Sources++
		fingerprint, err := sourceFingerprint(s)
		if err != nil {
			return out, err
		}
		check, err := g.loadCheck("source:"+s.ID, fingerprint)
		if err != nil {
			return out, err
		}
		if check != nil {
			out.SourceChecks[s.ID] = *check
		}
	}
	settings, _ := g.Config()
	out.ModelConfigured = strings.TrimSpace(settings.Model) != ""
	fingerprint, err := g.modelFingerprint()
	if err != nil {
		return out, err
	}
	out.ModelCheck, err = g.loadCheck("model", fingerprint)
	if err != nil {
		return out, err
	}
	out.ModelReady = out.ModelCheck != nil && out.ModelCheck.Status == "success" && !out.ModelCheck.Stale && out.ToolsReady && settings.Mode == "model"
	var ev Event
	var stamp, detail string
	err = g.db.QueryRow("SELECT id,time,kind,actor,status,title,duration_ms,detail FROM activity WHERE kind='agent' ORDER BY time DESC LIMIT 1").Scan(&ev.ID, &stamp, &ev.Kind, &ev.Actor, &ev.Status, &ev.Title, &ev.DurationMS, &detail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("read agent connection activity: %w", err)
	}
	if err == nil {
		ev.Time, err = time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return out, fmt.Errorf("parse agent connection time: %w", err)
		}
		ev.Detail = json.RawMessage(detail)
		out.AgentLastRequest = &ev
	}
	return out, nil
}

func (g *Gateway) TestSource(ctx context.Context, s Server) (check ConnectionCheck, retErr error) {
	if err := g.SaveServer(s); err != nil {
		return check, err
	}
	fingerprint, err := sourceFingerprint(s)
	if err != nil {
		return check, err
	}
	check.CheckedAt = time.Now()
	defer func() {
		if retErr != nil {
			check.Status = "error"
			check.Message = "配置已保存，连接尚未试通"
			check.Detail = g.errorDetail(retErr)
		}
		if err := g.saveCheck("source:"+s.ID, fingerprint, check); err != nil {
			retErr = errors.Join(retErr, err)
		}
	}()
	if s.Kind == "mcp" {
		count, err := g.ImportMCP(ctx, s.ID)
		if err != nil {
			return check, err
		}
		check.Status, check.ToolCount = "success", count
		check.Message = fmt.Sprintf("MCP 已连接，读取到 %d 个工具。请选择需要开放给 Agent 的工具。", count)
		return check, nil
	}
	if err := checkEnvironment(s); err != nil {
		return check, problem(400, "请求头引用的环境变量尚未设置", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.Endpoint, nil)
	if err != nil {
		return check, fmt.Errorf("create api connectivity request: %w", err)
	}
	for k, v := range s.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	response, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return check, problem(502, "无法连接 HTTP API", err)
	}
	defer closeBody(response)
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return check, problem(502, fmt.Sprintf("服务返回 HTTP %d，请检查认证请求头", response.StatusCode), nil)
	}
	if response.StatusCode >= 500 {
		return check, problem(502, fmt.Sprintf("服务返回 HTTP %d", response.StatusCode), nil)
	}
	check.Status = "success"
	check.Message = fmt.Sprintf("已收到 HTTP %d 响应，服务地址可达。继续配置具体工具路径和参数后试调用。", response.StatusCode)
	return check, nil
}

func (g *Gateway) EnableTools(id string, ids []string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !slices.ContainsFunc(g.registry.Servers, func(s Server) bool { return s.ID == id }) {
		return invalid("服务不存在")
	}
	for _, toolID := range ids {
		if !slices.ContainsFunc(g.registry.Tools, func(t Tool) bool { return t.ID == toolID && t.ServerID == id }) {
			return invalid("工具不属于当前服务")
		}
	}
	updated := Registry{Servers: g.registry.Servers, Tools: slices.Clone(g.registry.Tools)}
	for i, t := range updated.Tools {
		if t.ServerID == id {
			updated.Tools[i].Enabled = slices.Contains(ids, t.ID)
		}
	}
	if err := g.persistRegistry(updated); err != nil {
		return err
	}
	g.registry = updated
	return nil
}

func (g *Gateway) TestModel(ctx context.Context, intent string) (DiscoverResult, error) {
	tools := g.realTools()
	if len(tools) == 0 {
		return DiscoverResult{}, invalid("请先接入并启用至少一个自己的工具，再测试模型路由")
	}
	if strings.TrimSpace(intent) == "" {
		intent = "我需要完成这个任务：" + tools[0].Description + "。请选择合适的工具。"
	}
	fingerprint, err := g.modelFingerprint()
	if err != nil {
		return DiscoverResult{}, err
	}
	result, routeErr := g.Discover(ctx, DiscoverRequest{Intent: intent, Mode: "model", Actor: "developer"})
	check := ConnectionCheck{Status: "success", Message: "模型已响应，决策 JSON 和工具 ID 校验通过", CheckedAt: time.Now()}
	if routeErr != nil {
		check.Status = "error"
		check.Message = "模型路由未试通"
		check.Detail = g.errorDetail(routeErr)
	}
	if err := g.saveCheck("model", fingerprint, check); err != nil {
		return result, errors.Join(routeErr, err)
	}
	result.Candidates = nil
	return result, routeErr
}

// Keep error details useful for local developers without echoing live credentials.
func (g *Gateway) errorDetail(err error) string {
	message := err.Error()
	g.mu.RLock()
	key := g.apiKey
	g.mu.RUnlock()
	secrets := []string{key}
	for _, s := range g.Snapshot().Servers {
		for _, v := range s.Headers {
			expanded := os.ExpandEnv(v)
			secrets = append(secrets, expanded, strings.TrimPrefix(expanded, "Bearer "))
		}
		for _, v := range s.Env {
			secrets = append(secrets, os.ExpandEnv(v))
		}
	}
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if len(message) > 4000 {
		message = message[:4000]
	}
	return message
}

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
func (g *Gateway) trackMCP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		var input struct {
			Method string `json:"method"`
			Params struct {
				ClientInfo struct {
					Name string `json:"name"`
				} `json:"clientInfo"`
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal(body, &input); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		tracked := &trackingWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		if slices.Contains([]string{"initialize", "tools/list"}, input.Method) && tracked.status == 200 {
			client := input.Params.ClientInfo.Name
			if client == "" {
				client = r.UserAgent()
			}
			if client == "" {
				client = "MCP client"
			}
			g.record("agent", "developer", "observed", input.Method, start, map[string]any{"client": client, "method": input.Method})
		}
	})
}

func (g *Gateway) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, status)
	})
	mux.HandleFunc("POST /api/setup/source", func(w http.ResponseWriter, r *http.Request) {
		var source Server
		if err := decode(w, r, &source); err != nil {
			fail(w, err)
			return
		}
		check, err := g.TestSource(r.Context(), source)
		if err != nil {
			if check.Message == "" {
				check.Message = "工具来源配置有误"
			}
			respond(w, 400, map[string]any{"error": check.Message, "detail": g.errorDetail(err)})
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
	mux.HandleFunc("POST /api/setup/model", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Settings
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
			Intent   string `json:"intent"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		input.Settings.Mode = "model"
		if err := g.SaveSettings(input.Settings, input.APIKey, input.ClearKey); err != nil {
			fail(w, err)
			return
		}
		result, err := g.TestModel(r.Context(), input.Intent)
		if err != nil {
			respond(w, 400, map[string]any{"error": "配置已保存，模型路由尚未试通", "detail": g.errorDetail(err)})
			return
		}
		respond(w, 200, result)
	})
}

func (g *Gateway) recordAgentTool(name, actor string, start time.Time, err error) {
	status := "success"
	detail := map[string]any{"method": "tools/call", "tool": name}
	if err != nil {
		status = "error"
		detail["error"] = g.errorDetail(err)
	}
	g.record("agent", actor, status, name, start, detail)
}
