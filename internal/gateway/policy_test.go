package gateway

import (
	"context"
	"slices"
	"strings"
	"testing"

	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
)

func TestPolicyCatalogMatchesAuthorization(t *testing.T) {
	t.Setenv("GATEWAY_AUTH_FILE", "")
	g, svcs, err := New(t.TempDir(), "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	server := registry.Server{ID: "svc", Enabled: true, Kind: "http", Name: "Service", Endpoint: "http://localhost"}
	if err := svcs.Registry.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	tools := []registry.Tool{
		{ID: "svc.approve", Actors: []string{"*"}, ReadOnly: true, Enabled: true},
		{ID: "svc.deny", Actors: []string{"developer"}, Enabled: true},
		{ID: "svc.allow", Actors: []string{"developer"}, ReadOnly: true, Enabled: true},
		{ID: "svc.disabled", Actors: []string{"*"}, ReadOnly: true},
	}
	for i := range tools {
		tool := &tools[i]
		tool.ServerID, tool.Name, tool.Description = server.ID, tool.ID, "Policy fixture"
		tool.Method, tool.Path, tool.InputSchema = "GET", "/", objectSchema(map[string]any{})
		if err := svcs.Registry.SaveTool(*tool); err != nil {
			t.Fatal(err)
		}
	}
	for i, effect := range []string{policy.EffectApprove, policy.EffectDeny} {
		if _, err := g.SavePolicyRule(policy.Rule{Effect: effect, Enabled: true, RepoKey: "*", ServiceID: server.ID, ToolIDs: []string{tools[i].ID}, Access: "*", Actor: "group:absent"}); err != nil {
			t.Fatal(err)
		}
	}
	parent := t.Context()
	contexts := map[string]context.Context{
		"unscoped": parent,
		"bypass":   scope.WithRequestScope(parent, scope.ResolvedRequestScope{Mode: scope.ModeBypass}),
		"enforced": scope.WithRequestScope(parent, scope.ResolvedRequestScope{
			Mode: scope.ModeEnforced, Rule: scope.ScopeRule{RepoKey: "*", Grants: []scope.ServiceGrant{{ServerID: server.ID, Access: "ro", ToolIDs: []string{tools[0].ID}}}},
		}),
		"empty baseline": scope.WithRequestScope(parent, scope.ResolvedRequestScope{
			Mode: scope.ModeEnforced, Rule: scope.ScopeRule{RepoKey: "*"},
		}),
	}
	for name, ctx := range contexts {
		for _, actor := range []string{"developer", "observer"} {
			t.Run(name+"/"+actor, func(t *testing.T) {
				catalog := g.Catalog(ctx, actor, false)
				for _, tool := range tools {
					decision := g.authorize(ctx, tool, actor)
					i := slices.IndexFunc(catalog.Tools, func(capability routing.Capability) bool { return capability.ID == tool.ID })
					if (i >= 0) != (decision.Effect != policy.EffectDeny) {
						t.Fatalf("%s: catalog %+v, decision %+v", tool.ID, catalog, decision)
					}
					if i >= 0 && catalog.Tools[i].NeedsApproval != (decision.Effect == policy.EffectApprove) {
						t.Fatalf("%s: wrong approval flag for %+v", tool.ID, decision)
					}
					if tool.ID == "svc.approve" && name != "empty baseline" && (i < 0 || !catalog.Tools[i].NeedsApproval) {
						t.Fatalf("local approval must be visible for %s: %+v", actor, catalog)
					}
				}
			})
		}
	}
	decision := g.authorize(scope.WithInternalBypass(parent), tools[3], "observer")
	if decision.RuleID != "internal" || decision.Effect != policy.EffectAllow {
		t.Fatalf("explicit internal bypass: %+v", decision)
	}
}

func TestPolicyRulesSurviveDeletedTargetsAndRestart(t *testing.T) {
	t.Setenv("GATEWAY_AUTH_FILE", "")
	dir := t.TempDir()
	g, svcs, err := New(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	repo, err := svcs.Scope.SaveRepository(t.Context(), scope.SaveRepositoryRequest{Name: "Repo", Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	server := registry.Server{ID: "svc", Enabled: true, Kind: "http", Name: "Service", Endpoint: "http://localhost"}
	if err := g.SaveServer(server); err != nil {
		t.Fatal(err)
	}
	tool := registry.Tool{ID: "svc.get", ServerID: server.ID, Name: "Get", Description: "Read fixture", Actors: []string{"*"}, Enabled: true, ReadOnly: true, Method: "GET", Path: "/", InputSchema: objectSchema(map[string]any{})}
	if err := svcs.Registry.SaveTool(tool); err != nil {
		t.Fatal(err)
	}
	rule, err := g.SavePolicyRule(policy.Rule{Effect: policy.EffectDeny, Enabled: true, RepoKey: repo.ID, ServiceID: server.ID, ToolIDs: []string{tool.ID}, Access: "*"})
	if err != nil || !strings.HasPrefix(rule.ID, "pol-") || rule.UpdatedAt == "" {
		t.Fatalf("save = %+v, %v", rule, err)
	}
	if err := svcs.Scope.DeleteRepository(repo.ID); err != nil {
		t.Fatal(err)
	}
	if err := g.DeleteServer(server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SavePolicyRule(rule); err == nil {
		t.Fatal("an update with a missing target must be rejected")
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _, err := New(dir, "http://localhost")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if overview := reopened.PolicyOverview(); len(overview.Rules) != 1 || overview.Rules[0].ID != rule.ID {
		t.Fatalf("dangling rule was not retained: %+v", overview)
	}
	if err := reopened.DeletePolicyRule(rule.ID); err != nil || len(reopened.PolicyOverview().Rules) != 0 {
		t.Fatalf("delete dangling rule: %v", err)
	}
}
