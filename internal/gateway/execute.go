package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
)

func (g *Gateway) ImportMCP(ctx context.Context, id string) (int, error) {
	r := g.registry.Snapshot()
	i := slices.IndexFunc(r.Servers, func(s registry.Server) bool { return s.ID == id })
	if i < 0 || r.Servers[i].Kind != "mcp" {
		return 0, invalid("请选择已注册的 MCP 服务")
	}
	timeout := 30 * time.Second
	if r.Servers[i].OAuth {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	imported, err := g.upstream.ImportTools(ctx, r.Servers[i])
	if err != nil {
		return 0, err
	}
	// Commit all imported definitions atomically. New and changed definitions
	// remain disabled until their local owner explicitly enables them again.
	err = g.registry.Update(func(current registry.Registry) (registry.Registry, error) {
		updated := registry.Registry{Servers: current.Servers, Tools: slices.Clone(current.Tools)}
		for i, t := range updated.Tools {
			if t.ServerID == id && !slices.ContainsFunc(imported, func(found registry.Tool) bool { return found.ID == t.ID }) {
				updated.Tools[i].Enabled = false
			}
		}
		for _, t := range imported {
			i := slices.IndexFunc(updated.Tools, func(old registry.Tool) bool { return old.ID == t.ID })
			if i >= 0 {
				old := updated.Tools[i]
				a, err := json.Marshal(old.InputSchema)
				if err != nil {
					return registry.Registry{}, fmt.Errorf("encode existing schema: %w", err)
				}
				b, err := json.Marshal(t.InputSchema)
				if err != nil {
					return registry.Registry{}, fmt.Errorf("encode updated schema: %w", err)
				}
				t.Enabled = old.Enabled && old.Description == t.Description && bytes.Equal(a, b)
				t.Actors, t.Tags, t.Example = old.Actors, old.Tags, old.Example
				updated.Tools[i] = t
			} else {
				updated.Tools = append(updated.Tools, t)
			}
		}
		return updated, nil
	})
	if err != nil {
		return 0, err
	}
	return len(imported), nil
}

func (g *Gateway) Invoke(ctx context.Context, req InvokeRequest) (result InvokeResult, retErr error) {
	req.Actor = access.RequestActor(ctx, req.Actor)
	started := time.Now()
	scopeEv := scope.EventForContext(ctx)
	defer func() {
		status := "success"
		detail := map[string]any{"tool_id": req.ToolID, "fields": req.Fields, "scope": scopeEv}
		if retErr != nil {
			status = "error"
			detail["error"] = g.ErrorDetail(retErr)
		}
		g.activity.Record("invoke", req.Actor, status, req.ToolID, started, detail)
	}()
	if err := g.scope.CheckExecutionScope(ctx); err != nil {
		return result, err
	}
	r := g.registry.Snapshot()
	i := slices.IndexFunc(r.Tools, func(t registry.Tool) bool { return t.ID == req.ToolID })
	if i < 0 {
		return result, fault.New(fault.NotFound, "工具不存在", nil)
	}
	t := r.Tools[i]
	j := slices.IndexFunc(r.Servers, func(s registry.Server) bool { return s.ID == t.ServerID })
	if j < 0 || !r.Servers[j].Enabled || !g.authorizedTool(ctx, t, req.Actor) {
		return result, fault.New(fault.Forbidden, "工具已禁用，或当前身份没有调用权限", nil)
	}
	s := r.Servers[j]
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	schema, err := registry.CompileSchema(t.InputSchema)
	if err != nil {
		return result, fault.New(fault.Internal, "工具 schema 无效", err)
	}
	if err := schema.Validate(req.Arguments); err != nil {
		return result, fault.New(fault.Invalid, fmt.Sprintf("参数不符合工具 schema: %v", err), nil)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var data any
	if t.Adapter != "" {
		data, err = g.callAdapter(ctx, s, t, req.Arguments)
	} else if s.Kind == "mcp" {
		data, err = g.upstream.CallMCP(ctx, s, t, req.Arguments)
	} else {
		data, err = upstream.CallHTTPWithLookup(ctx, s, t, req.Arguments, g.serverLookup(s))
	}
	if err != nil {
		if errors.Is(err, upstream.ErrInvalid) {
			return result, err
		}
		var sandboxErr *sandbox.ValidationError
		if errors.As(err, &sandboxErr) {
			return result, err
		}
		var faultErr *fault.Error
		if errors.As(err, &faultErr) {
			return result, err
		}
		return result, fault.New(fault.Upstream, "下游工具调用失败", err)
	}
	data = upstream.RedactResponseWithLookup(data, s, g.serverLookup(s))
	if len(req.Fields) > 0 {
		data = upstream.Project(data, req.Fields)
	}
	return InvokeResult{ID: newID(), ToolID: t.ID, Protocol: s.Kind, Data: data, DurationMS: time.Since(started).Milliseconds()}, nil
}
