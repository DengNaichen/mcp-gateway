package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/activity"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/workspace"
)

func TestPolicyVisibilityInvokeAndDryRun(t *testing.T) {
	f := newActorFixture(t, false)
	rule := decodeM1Response[apitypes.PolicyRule](t, f.call(t, "PUT", "/api/policies/rules", "",
		`{"effect":"approve","enabled":true,"repo_key":"*","service_id":"svc","tool_ids":["svc.develop"],"access":"ro","actor":"group:absent"}`), 200)
	if !strings.HasPrefix(rule.ID, "pol-") || rule.UpdatedAt == "" {
		t.Fatalf("save approval: %+v", rule)
	}
	overview := decodeM1Response[apitypes.PolicyOverview](t, f.call(t, "GET", "/api/policies", "", ""), 200)
	if overview.Enforce || overview.BaselineFrom != "scope_rules" || len(overview.Rules) != 1 || overview.Rules[0].ID != rule.ID {
		t.Fatalf("policy overview: %+v", overview)
	}
	boot := decodeM1Response[apitypes.Bootstrap](t, f.call(t, "GET", "/api/bootstrap", "", ""), 200)
	if len(boot.Policies.Rules) != 1 || boot.Policies.Rules[0].ID != rule.ID {
		t.Fatalf("bootstrap policies: %+v", boot.Policies)
	}
	evaluation := decodeM1Response[apitypes.PolicyEvaluateResult](t, f.call(t, "POST", "/api/policies/evaluate", "", `{"tool_id":"svc.develop"}`), 200)
	if evaluation.Effect != "approve" || evaluation.RuleID != "policy:"+rule.ID || evaluation.Trust != "declared" || len(evaluation.Trace) == 0 {
		t.Fatalf("dry run: %+v", evaluation)
	}
	if len(f.svcs.Activity.Recent()) != 0 {
		t.Fatal("dry run created activity")
	}

	catalog := decodeM1Response[apitypes.Catalog](t, f.call(t, "GET", "/api/catalog", "", ""), 200)
	if !slices.ContainsFunc(catalog.Tools, func(tool apitypes.Capability) bool { return tool.ID == "svc.develop" && tool.NeedsApproval }) {
		t.Fatalf("approval missing from catalog: %+v", catalog)
	}
	schema := decodeM1Response[apitypes.ToolSchema](t, f.call(t, "GET", "/api/tools/svc.develop/schema", "", ""), 200)
	if !schema.NeedsApproval {
		t.Fatalf("approval missing from schema: %+v", schema)
	}
	discovered := decodeM1Response[apitypes.DiscoverResult](t, f.call(t, "POST", "/api/discover", "", `{"intent":"develop"}`), 200)
	if !slices.ContainsFunc(discovered.Tools, func(tool apitypes.Selection) bool { return tool.ID == "svc.develop" && tool.NeedsApproval }) {
		t.Fatalf("approval missing from discovery: %+v", discovered)
	}
	summary := decodeM1Response[apitypes.DiscoverSummaryResult](t, f.call(t, "POST", "/api/discover", "", `{"intent":"develop","summary_only":true}`), 200)
	if !slices.ContainsFunc(summary.Tools, func(tool apitypes.Capability) bool { return tool.ID == "svc.develop" && tool.NeedsApproval }) {
		t.Fatalf("approval missing from summary discovery: %+v", summary)
	}

	invoke := func(status int, code string) {
		t.Helper()
		out := f.call(t, "POST", "/api/invoke", "", `{"tool_id":"svc.develop","arguments":{}}`)
		if status == 200 {
			decodeM1Response[apitypes.InvokeResult](t, out, status)
		} else if got := decodeM1Response[apitypes.Error](t, out, status); got.Code != code {
			t.Fatalf("invoke error = %+v, want %s", got, code)
		}
	}
	invoke(403, "approval_required_not_enabled")
	rule.Effect = "deny"
	decodeM1Response[apitypes.PolicyRule](t, f.call(t, "PUT", "/api/policies/rules", "", string(mustJSON(t, rule))), 200)
	denied := decodeM1Response[apitypes.Catalog](t, f.call(t, "GET", "/api/catalog", "", ""), 200)
	if slices.ContainsFunc(denied.Tools, func(tool apitypes.Capability) bool { return tool.ID == "svc.develop" }) {
		t.Fatal("deny rule leaked into catalog")
	}
	invoke(403, "policy_denied")
	if f.developerCalls.Load() != 0 {
		t.Fatal("denied or approval-required invoke reached upstream")
	}
	decodeM1Response[apitypes.Deleted](t, f.call(t, "DELETE", "/api/policies/rules/"+rule.ID, "", ""), 200)
	invoke(200, "")
	if f.developerCalls.Load() != 1 {
		t.Fatal("allowed invoke did not reach upstream")
	}
	for _, tc := range []struct{ effect, status, ruleID string }{
		{"allow", "success", "default"}, {"deny", "error", "policy:" + rule.ID}, {"approve", "error", "policy:" + rule.ID},
	} {
		path := "/api/events/query?actor=developer&service_id=svc&protocol=http&q=DEVELOP&decision=" + tc.effect + "&status=" + tc.status
		page := decodeM1Response[apitypes.EventPage](t, f.call(t, "GET", path, "", ""), 200)
		if len(page.Items) != 1 {
			t.Fatalf("%s query: %+v", tc.effect, page)
		}
		ev := page.Items[0]
		if ev.Decision != tc.effect || ev.RuleID != tc.ruleID || ev.Action != "svc.develop" || ev.ApprovalID != "" || ev.SkillID != "" {
			t.Fatalf("structured audit: %+v", ev)
		}
	}
}

