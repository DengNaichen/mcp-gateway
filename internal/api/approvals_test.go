package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
)

func newApprovalFixture(t *testing.T, shared bool, localToken string) *actorFixture {
	t.Helper()
	f := newActorFixtureWithOptions(t, shared, gateway.Options{LocalAdminToken: localToken})
	tool := registry.Tool{ID: "svc.change", ServerID: "svc", Name: "change", Description: "Change status", Enabled: true, Actors: []string{"developer", "observer"},
		Method: "POST", Path: "/change", InputSchema: objectSchema(map[string]any{"password": map[string]any{"type": "string"}, "value": map[string]any{"type": "string"}})}
	if err := f.svcs.Registry.SaveTool(tool); err != nil {
		t.Fatal(err)
	}
	_, err := f.g.SavePolicyRule(policy.Rule{Enabled: true, Effect: "approve", RepoKey: "*", ServiceID: "svc", Access: "rw"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func localApprovalRequest(f *actorFixture, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
	req.Header.Set("X-Gateway-Admin-Token", adminTestToken)
	out := httptest.NewRecorder()
	f.handler.ServeHTTP(out, req)
	return out
}

func requireChallenge(t *testing.T, out *httptest.ResponseRecorder, status string) string {
	t.Helper()
	r := decodeM1Response[apitypes.InvokeResult](t, out, 202)
	if r.Status != status || r.Approval == nil || !strings.HasPrefix(r.Approval.ApprovalID, "apv_") || r.Approval.ExpiresIn <= 0 || r.Approval.Summary == "" || r.Approval.Message == "" {
		t.Fatalf("invalid challenge: %+v", r)
	}
	return r.Approval.ApprovalID
}

func requireApprovalError(t *testing.T, out *httptest.ResponseRecorder, code string) {
	t.Helper()
	if e := decodeM1Response[apitypes.Error](t, out, 403); e.Code != code {
		t.Fatalf("got %+v, want %s", e, code)
	}
}

func decideLocal(t *testing.T, f *actorFixture, id, decision string) apitypes.Approval {
	t.Helper()
	return decodeM1Response[apitypes.Approval](t, localApprovalRequest(f, "POST", "/api/approvals/"+id+"/"+decision, `{"note":"reviewed"}`), 200)
}

const changeBody = `{"tool_id":"svc.change","arguments":{}}`

func retryBody(id string) string {
	return fmt.Sprintf(`{"tool_id":"svc.change","arguments":{},"approval_id":%q}`, id)
}

func TestApprovalBindingsAndAudit(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	roots := []string{t.TempDir(), t.TempDir()}
	for _, root := range roots {
		if _, err := f.svcs.Scope.SaveRepository(t.Context(), scope.SaveRepositoryRequest{Name: filepath.Base(root), Path: root}); err != nil {
			t.Fatal(err)
		}
	}
	args := map[string]any{"password": "fixture-sensitive", "value": "release"}
	in := map[string]any{"tool_id": "svc.change", "arguments": args}
	invoke := func(root string) *httptest.ResponseRecorder {
		return signedM1Request(t, f, root, "POST", "/api/invoke", "invoke_tool", in)
	}
	id := requireChallenge(t, invoke(roots[0]), "approval_required")
	in["fields"] = []string{"status"}
	if got := requireChallenge(t, invoke(roots[0]), "approval_required"); got != id {
		t.Fatal("projection changed binding")
	}
	in["approval_id"] = id
	if got := requireChallenge(t, invoke(roots[0]), "approval_pending"); got != id {
		t.Fatal("poll created another request")
	}
	boot := decodeM1Response[apitypes.Bootstrap](t, localApprovalRequest(f, "GET", "/api/bootstrap", ""), 200)
	if boot.PendingApprovals != 1 || len(boot.Approvals) != 1 || boot.Approvals[0].ID != id || strings.Contains(boot.Approvals[0].ArgsRedacted+boot.Approvals[0].Summary, "fixture-sensitive") || !strings.Contains(boot.Approvals[0].ArgsRedacted, "[redacted]") {
		t.Fatalf("bootstrap approvals: %+v", boot.Approvals)
	}
	a := decideLocal(t, f, id, "approve")
	if a.Status != "approved" || a.DecidedBy != "local_admin" || a.Agent != "codex" || a.RequesterSession != "test-sid" || a.RuleID == "" {
		t.Fatalf("wrong decision: %+v", a)
	}
	start, _ := time.Parse(time.RFC3339Nano, a.ApprovedAt)
	end, _ := time.Parse(time.RFC3339Nano, a.ExpiresAt)
	if end.Sub(start) != 5*time.Minute {
		t.Fatal("wrong execution window")
	}
	requireApprovalError(t, invoke(roots[1]), "approval_invalid")
	args["password"] = "changed"
	requireApprovalError(t, invoke(roots[0]), "approval_invalid")
	args["password"] = "fixture-sensitive"
	in["actor"] = "observer"
	requireApprovalError(t, invoke(roots[0]), "approval_invalid")
	delete(in, "actor")
	reg := f.svcs.Registry.Snapshot()
	for _, tool := range reg.Tools {
		if tool.ID != "svc.change" {
			continue
		}
		tool.InputSchema["description"] = "revised schema"
		if err := f.svcs.Registry.SaveTool(tool); err != nil {
			t.Fatal(err)
		}
		requireApprovalError(t, invoke(roots[0]), "approval_invalid")
		delete(tool.InputSchema, "description")
		if err := f.svcs.Registry.SaveTool(tool); err != nil {
			t.Fatal(err)
		}
	}

	if f.developerCalls.Load() != 0 {
		t.Fatal("unapproved execution")
	}
	decodeM1Response[apitypes.InvokeResult](t, invoke(roots[0]), 200)
	requireApprovalError(t, invoke(roots[0]), "approval_invalid")
	if f.developerCalls.Load() != 1 {
		t.Fatal("wrong execution count")
	}
	rows := decodeM1Response[[]apitypes.Approval](t, localApprovalRequest(f, "GET", "/api/approvals?status=consumed&limit=1", ""), 200)
	if len(rows) != 1 || rows[0].ConsumedAt == "" {
		t.Fatalf("consumed rows: %+v", rows)
	}
	events := decodeM1Response[apitypes.EventPage](t, localApprovalRequest(f, "GET", "/api/events/query?service_id=svc", ""), 200)
	seen := map[string]bool{}
	for _, e := range events.Items {
		if e.Kind == "invoke" && e.ApprovalID == id && e.RuleID == a.RuleID && e.Decision == "approve" {
			seen[e.Status] = true
		}
	}
	if !seen["pending"] || !seen["success"] || !seen["error"] {
		t.Fatalf("missing audit events: %v", seen)
	}
}

func TestApprovalConcurrentSingleExecution(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	id := requireChallenge(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required")
	decideLocal(t, f, id, "approve")
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 10)
	for range 10 {
		go func() { <-start; results <- f.call(t, "POST", "/api/invoke", "", retryBody(id)) }()
	}
	close(start)
	wins := 0
	for range 10 {
		out := <-results
		if out.Code == 200 {
			wins++
			continue
		}
		e := decodeM1Response[apitypes.Error](t, out, 403)
		if e.Code != "approval_already_used" && e.Code != "approval_invalid" {
			t.Fatalf("unexpected denial: %+v", e)
		}
	}
	if wins != 1 || f.developerCalls.Load() != 1 {
		t.Fatalf("successes=%d upstream executions=%d", wins, f.developerCalls.Load())
	}
}

func TestApprovalAdminBoundaries(t *testing.T) {
	t.Run("shared", func(t *testing.T) {
		f := newApprovalFixture(t, true, "")
		request := func(token string) string {
			return requireChallenge(t, f.call(t, "POST", "/api/invoke", token, changeBody), "approval_required")
		}
		own := request(adminTestToken)
		requireApprovalError(t, f.call(t, "POST", "/api/approvals/"+own+"/approve", adminTestToken, "{}"), "self_approval_forbidden")
		id := request(observerTestToken)
		for _, method := range []string{"approve", "deny"} {
			decodeM1Response[apitypes.Error](t, f.call(t, "POST", "/api/approvals/"+id+"/"+method, observerTestToken, "{}"), 403)
		}
		decodeM1Response[apitypes.Error](t, f.call(t, "GET", "/api/approvals", observerTestToken, ""), 403)
		a := decodeM1Response[apitypes.Approval](t, f.call(t, "POST", "/api/approvals/"+id+"/approve", adminTestToken, "{}"), 200)
		if a.DecidedBy != "owner" || a.Actor != "watcher" {
			t.Fatalf("wrong identities: %+v", a)
		}
		decodeM1Response[apitypes.InvokeResult](t, f.call(t, "POST", "/api/invoke", observerTestToken, retryBody(id)), 200)
	})
	t.Run("local header required", func(t *testing.T) {
		f := newApprovalFixture(t, false, adminTestToken)
		id := requireChallenge(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required")
		for _, token := range []string{"", adminTestToken} {
			decodeM1Response[apitypes.Error](t, f.call(t, "GET", "/api/approvals", token, ""), 403)
			for _, action := range []string{"approve", "deny"} {
				decodeM1Response[apitypes.Error](t, f.call(t, "POST", "/api/approvals/"+id+"/"+action, token, "{}"), 403)
			}
		}
		decideLocal(t, f, id, "deny")
		requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), "approval_invalid")
	})
	t.Run("tokenless compatibility", func(t *testing.T) {
		f := newApprovalFixture(t, false, "")
		requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required_not_enabled")
		for _, action := range []string{"approve", "deny"} {
			decodeM1Response[apitypes.Error](t, localApprovalRequest(f, "POST", "/api/approvals/missing/"+action, "{}"), 403)
		}
		if f.developerCalls.Load() != 0 {
			t.Fatal("tokenless invoke reached upstream")
		}
	})
}

func TestApprovalPolicyTightened(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	id := requireChallenge(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required")
	decideLocal(t, f, id, "approve")
	if _, err := f.g.SavePolicyRule(policy.Rule{Enabled: true, Effect: "deny", RepoKey: "*", ServiceID: "svc", Access: "rw"}); err != nil {
		t.Fatal(err)
	}
	requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), "policy_denied")
	requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), "approval_invalid")
	rows := decodeM1Response[[]apitypes.Approval](t, localApprovalRequest(f, "GET", "/api/approvals?status=denied", ""), 200)
	if len(rows) != 1 || rows[0].ID != id || f.developerCalls.Load() != 0 {
		t.Fatalf("revocation failed: %+v", rows)
	}
}

