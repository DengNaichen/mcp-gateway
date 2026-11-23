package gateway

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"

	"gatewaylab/internal/access"
	"gatewaylab/internal/fault"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/store"
)

type policyState struct {
	mu    sync.RWMutex
	rules []policy.Rule
}

type PolicyOverview struct {
	Enforce      bool
	BaselineFrom string
	Rules        []policy.Rule
}

type PolicyEvalRequest struct {
	ToolID string
	RepoID string
	Actor  string
}

type PolicyEvalResult struct {
	Decision policy.Decision
	Trust    string
}

func (g *Gateway) authorize(ctx context.Context, t registry.Tool, actor string) policy.Decision {
	if scope.HasInternalBypass(ctx) {
		return policy.Evaluate(policy.Input{InternalBypass: true}, nil)
	}
	principal, _ := access.PrincipalFrom(ctx)
	resolved := g.scope.ExecutionScope(ctx)
	in := policy.Input{
		IdentityAllowed: g.access.IdentityAllowsTool(ctx, t, actor),
		SharedMode:      g.access != nil,
		PrincipalID:     principal.ID,
		PrincipalGroups: principal.Groups,
		ScopeMode:       policy.ScopeEnforced,
		RepoID:          resolved.Event.RepoID,
		BaselineRuleKey: resolved.Rule.RepoKey,
		ServiceID:       t.ServerID,
		ToolID:          t.ID,
		ReadOnly:        t.ReadOnly,
	}
	if !in.IdentityAllowed {
		in.IdentityReason = "Tool is disabled or outside the caller's permissions"
	}
	switch resolved.Mode {
	case scope.ModeBypass:
		in.ScopeMode = policy.ScopeBypass
	case scope.ModeCompatUnscoped:
		in.ScopeMode = policy.ScopeCompatUnscoped
	case scope.ModeEnforced:
		in.BaselineGrants = make([]policy.BaselineGrant, len(resolved.Rule.Grants))
		for i, grant := range resolved.Rule.Grants {
			in.BaselineGrants[i] = policy.BaselineGrant(grant)
		}
	}

	g.policies.mu.RLock()
	defer g.policies.mu.RUnlock()
	return policy.Evaluate(in, g.policies.rules)
}

func (g *Gateway) PolicyOverview() PolicyOverview {
	return PolicyOverview{Enforce: g.scope.IsEnforced(), BaselineFrom: "scope_rules", Rules: g.policyRules()}
}

func (g *Gateway) policyRules() []policy.Rule {
	g.policies.mu.RLock()
	defer g.policies.mu.RUnlock()
	rules := make([]policy.Rule, len(g.policies.rules))
	for i, rule := range g.policies.rules {
		rule.ToolIDs = slices.Clone(rule.ToolIDs)
		rules[i] = rule
	}
	return rules
}

func (g *Gateway) SavePolicyRule(rule policy.Rule) (policy.Rule, error) {
	if rule.ID != "" && !registry.ValidID(rule.ID) {
		return policy.Rule{}, invalid("策略规则 ID 无效")
	}
	if rule.Effect != policy.EffectDeny && rule.Effect != policy.EffectApprove {
		return policy.Rule{}, invalid("effect 必须是 deny 或 approve")
	}
	if rule.Access != "*" && rule.Access != "ro" && rule.Access != "rw" {
		return policy.Rule{}, invalid("access 必须是 *、ro 或 rw")
	}
	if rule.RepoKey != "*" && !g.scope.HasRepository(rule.RepoKey) {
		return policy.Rule{}, invalid("指定的仓库不存在")
	}
	reg := g.registry.Snapshot()
	if rule.ServiceID != "*" && !slices.ContainsFunc(reg.Servers, func(s registry.Server) bool { return s.ID == rule.ServiceID }) {
		return policy.Rule{}, invalid("指定的服务不存在")
	}
	for _, id := range rule.ToolIDs {
		if !slices.ContainsFunc(reg.Tools, func(t registry.Tool) bool {
			return t.ID == id && (rule.ServiceID == "*" || t.ServerID == rule.ServiceID)
		}) {
			return policy.Rule{}, invalid("指定的工具不存在或不属于所选服务: " + id)
		}
	}

	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	repo := store.PolicyRules[policy.Rule](g.db)
	rule.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if rule.ID == "" {
		rule.ID = "pol-" + uuid.NewString()[:12]
		if err := repo.Insert(rule.ID, rule); err != nil {
			return policy.Rule{}, fmt.Errorf("create policy rule: %w", err)
		}
	} else if err := repo.Put(rule.ID, rule); err != nil {
		return policy.Rule{}, fmt.Errorf("save policy rule: %w", err)
	}
	g.policies.mu.Lock()
	defer g.policies.mu.Unlock()
	saved := rule
	saved.ToolIDs = slices.Clone(rule.ToolIDs)
	i := slices.IndexFunc(g.policies.rules, func(r policy.Rule) bool { return r.ID == rule.ID })
	if i < 0 {
		g.policies.rules = append(g.policies.rules, saved)
	} else {
		g.policies.rules[i] = saved
	}
	return rule, nil
}

func (g *Gateway) DeletePolicyRule(id string) error {
	if !registry.ValidID(id) {
		return invalid("策略规则 ID 无效")
	}
	g.publicationConfigMu.Lock()
	defer g.publicationConfigMu.Unlock()
	if err := store.PolicyRules[policy.Rule](g.db).Delete(id); err != nil {
		return fmt.Errorf("delete policy rule: %w", err)
	}
	g.policies.mu.Lock()
	defer g.policies.mu.Unlock()
	g.policies.rules = slices.DeleteFunc(g.policies.rules, func(r policy.Rule) bool { return r.ID == id })
	return nil
}

func (g *Gateway) EvaluatePolicy(ctx context.Context, req PolicyEvalRequest) (PolicyEvalResult, error) {
	trust := "declared"
	if _, authenticated := access.PrincipalFrom(ctx); authenticated && req.Actor == "" {
		trust = "authenticated"
	}
	if req.Actor != "" && g.access != nil {
		var err error
		ctx, err = g.access.WithDeclaredPrincipal(ctx, req.Actor)
		if err != nil {
			return PolicyEvalResult{}, err
		}
	}
	if req.RepoID != "" {
		resolved, err := g.scope.EvaluateSimulatedScope(scope.SimScope{RepoKey: req.RepoID})
		if err != nil {
			return PolicyEvalResult{}, err
		}
		ctx = scope.WithRequestScope(ctx, resolved)
	}

	reg := g.registry.Snapshot()
	for _, tool := range reg.Tools {
		if tool.ID != req.ToolID {
			continue
		}
		// Availability is part of the identity gate for registered tools.
		tool.Enabled = tool.Enabled && slices.ContainsFunc(reg.Servers, func(s registry.Server) bool {
			return s.ID == tool.ServerID && s.Enabled
		})
		return PolicyEvalResult{Decision: g.authorize(ctx, tool, req.Actor), Trust: trust}, nil
	}
	return PolicyEvalResult{}, fault.New(fault.NotFound, "工具不存在", nil)
}