func TestAuditQueryAuthorizationAndPagination(t *testing.T) {
	t.Run("local admin token", func(t *testing.T) {
		t.Setenv("GATEWAY_AUTH_FILE", "")
		g, svcs, err := gateway.NewWithOptions(t.TempDir(), "http://localhost", gateway.Options{LocalAdminToken: adminTestToken})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = g.Close() })
		handler := Handler(g, svcs)
		for _, token := range []string{"", "wrong", adminTestToken} {
			req := httptest.NewRequest("GET", "http://localhost/api/events/query", nil)
			req.Header.Set("X-Gateway-Admin-Token", token)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, req)
			if token != adminTestToken {
				decodeM1Response[apitypes.Error](t, out, 403)
				continue
			}
			page := decodeM1Response[apitypes.EventPage](t, out, 200)
			if page.Items == nil || len(page.Items) != 0 {
				t.Fatalf("query with local token: %+v", page)
			}
		}
	})

	t.Run("shared principals and page cursors", func(t *testing.T) {
		f := newActorFixture(t, true)
		meta := activity.AuditMeta{RepoID: "repo", ServiceID: "svc", Protocol: "http", Action: "inspect-cases", Decision: "deny", RuleID: "policy:fixture"}
		for _, actor := range []string{"watcher", "owner", "watcher"} {
			f.svcs.Activity.RecordAudit("invoke", actor, "error", "Audit event", time.Now(), meta, map[string]string{"note": "detail-only"})
		}
		f.svcs.Activity.RecordAudit("invoke", "watcher", "error", "Audit event", time.Now(), activity.AuditMeta{RepoID: "elsewhere"}, nil)
		base := "/api/events/query?repo_id=repo&service_id=svc&protocol=http&decision=deny&status=error&q=inspect"
		decodeM1Response[apitypes.Error](t, f.call(t, "GET", base, "", ""), 401)
		all := decodeM1Response[apitypes.EventPage](t, f.call(t, "GET", base, adminTestToken, ""), 200)
		if len(all.Items) != 3 || all.Items[1].Actor != "owner" {
			t.Fatalf("admin page: %+v", all)
		}
		callerQuery := base + "&actor=owner&limit=1"
		first := decodeM1Response[apitypes.EventPage](t, f.call(t, "GET", callerQuery, observerTestToken, ""), 200)
		if !first.HasMore || first.NextCursor == "" || len(first.Items) != 1 || first.Items[0].ID != all.Items[0].ID {
			t.Fatalf("first page with actor override: %+v", first)
		}
		follow := func(before string) apitypes.EventPage {
			path := callerQuery + "&before=" + url.QueryEscape(before)
			return decodeM1Response[apitypes.EventPage](t, f.call(t, "GET", path, observerTestToken, ""), 200)
		}
		last := follow(first.NextCursor)
		if last.HasMore || last.NextCursor != "" || len(last.Items) != 1 || last.Items[0].ID != all.Items[2].ID {
			t.Fatalf("last page with actor override: %+v", last)
		}
		byTime := follow(first.Items[0].Time.Format(time.RFC3339Nano))
		if len(byTime.Items) != 1 || byTime.Items[0].ID != last.Items[0].ID {
			t.Fatalf("legacy timestamp cursor: %+v", byTime)
		}
		excluded := decodeM1Response[apitypes.EventPage](t, f.call(t, "GET", "/api/events/query?q=detail-only", adminTestToken, ""), 200)
		if len(excluded.Items) != 0 {
			t.Fatalf("searched raw detail: %+v", excluded)
		}
		for _, query := range []string{"before=invalid", "limit=0", "limit=-1", "limit=invalid"} {
			decodeM1Response[apitypes.Error](t, f.call(t, "GET", "/api/events/query?"+query, adminTestToken, ""), 400)
		}
		decodeM1Response[apitypes.Error](t, f.call(t, "GET", "/api/events", observerTestToken, ""), 403)
		for _, path := range []string{"/api/policies", "/api/policies/rules", "/api/policies/rules/rule", "/api/policies/evaluate"} {
			method := map[string]string{
				"/api/policies": "GET", "/api/policies/rules": "PUT", "/api/policies/rules/rule": "DELETE", "/api/policies/evaluate": "POST",
			}[path]
			decodeM1Response[apitypes.Error](t, f.call(t, method, path, observerTestToken, "{}"), 403)
		}
		for _, tc := range []struct{ body, trust, effect string }{
			{`{"tool_id":"svc.develop"}`, "authenticated", "allow"},
			{`{"tool_id":"svc.develop","actor":"watcher"}`, "declared", "deny"},
		} {
			result := decodeM1Response[apitypes.PolicyEvaluateResult](t, f.call(t, "POST", "/api/policies/evaluate", adminTestToken, tc.body), 200)
			if result.Trust != tc.trust || result.Effect != tc.effect {
				t.Fatalf("shared dry run: %+v", result)
			}
		}
		if len(f.svcs.Activity.Recent()) != 4 {
			t.Fatal("one of the dry runs or queries created activity")
		}
	})
}

