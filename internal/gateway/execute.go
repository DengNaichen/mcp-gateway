package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type headerTransport struct {
	headers map[string]string
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	for k, v := range t.headers {
		r.Header.Set(k, os.ExpandEnv(v))
	}
	return t.base.RoundTrip(r)
}

func connectMCP(ctx, processContext context.Context, s Server) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-lab", Version: "0.1.0"}, nil)
	if err := checkEnvironment(s); err != nil {
		return nil, err
	}
	if s.Transport == "stdio" {
		command := exec.CommandContext(processContext, s.Command, s.Args...)
		command.Dir = s.WorkDir
		command.Env = processEnvironment(nil)
		for key, value := range s.Env {
			command.Env = append(command.Env, key+"="+os.ExpandEnv(value))
		}
		command.Stderr = os.Stderr
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
		if err != nil {
			return nil, fmt.Errorf("start stdio mcp server: %w", err)
		}
		return session, nil
	}
	httpClient := &http.Client{Transport: headerTransport{headers: s.Headers, base: http.DefaultTransport}, CheckRedirect: noRedirect}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: s.Endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect native mcp server: %w", err)
	}
	return session, nil
}

func closeSession(session *mcp.ClientSession) {
	if err := session.Close(); err != nil {
		slog.Debug("close mcp session", "error", err)
	}
}

type stdioSession struct {
	fingerprint string
	session     *mcp.ClientSession
	cancel      context.CancelFunc
}

// Keep a local process alive across discovery and calls so stateful tools can
// preserve their session. Reconfiguration or gateway shutdown closes it.
func (g *Gateway) mcpSession(ctx context.Context, s Server) (*mcp.ClientSession, func(), error) {
	if s.Transport != "stdio" {
		session, err := connectMCP(ctx, ctx, s)
		if err != nil {
			return nil, nil, err
		}
		return session, func() { closeSession(session) }, nil
	}
	fingerprint, err := sourceFingerprint(s)
	if err != nil {
		return nil, nil, err
	}
	g.sessionMu.Lock()
	defer g.sessionMu.Unlock()
	if g.sessions == nil {
		g.sessions = map[string]stdioSession{}
	}
	if current, ok := g.sessions[s.ID]; ok {
		if current.fingerprint == fingerprint {
			return current.session, func() {}, nil
		}
		closeSession(current.session)
		current.cancel()
		delete(g.sessions, s.ID)
	}
	processContext, cancel := context.WithCancel(context.Background())
	session, err := connectMCP(ctx, processContext, s)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	g.sessions[s.ID] = stdioSession{fingerprint, session, cancel}
	return session, func() {}, nil
}

func (g *Gateway) forgetClosedSession(id string, session *mcp.ClientSession, err error) {
	if !errors.Is(err, mcp.ErrConnectionClosed) {
		return
	}
	g.sessionMu.Lock()
	defer g.sessionMu.Unlock()
	if current, ok := g.sessions[id]; ok && current.session == session {
		closeSession(current.session)
		current.cancel()
		delete(g.sessions, id)
	}
}

func (g *Gateway) closeDownstreams() error {
	g.sessionMu.Lock()
	defer g.sessionMu.Unlock()
	var result error
	for id, current := range g.sessions {
		if err := current.session.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close stdio server %s: %w", id, err))
		}
		current.cancel()
		delete(g.sessions, id)
	}
	return result
}

