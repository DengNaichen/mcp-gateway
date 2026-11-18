package main

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// overviewLocalState holds UI-thread state local to the Overview page.
type overviewLocalState struct {
	bannerDismissed   bool
	testingModel      bool
	testErrTitle      string
	testErrDetail     string
	rightTab          int // 0: 服务, 1: 高频工具
	hoveredPulseIdx   int
	drawerOpen        bool
	drawerType        string // "log", "service", "tool"
	drawerID          string
	drawerBindingRepo bool
}

var (
	ovIconClose = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
	ovIconPlus  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`))
)

func (u *DesktopUI) overviewState() *overviewLocalState {
	if u.overview == nil {
		u.overview = &overviewLocalState{hoveredPulseIdx: -1}
	}
	return u.overview
}

func (st *overviewLocalState) openDrawer(kind, id string) {
	st.drawerType = kind
	st.drawerID = id
	st.drawerOpen = true
}

func (u *DesktopUI) overviewQuickVerifyModel(ov *overviewLocalState) {
	if ov.testingModel || u.Client == nil || u.State == nil {
		return
	}
	settings := u.State.Settings
	ov.testingModel = true
	ov.testErrTitle = ""
	ov.testErrDetail = ""

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()

		_, err := u.Client.TestModel(ctx, settings, "", false, "")
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() {
					ov.testingModel = false
				})
				return err
			}
			title, detail := splitAPIError(err)
			u.updateUI(func() {
				ov.testingModel = false
				ov.testErrTitle = title
				ov.testErrDetail = detail
			})
			return nil
		}

		_ = u.RefreshSync()
		u.updateUI(func() {
			ov.testingModel = false
			ov.testErrTitle = ""
			ov.testErrDetail = ""
		})
		u.Notify(i18n.T("overview.toast.model_check_passed"))
		return nil
	})
}

type overviewSnapshot struct {
	realServers          []apitypes.Server
	realTools            []apitypes.Tool
	toolsByServer        map[string][]apitypes.Tool
	toolByID             map[string]apitypes.Tool
	readyServerCount     int
	errServerCount       int
	pendingServerCount   int
	enabledRealToolCount int
	modelReady           bool
	maxTopHits           int
}

func (u *DesktopUI) buildOverviewSnapshot(state *apitypes.Bootstrap, telemetry GatewayTelemetry) overviewSnapshot {
	realServers := make([]apitypes.Server, 0, len(state.Registry.Servers))
	realServerMap := make(map[string]bool, len(state.Registry.Servers))
	for _, s := range state.Registry.Servers {
		if !s.Demo {
			realServers = append(realServers, s)
			realServerMap[s.ID] = true
		}
	}

	realTools := make([]apitypes.Tool, 0, len(state.Registry.Tools))
	toolsByServer := make(map[string][]apitypes.Tool, len(realServers))
	toolByID := make(map[string]apitypes.Tool, len(state.Registry.Tools))
	for _, t := range state.Registry.Tools {
		toolByID[t.ID] = t
		if realServerMap[t.ServerID] {
			realTools = append(realTools, t)
			toolsByServer[t.ServerID] = append(toolsByServer[t.ServerID], t)
		}
	}

	readyServerCount := 0
	readyServerMap := make(map[string]bool, len(realServers))
	errServerCount := 0
	pendingServerCount := 0
	for _, s := range realServers {
		r := u.readinessForServer(s, toolsByServer[s.ID])
		switch {
		case r.IsHealthyReady():
			readyServerCount++
			readyServerMap[s.ID] = true
		case r.HasError():
			errServerCount++
		case r.NeedsAttention():
			pendingServerCount++
		}
	}

	enabledRealToolCount := 0
	for _, t := range realTools {
		if t.Enabled && readyServerMap[t.ServerID] {
			enabledRealToolCount++
		}
	}

	setup := state.Setup
	maxTopHits := 1
	if len(telemetry.TopTools) > 0 && telemetry.TopTools[0].Hits > 0 {
		maxTopHits = telemetry.TopTools[0].Hits
	}

	return overviewSnapshot{
		realServers:          realServers,
		realTools:            realTools,
		toolsByServer:        toolsByServer,
		toolByID:             toolByID,
		readyServerCount:     readyServerCount,
		errServerCount:       errServerCount,
		pendingServerCount:   pendingServerCount,
		enabledRealToolCount: enabledRealToolCount,
		modelReady:           setup.ModelReady && (setup.ModelCheck == nil || !setup.ModelCheck.Stale),
		maxTopHits:           maxTopHits,
	}
}

func (u *DesktopUI) renderOverview(c *ui.Context) {
	if u.State == nil {
		return
	}
	state := u.State
	ov := u.overviewState()
	telemetry := u.Telemetry
	snap := u.buildOverviewSnapshot(state, telemetry)

	if ov.drawerOpen {
		u.renderOverviewDrawer(c, ov, state, snap, telemetry)
	}

	// 1. Clean Page Header (pure runtime gateway actions).
	SailSectionHeader(c, i18n.T("overview.header"), "", func() {
		sandboxBtn := SailButton(c.Key("ov-hdr-sandbox"), "").Padding(5, 11).FontSize(12.5)
		sandboxBtn.Children(func() {
			ui.Icon(c, IconWaypoints).Size(13.5, 13.5)
			ui.Text(c, i18n.T("overview.action.sandbox")).SingleLine()
		})
		if sandboxBtn.Clicked() {
			u.OpenPlaygroundReplay("", "")
		}

		addSvcBtn := SailPrimaryButton(c.Key("ov-hdr-add-svc"), "").Padding(5, 12).FontSize(12.5)
		addSvcBtn.Children(func() {
			ui.Icon(c, ovIconPlus).Size(13.5, 13.5)
			ui.Text(c, i18n.T("overview.action.add_service")).SingleLine()
		})
		if addSvcBtn.Clicked() {
			u.OpenAddServiceModal()
		}
	})

	// 2. Day-0 / Unverified Setup Banner (native safety guard).
	if !ov.bannerDismissed && (!state.Setup.ToolsReady || !snap.modelReady) {
		u.renderOverviewSetupBanner(c, ov, state, snap.realServers)
	}
	if ov.testErrTitle != "" || ov.testErrDetail != "" {
		u.renderOverviewTestError(c, ov)
	}

	// 3. 4-Column Runtime Signal KPI Grid.
	u.renderOverviewKPIGrid(c, state, telemetry, snap)

	// 4. 12-Hour Request Pulse Strip (48 x 15-min buckets).
	u.renderOverviewPulseCard(c, ov, telemetry.PulseBuckets)

	// 5. 1:1 Equal-Height Two-Column Operational Split.
	ui.Grid(c).
		FillWidth().
		ColumnTracks(ui.Fr(1.35).Min(0), ui.Fr(1.0).Min(300)).
		Gap(20).
		AlignItems(ui.Stretch).
		Children(func() {
			u.renderOverviewRecentLogsCard(c, ov, state.Events, snap.toolByID)
			u.renderOverviewRightSwitchCard(c, ov, snap, telemetry)
		})
}

func (u *DesktopUI) renderOverviewSetupBanner(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	realServers []apitypes.Server,
) {
	setup := state.Setup
	settings := state.Settings

	var title string
	if !setup.ToolsReady {
		title = i18n.T("overview.banner.no_tools")
	} else if !setup.ModelConfigured {
		title = i18n.T("overview.banner.no_model")
	} else if !state.KeyConfigured {
		title = fmt.Sprintf(i18n.T("overview.banner.model_no_key"), settings.Model)
	} else {
		title = fmt.Sprintf(i18n.T("overview.banner.model_unverified"), settings.Model)
	}

	ui.Row(c).
		FillWidth().
		Padding(12, 16).
		Gap(12).
		Radius(8).
		Background(SailWarningBg).
		Border(1, SailWarningBorder).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
				SailStatusDot(c, SailWarningInk)
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(SailWarningInk).SingleLine()
			})

			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				if !setup.ToolsReady {
					label := i18n.T("overview.banner.enable_tools")
					if len(realServers) == 0 {
						label = i18n.T("overview.banner.connect_service")
					}
					if SailSmallButton(c.Key("ov-banner-tools-btn"), label).Clicked() {
						if len(realServers) == 0 {
							u.OpenAddServiceModal()
						} else {
							u.Navigate(ViewServices)
						}
					}
				} else if setup.ModelConfigured && state.KeyConfigured {
					verifyBtn := SailButton(c.Key("ov-banner-verify-btn"), "").
						Height(SailControlHeightSm).
						Padding(0, 10).
						FontSize(12).
						Disabled(ov.testingModel)
					verifyBtn.Children(func() {
						if ov.testingModel {
							ui.Spinner(c).Size(12, 12).Label(i18n.T("overview.banner.checking_label"))
							ui.Text(c, i18n.T("overview.banner.checking")).SingleLine()
						} else {
							ui.Text(c, i18n.T("overview.banner.check_now")).SingleLine()
						}
					})
					if verifyBtn.Clicked() {
						u.overviewQuickVerifyModel(ov)
					}
				}

				if setup.ToolsReady {
					if SailSmallButton(c.Key("ov-banner-policy-btn"), i18n.T("overview.banner.configure_model")).Clicked() {
						u.Navigate(ViewSettings)
					}
				}

				dismissBtn := ui.ButtonBase(c.Key("ov-banner-dismiss-btn")).
					Size(24, 24).
					Radius(5).
					Center().
					Cursor(ui.CursorPointer).
					Label(i18n.T("overview.banner.dismiss"))
				if dismissBtn.Hovered() {
					dismissBtn.Background(ui.RGBA(180, 83, 9, 0.1))
				}
				dismissBtn.Children(func() {
					ui.Icon(c, ovIconClose).Size(13, 13).TextColor(SailWarningInk)
				})
				if dismissBtn.Clicked() {
					ov.bannerDismissed = true
				}
			})
		})
}

func (u *DesktopUI) renderOverviewTestError(c *ui.Context, ov *overviewLocalState) {
	ui.Row(c).
		FillWidth().
		Padding(12, 16).
		Gap(12).
		Radius(8).
		Background(SailDangerBg).
		Border(1, SailDangerBorder).
		AlignItems(ui.Start).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				errTitle := ov.testErrTitle
				if errTitle == "" {
					errTitle = i18n.T("overview.banner.model_check_failed")
				}
				ui.Text(c, errTitle).FontSize(13).FontWeight(600).TextColor(SailDangerInk).Selectable()
				if ov.testErrDetail != "" {
					ui.Text(c, ov.testErrDetail).Font("monospace").FontSize(11.5).TextColor(SailDangerInk).Selectable()
				}
			})
			closeBtn := ui.ButtonBase(c.Key("ov-testerr-close")).
				Size(24, 24).
				Radius(4).
				Center().
				Cursor(ui.CursorPointer).
				Label(i18n.T("overview.banner.close_error"))
			if closeBtn.Hovered() {
				closeBtn.Background(ui.RGBA(185, 28, 28, 0.1))
			}
			closeBtn.Children(func() {
				ui.Icon(c, ovIconClose).Size(13, 13).TextColor(SailDangerInk)
			})
			if closeBtn.Clicked() {
				ov.testErrTitle = ""
				ov.testErrDetail = ""
			}
		})
}

func (u *DesktopUI) renderOverviewKPIGrid(
	c *ui.Context,
	state *apitypes.Bootstrap,
	telemetry GatewayTelemetry,
	snap overviewSnapshot,
) {
	ui.Grid(c).Columns(4).Gap(16).FillWidth().Children(func() {
		ovRenderRequestsCard(c, telemetry)
		ovRenderSuccessRateCard(c, telemetry)
		ovRenderSavingsCard(c, telemetry, state.Settings.MaxTools)
		ovRenderServicesCard(c, snap)
	})
}

func ovRenderRequestsCard(c *ui.Context, telemetry GatewayTelemetry) {
	ovMetricCard(c, func() {
		ui.Text(c, i18n.T("overview.metric.requests")).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()

		ui.Textf(c, "%d", telemetry.SampleSize).
			FontSize(24).
			Bold().
			TextColor(SailInkStrong).
			FontFeatures("tnum").
			SingleLine()

		reqHint := fmt.Sprintf(i18n.T("overview.metric.requests_hint"), telemetry.DiscoverCount, telemetry.InvokeCount)
		if telemetry.AgentCount > 0 {
			reqHint += fmt.Sprintf(i18n.T("overview.metric.requests_hint_agent"), telemetry.AgentCount)
		}
		ui.Text(c, reqHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
	})
}

func ovRenderSuccessRateCard(c *ui.Context, telemetry GatewayTelemetry) {
	ovMetricCard(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
			ui.Text(c, i18n.T("overview.metric.success_rate")).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			if telemetry.ErrorCount > 0 {
				SailBadge(c, fmt.Sprintf(i18n.T("overview.metric.failures"), telemetry.ErrorCount), ToneDanger)
			}
		})

		rateStr := "—"
		if telemetry.SuccessRatePct != nil {
			rateStr = fmt.Sprintf("%.1f%%", *telemetry.SuccessRatePct)
		}
		ui.Text(c, rateStr).FontSize(24).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

		unmatched := telemetry.NoMatchCount + telemetry.NeedsContextCount
		var rateHint string
		switch {
		case telemetry.SampleSize == 0:
			rateHint = i18n.T("overview.metric.no_requests")
		case telemetry.ErrorCount > 0:
			rateHint = fmt.Sprintf(i18n.T("overview.metric.rate_errors"), telemetry.ErrorCount, unmatched)
		case unmatched > 0:
			rateHint = fmt.Sprintf(i18n.T("overview.metric.rate_unmatched"), unmatched)
		default:
			rateHint = i18n.T("overview.metric.rate_clean")
		}
		ui.Text(c, rateHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
	})
}

func ovRenderSavingsCard(c *ui.Context, telemetry GatewayTelemetry, maxTools int) {
	ovMetricCard(c, func() {
		ui.Text(c, i18n.T("overview.metric.context_savings")).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()

		compStr := "—"
		if telemetry.CompressionPct != nil {
			compStr = fmt.Sprintf("~%.0f%%", math.Round(*telemetry.CompressionPct))
		}
		ui.Text(c, compStr).FontSize(24).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

		if telemetry.AvgEligible == nil || telemetry.AvgSelectedOnHit == nil {
			ui.Textf(c, i18n.T("overview.metric.max_tools"), maxTools).
				FontSize(12).
				TextColor(SailInkMuted).
				FontFeatures("tnum").
				SingleLine()
			return
		}

		keptPct := max(float32(4), min(float32(100), float32(math.Round(100.0-*telemetry.CompressionPct))))
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Textf(
				c,
				i18n.T("overview.metric.avg_candidates"),
				int(math.Round(*telemetry.AvgEligible)),
				*telemetry.AvgSelectedOnHit,
			).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()

			ui.Row(c).
				FillWidth().
				Height(4).
				Radius(999).
				Background(SailBgMuted).
				Clip().
				Tooltip(fmt.Sprintf(i18n.T("overview.metric.candidates_tooltip"), int(math.Round(*telemetry.AvgEligible)), float64(keptPct))).
				Children(func() {
					ui.Box(c).WidthPercent(keptPct).FillHeight().Radius(999).Background(SailAccent)
				})
		})
	})
}

func ovRenderServicesCard(c *ui.Context, snap overviewSnapshot) {
	ovMetricCard(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
			ui.Text(c, i18n.T("overview.metric.services")).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			if snap.errServerCount > 0 {
				SailBadge(c, fmt.Sprintf(i18n.T("overview.metric.services_errors"), snap.errServerCount), ToneDanger)
			} else if snap.pendingServerCount > 0 {
				SailBadge(c, fmt.Sprintf(i18n.T("overview.metric.services_pending"), snap.pendingServerCount), ToneWarning)
			}
		})

		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(5).Children(func() {
			ui.Textf(c, "%d", snap.readyServerCount).
				FontSize(24).
				Bold().
				TextColor(SailInkStrong).
				FontFeatures("tnum").
				SingleLine()
			ui.Textf(c, i18n.T("overview.metric.services_ready"), len(snap.realServers)).
				FontSize(13).
				FontWeight(500).
				TextColor(SailInkMuted).
				FontFeatures("tnum").
				SingleLine()
		})

		svcHint := fmt.Sprintf(i18n.T("overview.metric.tools_available"), snap.enabledRealToolCount, len(snap.realTools))
		if snap.errServerCount > 0 {
			svcHint += fmt.Sprintf(i18n.T("overview.metric.tools_need_fix"), snap.errServerCount)
		} else if snap.pendingServerCount > 0 {
			svcHint += fmt.Sprintf(i18n.T("overview.metric.tools_pending"), snap.pendingServerCount)
		}
		ui.Text(c, svcHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
	})
}

func ovMetricCard(c *ui.Context, fn func()) {
	applyCardElevation(
		ui.Column(c).
			FillWidth().
			MinWidth(0).
			Padding(18, 20).
			Gap(8).
			Radius(10).
			Background(SailBgSurface),
	).Children(fn)
}

func ovPulseStats(buckets []apitypes.PulseBucket, hoveredIdx int) (maxBucket int, readout string) {
	maxBucket = 1
	peakIdx := -1
	peakTotal := 0
	for i, b := range buckets {
		if b.Total > maxBucket {
			maxBucket = b.Total
		}
		if b.Total > 0 && b.Total >= peakTotal {
			peakIdx = i
			peakTotal = b.Total
		}
	}
	readout = i18n.T("overview.pulse.no_requests")
	if hoveredIdx >= 0 && hoveredIdx < len(buckets) {
		readout = ovFormatPulseBucket(buckets[hoveredIdx])
	} else if peakIdx >= 0 {
		readout = i18n.T("overview.pulse.peak", ovFormatPulseBucket(buckets[peakIdx]))
	}
	return maxBucket, readout
}

func ovRenderPulseBar(c *ui.Context, idx int, b apitypes.PulseBucket, maxBucket int) bool {
	slot := ui.Column(c.Key(fmt.Sprintf("ov-pulse-%d", idx))).
		Grow(1).
		MinWidth(0).
		FillHeight().
		Justify(ui.End).
		Cursor(ui.CursorPointer).
		Tooltip(ovFormatPulseBucket(b))
	isHovered := slot.Hovered()
	slot.Children(func() {
		if b.Total == 0 {
			col := SailBgMuted
			if isHovered {
				col = SailBorderStrong
			}
			ui.Box(c).FillWidth().Height(3).Radius(1.5).Background(col)
			return
		}

		barH := max(float32(8), min(float32(42), float32(math.Round((float64(b.Total)/float64(maxBucket))*42.0))))
		okCount := max(0, b.Total-b.Error)
		if b.Error == 0 {
			ui.Box(c).FillWidth().Height(barH).Radius(2.5, 2.5, 1.5, 1.5).Background(SailSuccessInk)
			return
		}
		if okCount == 0 {
			ui.Box(c).FillWidth().Height(barH).Radius(2.5, 2.5, 1.5, 1.5).Background(SailDangerInk)
			return
		}

		errH := max(float32(3), min(barH-3, float32(math.Round(float64(barH)*(float64(b.Error)/float64(b.Total))))))
		ui.Column(c).
			FillWidth().
			Height(barH).
			Radius(2.5, 2.5, 1.5, 1.5).
			Clip().
			Children(func() {
				ui.Box(c).FillWidth().Height(errH).Background(SailDangerInk)
				ui.Box(c).FillWidth().Grow(1).Background(SailSuccessInk)
			})
	})
	return isHovered
}

func (u *DesktopUI) renderOverviewPulseCard(
	c *ui.Context,
	ov *overviewLocalState,
	buckets []apitypes.PulseBucket,
) {
	maxBucket, readout := ovPulseStats(buckets, ov.hoveredPulseIdx)

	applyCardElevation(
		ui.Column(c).
			FillWidth().
			Padding(14, 20).
			Gap(10).
			Radius(10).
			Background(SailBgSurface),
	).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, i18n.T("overview.pulse.title")).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				ui.Text(c, i18n.T("overview.pulse.subtitle")).FontSize(12).TextColor(SailInkMuted).SingleLine()
			})
			ui.Text(c, readout).
				Font("monospace").
				FontSize(11.5).
				TextColor(SailInkMuted).
				FontFeatures("tnum").
				SingleLine()
		})

		hoveredNow := -1
		ui.Row(c).
			FillWidth().
			Height(46).
			Padding(4, 0, 0, 0).
			Gap(3).
			AlignItems(ui.End).
			Children(func() {
				for i, b := range buckets {
					if ovRenderPulseBar(c, i, b, maxBucket) {
						hoveredNow = i
					}
				}
			})
		ov.hoveredPulseIdx = hoveredNow

		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("overview.pulse.axis_12h")).Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
			ui.Text(c, i18n.T("overview.pulse.axis_6h")).Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
			ui.Text(c, i18n.T("overview.pulse.axis_now")).Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
		})
	})
}

func ovFormatPulseBucket(b apitypes.PulseBucket) string {
	slot := fmt.Sprintf("%s–%s", b.Start.Local().Format("15:04"), b.End.Local().Format("15:04"))
	if b.Total == 0 {
		return slot + i18n.T("overview.pulse.bucket_no_requests")
	}
	if b.Error > 0 {
		ok := b.Total - b.Error
		if ok < 0 {
			ok = 0
		}
		return fmt.Sprintf(i18n.T("overview.pulse.bucket_stats"), slot, b.Total, ok, b.Error)
	}
	return fmt.Sprintf(i18n.T("overview.pulse.bucket_all_ok"), slot, b.Total)
}

func (u *DesktopUI) renderOverviewRecentLogsCard(
	c *ui.Context,
	ov *overviewLocalState,
	events []apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	SailFlushCard(c, func() {
		SailListCardHeader(c, i18n.T("overview.recent.title"), "", func() {
			if ovRenderArrowLinkBtn(c, "ov-all-logs-link", i18n.T("overview.recent.all_logs")) {
				u.OpenLogsEvent("")
			}
		})

		if len(events) == 0 {
			ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
				SailEmptyState(c, i18n.T("overview.recent.empty"), "", "", nil)
			})
			return
		}

		limit := min(len(events), 7)
		for idx := 0; idx < limit; idx++ {
			ev := events[idx]
			isLast := idx == limit-1
			parsed := logsParseEventDetail(ev, toolByID)

			titleText := strings.TrimSpace(ev.Title)
			if titleText == "" {
				titleText = ev.ID
			}
			row := ui.ButtonBase(c.Key("ov-recent-log-"+ev.ID)).
				FillWidth().
				Padding(13, 20).
				Gap(14).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Cursor(ui.CursorPointer).
				Label(titleText)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			hovered := row.Hovered()
			if hovered {
				row.Background(SailBgHover)
			}

			row.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						SailStatusDot(c, logsStatusDotColor(ev.Status))
						tEl := ui.Text(c, titleText).
							Grow(1).
							MinWidth(0).
							FontSize(13).
							FontWeight(600).
							TextColor(SailInkStrong).
							SingleLine()
						if ev.Kind != "discover" {
							tEl.Font("monospace")
						}
					})

					ui.Text(c, ovFormatLogSummary(ev, parsed)).
						FontSize(12).
						TextColor(SailInkMuted).
						SingleLine()
				})

				ui.Row(c).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Textf(c, "%d ms", ev.DurationMS).
						Font("monospace").
						FontSize(12).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
					ui.Text(c, FormatTime(ev.Time)).
						FontSize(12).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
					arrowCol := SailInkFaint
					if hovered {
						arrowCol = SailAccent
					}
					ui.Icon(c, IconChevronRight).Size(14, 14).TextColor(arrowCol).Shrink(0)
				})
			})

			if row.Clicked() {
				ov.openDrawer("log", ev.ID)
			}
		}
	}).FillHeight()
}

func ovFormatLogSummary(ev apitypes.Event, parsed logsParsedDetail) string {
	var src string
	if sc := parsed.Scope; sc != nil {
		agentLbl := agentDisplayName(sc.Agent)
		if agentLbl == "" {
			agentLbl = i18n.T("overview.detail.client")
		}
		repoLbl := sc.RepoName
		if repoLbl == "" && sc.Root != "" {
			repoLbl = filepath.Base(sc.Root)
		}
		if repoLbl != "" {
			src = agentLbl + " · " + repoLbl
		} else {
			src = agentLbl
		}
	}

	var outcome string
	switch {
	case logsIsErrorStatus(ev.Status):
		if parsed.Error != "" {
			outcome = CleanDescriptionSummary(parsed.Error)
		}
		if outcome == "" {
			outcome = i18n.T("overview.event.call_failed")
		}
	case ev.Status == "selected":
		if ev.Kind == "discover" {
			outcome = fmt.Sprintf(i18n.T("overview.event.tools_matched"), len(parsed.Tools))
		} else {
			outcome = i18n.T("overview.event.executed")
		}
	case ev.Status == "needs_context":
		outcome = i18n.T("overview.event.needs_context")
	case ev.Status == "no_match":
		outcome = i18n.T("overview.event.no_match")
	default:
		outcome = ev.Status
	}

	if src != "" {
		return src + " · " + outcome
	}
	return outcome
}

func (u *DesktopUI) renderOverviewRightSwitchCard(
	c *ui.Context,
	ov *overviewLocalState,
	snap overviewSnapshot,
	telemetry GatewayTelemetry,
) {
	SailFlushCard(c, func() {
		ui.Row(c).
			FillWidth().
			Padding(12, 20).
			Gap(10).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailDivider).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Segmented(c.Key("ov-right-tab"), &ov.rightTab, i18n.T("overview.right_tab.services"), i18n.T("overview.right_tab.top_tools")).
					Label(i18n.T("overview.right_tab.switch"))

				if ov.rightTab == 0 {
					if ovRenderArrowLinkBtn(c, "ov-manage-upstreams", i18n.T("overview.right.manage")) {
						u.Navigate(ViewServices)
					}
				} else {
					if ovRenderArrowLinkBtn(c, "ov-top-all-catalog", i18n.T("overview.right.catalog")) {
						u.OpenCatalogFilter("", "all")
					}
				}
			})

		if ov.rightTab == 0 {
			u.ovRenderServicesSwitchList(c, ov, snap)
			return
		}
		ovRenderTopToolsSwitchList(c, ov, telemetry.TopTools, snap.maxTopHits)
	}).FillHeight()
}

func (u *DesktopUI) ovRenderServicesSwitchList(c *ui.Context, ov *overviewLocalState, snap overviewSnapshot) {
	if len(snap.realServers) == 0 {
		ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
			SailEmptyState(c, i18n.T("overview.services.empty"), "", i18n.T("overview.action.add_service"), func() { u.OpenAddServiceModal() })
		})
		return
	}
	for idx, srv := range snap.realServers {
		u.ovRenderServiceSwitchRow(c, ov, srv, snap, idx == len(snap.realServers)-1)
	}
}

func (u *DesktopUI) ovRenderServiceSwitchRow(
	c *ui.Context,
	ov *overviewLocalState,
	s apitypes.Server,
	snap overviewSnapshot,
	isLast bool,
) {
	srvTools := snap.toolsByServer[s.ID]
	srvToolTotal, openTotal, openWrite := countServerTools(s.ID, srvTools)
	readiness := u.readinessForServer(s, srvTools)

	row := ui.ButtonBase(c.Key("ov-srv-row-"+s.ID)).
		FillWidth().
		Padding(13, 20).
		Gap(12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Cursor(ui.CursorPointer).
		Label(s.Name)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	hovered := row.Hovered()
	if hovered {
		row.Background(SailBgHover)
	}

	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				SailStatusDot(c, readiness.DotColor)
				ui.Text(c, s.Name).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				if readiness.BadgeLabel != "" {
					SailBadge(c, readiness.BadgeLabel, readiness.BadgeTone)
				} else if !s.Enabled {
					ui.Text(c, i18n.T("common.disabled")).FontSize(11.5).TextColor(SailInkFaint).SingleLine()
				}
				if s.Enabled && openWrite > 0 {
					SailBadge(c, fmt.Sprintf(i18n.T("common.write_badge"), openWrite), ToneWarning)
				}
			})

			if readiness.RowSummary != "" {
				sumColor := SailInkMuted
				if readiness.HasError() {
					sumColor = SailDangerInk
				}
				ui.Text(c, CleanDescriptionSummary(readiness.RowSummary)).FontSize(12).TextColor(sumColor).SingleLine()
			} else {
				ui.Textf(c, i18n.T("overview.services.protocol_tools"), ServerProtocolLabel(s), openTotal, srvToolTotal).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			}
		})

		arrowColor := SailInkFaint
		if hovered {
			arrowColor = SailAccent
		}
		ui.Icon(c, IconChevronRight).Size(15, 15).TextColor(arrowColor).Shrink(0)
	})

	if row.Clicked() {
		ov.openDrawer("service", s.ID)
	}
}

func ovRenderTopToolsSwitchList(c *ui.Context, ov *overviewLocalState, topTools []TopToolItem, maxTopHits int) {
	if len(topTools) == 0 {
		ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
			SailEmptyState(c, i18n.T("overview.top_tools.empty"), "", "", nil)
		})
		return
	}
	for idx, t := range topTools {
		ovRenderTopToolRow(c, ov, t, maxTopHits, idx == len(topTools)-1)
	}
}

func ovRenderTopToolRow(c *ui.Context, ov *overviewLocalState, t TopToolItem, maxTopHits int, isLast bool) {
	row := ui.ButtonBase(c.Key("ov-toptool-"+t.ID)).
		FillWidth().
		Padding(12, 20).
		Gap(12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Cursor(ui.CursorPointer).
		Label(t.Name)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	hovered := row.Hovered()
	if hovered {
		row.Background(SailBgHover)
	}

	row.Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, t.Name).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
			ui.Text(c, t.ServerName).FontSize(12).TextColor(SailInkFaint).SingleLine()
			if !t.ReadOnly {
				SailBadge(c, i18n.T("overview.badge.write"), ToneWarning)
			}
		})

		pct := max(float32(8), min(float32(100), float32(math.Round((float64(t.Hits)/float64(maxTopHits))*100))))
		ui.Row(c).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Row(c).Width(54).Height(5).Radius(999).Background(SailBgMuted).Clip().Children(func() {
				ui.Box(c).WidthPercent(pct).FillHeight().Radius(999).Background(SailInkBody)
			})
			ui.Textf(c, i18n.T("overview.top_tools.hits"), t.Hits).
				Width(40).
				Font("monospace").
				FontSize(12).
				FontWeight(600).
				TextColor(SailInkStrong).
				FontFeatures("tnum").
				TextAlign(ui.End).
				SingleLine()
			arrowCol := SailInkFaint
			if hovered {
				arrowCol = SailAccent
			}
			ui.Icon(c, IconChevronRight).Size(14, 14).TextColor(arrowCol).Shrink(0)
		})
	})

	if row.Clicked() {
		ov.openDrawer("tool", t.ID)
	}
}

func countServerTools(serverID string, realTools []apitypes.Tool) (total, open, openWrite int) {
	for _, t := range realTools {
		if t.ServerID != serverID {
			continue
		}
		total++
		if t.Enabled {
			open++
			if !t.ReadOnly {
				openWrite++
			}
		}
	}
	return total, open, openWrite
}

// --- Floating Rounded Slide-Over Drawer ---

func (u *DesktopUI) renderOverviewDrawer(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	snap overviewSnapshot,
	telemetry GatewayTelemetry,
) {
	switch ov.drawerType {
	case "log":
		var target *apitypes.Event
		for i := range state.Events {
			if state.Events[i].ID == ov.drawerID {
				target = &state.Events[i]
				break
			}
		}
		if target == nil {
			ov.drawerOpen = false
			return
		}
		u.renderOverviewLogDrawer(c, ov, state, *target, snap.toolByID)
	case "service":
		var target *apitypes.Server
		for i := range state.Registry.Servers {
			if state.Registry.Servers[i].ID == ov.drawerID {
				target = &state.Registry.Servers[i]
				break
			}
		}
		if target == nil {
			ov.drawerOpen = false
			return
		}
		u.renderOverviewServiceDrawer(c, ov, state, *target, snap.realTools)
	case "tool":
		t, ok := snap.toolByID[ov.drawerID]
		if !ok {
			ov.drawerOpen = false
			return
		}
		serverName := t.ServerID
		for _, s := range state.Registry.Servers {
			if s.ID == t.ServerID {
				serverName = s.Name
				break
			}
		}
		u.renderOverviewToolDrawer(c, ov, t, serverName, telemetry.ToolHitCounts[t.ID])
	default:
		ov.drawerOpen = false
	}
}

func ovRenderSlideDrawerShell(
	c *ui.Context,
	ov *overviewLocalState,
	header func(),
	body func(),
	footerActions func(),
) {
	ui.DialogBase(c.Key("ov-slide-drawer"), &ov.drawerOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(SailDrawerBackdrop).
			Padding(12).
			AlignItems(ui.End).
			Justify(ui.Start)

		panel.Width(460).
			MaxWidthPercent(100).
			FillHeight().
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow).
			Clip()

		// Header
		ui.Row(c).
			FillWidth().
			Padding(16, 20).
			Gap(12).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailDivider).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(header)
				if SailSmallButton(c.Key("ov-drawer-close"), i18n.T("common.close")).Clicked() {
					ov.drawerOpen = false
				}
			})

		// Body
		ui.Scroll(c.Key("ov-drawer-scroll-"+ov.drawerType+"-"+ov.drawerID)).
			Grow(1).
			FillWidth().
			Padding(18, 20).
			Gap(16).
			Children(body)

		// Footer
		ui.Row(c).
			FillWidth().
			Padding(12, 20).
			Gap(10).
			BorderWidth(1, 0, 0, 0).
			BorderColor(SailDivider).
			Background(SailBgSubtle).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Text(c, i18n.T("overview.drawer.esc_hint")).FontSize(12).TextColor(SailInkMuted).SingleLine()
				if footerActions != nil {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(footerActions)
				}
			})
	})
}

func ovLogStatusAndKindLabels(ev apitypes.Event) (stText, kindText string) {
	stText = i18n.T("overview.detail.status_matched")
	if logsIsErrorStatus(ev.Status) {
		stText = i18n.T("overview.event.call_failed")
	} else if logsIsUnmatchedStatus(ev.Status) {
		stText = i18n.T("overview.event.no_match")
	}
	kindText = i18n.T("overview.detail.kind_tool")
	if ev.Kind == "discover" {
		kindText = i18n.T("overview.detail.kind_discover")
	} else if ev.Kind == "agent" || ev.Kind == "agent_tool" {
		kindText = i18n.T("overview.detail.kind_agent")
	}
	return stText, kindText
}

type ovLogDrawerView struct {
	ev       apitypes.Event
	parsed   logsParsedDetail
	stText   string
	kindText string
	canBind  bool
}

func (u *DesktopUI) renderOverviewLogDrawer(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	ev apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	parsed := logsParseEventDetail(ev, toolByID)
	stText, kindText := ovLogStatusAndKindLabels(ev)

	var canBind bool
	if sc := parsed.Scope; sc != nil {
		bound := logsMatchedBoundRepo(state, *sc)
		canBind = bound == nil && (sc.RepoID == "" || sc.RepoID == "*") && (sc.Root != "" || sc.GitCommonDir != "")
	}
	view := ovLogDrawerView{
		ev:       ev,
		parsed:   parsed,
		stText:   stText,
		kindText: kindText,
		canBind:  canBind,
	}

	ovRenderSlideDrawerShell(
		c,
		ov,
		func() {
			SailStatusDot(c, logsStatusDotColor(ev.Status))
			ui.Text(c, i18n.T("overview.detail.title")).FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
		},
		func() {
			ovRenderLogDrawerBody(c, view)
		},
		func() {
			u.ovRenderLogDrawerFooter(c, ov, state, view)
		},
	)
}

func ovRenderLogDrawerBody(c *ui.Context, view ovLogDrawerView) {
	ev, parsed := view.ev, view.parsed
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		titleText := strings.TrimSpace(ev.Title)
		if titleText == "" {
			titleText = ev.ID
		}
		ui.Text(c, titleText).FontSize(15).FontWeight(600).LineHeight(1.4).TextColor(SailInkStrong).Selectable()
		ui.Textf(c, "%s · %s · %d ms · %s", view.kindText, view.stText, ev.DurationMS, FormatTime(ev.Time)).
			FontSize(12).
			TextColor(SailInkMuted).
			FontFeatures("tnum")
	})

	ovRenderLogDrawerMetaCard(c, parsed, view.stText)

	if parsed.Error != "" {
		SailAlertBox(c, ToneDanger, "", parsed.Error)
	}
	if parsed.Summary != "" {
		SailAlertBox(c, ToneInfo, "", parsed.Summary)
	}
	if parsed.Question != "" {
		SailAlertBox(c, ToneWarning, "", parsed.Question)
	}

	if len(parsed.Tools) > 0 {
		ovRenderLogDrawerTools(c, ev.ID, parsed.Tools)
	}
}

func ovRenderLogDrawerMetaCard(c *ui.Context, parsed logsParsedDetail, stText string) {
	SailSubtleCard(c, func() {
		SailKeyValueRow(c, i18n.T("overview.detail.status"), stText, false)
		if sc := parsed.Scope; sc != nil {
			if sc.Agent != "" {
				SailKeyValueRow(c, i18n.T("overview.detail.client"), agentDisplayName(sc.Agent), false)
			}
			repoLbl := sc.RepoName
			if repoLbl == "" {
				repoLbl = sc.Root
			}
			if repoLbl != "" {
				SailKeyValueRow(c, i18n.T("overview.detail.repo"), repoLbl, true)
			}
		}
		if parsed.PromptTokens != nil || parsed.OutputTokens != nil {
			pt, ot := 0, 0
			if parsed.PromptTokens != nil {
				pt = *parsed.PromptTokens
			}
			if parsed.OutputTokens != nil {
				ot = *parsed.OutputTokens
			}
			SailKeyValueRow(c, i18n.T("overview.detail.tokens"), fmt.Sprintf("%d tok (%d in · %d out)", pt+ot, pt, ot), true)
		} else if parsed.Model != "" {
			SailKeyValueRow(c, i18n.T("overview.detail.model"), parsed.Model, true)
		}
	})
}

func ovRenderLogDrawerTools(c *ui.Context, evID string, tools []logsMatchedTool) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, i18n.T("overview.detail.matched_tools")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
		SailFlushCard(c, func() {
			for idx, mt := range tools {
				r := ui.Column(c.Key(fmt.Sprintf("ov-drawer-hit-%s-%d", evID, idx))).
					FillWidth().
					Padding(11, 14).
					Gap(3)
				if idx < len(tools)-1 {
					r.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
				}
				r.Children(func() {
					displayID := mt.ID
					if displayID == "" {
						displayID = mt.Name
					}
					ui.Text(c, displayID).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					if mt.Reason != "" {
						ui.Text(c, mt.Reason).FontSize(12).TextColor(SailInkMuted).SingleLine()
					}
				})
			}
		})
	})
}

func (u *DesktopUI) ovRenderLogDrawerFooter(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	view ovLogDrawerView,
) {
	ev, parsed := view.ev, view.parsed
	if view.canBind && parsed.Scope != nil {
		lbl := i18n.T("overview.detail.bind_repo")
		if ov.drawerBindingRepo {
			lbl = i18n.T("overview.detail.binding")
		}
		if SailSmallButton(c.Key("ov-drawer-bind-repo"), lbl).Disabled(ov.drawerBindingRepo).Clicked() {
			u.bindRepoFromOverviewDrawer(ov, *parsed.Scope)
		}
	}
	if ev.Kind == "discover" && strings.TrimSpace(ev.Title) != "" {
		if SailSmallButton(c.Key("ov-drawer-replay"), i18n.T("overview.detail.replay")).Clicked() {
			ov.drawerOpen = false
			if parsed.Scope != nil {
				replayRepo := logsResolveReplayRepoKey(state, *parsed.Scope)
				u.OpenPlaygroundReplay(ev.Title, ev.Actor, replayRepo)
			} else {
				u.OpenPlaygroundReplay(ev.Title, ev.Actor)
			}
		}
	}
	if ovRenderDrawerArrowBtn(c, "ov-drawer-open-logs", i18n.T("overview.detail.log_details")) {
		ov.drawerOpen = false
		u.OpenLogsEvent(ev.ID)
	}
}

func (u *DesktopUI) bindRepoFromOverviewDrawer(ov *overviewLocalState, sc logsParsedScope) {
	if ov.drawerBindingRepo || u.Client == nil {
		return
	}
	ov.drawerBindingRepo = true
	name := strings.TrimSpace(sc.RepoName)
	if name == "" && sc.Root != "" {
		name = filepath.Base(sc.Root)
	}
	in := apitypes.SaveRepositoryRequest{Name: name}
	if u.State != nil {
		for _, obs := range u.State.Scopes.Observed {
			if (sc.Root != "" && obs.Root == sc.Root) || (sc.GitCommonDir != "" && obs.GitCommonDir == sc.GitCommonDir) {
				if obs.GitCommonDir != "" {
					in.GitCommonDirs = []string{obs.GitCommonDir}
				}
				if obs.RemoteHint != "" {
					in.RemoteHint = obs.RemoteHint
				}
				break
			}
		}
	}
	if len(in.GitCommonDirs) == 0 && sc.GitCommonDir != "" {
		in.GitCommonDirs = []string{sc.GitCommonDir}
	}
	if in.RemoteHint == "" && sc.RemoteHint != "" {
		in.RemoteHint = sc.RemoteHint
	}
	shared := u.State != nil && u.State.Access.Shared
	if len(in.GitCommonDirs) == 0 && sc.Root != "" {
		if shared {
			in.RootPaths = []string{sc.Root}
		} else {
			in.Path = sc.Root
		}
	}
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		saved, err := u.Client.SaveRepository(ctx, in)
		if err != nil {
			u.updateUI(func() { ov.drawerBindingRepo = false })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			ov.drawerBindingRepo = false
			ov.drawerOpen = false
		})
		u.Notify(i18n.T("overview.toast.repo_bound", saved.Name))
		return nil
	})
}

func (u *DesktopUI) renderOverviewServiceDrawer(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	srv apitypes.Server,
	realTools []apitypes.Tool,
) {
	total, open, _ := countServerTools(srv.ID, realTools)
	var srvTools []apitypes.Tool
	for _, t := range realTools {
		if t.ServerID == srv.ID {
			srvTools = append(srvTools, t)
		}
	}
	readiness := u.readinessForServer(srv, srvTools)
	enableLabel := i18n.T("common.disabled")
	if srv.Enabled {
		enableLabel = i18n.T("common.enabled")
	}

	ovRenderSlideDrawerShell(
		c,
		ov,
		func() {
			SailStatusDot(c, readiness.DotColor)
			ui.Text(c, srv.Name).FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
		},
		func() {
			if readiness.AlertTitle != "" || readiness.AlertDetail != "" {
				SailAlertBox(c, readiness.AlertTone, readiness.AlertTitle, readiness.AlertDetail)
			}

			SailSubtleCard(c, func() {
				SailKeyValueRow(c, i18n.T("common.protocol"), ServerProtocolLabel(srv), false)
				SailKeyValueRow(c, i18n.T("overview.drawer.current_status"), readiness.StatusLabel, false)
				SailKeyValueRow(c, i18n.T("overview.drawer.enable_switch"), enableLabel, false)
				if readiness.CheckLabel != "" {
					SailKeyValueRow(c, i18n.T("common.last_check"), readiness.CheckLabel, true)
				}
				SailKeyValueRow(c, i18n.T("overview.drawer.open_tools"), fmt.Sprintf("%d/%d", open, total), true)
			})

			SailDescriptionDetail(c, "ov-srv-"+srv.ID, srv.Description)

			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, i18n.T("overview.drawer.tools")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				if len(srvTools) == 0 {
					ui.Text(c, i18n.T("overview.drawer.no_tools")).FontSize(12.5).TextColor(SailInkFaint)
					return
				}
				SailFlushCard(c, func() {
					limit := min(len(srvTools), 8)
					for idx := 0; idx < limit; idx++ {
						t := srvTools[idx]
						isLast := idx == limit-1
						r := ui.Row(c.Key("ov-drawer-srv-tool-"+t.ID)).
							FillWidth().
							Padding(10, 14).
							Gap(10).
							AlignItems(ui.Center).
							Justify(ui.SpaceBetween)
						if !isLast {
							r.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
						}
						r.Children(func() {
							ui.Text(c, t.Name).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
							if !t.ReadOnly {
								SailBadge(c, i18n.T("overview.badge.write"), ToneWarning)
							}
						})
					}
				})
			})
		},
		func() {
			if ovRenderDrawerArrowBtn(c, "ov-drawer-open-srv", i18n.T("overview.drawer.manage_service")) {
				ov.drawerOpen = false
				u.OpenServiceDetail(srv.ID)
			}
		},
	)
}

func (u *DesktopUI) renderOverviewToolDrawer(
	c *ui.Context,
	ov *overviewLocalState,
	t apitypes.Tool,
	serverName string,
	hits int,
) {
	stateStr := i18n.T("overview.tool.disabled")
	if t.Enabled {
		stateStr = i18n.T("overview.tool.open")
	}

	ovRenderSlideDrawerShell(
		c,
		ov,
		func() {
			ui.Text(c, i18n.T("overview.tool.title")).FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
		},
		func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, t.Name).Font("monospace").FontSize(15).FontWeight(600).TextColor(SailInkStrong)
					if !t.ReadOnly {
						SailBadge(c, i18n.T("common.write_operation"), ToneWarning)
					} else {
						ui.Text(c, i18n.T("overview.tool.read_only")).FontSize(12).TextColor(SailInkMuted)
					}
				})
				ui.Textf(c, i18n.T("overview.tool.meta"), serverName, hits, stateStr).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum")
			})

			SailDescriptionDetail(c, "ov-tool-"+t.ID, t.Description)

			SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(t.InputSchema), func() {
				u.Notify(i18n.T("overview.tool.schema_copied"))
			})
		},
		func() {
			if SailSmallButton(c.Key("ov-drawer-tool-pg"), i18n.T("overview.tool.sandbox")).Clicked() {
				ov.drawerOpen = false
				u.OpenPlaygroundReplay(i18n.T("overview.tool.replay_with", t.Name), "developer")
			}
			if ovRenderDrawerArrowBtn(c, "ov-drawer-open-cat", i18n.T("overview.tool.open_catalog")) {
				ov.drawerOpen = false
				u.OpenCatalogTool(t.ID, t.ServerID)
			}
		},
	)
}

// --- Local Styling Helpers ---

func ovRenderArrowLinkBtn(c *ui.Context, key, label string) bool {
	btn := ui.ButtonBase(c.Key(key)).
		Padding(2, 6).
		Radius(4).
		Gap(4).
		AlignItems(ui.Center).
		Cursor(ui.CursorPointer)
	hovered := btn.Hovered()
	if hovered {
		btn.Background(SailAccentSoft)
	}
	btn.Children(func() {
		col := SailInkMuted
		if hovered {
			col = SailAccent
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(col).SingleLine()
		ui.Icon(c, IconArrowUpRight).Size(12, 12).TextColor(col).Shrink(0)
	})
	return btn.Clicked()
}

func ovRenderDrawerArrowBtn(c *ui.Context, key, label string) bool {
	btn := SailButton(c.Key(key), "").Padding(5, 11).FontSize(12.5)
	btn.Children(func() {
		ui.Text(c, label).SingleLine()
		ui.Icon(c, IconArrowUpRight).Size(12, 12).Shrink(0)
	})
	return btn.Clicked()
}