func TestApprovalDeadlines(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	db, err := sql.Open("sqlite", filepath.Join(f.svcs.DataDir, "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, state := range []string{"pending", "approved"} {
		id := requireChallenge(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required")
		if state == "approved" {
			decideLocal(t, f, id, "approve")
		}
		// Move only the respective deadline into the past, keeping real HTTP
		// retries and transitions on the normal gateway clock.
		_, err := db.Exec(`UPDATE approvals SET pending_expires_at=CASE WHEN status='pending' THEN '2000' ELSE pending_expires_at END,
expires_at=CASE WHEN status='approved' THEN '2000' ELSE expires_at END WHERE id=?`, id)
		if err != nil {
			t.Fatal(err)
		}
		requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), "approval_invalid")
		requireApprovalError(t, localApprovalRequest(f, "POST", "/api/approvals/"+id+"/approve", "{}"), "approval_invalid")
	}
	if f.developerCalls.Load() != 0 {
		t.Fatal("expired request reached upstream")
	}
}

func TestApprovalFloodLimitAndReuse(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	call := func(agent string, n int) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"tool_id":"svc.change","arguments":{"value":"%d"}}`, n)
		return f.call(t, "POST", "/api/invoke?agent="+agent, "", body)
	}
	first := requireChallenge(t, call("codex", 0), "approval_required")
	for i := 1; i < 20; i++ {
		requireChallenge(t, call("codex", i), "approval_required")
	}
	decodeM1Response[apitypes.Error](t, call("codex", 20), 429)
	if id := requireChallenge(t, call("codex", 0), "approval_required"); id != first {
		t.Fatal("pending approval not reused at limit")
	}
	if id := requireChallenge(t, call("claude", 0), "approval_required"); id == first {
		t.Fatal("agents shared approval")
	}
	requireApprovalError(t, f.call(t, "POST", "/api/invoke?agent=claude", "", fmt.Sprintf(`{"tool_id":"svc.change","arguments":{"value":"0"},"approval_id":%q}`, first)), "approval_invalid")
	decideLocal(t, f, first, "approve")
	if id := requireChallenge(t, call("codex", 0), "approval_required"); id != first {
		t.Fatal("approved request not reused")
	}
	requireChallenge(t, call("codex", 20), "approval_required")
}

func TestApprovalMCPChallengeAndRetry(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	host := httptest.NewServer(f.handler)
	defer host.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "approval-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp?agent=codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(session)
	args := map[string]any{"tool_id": "svc.change", "arguments": map[string]any{}}
	invoke := func() apitypes.InvokeResult {
		t.Helper()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: args})
		if err != nil || result.IsError {
			t.Fatalf("invoke_tool: %+v %v", result, err)
		}
		var out apitypes.InvokeResult
		if err := json.Unmarshal(mustJSON(t, result.StructuredContent), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	r := invoke()
	if r.Status != "approval_required" || r.Approval == nil {
		t.Fatalf("MCP challenge: %+v", r)
	}
	args["approval_id"] = r.Approval.ApprovalID
	if pending := invoke(); pending.Status != "approval_pending" {
		t.Fatalf("poll: %+v", pending)
	}
	decideLocal(t, f, r.Approval.ApprovalID, "approve")
	if result := invoke(); result.Data == nil || f.developerCalls.Load() != 1 {
		t.Fatalf("MCP result: %+v", result)
	}
}

func TestApprovalConsumedAfterUpstreamError(t *testing.T) {
	f := newApprovalFixture(t, false, adminTestToken)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.developerCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	if err := f.svcs.Registry.SaveServer(registry.Server{ID: "svc", Name: "Service", Kind: "http", Endpoint: srv.URL, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	id := requireChallenge(t, f.call(t, "POST", "/api/invoke", "", changeBody), "approval_required")
	decideLocal(t, f, id, "approve")
	decodeM1Response[apitypes.Error](t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), 502)
	requireApprovalError(t, f.call(t, "POST", "/api/invoke", "", retryBody(id)), "approval_invalid")
	row := decodeM1Response[[]apitypes.Approval](t, localApprovalRequest(f, "GET", "/api/approvals?status=consumed", ""), 200)
	if len(row) != 1 || row[0].ID != id || f.developerCalls.Load() != 1 {
		t.Fatal("failed execution was not consumed exactly once")
	}
}