func (g *Gateway) ImportMCP(ctx context.Context, id string) (int, error) {
	r := g.Snapshot()
	i := slices.IndexFunc(r.Servers, func(s Server) bool { return s.ID == id })
	if i < 0 || r.Servers[i].Kind != "mcp" {
		return 0, invalid("请选择已注册的 MCP 服务")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, release, err := g.mcpSession(ctx, r.Servers[i])
	if err != nil {
		return 0, problem(502, "连接 MCP 服务失败", err)
	}
	defer release()
	imported := []Tool{}
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			g.forgetClosedSession(id, session, err)
			return 0, problem(502, "读取下游 tools/list 失败", err)
		}
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return 0, fmt.Errorf("encode imported schema: %w", err)
		}
		var schema map[string]any
		if err := json.Unmarshal(b, &schema); err != nil {
			return 0, problem(502, "下游工具的 inputSchema 无效", err)
		}
		if _, err := compileSchema(schema); err != nil {
			return 0, problem(502, "下游工具 schema 无法编译", err)
		}
		tool := Tool{ID: id + "." + t.Name, ServerID: id, Name: t.Name, Description: t.Description, InputSchema: schema, Tags: []string{}, Actors: []string{"developer"}}
		if t.Annotations != nil {
			tool.ReadOnly = t.Annotations.ReadOnlyHint
		}
		if !validID(tool.ID) {
			return 0, invalid("下游工具名称组合后不是有效工具 ID")
		}
		imported = append(imported, tool)
	}
	// Commit all imported definitions atomically. New and changed definitions
	// remain disabled until their local owner explicitly enables them again.
	g.mu.Lock()
	defer g.mu.Unlock()
	updated := Registry{Servers: g.registry.Servers, Tools: slices.Clone(g.registry.Tools)}
	for i, t := range updated.Tools {
		if t.ServerID == id && !slices.ContainsFunc(imported, func(found Tool) bool { return found.ID == t.ID }) {
			updated.Tools[i].Enabled = false
		}
	}
	for _, t := range imported {
		i := slices.IndexFunc(updated.Tools, func(old Tool) bool { return old.ID == t.ID })
		if i >= 0 {
			old := updated.Tools[i]
			a, err := json.Marshal(old.InputSchema)
			if err != nil {
				return 0, fmt.Errorf("encode existing schema: %w", err)
			}
			b, err := json.Marshal(t.InputSchema)
			if err != nil {
				return 0, fmt.Errorf("encode updated schema: %w", err)
			}
			t.Enabled = old.Enabled && old.Description == t.Description && bytes.Equal(a, b)
			t.Actors, t.Tags, t.Example = old.Actors, old.Tags, old.Example
			updated.Tools[i] = t
		} else {
			updated.Tools = append(updated.Tools, t)
		}
	}
	if err := g.persistRegistry(updated); err != nil {
		return 0, err
	}
	g.registry = updated
	return len(imported), nil
}

