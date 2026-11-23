package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

var approvalTestNow = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

func pinApprovalClock(t *testing.T) {
	t.Helper()
	prev := approvalNow
	approvalNow = func() time.Time { return approvalTestNow }
	t.Cleanup(func() { approvalNow = prev })
}

func approvalTS(d time.Duration) string {
	return approvalTestNow.Add(d).Format(time.RFC3339Nano)
}

func testPendingApproval(id, actor string, created, expiresIn time.Duration) apitypes.Approval {
	return apitypes.Approval{
		ID: id, Status: "pending", Actor: actor, Agent: "codex",
		RepoID: "repo-1", RepoName: "alpha-repo", Protocol: "cli", ServiceID: "gh", Action: "gh.pr.merge",
		RuleID: "policy:pol-2", Summary: "gh pr merge 482 --squash", ArgsRedacted: `{"pr":482,"token":"[redacted]"}`,
		CreatedAt: approvalTS(created), PendingExpiresAt: approvalTS(expiresIn),
	}
}

func approvalBootstrap(approvals ...apitypes.Approval) apitypes.Bootstrap {
	boot := logsLedgerBootstrap()
	boot.Approvals = approvals
	for _, a := range approvals {
		if a.Status == "pending" {
			boot.PendingApprovals++
		}
	}
	boot.Events = append([]apitypes.Event{{
		ID: "ev-approve", Time: approvalTestNow, Kind: "invoke", Status: "pending", Title: "gh pr merge 482",
		Protocol: "cli", Decision: "approve", ServiceID: "gh", RepoID: "repo-1", Action: "gh.pr.merge", ApprovalID: "apv-1",
	}}, boot.Events...)
	return boot
}

func TestApprovalHelpers(t *testing.T) {
	older := testPendingApproval("apv-old", "alice", -10*time.Minute, 5*time.Minute)
	newer := testPendingApproval("apv-new", "bob", -time.Minute, 4*time.Minute+12*time.Second)
	decided := older
	decided.ID, decided.Status = "apv-done", "approved"
	state := &apitypes.Bootstrap{Approvals: []apitypes.Approval{older, decided, newer}}

	got := pendingApprovals(state)
	if len(got) != 2 || got[0].ID != "apv-new" || got[1].ID != "apv-old" {
		t.Fatalf("pending approvals newest-first = %+v", got)
	}
	if line := approvalSourceLine(newer); line != "bob / alpha-repo / gh / gh.pr.merge" {
		t.Fatalf("source line = %q", line)
	}
	if ttl := approvalTTLLabel(newer, approvalTestNow); ttl != "剩余 4m 12s" {
		t.Fatalf("ttl = %q", ttl)
	}
	expired := testPendingApproval("apv-exp", "alice", -10*time.Minute, -time.Second)
	if !approvalExpired(expired, approvalTestNow) || approvalTTLLabel(expired, approvalTestNow) != "已过期" {
		t.Fatal("expected lapsed pending window to read as expired")
	}

	shared := apitypes.AccessInfo{Shared: true, Actor: "alice"}
	local := apitypes.AccessInfo{Actor: "alice"}
	if b := approvalBlock(older, shared, approvalTestNow); b != "禁止自批：须由其他管理员处理" {
		t.Fatalf("shared self-approval block = %q", b)
	}
	if b := approvalBlock(newer, shared, approvalTestNow); b != "" {
		t.Fatalf("other admin should be able to decide, got %q", b)
	}
	if b := approvalBlock(older, local, approvalTestNow); b != "" {
		t.Fatalf("local guardrail mode must not apply self-approval block, got %q", b)
	}
	if b := approvalBlock(expired, shared, approvalTestNow); b == "" {
		t.Fatal("expired approval must be blocked")
	}
	if b := approvalBlock(decided, shared, approvalTestNow); b == "" {
		t.Fatal("decided approval must be blocked")
	}
}

func TestLogsNavItemPendingBadge(t *testing.T) {
	if item := logsNavItem(12, 0); item.badge != "12" || item.badgeDanger {
		t.Fatalf("no pending approvals should show event count, got %+v", item)
	}
	if item := logsNavItem(12, 3); item.badge != "3" || !item.badgeDanger {
		t.Fatalf("pending approvals should show a red count, got %+v", item)
	}

	pinApprovalClock(t)
	boot := approvalBootstrap(testPendingApproval("apv-1", "alice", -time.Minute, 5*time.Minute))
	u := NewDesktopUI(nil)
	u.State = &boot
	u.Loading = false
	tester := ui.NewTester(u.View, 1360, 900)
	if err := tester.Click("请求日志"); err != nil {
		t.Fatal(err)
	}
	if u.CurrentView() != ViewLogs || !tester.HasText("待审批队列") {
		t.Fatalf("sidebar logs item should open the review queue: %v", tester.Texts())
	}
}

type approvalBackend struct {
	mu        sync.Mutex
	boot      apitypes.Bootstrap
	decisions []string
	notes     []string
}

func (b *approvalBackend) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case r.URL.Path == "/api/bootstrap":
		_ = json.NewEncoder(w).Encode(b.boot)
	case strings.HasPrefix(r.URL.Path, "/api/approvals/") && r.Method == http.MethodPost:
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/approvals/"), "/")
		var req apitypes.ApprovalDecisionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		b.decisions = append(b.decisions, parts[0]+":"+parts[1])
		b.notes = append(b.notes, req.Note)
		status := map[string]string{"approve": "approved", "deny": "denied"}[parts[1]]
		var out apitypes.Approval
		for i := range b.boot.Approvals {
			if b.boot.Approvals[i].ID == parts[0] {
				b.boot.Approvals[i].Status = status
				b.boot.Approvals[i].DecidedBy = "bob"
				b.boot.Approvals[i].DecisionNote = req.Note
				out = b.boot.Approvals[i]
			}
		}
		b.boot.PendingApprovals = 0
		_ = json.NewEncoder(w).Encode(out)
	default:
		_, _ = w.Write([]byte(`{}`))
	}
}

