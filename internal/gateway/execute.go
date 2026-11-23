package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/cliexec"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
	"gatewaylab/internal/workspace"
)

func (g *Gateway) ImportMCP(ctx context.Context, id string) (int, error) {
	r := g.registry.Snapshot()
	i := slices.IndexFunc(r.Servers, func(s registry.Server) bool { return s.ID == id })
	if i < 0 {
		return 0, invalid("请选择已注册的 MCP 服务")
	}
	switch r.Servers[i].Kind {
	case "mcp":
	case "http", "cli":
		return 0, invalid("仅 MCP 服务支持工具同步")
	default:
		return 0, invalid("未知服务协议")
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
		if !slices.ContainsFunc(current.Servers, func(s registry.Server) bool {
			return s.ID == id && s.Kind == "mcp"
		}) {
			return registry.Registry{}, invalid("服务已删除或不再是 MCP 服务")
		}
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
	if req.SkillID != "" && !registry.ValidID(req.SkillID) {
		return result, invalid("Invalid skill ID")
	}
	audit := activity.AuditMeta{RepoID: scopeEv.RepoID, Action: req.ToolID, SkillID: req.SkillID}
	if req.CLIServiceID != "" {
		audit.ServiceID, audit.Protocol = req.CLIServiceID, "cli"
	}
	defer func() {
		status := "success"
		detail := map[string]any{"tool_id": req.ToolID, "fields": req.Fields, "scope": scopeEv}
		if audit.SkillID != "" {
			detail["skill_trust"] = "declared"
		}
		if result.Approval != nil {
			status = "pending"
			audit.ApprovalID = result.Approval.ApprovalID
		}
		if retErr != nil {
			status = "error"
			detail["error"] = invokeAuditError(retErr)
		}
		g.activity.RecordAudit("invoke", req.Actor, status, req.ToolID, started, audit, detail)
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
	if req.CLIServiceID != "" && (j < 0 || t.ServerID != req.CLIServiceID || r.Servers[j].Kind != "cli") {
		return result, fault.WithCode(fault.New(fault.NotFound, "No matching controlled CLI service", nil), "cli_service_not_found")
	}
	audit.ServiceID = t.ServerID
	available := j >= 0 && r.Servers[j].Enabled
	if j >= 0 {
		audit.Protocol = r.Servers[j].Kind
	}
	t.Enabled = t.Enabled && available
	decision := g.authorizeWithSkill(ctx, t, req.Actor, req.SkillID)
	audit.Decision, audit.RuleID = decision.Effect, decision.RuleID
	if j < 0 || (req.ApprovalID == "" && (decision.Effect == policy.EffectDeny || !available)) {
		return result, fault.WithCode(fault.New(fault.Forbidden, "工具已禁用，或当前身份没有调用权限", nil), "policy_denied")
	}
	needsApproval := decision.Effect == policy.EffectApprove || req.ApprovalID != ""
	if needsApproval && g.localAdmin == nil && g.access == nil {
		return result, fault.WithCode(fault.New(fault.Forbidden, "此调用需要审批，当前网关尚未启用审批", nil), "approval_required_not_enabled")
	}
	s := r.Servers[j]
	switch s.Kind {
	case "cli", "mcp", "http":
	default:
		return result, fault.WithCode(fault.New(fault.Invalid, "Unknown service protocol", nil), "service_kind_invalid")
	}
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	var hash string
	var err error
	if needsApproval {
		// Channel metadata is never upstream input or part of an approval.
		req.Arguments = maps.Clone(req.Arguments)
		maps.DeleteFunc(req.Arguments, func(key string, _ any) bool { return workspace.IsReservedContextField(key) })
		hash, err = approvalHash(req, scopeEv, t, s)
		if err != nil {
			return result, err
		}
	}
	if req.ApprovalID != "" {
		out, lease, err := g.retryApproval(ctx, req, t, s, hash, &audit)
		if err != nil || out.Approval != nil {
			return out, err
		}
		defer func() {
			if err := g.storage.MarkConsumed(req.ApprovalID, lease, time.Now()); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("finalize approval: %w", err))
			}
		}()
	}
	cliAction := ""
	if s.Kind == "cli" {
		cliAction, _ = strings.CutPrefix(t.ID, s.ID+".")
		if _, err := cliexec.RenderGHArgs(cliAction, req.Arguments); err != nil {
			return result, err
		}
	} else if err := validateInvokeArguments(t, req); err != nil {
		return result, err
	}
	if needsApproval && req.ApprovalID == "" {
		return g.requestApproval(ctx, req, t, s, hash, decision)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	var data any
	switch s.Kind {
	case "cli":
		data, err = cliexec.Run(ctx, g.dir, s, cliAction, req.Arguments, g.lookupServerSecretOnly(s.ID, "GH_TOKEN"))
	case "mcp":
		data, err = g.upstream.CallMCP(ctx, s, t, req.Arguments)
	case "http":
		if t.Adapter != "" {
			data, err = g.callAdapter(ctx, s, t, req.Arguments)
		} else {
			data, err = upstream.CallHTTPWithLookup(ctx, s, t, req.Arguments, g.serverLookup(s))
		}
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
	data = g.upstream.RedactResponse(data, s)
	if len(req.Fields) > 0 {
		data = upstream.Project(data, req.Fields)
	}
	return InvokeResult{ID: newID(), ToolID: t.ID, Protocol: s.Kind, Data: data, DurationMS: time.Since(started).Milliseconds()}, nil
}

// Invocation errors may contain raw downstream payloads or schema values.
// The ledger only persists a stable error description.
func invokeAuditError(err error) string {
	var fe *fault.Error
	if errors.As(err, &fe) {
		if fe.Code != "" {
			return fe.Code
		}
		switch fe.Kind {
		case fault.Invalid:
			return "Invalid tool arguments"
		case fault.NotFound:
			return "Tool not found"
		case fault.Upstream:
			return "Upstream tool call failed"
		}
	}
	var se *scope.Error
	if errors.As(err, &se) && se.Code != "" {
		return se.Code
	}
	return "Tool call failed"
}

func validateInvokeArguments(t registry.Tool, req InvokeRequest) error {
	schema, err := registry.CompileSchema(t.InputSchema)
	if err != nil {
		return fault.New(fault.Internal, "工具 schema 无效", err)
	}
	if err := schema.Validate(req.Arguments); err != nil {
		return fault.New(fault.Invalid, fmt.Sprintf("参数不符合工具 schema: %v", err), nil)
	}
	return nil
}