func (g *Gateway) Invoke(ctx context.Context, req InvokeRequest) (result InvokeResult, retErr error) {
	req.Actor = requestActor(ctx, req.Actor)
	started := time.Now()
	defer func() {
		status := "success"
		detail := map[string]any{"tool_id": req.ToolID, "fields": req.Fields}
		if retErr != nil {
			status = "error"
			detail["error"] = g.errorDetail(retErr)
		}
		g.record("invoke", req.Actor, status, req.ToolID, started, detail)
	}()
	r := g.Snapshot()
	i := slices.IndexFunc(r.Tools, func(t Tool) bool { return t.ID == req.ToolID })
	if i < 0 {
		return result, problem(404, "工具不存在", nil)
	}
	t := r.Tools[i]
	j := slices.IndexFunc(r.Servers, func(s Server) bool { return s.ID == t.ServerID })
	if j < 0 || !r.Servers[j].Enabled || !g.authorizedTool(ctx, t, req.Actor) {
		return result, problem(403, "工具已禁用，或当前身份没有调用权限", nil)
	}
	s := r.Servers[j]
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	schema, err := compileSchema(t.InputSchema)
	if err != nil {
		return result, problem(500, "工具 schema 无效", err)
	}
	if err := schema.Validate(req.Arguments); err != nil {
		return result, problem(400, fmt.Sprintf("参数不符合工具 schema: %v", err), nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var data any
	if t.Adapter != "" {
		data, err = g.callAdapter(ctx, s, t, req.Arguments)
	} else if s.Kind == "mcp" {
		data, err = g.callMCP(ctx, s, t, req.Arguments)
	} else {
		data, err = callHTTP(ctx, s, t, req.Arguments)
	}
	if err != nil {
		return result, problem(502, "下游工具调用失败", err)
	}
	data = redactResponse(data, s)
	if len(req.Fields) > 0 {
		data = project(data, req.Fields)
	}
	return InvokeResult{ID: newID(), ToolID: t.ID, Protocol: s.Kind, Data: data, DurationMS: time.Since(started).Milliseconds()}, nil
}

func callHTTP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	if t.HTTP != nil {
		return callMappedHTTP(ctx, s, t, args)
	}
	if err := checkEnvironment(s); err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(s.Endpoint, "/") + "/" + strings.TrimLeft(t.Path, "/")
	method := t.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodGet || method == http.MethodDelete {
		u, err := url.Parse(endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse tool endpoint: %w", err)
		}
		q := u.Query()
		for k, v := range args {
			if text, ok := v.(string); ok {
				q.Set(k, text)
			} else {
				b, err := json.Marshal(v)
				if err != nil {
					return nil, fmt.Errorf("encode query argument: %w", err)
				}
				q.Set(k, string(b))
			}
		}
		u.RawQuery = q.Encode()
		endpoint = u.String()
	} else {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("encode tool arguments: %w", err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create downstream request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.Headers {
		req.Header.Set(k, os.ExpandEnv(v))
	}
	resp, err := (&http.Client{CheckRedirect: noRedirect}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("send downstream request: %w", err)
	}
	defer closeBody(resp)
	b, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read downstream response: %w", err)
	}
	if len(b) > 8<<20 {
		return nil, fmt.Errorf("downstream response exceeds 8 mb")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downstream returned http %d", resp.StatusCode)
	}
	var data any
	if err := json.Unmarshal(b, &data); err != nil {
		return map[string]any{"text": string(b)}, nil
	}
	return data, nil
}

func (g *Gateway) callMCP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	session, release, err := g.mcpSession(ctx, s)
	if err != nil {
		return nil, err
	}
	defer release()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: t.Name, Arguments: args})
	if err != nil {
		g.forgetClosedSession(s.ID, session, err)
		return nil, fmt.Errorf("call native mcp tool: %w", err)
	}
	if result.IsError {
		messages := []string{}
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				messages = append(messages, text.Text)
			}
		}
		message := strings.Join(messages, "\n")
		if len(message) > 4000 {
			message = message[:4000]
		}
		return nil, fmt.Errorf("native mcp tool returned an execution error: %s", message)
	}
	if result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	if len(result.Content) == 1 {
		if content, ok := result.Content[0].(*mcp.TextContent); ok {
			var data any
			if err := json.Unmarshal([]byte(content.Text), &data); err == nil {
				return data, nil
			}
			return map[string]any{"text": content.Text}, nil
		}
	}
	return map[string]any{"content": result.Content}, nil
}

// project applies nested field paths to objects and each item of an array.
// It is a response-size feature, not a substitute for authorization/redaction.
func project(value any, fields []string) any {
	if arr, ok := value.([]any); ok {
		out := make([]any, len(arr))
		for i, v := range arr {
			out[i] = project(v, fields)
		}
		return out
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return value
	}
	groups := map[string][]string{}
	for _, field := range fields {
		head, tail, found := strings.Cut(strings.TrimSpace(field), ".")
		if head == "" {
			continue
		}
		if !found {
			groups[head] = append(groups[head], "")
		} else {
			groups[head] = append(groups[head], tail)
		}
	}
	out := map[string]any{}
	for head, children := range groups {
		if v, ok := obj[head]; ok {
			if slices.Contains(children, "") {
				out[head] = v
			} else {
				out[head] = project(v, children)
			}
		}
	}
	return out
}