func (b *approvalBackend) snapshot() ([]string, []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.decisions...), append([]string(nil), b.notes...)
}

func newApprovalTestUI(t *testing.T, boot apitypes.Bootstrap) (*DesktopUI, *approvalBackend, *ui.Tester) {
	t.Helper()
	pinApprovalClock(t)
	backend := &approvalBackend{boot: boot}
	backend.boot.Approvals = append([]apitypes.Approval(nil), boot.Approvals...) // the UI must not share backend rows
	srv := httptest.NewServer(http.HandlerFunc(backend.handler))
	t.Cleanup(srv.Close)
	u := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, Mode: modeAttached})
	state := boot
	u.State = &state
	u.Loading = false
	tester := ui.NewTester(func(c *ui.Context) {
		defer u.enterHeadlessFrame()()
		u.renderLogs(c)
	}, 1400, 1400)
	return u, backend, tester
}

func TestLogsApprovalDecisionsCallBackendAndRefresh(t *testing.T) {
	for _, tc := range []struct {
		name, button, want string
	}{
		{name: "approve", button: "批准", want: "apv-1:approve"},
		{name: "deny", button: "拒绝", want: "apv-1:deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, backend, tester := newApprovalTestUI(t, approvalBootstrap(testPendingApproval("apv-1", "alice", -time.Minute, 5*time.Minute)))
			for _, want := range []string{"待审批队列", "审批检视", "gh pr merge 482 --squash", `{"pr":482,"token":"[redacted]"}`, "申请来源", "codex", "policy:pol-2", "剩余 5m 0s", "本地护栏模式：通过桌面进程内管理令牌裁决"} {
				if !tester.HasText(want) {
					t.Fatalf("review panel missing %q: %v", want, tester.Texts())
				}
			}
			*u.logsState().approvals.note("apv-1") = "  checked diff  "
			if err := tester.Click(tc.button); err != nil {
				t.Fatal(err)
			}
			waitForUI(t, tester, u, func() bool { return u.State.PendingApprovals == 0 && u.BusyCount == 0 })
			decisions, notes := backend.snapshot()
			if len(decisions) != 1 || decisions[0] != tc.want || notes[0] != "checked diff" {
				t.Fatalf("decisions=%v notes=%q", decisions, notes)
			}
			if tester.HasText("待审批队列") {
				t.Fatal("queue should disappear after the last pending approval is decided")
			}
			if logsNavItem(len(u.State.Events), u.State.PendingApprovals).badgeDanger {
				t.Fatal("sidebar badge should fall back once nothing is pending")
			}
			// The linked approval card on the audit event shows the verdict.
			for _, want := range []string{"关联审批", "decided_by", "bob", "checked diff"} {
				if !tester.HasText(want) {
					t.Fatalf("linked approval missing %q: %v", want, tester.Texts())
				}
			}
		})
	}
}

func TestLogsApprovalBlocksSelfAndExpired(t *testing.T) {
	cases := []struct {
		name   string
		access apitypes.AccessInfo
		a      apitypes.Approval
		hint   string
	}{
		{
			name:   "sharedSelf",
			access: apitypes.AccessInfo{Shared: true, SecurityMode: apitypes.SecurityModeSharedBoundary, Actor: "alice"},
			a:      testPendingApproval("apv-1", "alice", -time.Minute, 5*time.Minute),
			hint:   "禁止自批：须由其他管理员处理",
		},
		{
			name:   "expired",
			access: apitypes.AccessInfo{Actor: "developer"},
			a:      testPendingApproval("apv-1", "alice", -10*time.Minute, -time.Second),
			hint:   "审批单已过期，须由调用方重新发起。",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boot := approvalBootstrap(tc.a)
			boot.Access = tc.access
			u, backend, tester := newApprovalTestUI(t, boot)
			if !tester.HasText(tc.hint) {
				t.Fatalf("missing block hint %q: %v", tc.hint, tester.Texts())
			}
			_ = tester.Click("批准")
			_ = tester.Click("拒绝")
			tester.Frame()
			time.Sleep(20 * time.Millisecond)
			tester.Frame()
			if decisions, _ := backend.snapshot(); len(decisions) != 0 || u.logsState().approvals.deciding != "" {
				t.Fatalf("blocked approval must not be decided, got %v", decisions)
			}
		})
	}
}

func TestLogsApprovalQueueSwitchesSelection(t *testing.T) {
	first := testPendingApproval("apv-1", "alice", -time.Minute, 5*time.Minute)
	second := testPendingApproval("apv-2", "carol", -2*time.Minute, 3*time.Minute)
	second.Summary = "gh release create v2"
	u, _, tester := newApprovalTestUI(t, approvalBootstrap(first, second))
	if u.logsState().approvals.selectedID != "apv-1" {
		t.Fatalf("newest approval should be selected first, got %q", u.logsState().approvals.selectedID)
	}
	if err := tester.Click("gh release create v2"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if u.logsState().approvals.selectedID != "apv-2" || !tester.HasText("carol") {
		t.Fatalf("clicking a queue row should switch the review panel: %v", tester.Texts())
	}
}
