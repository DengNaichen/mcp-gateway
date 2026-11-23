package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"gatewaylab/internal/access"
	"gatewaylab/internal/activity"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"
)

type Approval = store.Approval

const (
	pendingTTL     = 30 * time.Minute
	approvedTTL    = 5 * time.Minute
	executionLease = 2 * time.Minute // Longer than the upstream invocation timeout.
)

func forbiddenApproval(code, message string) error {
	return fault.WithCode(fault.New(fault.Forbidden, message, nil), code)
}

func invalidApproval() error {
	return forbiddenApproval("approval_invalid", "审批单无效、已过期或与当前调用不匹配")
}

func (g *Gateway) Approvals(ctx context.Context, status string, limit int) ([]Approval, int, error) {
	if (g.localAdmin == nil && g.access == nil) || !g.isAdminCaller(ctx) {
		return nil, 0, forbiddenApproval("approval_admin_required", "查看审批单需要管理身份")
	}
	switch status {
	case "", "pending", "approved", "executing", "consumed", "denied", "expired":
	default:
		return nil, 0, invalid("未知审批状态")
	}
	if limit <= 0 {
		limit = 100
	}
	return g.storage.ListApprovals(status, min(limit, 500), time.Now())
}

func (g *Gateway) DecideApproval(ctx context.Context, id, decision, note string) (Approval, error) {
	if (g.localAdmin == nil && g.access == nil) || !g.isAdminCaller(ctx) {
		return Approval{}, forbiddenApproval("approval_admin_required", "审批需要管理身份")
	}
	a, err := g.storage.ApprovalByID(id, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return Approval{}, invalidApproval()
	}
	if err != nil {
		return Approval{}, err
	}
	by := "local_admin"
	if g.access != nil {
		p, _ := access.PrincipalFrom(ctx)
		by = p.ID
		if by == a.Actor {
			return Approval{}, forbiddenApproval("self_approval_forbidden", "不能裁决自己的审批请求")
		}
	}
	// Notes can quote arguments or configured credentials.
	raw, err := json.Marshal(note)
	if err != nil {
		return Approval{}, err
	}
	raw, err = g.redactActivityDetail(raw)
	if err != nil {
		return Approval{}, err
	}
	if err = json.Unmarshal(raw, &note); err != nil {
		return Approval{}, err
	}
	a, err = g.storage.Decide(id, decision, by, note, time.Now(), approvedTTL)
	if errors.Is(err, store.ErrNotFound) {
		return Approval{}, invalidApproval()
	}
	if err != nil {
		return Approval{}, err
	}
	g.activity.RecordAudit("approval", by, decision, a.Action, time.Now(), activity.AuditMeta{
		ApprovalID: id, RuleID: a.RuleID, Decision: policy.EffectApprove, RepoID: a.RepoID, Protocol: a.Protocol, ServiceID: a.ServiceID, Action: a.Action,
	}, nil)
	return a.Approval, nil
}

