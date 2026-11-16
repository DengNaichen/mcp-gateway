package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/store"
	"gatewaylab/internal/upstream"
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
	AgentLastRequest *activity.Event            `json:"agent_last_request"`
}

func digest(v any) (string, error) {
	sum, err := jsonutil.Digest(v)
	if err != nil {
		return "", fmt.Errorf("encode connection fingerprint: %w", err)
	}
	return sum, nil
}

func (g *Gateway) modelFingerprint() (string, error) {
	settings, key := g.model.RuntimeConfig()
	return digest([]any{routingProtocol, settings, key, g.realTools()})
}

func (g *Gateway) realTools() []registry.Tool {
	r := g.Snapshot()
	out := []registry.Tool{}
	for _, tool := range r.Tools {
		if access.Allowed(tool, "developer") && slices.ContainsFunc(r.Servers, func(s registry.Server) bool { return s.ID == tool.ServerID && s.Enabled && !s.Demo }) {
			out = append(out, tool)
		}
	}
	return out
}

func (g *Gateway) saveCheck(id, fingerprint string, check ConnectionCheck) error {
	b, err := json.Marshal(check)
	if err != nil {
		return fmt.Errorf("encode connection check: %w", err)
	}
	if err := store.ConnectionChecks[json.RawMessage](g.db).Put(id, fingerprint, b); err != nil {
		return fmt.Errorf("save connection check: %w", err)
	}
	return nil
}

func (g *Gateway) loadCheck(id, fingerprint string) (*ConnectionCheck, error) {
	raw, stored, err := store.ConnectionChecks[json.RawMessage](g.db).Get(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read connection check: %w", err)
	}
	var check ConnectionCheck
	if err := json.Unmarshal(raw, &check); err != nil {
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
		fingerprint, err := upstream.Fingerprint(s, g.lookupEnv)
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
	settings, _ := g.Model().Config()
	out.ModelConfigured = strings.TrimSpace(settings.Model) != ""
	fingerprint, err := g.modelFingerprint()
	if err != nil {
		return out, err
	}
	out.ModelCheck, err = g.loadCheck("model", fingerprint)
	if err != nil {
		return out, err
	}
	out.ModelReady = out.ModelCheck != nil && out.ModelCheck.Status == "success" && !out.ModelCheck.Stale && out.ToolsReady
	latest, err := store.Activity(g.db).LatestAgent()
	if err != nil {
		return out, err
	}
	if latest != nil {
		ev := activity.Event(*latest)
		out.AgentLastRequest = &ev
	}
	return out, nil
}

func (g *Gateway) TestSource(ctx context.Context, s registry.Server) (check ConnectionCheck, retErr error) {
	if err := g.Registry().SaveServer(s); err != nil {
		return check, err
	}
	fingerprint, err := upstream.Fingerprint(s, g.lookupEnv)
	if err != nil {
		return check, err
	}
	check.CheckedAt = time.Now()
	defer func() {
		if retErr != nil {
			check.Status = "error"
			check.Message = "配置已保存，连接尚未试通"
			check.Detail = g.ErrorDetail(retErr)
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
	if err := upstream.CheckEnvironment(s, g.lookupEnv); err != nil {
		return check, fault.New(fault.Invalid, "请求头引用的环境变量尚未设置", err)
	}
	status, err := upstream.CheckHTTPConnectivity(ctx, s, g.lookupEnv)
	if err != nil {
		return check, err
	}

	check.Status = "success"
	check.Message = fmt.Sprintf("已收到 HTTP %d 响应，服务地址可达。继续配置具体工具路径和参数后试调用。", status)
	return check, nil
}

func (g *Gateway) EnableTools(id string, ids []string) error {
	return g.registry.Update(func(current registry.Registry) (registry.Registry, error) {
		if !slices.ContainsFunc(current.Servers, func(s registry.Server) bool { return s.ID == id }) {
			return registry.Registry{}, invalid("服务不存在")
		}
		for _, toolID := range ids {
			if !slices.ContainsFunc(current.Tools, func(t registry.Tool) bool { return t.ID == toolID && t.ServerID == id }) {
				return registry.Registry{}, invalid("工具不属于当前服务")
			}
		}
		updated := registry.Registry{Servers: current.Servers, Tools: slices.Clone(current.Tools)}
		for i, t := range updated.Tools {
			if t.ServerID == id {
				updated.Tools[i].Enabled = slices.Contains(ids, t.ID)
			}
		}
		return updated, nil
	})
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
	result, routeErr := g.Discover(scope.WithInternalBypass(ctx), DiscoverRequest{Intent: intent, Actor: "developer"})
	check := ConnectionCheck{Status: "success", Message: "Decisions 已响应，回答格式、概率和候选映射校验通过", CheckedAt: time.Now()}
	if routeErr != nil {
		check.Status = "error"
		check.Message = "模型路由未试通"
		check.Detail = g.ErrorDetail(routeErr)
	}
	if err := g.saveCheck("model", fingerprint, check); err != nil {
		return result, errors.Join(routeErr, err)
	}
	return result, routeErr
}

// Keep error details useful for local developers without echoing live credentials.
func (g *Gateway) ErrorDetail(err error) string {
	message := err.Error()
	_, key := g.model.RuntimeConfig()
	secrets := append([]string{key}, g.upstream.TokenSecrets()...)
	for _, s := range g.Snapshot().Servers {
		for _, v := range s.Headers {
			expanded := g.expandEnv(v)
			secrets = append(secrets, expanded, strings.TrimPrefix(expanded, "Bearer "))
		}
		for _, v := range s.Env {
			secrets = append(secrets, g.expandEnv(v))
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

func (g *Gateway) RecordRejectedMCPScope(ctx context.Context, agent, toolName string, rawArgs []byte, start time.Time, scopeErr error) {
	actor := access.RequestActor(ctx, "")
	status := "invalid"
	var app *scope.Error
	if errors.As(scopeErr, &app) {
		switch app.Code {
		case "scope_required":
			status = "missing"
		case "ambiguous_workspace":
			status = "ambiguous"
		}
	}
	evScope := scope.EventScope{Agent: agent, Status: status}
	switch toolName {
	case "discover_tools":
		var in struct {
			Intent string `json:"intent"`
		}
		_ = json.Unmarshal(rawArgs, &in)
		title := in.Intent
		if title == "" {
			title = "discover_tools"
		}
		g.record("discover", actor, "error", title, start, map[string]any{"error": g.ErrorDetail(scopeErr), "scope": evScope})
	case "invoke_tool":
		var in struct {
			ToolID string `json:"tool_id"`
		}
		_ = json.Unmarshal(rawArgs, &in)
		title := in.ToolID
		if title == "" {
			title = "invoke_tool"
		}
		g.record("invoke", actor, "error", title, start, map[string]any{"tool_id": in.ToolID, "error": g.ErrorDetail(scopeErr), "scope": evScope})
	}
	g.RecordAgentTool(toolName, actor, start, scopeErr)
}

func (g *Gateway) RecordAgentTool(name, actor string, start time.Time, err error) {
	status := "success"
	detail := map[string]any{"method": "tools/call", "tool": name}
	if err != nil {
		status = "error"
		detail["error"] = g.ErrorDetail(err)
	}
	g.record("agent", actor, status, name, start, detail)
}
