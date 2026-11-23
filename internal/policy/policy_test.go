package policy

import (
	"fmt"
	"testing"
	"testing/quick"
)

func TestEvaluate(t *testing.T) {
	approve := Rule{ID: "app", Effect: EffectApprove, Enabled: true, RepoKey: "repo", ServiceID: "svc", Access: "ro", Actor: "group:ops"}
	deny := Rule{ID: "block", Effect: EffectDeny, Enabled: true, RepoKey: "*", ServiceID: "*", Access: "rw", ToolIDs: []string{"svc.tool"}}
	base := Input{
		IdentityAllowed: true, SharedMode: true, PrincipalID: "owner", PrincipalGroups: []string{"ops"},
		ScopeMode: ScopeEnforced, RepoID: "repo", BaselineRuleKey: "repo", ServiceID: "svc", ToolID: "svc.tool", ReadOnly: true,
		BaselineGrants: []BaselineGrant{{ServerID: "svc", Access: "ro", ToolIDs: []string{"svc.tool"}}},
	}
	for _, tc := range []struct {
		name   string
		change func(*Input)
		rules  []Rule
		effect string
		ruleID string
		last   string
	}{
		{"internal bypass precedes identity and rules", func(in *Input) { in.InternalBypass, in.IdentityAllowed, in.ReadOnly = true, false, false }, []Rule{deny}, EffectAllow, "internal", "internal"},
		{"identity precedes explicit deny", func(in *Input) { in.IdentityAllowed, in.ReadOnly = false, false }, []Rule{deny}, EffectDeny, "identity", "identity"},
		{"explicit deny precedes baseline", func(in *Input) { in.ReadOnly = false }, []Rule{approve, deny}, EffectDeny, "policy:block", "deny"},
		{"baseline denial precedes approval", func(in *Input) { in.BaselineGrants = nil }, []Rule{approve}, EffectDeny, "scope:repo", "baseline"},
		{"baseline excludes writes", func(in *Input) { in.ReadOnly = false }, nil, EffectDeny, "scope:repo", "baseline"},
		{"baseline excludes other tools", func(in *Input) { in.ToolID = "svc.other" }, nil, EffectDeny, "scope:repo", "baseline"},
		{"baseline excludes other services", func(in *Input) { in.ServiceID = "other" }, nil, EffectDeny, "scope:repo", "baseline"},
		{"approval after granting baseline", nil, []Rule{approve}, EffectApprove, "policy:app", "approve"},
		{"first rule of equal effect wins", nil, []Rule{approve, {ID: "app2", Effect: EffectApprove, Enabled: true, RepoKey: "*", ServiceID: "*", Access: "*"}}, EffectApprove, "policy:app", "approve"},
		{"shared group selector must match", func(in *Input) { in.PrincipalGroups = []string{"ops-extra"} }, []Rule{approve}, EffectAllow, "scope:repo", "allow"},
		{"shared principal selector matches", nil, []Rule{{ID: "owner", Effect: EffectDeny, Enabled: true, RepoKey: "*", ServiceID: "*", Access: "*", Actor: "owner"}}, EffectDeny, "policy:owner", "deny"},
		{"local approval selector fails closed", func(in *Input) { in.SharedMode, in.PrincipalGroups = false, nil }, []Rule{approve}, EffectApprove, "policy:app", "approve"},
		{"local deny selector fails closed", func(in *Input) { in.SharedMode, in.PrincipalID = false, "" }, []Rule{{ID: "local", Effect: EffectDeny, Enabled: true, RepoKey: "*", ServiceID: "*", Access: "*", Actor: "other"}}, EffectDeny, "policy:local", "deny"},
		{"disabled explicit rule ignored", nil, []Rule{{ID: "off", Effect: EffectDeny, RepoKey: "*", ServiceID: "*", Access: "*"}}, EffectAllow, "scope:repo", "allow"},
		{"bypass skips empty baseline", func(in *Input) { in.ScopeMode, in.BaselineRuleKey, in.BaselineGrants = ScopeBypass, "", nil }, nil, EffectAllow, "default", "allow"},
		{"compat unscoped approval still applies", func(in *Input) { in.ScopeMode, in.BaselineGrants = ScopeCompatUnscoped, nil }, []Rule{approve}, EffectApprove, "policy:app", "approve"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			if tc.change != nil {
				tc.change(&in)
			}
			got := Evaluate(in, tc.rules)
			last := got.Trace[len(got.Trace)-1]
			if got.Effect != tc.effect || got.RuleID != tc.ruleID || last.Stage != tc.last ||
				!last.Matched || last.Effect != got.Effect || last.RuleID != got.RuleID {
				t.Fatalf("decision = %+v, want %s %s at %s", got, tc.effect, tc.ruleID, tc.last)
			}
		})
	}
}

func TestDenyAndUnscopedProperties(t *testing.T) {
	if err := quick.Check(func(repo, service, tool, actor string, ro, useStar bool) bool {
		in := Input{IdentityAllowed: true, RepoID: repo, ServiceID: service, ToolID: tool, ReadOnly: ro,
			BaselineRuleKey: "*", BaselineGrants: []BaselineGrant{{ServerID: service, Access: "rw"}}}
		rule := Rule{ID: "global", Enabled: true, Effect: EffectDeny, RepoKey: "*", ServiceID: service,
			ToolIDs: []string{tool}, Access: "*", Actor: actor}
		for _, mode := range []ScopeMode{ScopeBypass, ScopeCompatUnscoped, ScopeEnforced} {
			in.ScopeMode = mode
			if got := Evaluate(in, []Rule{rule}); got.Effect != EffectDeny || got.RuleID != "policy:global" {
				return false
			}
		}
		in.RepoID = ""
		if useStar {
			in.RepoID = "*"
		}
		rule.RepoKey = repo
		if rule.RepoKey == "*" {
			rule.RepoKey = "repo"
		}
		for _, effect := range []string{EffectDeny, EffectApprove} {
			rule.Effect = effect
			if got := Evaluate(in, []Rule{rule}); got.Effect != EffectAllow {
				return false
			}
			wildcard := rule
			wildcard.RepoKey = "*"
			if got := Evaluate(in, []Rule{rule, wildcard}); got.Effect != effect {
				return false
			}
		}
		return true
	}, &quick.Config{MaxCount: 1000}); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkEvaluate(b *testing.B) {
	in := Input{
		IdentityAllowed: true, SharedMode: true, PrincipalID: "owner", PrincipalGroups: []string{"developers"},
		ScopeMode: ScopeEnforced, RepoID: "repo", BaselineRuleKey: "repo",
		BaselineGrants: []BaselineGrant{{ServerID: "svc", Access: "rw", ToolIDs: []string{"svc.edit"}}},
		ServiceID:      "svc", ToolID: "svc.edit",
	}
	rules := make([]Rule, 32)
	for i := range rules {
		rules[i] = Rule{ID: fmt.Sprintf("rule-%d", i), Effect: EffectDeny, Enabled: true, RepoKey: "repo", ServiceID: fmt.Sprintf("other-%d", i), Access: "rw"}
	}
	rules[len(rules)-1] = Rule{ID: "review", Effect: EffectApprove, Enabled: true, RepoKey: "repo", ServiceID: "svc", Access: "rw", Actor: "group:developers"}
	b.ReportAllocs()
	for b.Loop() {
		Evaluate(in, rules)
	}
}
