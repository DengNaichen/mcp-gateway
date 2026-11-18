package main

import (
	"testing"
	"time"

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
