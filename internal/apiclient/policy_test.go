package apiclient

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"gatewaylab/internal/apitypes"
)

func TestPolicyAndAuditRequests(t *testing.T) {
	filter := apitypes.EventFilter{
		Actor: "owner", RepoID: "*", ServiceID: "svc", Protocol: "mcp", Decision: "deny",
		Status: "error", Query: "a + b", Before: "2026-11-23T12:00:00+08:00|event-id", Limit: 37,
	}
	wantQuery := url.Values{
		"actor": {"owner"}, "repo_id": {"*"}, "service_id": {"svc"}, "protocol": {"mcp"},
		"decision": {"deny"}, "status": {"error"}, "q": {"a + b"}, "before": {filter.Before}, "limit": {"37"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/policies":
			_, _ = w.Write([]byte(`{"enforce":true,"baseline_from":"scope_rules","rules":[]}`))
		case "PUT /api/policies/rules":
			var rule apitypes.PolicyRule
			if err := json.NewDecoder(r.Body).Decode(&rule); err != nil || rule.Effect != "deny" {
				t.Errorf("rule payload: %+v, %v", rule, err)
			}
			_, _ = w.Write([]byte(`{"id":"pol-test","effect":"deny"}`))
		case "DELETE /api/policies/rules/pol-test":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case "POST /api/policies/evaluate":
			var req apitypes.PolicyEvaluateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ToolID != "svc.get" || req.RepoID != "*" || req.Actor != "owner" {
				t.Errorf("evaluation payload: %+v, %v", req, err)
			}
			_, _ = w.Write([]byte(`{"effect":"deny","rule_id":"scope:*","trace":[],"trust":"declared"}`))
		case "GET /api/events/query":
			if got := r.URL.Query(); !maps.EqualFunc(got, wantQuery, slices.Equal[[]string]) {
				t.Errorf("audit query: %v, want %v", got, wantQuery)
			}
			_, _ = w.Write([]byte(`{"items":[{"id":"event-id","decision":"deny"}],"next_cursor":"next","has_more":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	ctx := t.Context()
	client := New(srv.URL)
	if got, err := client.Policies(ctx); err != nil || !got.Enforce || got.BaselineFrom != "scope_rules" {
		t.Fatalf("policies: %+v, %v", got, err)
	}
	if got, err := client.SavePolicyRule(ctx, apitypes.PolicyRule{Effect: "deny"}); err != nil || got.ID != "pol-test" {
		t.Fatalf("save: %+v, %v", got, err)
	}
	if err := client.DeletePolicyRule(ctx, "pol-test"); err != nil {
		t.Fatal(err)
	}
	if got, err := client.EvaluatePolicy(ctx, apitypes.PolicyEvaluateRequest{ToolID: "svc.get", RepoID: "*", Actor: "owner"}); err != nil || got.RuleID != "scope:*" || got.Trust != "declared" {
		t.Fatalf("evaluate: %+v, %v", got, err)
	}
	if page, err := client.QueryEvents(ctx, filter); err != nil || len(page.Items) != 1 || page.Items[0].Decision != "deny" || page.NextCursor != "next" || !page.HasMore {
		t.Fatalf("query: %+v, %v", page, err)
	}
}