func TestRequestBaselineNarrowsSignedAndSimulatedScopeWithGlobalOff(t *testing.T) {
	f := newActorFixture(t, false)
	ctx := t.Context()
	cwd := t.TempDir()
	repo, err := f.svcs.Scope.SaveRepository(ctx, scope.SaveRepositoryRequest{Name: "Scoped", Path: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svcs.Scope.SaveScopeRule(scope.ScopeRule{RepoKey: repo.ID, Grants: []scope.ServiceGrant{{ServerID: "svc", Access: "ro", ToolIDs: []string{"svc.observe"}}}}); err != nil {
		t.Fatal(err)
	}
	if f.svcs.Scope.IsEnforced() {
		t.Fatal("fixture must have global enforcement off")
	}
	unscoped := decodeM1Response[apitypes.Catalog](t, f.call(t, "GET", "/api/catalog", "", ""), 200)
	if len(unscoped.Tools) != 3 {
		t.Fatalf("unscoped compatibility: %+v", unscoped)
	}
	query := "/api/catalog?sim_scope=" + repo.ID
	simulated := decodeM1Response[apitypes.Catalog](t, f.call(t, "GET", query, "", ""), 200)
	if len(simulated.Tools) != 1 || simulated.Tools[0].ID != "svc.observe" {
		t.Fatalf("simulated baseline was not applied: %+v", simulated)
	}

	signed := func(method, path, op, body string, args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		claims := signMCPArgs(t, f.svcs.DataDir, "codex", cwd, false, op, args)
		token, ok := claims[workspace.CurrentContextField()].(string)
		if !ok {
			t.Fatal("fixture did not produce a signed scope token")
		}
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("X-Gateway-Scope", token)
		out := httptest.NewRecorder()
		f.handler.ServeHTTP(out, req)
		return out
	}
	verified := decodeM1Response[apitypes.Catalog](t, signed("GET", "/api/catalog", "catalog", "", nil), 200)
	if len(verified.Tools) != 1 || verified.Tools[0].ID != "svc.observe" {
		t.Fatalf("signed scope baseline was not applied: %+v", verified)
	}
	args := map[string]any{"tool_id": "svc.deploy", "arguments": map[string]any{}}
	deny := decodeM1Response[apitypes.Error](t, signed("POST", "/api/invoke", "invoke_tool", string(mustJSON(t, args)), args), 403)
	if deny.Code != "policy_denied" {
		t.Fatalf("signed invoke denial: %+v", deny)
	}
	page, err := f.svcs.Activity.Query(activity.Filter{RepoID: repo.ID, Query: "svc.deploy", Decision: "deny"})
	if err != nil || len(page.Items) != 1 || page.Items[0].RuleID != "scope:"+repo.ID {
		t.Fatalf("signed scope audit: %+v, %v", page, err)
	}

	// Explicit deny also applies in the administrator's scope-bypass view.
	decodeM1Response[apitypes.PolicyRule](t, f.call(t, "PUT", "/api/policies/rules", "", `{"effect":"deny","enabled":true,"repo_key":"*","service_id":"svc","access":"ro"}`), 200)
	bypass := decodeM1Response[apitypes.Catalog](t, f.call(t, "GET", "/api/catalog?sim_scope=", "", ""), 200)
	if len(bypass.Tools) != 1 || bypass.Tools[0].ID != "svc.deploy" {
		t.Fatalf("bypass suppressed explicit denials: %+v", bypass)
	}
}

func decodeM1Response[T any](t *testing.T, got *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var out T
	if got.Code != status {
		t.Fatalf("HTTP %d, want %d: %s", got.Code, status, got.Body)
	}
	if err := json.Unmarshal(got.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode HTTP response %s: %v", got.Body, err)
	}
	return out
}
