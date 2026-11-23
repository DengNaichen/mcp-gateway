package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func TestBuildOverviewSnapshotAndHelpers(t *testing.T) {
	uiApp := NewDesktopUI(nil)
	state := &apitypes.Bootstrap{
		Registry: apitypes.Registry{
			Servers: []apitypes.Server{
				{ID: "gh", Name: "GitHub", Enabled: true},
				{ID: "demo", Name: "Demo", Enabled: true, Demo: true},
			},
			Tools: []apitypes.Tool{
				{ID: "gh.list", ServerID: "gh", Enabled: true},
				{ID: "gh.write", ServerID: "gh", Enabled: false},
				{ID: "demo.ping", ServerID: "demo", Enabled: true},
			},
		},
		Setup: apitypes.SetupStatus{
			ModelReady: true,
			SourceChecks: map[string]apitypes.ConnectionCheck{
				"gh": {Status: "ok", ToolCount: 2},
			},
		},
	}
	uiApp.State = state
	tel := GatewayTelemetry{
		TopTools: []TopToolItem{{ID: "gh.list", Hits: 7}},
	}
	snap := uiApp.buildOverviewSnapshot(state, tel)
	if len(snap.realServers) != 1 || len(snap.realTools) != 2 || len(snap.toolByID) != 3 {
		t.Fatalf("unexpected real/all counts: %+v", snap)
	}
	if snap.readyServerCount != 1 || snap.enabledRealToolCount != 1 || !snap.modelReady || snap.maxTopHits != 7 {
		t.Fatalf("unexpected readiness/hits snapshot: %+v", snap)
	}

	now := time.Now()
	buckets := []apitypes.PulseBucket{
		{Start: now.Add(-30 * time.Minute), End: now.Add(-15 * time.Minute), Total: 4, Error: 1},
		{Start: now.Add(-15 * time.Minute), End: now, Total: 2, Error: 0},
	}
	maxB, readout := ovPulseStats(buckets, -1)
	if maxB != 4 || readout == "" {
		t.Fatalf("unexpected pulse stats: max=%d readout=%q", maxB, readout)
	}
	_, hoveredReadout := ovPulseStats(buckets, 1)
	if hoveredReadout == readout {
		t.Fatalf("expected hovered bucket readout to differ from peak readout")
	}

	stText, kindText := ovLogStatusAndKindLabels(apitypes.Event{Kind: "discover", Status: "no_match"})
	if stText != "未命中工具" || kindText != "路由发现" {
		t.Fatalf("unexpected labels: %q / %q", stText, kindText)
	}
}

func TestOverviewApprovalStripAndHandoff(t *testing.T) {
	pinApprovalClock(t)

	empty := approvalBootstrap()
	u := NewDesktopUI(nil)
	u.State = &empty
	u.Loading = false
	tester := ui.NewTester(u.View, 1360, 900)
	if tester.HasText("前往处理") {
		t.Fatal("approval strip must not render without pending approvals")
	}

	older := testPendingApproval("apv-0", "carol", -5*time.Minute, time.Minute)
	boot := approvalBootstrap(older, testPendingApproval("apv-1", "alice", -time.Minute, 5*time.Minute))
	u = NewDesktopUI(nil)
	u.State = &boot
	u.Loading = false
	tester = ui.NewTester(u.View, 1360, 900)
	for _, want := range []string{"2 条待处理审批", "alice / alpha-repo / gh / gh.pr.merge", "剩余 5m 0s", "前往处理"} {
		if !tester.HasText(want) {
			t.Fatalf("approval strip missing %q: %v", want, tester.Texts())
		}
	}
	if err := tester.Click("前往处理"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	st := u.logsState()
	if u.CurrentView() != ViewLogs || st.decisionIdx != logsDecisionApproveIdx {
		t.Fatalf("handoff should land on Logs filtered to approve: view=%s decision=%d", u.CurrentView(), st.decisionIdx)
	}
	if st.approvals.selectedID != "apv-1" || st.selectedID != "ev-approve" {
		t.Fatalf("handoff should select the latest approval and its event: approval=%q event=%q", st.approvals.selectedID, st.selectedID)
	}
}
