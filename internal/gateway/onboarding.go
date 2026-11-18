package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/model"
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

func modelConnectionFingerprint(settings model.Settings, key string) (string, error) {
	return digest([]any{
		routingProtocol,
		strings.TrimSpace(settings.BaseURL),
		strings.TrimSpace(settings.Model),
		settings.TimeoutSeconds,
		key,
	})
}

func (g *Gateway) modelFingerprint() (string, error) {
	settings, key := g.model.RuntimeConfig()
	return modelConnectionFingerprint(settings, key)
}

func modelCheckID(fingerprint string) string {
	return "model:" + fingerprint
}

func (g *Gateway) loadModelCheck(fingerprint string) (*ConnectionCheck, error) {
	check, err := g.loadCheck(modelCheckID(fingerprint), fingerprint)
	if err != nil || check != nil {
		return check, err
	}
	// Fall back to the pre-migration singleton "model" check only when its
	// stored fingerprint still matches the active model connection.
	legacy, err := g.loadCheck("model", fingerprint)
	if err != nil || legacy == nil || legacy.Stale {
		return nil, err
	}
	return legacy, nil
}

func (g *Gateway) recordModelCheck(fingerprint string, check ConnectionCheck) error {
	return g.saveCheck(modelCheckID(fingerprint), fingerprint, check)
}

func (g *Gateway) realTools() []registry.Tool {
	r := g.registry.Snapshot()
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
	for _, s := range g.registry.Snapshot().Servers {
		if s.Demo {
			continue
		}
		out.Sources++
		fingerprint, err := upstream.Fingerprint(s, g.serverLookup(s))
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
	settings, _ := g.model.Config()
	out.ModelConfigured = strings.TrimSpace(settings.Model) != ""
	fingerprint, err := g.modelFingerprint()
	if err != nil {
		return out, err
	}
	out.ModelCheck, err = g.loadModelCheck(fingerprint)
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
	if err := g.SaveServer(s); err != nil {
		return check, err
	}
	fingerprint, err := upstream.Fingerprint(s, g.serverLookup(s))
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
	if err := upstream.CheckEnvironment(s, g.serverLookup(s)); err != nil {
		return check, fault.New(fault.Invalid, "请求头引用的环境变量尚未设置", err)
	}
	status, err := upstream.CheckHTTPConnectivity(ctx, s, g.serverLookup(s))
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
	if err := g.recordModelCheck(fingerprint, check); err != nil {
		return result, errors.Join(routeErr, err)
	}
	return result, routeErr
}

// PingModel verifies model connectivity without saving settings and persists
// the passing check keyed by the model connection fingerprint.
func (g *Gateway) PingModel(ctx context.Context, s model.Settings, profileID, apiKey string, clearKey bool) (model.PingModelResult, error) {
	norm, effectiveKey, err := g.model.NormalizePingTarget(s, profileID, apiKey, clearKey)
	if err != nil {
		return model.PingModelResult{}, err
	}
	res, err := g.model.PingModel(ctx, norm, profileID, apiKey, clearKey)
	if err != nil {
		return res, err
	}
	fingerprint, err := modelConnectionFingerprint(norm, effectiveKey)
	if err != nil {
		return res, err
	}
	if err := g.recordModelCheck(fingerprint, ConnectionCheck{
		Status:    "success",
		Message:   "模型连接校验通过",
		CheckedAt: time.Now(),
	}); err != nil {
		return res, err
	}
	return res, nil
}

// VerifyModelConnection reuses a persisted passing check for the exact
// endpoint, model, timeout, and key, or performs a live ping and persists it.
func (g *Gateway) VerifyModelConnection(ctx context.Context, p model.ModelProfile, apiKey string, clearKey bool) error {
	s, err := model.ValidateModelProfile(p)
	if err != nil {
		return err
	}
	effectiveKey := g.model.ResolveEffectiveKey(p.ID, apiKey, clearKey)
	fingerprint, err := modelConnectionFingerprint(s, effectiveKey)
	if err != nil {
		return err
	}
	if check, err := g.loadModelCheck(fingerprint); err == nil && check != nil && check.Status == "success" && !check.Stale {
		return nil
	}
	if err := g.model.VerifyModelConnection(ctx, p, apiKey, clearKey); err != nil {
		return err
	}
	return g.recordModelCheck(fingerprint, ConnectionCheck{
		Status:    "success",
		Message:   "模型连接校验通过",
		CheckedAt: time.Now(),
	})
}

// SetupModelError preserves whether settings were persisted before model
// connection verification or routing check failed.
type SetupModelError struct {
	Saved bool
	Err   error
}

func (e *SetupModelError) Error() string { return e.Err.Error() }
func (e *SetupModelError) Unwrap() error { return e.Err }

// SetupModel verifies the model connection when connection parameters changed,
// persists the settings, and runs an initial routing check.
func (g *Gateway) SetupModel(ctx context.Context, settings model.Settings, apiKey string, clearKey bool, intent string) (DiscoverResult, error) {
	current, _ := g.model.Config()
	connectionChanged := strings.TrimSpace(settings.BaseURL) != strings.TrimSpace(current.BaseURL) ||
		strings.TrimSpace(settings.Model) != strings.TrimSpace(current.Model) ||
		settings.TimeoutSeconds != current.TimeoutSeconds ||
		strings.TrimSpace(apiKey) != "" ||
		clearKey
	if connectionChanged {
		prof := model.ModelProfile{
			BaseURL:        settings.BaseURL,
			Model:          settings.Model,
			MaxTools:       settings.MaxTools,
			TimeoutSeconds: settings.TimeoutSeconds,
		}
		if err := g.VerifyModelConnection(ctx, prof, apiKey, clearKey); err != nil {
			if errors.Is(err, model.ErrInvalid) {
				return DiscoverResult{}, err
			}
			return DiscoverResult{}, &SetupModelError{Saved: false, Err: err}
		}
	}
	if err := g.SaveSettings(settings, apiKey, clearKey); err != nil {
		return DiscoverResult{}, err
	}
	result, err := g.TestModel(ctx, intent)
	if err != nil {
		return DiscoverResult{}, &SetupModelError{Saved: true, Err: err}
	}
	return result, nil
}

// Keep error details useful for local developers without echoing live credentials.
func (g *Gateway) ErrorDetail(err error) string {
	message := err.Error()
	_, key := g.model.RuntimeConfig()
	secrets := append([]string{key}, g.upstream.TokenSecrets()...)
	for _, s := range g.registry.Snapshot().Servers {
		for _, v := range s.Headers {
			expanded := os.Expand(v, g.serverLookup(s))
			secrets = append(secrets, expanded, strings.TrimPrefix(expanded, "Bearer "))
		}
		for _, v := range s.Env {
			secrets = append(secrets, os.Expand(v, g.serverLookup(s)))
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

func (g *Gateway) RecordAgentTool(name, actor string, start time.Time, err error) {
	status := "success"
	detail := map[string]any{"method": "tools/call", "tool": name}
	if err != nil {
		status = "error"
		detail["error"] = g.ErrorDetail(err)
	}
	g.activity.Record("agent", actor, status, name, start, detail)
}
