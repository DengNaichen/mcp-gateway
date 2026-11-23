// Package policy evaluates a tool request against identity, repository grants,
// and explicit policy rules.
package policy

import "slices"

const (
	EffectAllow   = "allow"
	EffectApprove = "approve"
	EffectDeny    = "deny"
)

type ScopeMode string

const (
	ScopeBypass         ScopeMode = "bypass"
	ScopeCompatUnscoped ScopeMode = "compat_unscoped"
	ScopeEnforced       ScopeMode = "enforced"
)

type Rule struct {
	ID        string   `json:"id"`
	Effect    string   `json:"effect"`
	Enabled   bool     `json:"enabled"`
	RepoKey   string   `json:"repo_key"`
	ServiceID string   `json:"service_id"`
	ToolIDs   []string `json:"tool_ids,omitempty"`
	Access    string   `json:"access"`
	Actor     string   `json:"actor,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	UpdatedAt string   `json:"updated_at"`
}

type BaselineGrant struct {
	ServerID string
	Access   string
	ToolIDs  []string
}

type Input struct {
	IdentityAllowed bool
	IdentityReason  string
	SharedMode      bool
	PrincipalID     string
	PrincipalGroups []string
	ScopeMode       ScopeMode
	InternalBypass  bool
	RepoID          string
	BaselineRuleKey string
	BaselineGrants  []BaselineGrant
	ServiceID       string
	ToolID          string
	ReadOnly        bool
}

type Step struct {
	Stage   string `json:"stage"`
	Matched bool   `json:"matched"`
	Effect  string `json:"effect,omitempty"`
	RuleID  string `json:"rule_id,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type Decision struct {
	Effect string `json:"effect"`
	RuleID string `json:"rule_id"`
	Reason string `json:"reason,omitempty"`
	Trace  []Step `json:"trace"`
}

// Evaluate stops at the first terminal stage. Within the same effect the
// first matching rule wins, so the caller supplies rules in stable order.
func Evaluate(in Input, rules []Rule) Decision {
	trace := make([]Step, 0, 6)
	if in.InternalBypass {
		return finish(trace, "internal", EffectAllow, "internal", "Internal request")
	}
	trace = append(trace, Step{Stage: "internal"})
	if !in.IdentityAllowed {
		return finish(trace, "identity", EffectDeny, "identity", in.IdentityReason)
	}
	trace = append(trace, Step{Stage: "identity", Matched: true, Effect: EffectAllow, RuleID: "identity", Reason: in.IdentityReason})
	if rule := firstMatch(in, rules, EffectDeny); rule != nil {
		return finish(trace, "deny", EffectDeny, "policy:"+rule.ID, rule.Reason)
	}
	trace = append(trace, Step{Stage: "deny"})

	ruleID := "default"
	if in.BaselineRuleKey != "" {
		ruleID = "scope:" + in.BaselineRuleKey
	}
	if in.ScopeMode == ScopeEnforced {
		if !baselineAllows(in) {
			return finish(trace, "baseline", EffectDeny, "scope:"+in.BaselineRuleKey, "Repository grants do not allow this tool")
		}
		trace = append(trace, Step{Stage: "baseline", Matched: true, Effect: EffectAllow, RuleID: ruleID})
	} else {
		trace = append(trace, Step{Stage: "baseline", Reason: string(in.ScopeMode)})
	}

	if rule := firstMatch(in, rules, EffectApprove); rule != nil {
		return finish(trace, "approve", EffectApprove, "policy:"+rule.ID, rule.Reason)
	}
	trace = append(trace, Step{Stage: "approve"})
	return finish(trace, "allow", EffectAllow, ruleID, "")
}

func finish(trace []Step, stage, effect, ruleID, reason string) Decision {
	return Decision{effect, ruleID, reason, append(trace, Step{stage, true, effect, ruleID, reason})}
}

func firstMatch(in Input, rules []Rule, effect string) *Rule {
	for i := range rules {
		r := &rules[i]
		if r.Enabled && r.Effect == effect && matches(*r, in) {
			return r
		}
	}
	return nil
}

func matches(r Rule, in Input) bool {
	if r.RepoKey != "*" && (in.RepoID == "" || in.RepoID == "*" || r.RepoKey != in.RepoID) {
		return false
	}
	if r.ServiceID != "*" && r.ServiceID != in.ServiceID {
		return false
	}
	if len(r.ToolIDs) > 0 && !slices.Contains(r.ToolIDs, in.ToolID) {
		return false
	}
	if !(r.Access == "*" || r.Access == "ro" && in.ReadOnly || r.Access == "rw" && !in.ReadOnly) {
		return false
	}
	if !in.SharedMode || r.Actor == "" || r.Actor == "*" || r.Actor == in.PrincipalID {
		return true
	}
	for _, group := range in.PrincipalGroups {
		if r.Actor == "group:"+group {
			return true
		}
	}
	return false
}

func baselineAllows(in Input) bool {
	for _, grant := range in.BaselineGrants {
		if grant.ServerID == in.ServiceID && (grant.Access != "ro" || in.ReadOnly) &&
			(len(grant.ToolIDs) == 0 || slices.Contains(grant.ToolIDs, in.ToolID)) {
			return true
		}
	}
	return false
}
