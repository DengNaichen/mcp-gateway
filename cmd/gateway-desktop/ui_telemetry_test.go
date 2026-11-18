package main

import (
	"encoding/json"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"
)

func TestComputeGatewayTelemetryTableDriven(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"emptyState", func(t *testing.T) {
			tel := ComputeGatewayTelemetry(apitypes.Bootstrap{})
			if tel.SampleSize != 0 || tel.HitRatePct != nil || tel.SuccessRatePct != nil ||
				tel.AvgEligible != nil || tel.CompressionPct != nil || tel.P50DiscoverMs != nil {
				t.Fatalf("empty state must not produce rates: %+v", tel)
			}
			if len(tel.PulseBuckets) != 48 {
				t.Fatalf("expected 48 pulse buckets, got %d", len(tel.PulseBuckets))
			}
		}},
		{"needsContextAndNoMatchAreUnmatchedNotErrors", func(t *testing.T) {
			boot := apitypes.Bootstrap{
				Events: []apitypes.Event{
					{ID: "a", Time: now, Kind: "discover", Status: "needs_context", Title: "more info"},
					{ID: "b", Time: now, Kind: "discover", Status: "no_match", Title: "nothing"},
					{ID: "c", Time: now, Kind: "discover", Status: "selected", Title: "hit"},
				},
			}
			tel := ComputeGatewayTelemetry(boot)
			if tel.ErrorCount != 0 || tel.NoMatchCount != 1 || tel.NeedsContextCount != 1 || tel.SelectedCount != 1 {
				t.Fatalf("unexpected counts: %+v", tel)
			}
			if len(tel.AttentionItems) != 2 {
				t.Fatalf("expected 2 attention items, got %+v", tel.AttentionItems)
			}
		}},
		{"toolIDFallbackAndTokenSum", func(t *testing.T) {
			detail, _ := json.Marshal(map[string]any{
				"prompt_tokens": 5,
				"output_tokens": 2,
				"tools":         []map[string]any{{"tool_id": "srv.a"}},
			})
			boot := apitypes.Bootstrap{
				Registry: apitypes.Registry{
					Tools: []apitypes.Tool{{ID: "srv.a", ServerID: "srv", Name: "a", ReadOnly: true}},
				},
				Events: []apitypes.Event{
					{ID: "x", Time: now, Kind: "invoke", Status: "selected", Title: "t", Detail: detail},
				},
			}
			tel := ComputeGatewayTelemetry(boot)
			if tel.TotalTokens != 7 || tel.ToolHitCounts["srv.a"] != 1 {
				t.Fatalf("unexpected tokens/hits: %+v", tel)
			}
			if len(tel.TopTools) != 1 || tel.TopTools[0].ServerID != "srv" || !tel.TopTools[0].ReadOnly {
				t.Fatalf("unexpected top tools: %+v", tel.TopTools)
			}
		}},
		{"percentileAndInvokeAverage", func(t *testing.T) {
			boot := apitypes.Bootstrap{
				Events: []apitypes.Event{
					{ID: "1", Time: now, Kind: "discover", Status: "selected", DurationMS: 10},
					{ID: "2", Time: now, Kind: "discover", Status: "selected", DurationMS: 20},
					{ID: "3", Time: now, Kind: "invoke", Status: "selected", DurationMS: 100},
					{ID: "4", Time: now, Kind: "invoke", Status: "selected", DurationMS: 300},
				},
			}
			tel := ComputeGatewayTelemetry(boot)
			if tel.P50DiscoverMs == nil || *tel.P50DiscoverMs != 10 {
				t.Fatalf("unexpected p50: %v", tel.P50DiscoverMs)
			}
			if tel.P95DiscoverMs == nil || *tel.P95DiscoverMs != 20 {
				t.Fatalf("unexpected p95: %v", tel.P95DiscoverMs)
			}
			if tel.AvgInvokeMs == nil || *tel.AvgInvokeMs != 200 {
				t.Fatalf("unexpected avg invoke: %v", tel.AvgInvokeMs)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func TestTelemetryHelpers(t *testing.T) {
	if got := telemetryToolID("srv.tool"); got != "srv.tool" {
		t.Fatalf("telemetryToolID string = %q", got)
	}
	if got := telemetryToolID(map[string]any{"tool_id": "srv.b"}); got != "srv.b" {
		t.Fatalf("telemetryToolID tool_id = %q", got)
	}
	if got := telemetryToolID(map[string]any{"id": ""}); got != "" {
		t.Fatalf("telemetryToolID empty id = %q", got)
	}

	if got := telemetryErrorSummary(map[string]any{"error": "first\nsecond"}); got != "first" {
		t.Fatalf("telemetryErrorSummary error = %q", got)
	}
	if got := telemetryErrorSummary(map[string]any{"question": "why?"}); got != "why?" {
		t.Fatalf("telemetryErrorSummary question = %q", got)
	}

	if telemetryPercent(0, 0) != nil {
		t.Fatal("telemetryPercent 0/0 must be nil")
	}
	if p := telemetryPercent(1, 2); p == nil || *p != 50 {
		t.Fatalf("telemetryPercent 1/2 = %v", p)
	}

	eligible := 10.0
	selected := 2.0
	if p := telemetryCompression(&eligible, &selected); p == nil || *p != 80 {
		t.Fatalf("telemetryCompression = %v", p)
	}
	if telemetryCompression(nil, &selected) != nil || telemetryCompression(&eligible, nil) != nil {
		t.Fatal("telemetryCompression must be nil without both inputs")
	}
}