func approvalHash(req InvokeRequest, ev scope.EventScope, t registry.Tool, protocol string) (string, error) {
	args, err := workspace.CanonicalArgsHash(req.Arguments, "")
	if err != nil {
		return "", err
	}
	schema, err := json.Marshal(t.InputSchema)
	if err != nil {
		return "", err
	}
	// Unregistered repositories have no repo ID. Bind verified filesystem
	// identity too, so the empty ID cannot be replayed in another workspace.
	anchor := ""
	if ev.RepoID == "" {
		anchor = ev.GitCommonDir
		if anchor == "" {
			anchor = ev.Root
		}
	}
	b, err := json.Marshal([]string{req.Actor, ev.Agent, ev.RepoID, anchor, protocol, t.ServerID, t.ID, fmt.Sprintf("%x", sha256.Sum256(schema)), args})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

func approvalChallenge(a store.ApprovalRow, status string, now time.Time) (InvokeResult, error) {
	deadline, message := a.PendingExpiresAt, "请在桌面端审批，然后携带 approval_id 以相同参数重试。"
	if a.Status == "approved" {
		deadline, message = a.ExpiresAt, "审批已通过，请携带 approval_id 以相同参数重试。"
	}
	expires, err := time.Parse(time.RFC3339Nano, deadline)
	if err != nil {
		return InvokeResult{}, err
	}
	return InvokeResult{ToolID: a.Action, Protocol: a.Protocol, Status: status, Approval: &ApprovalChallenge{
		ApprovalID: a.ID, Summary: a.Summary, Message: message, ExpiresIn: max(0, int(math.Ceil(expires.Sub(now).Seconds()))),
	}}, nil
}

func (g *Gateway) requestApproval(ctx context.Context, req InvokeRequest, t registry.Tool, s registry.Server, hash string, d policy.Decision) (InvokeResult, error) {
	sc := g.scope.ExecutionScope(ctx)
	raw, err := json.Marshal(g.upstream.RedactResponse(req.Arguments, s))
	if err != nil {
		return InvokeResult{}, err
	}
	raw, err = g.redactActivityDetail(raw)
	if err != nil {
		return InvokeResult{}, err
	}
	summary := t.ID + " " + string(raw)
	if r := []rune(summary); len(r) > 240 {
		summary = string(r[:239]) + "…"
	}
	now := time.Now()
	a, err := g.storage.FindOrCreateApproval(store.ApprovalRow{RequestHash: hash, Approval: Approval{
		ID: "apv_" + uuid.NewString(), Actor: req.Actor, Agent: sc.Event.Agent, RequesterSession: sc.RequesterSession,
		RepoID: sc.Event.RepoID, RepoName: sc.Event.RepoName, Protocol: s.Kind, ServiceID: s.ID, Action: t.ID, ReadOnly: t.ReadOnly,
		RuleID: d.RuleID, Reason: d.Reason, ArgsRedacted: string(raw), Summary: summary,
	}}, now, pendingTTL)
	if errors.Is(err, store.ErrPendingLimit) {
		return InvokeResult{}, fault.WithCode(fault.New(fault.RateLimited, "待审批请求过多", nil), "rate_limited")
	}
	if err != nil {
		return InvokeResult{}, err
	}
	return approvalChallenge(a, "approval_required", now)
}

func (g *Gateway) retryApproval(ctx context.Context, req InvokeRequest, t registry.Tool, hash string, audit *activity.AuditMeta) (InvokeResult, string, error) {
	// Order rule updates against the final policy evaluation and lease claim.
	g.publicationConfigMu.RLock()
	defer g.publicationConfigMu.RUnlock()
	a, err := g.storage.ApprovalByID(req.ApprovalID, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return InvokeResult{}, "", invalidApproval()
	}
	if err != nil {
		return InvokeResult{}, "", err
	}
	audit.ApprovalID = a.ID
	if a.RequestHash != hash || a.Actor != req.Actor {
		return InvokeResult{}, "", invalidApproval()
	}
	switch a.Status {
	case "pending", "approved":
		// Both live states remain subject to the current policy.
	case "executing":
		return InvokeResult{}, "", forbiddenApproval("approval_already_used", "审批单已被领取")
	default:
		return InvokeResult{}, "", invalidApproval()
	}
	d := g.authorize(ctx, t, req.Actor)
	audit.Decision, audit.RuleID = d.Effect, d.RuleID
	if d.Effect == policy.EffectDeny || !t.Enabled {
		if err := g.storage.RevokeApproved(req.ApprovalID, hash, req.Actor, time.Now()); err != nil {
			return InvokeResult{}, "", err
		}
		return InvokeResult{}, "", forbiddenApproval("policy_denied", "当前策略禁止调用此工具")
	}
	audit.Decision, audit.RuleID = policy.EffectApprove, a.RuleID
	if a.Status == "pending" {
		out, err := approvalChallenge(a, "approval_pending", time.Now())
		return out, "", err
	}
	now := time.Now()
	lease := uuid.NewString()
	err = g.storage.ClaimApprovedForExecution(a.ID, hash, req.Actor, now, lease, executionLease)
	if errors.Is(err, store.ErrApprovalState) {
		err = forbiddenApproval("approval_already_used", "审批单已无法领取")
	}
	return InvokeResult{}, lease, err
}
