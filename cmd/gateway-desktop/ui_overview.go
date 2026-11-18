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

	"github.com/egoist/mygo/ui"
)

// overviewLocalState holds UI-thread state local to the Overview page.
type overviewLocalState struct {
	bannerDismissed   bool
	testingModel      bool
	testErrTitle      string
	testErrDetail     string
	rightTab          int // 0: 上游服务, 1: 高频工具
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
		u.Notify("路由模型连通性校验通过")
		return nil
	})
}

func (u *DesktopUI) renderOverview(c *ui.Context) {
	if u.State == nil {
		return
	}
	state := u.State
	ov := u.overviewState()

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

	enabledRealTools := make([]apitypes.Tool, 0, len(realTools))
	for _, t := range realTools {
		if t.Enabled && readyServerMap[t.ServerID] {
			enabledRealTools = append(enabledRealTools, t)
		}
	}

	setup := state.Setup
	modelReady := setup.ModelReady && (setup.ModelCheck == nil || !setup.ModelCheck.Stale)
	telemetry := u.Telemetry

	maxTopHits := 1
	if len(telemetry.TopTools) > 0 && telemetry.TopTools[0].Hits > 0 {
		maxTopHits = telemetry.TopTools[0].Hits
	}

	// Render floating rounded slide-over drawer when active.
	if ov.drawerOpen {
		u.renderOverviewDrawer(c, ov, state, realTools, toolByID, telemetry)
	}

	// 1. Clean Page Header (pure runtime gateway actions).
	SailSectionHeader(c, "概览", "", func() {
		sandboxBtn := ui.Button(c.Key("ov-hdr-sandbox"), "").Padding(5, 11).FontSize(12.5)
		sandboxBtn.Children(func() {
			ui.Icon(c, IconWaypoints).Size(13.5, 13.5)
			ui.Text(c, "路由沙盒").SingleLine()
		})
		if sandboxBtn.Clicked() {
			u.OpenPlaygroundReplay("", "")
		}

		addSvcBtn := ui.PrimaryButton(c.Key("ov-hdr-add-svc"), "").Padding(5, 12).FontSize(12.5)
		addSvcBtn.Children(func() {
			ui.Icon(c, ovIconPlus).Size(13.5, 13.5)
			ui.Text(c, "添加上游服务").SingleLine()
		})
		if addSvcBtn.Clicked() {
			u.OpenAddServiceModal()
		}
	})

	// 2. Day-0 / Unverified Setup Banner (native safety guard).
	showSetupBanner := !ov.bannerDismissed && (!setup.ToolsReady || !modelReady)
	if showSetupBanner {
		u.renderOverviewSetupBanner(c, ov, state, realServers)
	}

	if ov.testErrTitle != "" || ov.testErrDetail != "" {
		u.renderOverviewTestError(c, ov)
	}

	// 3. 4-Column Runtime Signal KPI Grid.
	u.renderOverviewKPIGrid(
		c,
		state,
		telemetry,
		readyServerCount,
		len(realServers),
		errServerCount,
		pendingServerCount,
		len(enabledRealTools),
		len(realTools),
	)

	// 4. 12-Hour Request Pulse Strip (48 x 15-min buckets).
	u.renderOverviewPulseCard(c, ov, telemetry.PulseBuckets)

	// 5. 1:1 Equal-Height Two-Column Operational Split.
	ui.Grid(c).
		FillWidth().
		ColumnTracks(ui.Fr(1.35).Min(0), ui.Fr(1.0).Min(300)).
		Gap(20).
		AlignItems(ui.Stretch).
		Children(func() {
			u.renderOverviewRecentLogsCard(c, ov, state.Events, toolByID)
			u.renderOverviewRightSwitchCard(c, ov, state, realServers, realTools, telemetry.TopTools, maxTopHits)
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
		title = "尚未开放任何上游工具"
	} else if !setup.ModelConfigured {
		title = "尚未配置路由模型"
	} else if !state.KeyConfigured {
		title = fmt.Sprintf("路由模型 %s 未检测到 API Key", settings.Model)
	} else {
		title = fmt.Sprintf("路由模型 %s 待完成连通性校验", settings.Model)
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
					label := "启用工具"
					if len(realServers) == 0 {
						label = "接入服务"
					}
					if SailSmallButton(c.Key("ov-banner-tools-btn"), label).Clicked() {
						if len(realServers) == 0 {
							u.OpenAddServiceModal()
						} else {
							u.Navigate(ViewServices)
						}
					}
				} else if setup.ModelConfigured && state.KeyConfigured {
					verifyBtn := ui.Button(c.Key("ov-banner-verify-btn"), "").
						Padding(5, 11).
						FontSize(12.5).
						Disabled(ov.testingModel)
					verifyBtn.Children(func() {
						if ov.testingModel {
							ui.Spinner(c).Size(12, 12).Label("校验中")
							ui.Text(c, "校验中…").SingleLine()
						} else {
							ui.Text(c, "立即校验").SingleLine()
						}
					})
					if verifyBtn.Clicked() {
						u.overviewQuickVerifyModel(ov)
					}
				}

				if setup.ToolsReady {
					if SailSmallButton(c.Key("ov-banner-policy-btn"), "配置模型").Clicked() {
						u.Navigate(ViewSettings)
					}
				}

				dismissBtn := ui.ButtonBase(c.Key("ov-banner-dismiss-btn")).
					Size(24, 24).
					Radius(5).
					Center().
					Cursor(ui.CursorPointer).
					Label("收起提示")
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
					errTitle = "路由模型校验失败"
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
				Label("关闭错误提示")
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
	readyServerCount int,
	realServerCount int,
	errServerCount int,
	pendingServerCount int,
	enabledRealToolCount int,
	realToolCount int,
) {
	settings := state.Settings

	ui.Grid(c).Columns(4).Gap(16).FillWidth().Children(func() {
		// Card 1: Gateway Requests
		ovMetricCard(c, func() {
			ui.Text(c, "网关请求").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()

			ui.Textf(c, "%d", telemetry.SampleSize).
				FontSize(24).
				Bold().
				TextColor(SailInkStrong).
				FontFeatures("tnum").
				SingleLine()

			reqHint := fmt.Sprintf("%d 路由发现 · %d 工具执行", telemetry.DiscoverCount, telemetry.InvokeCount)
			if telemetry.AgentCount > 0 {
				reqHint += fmt.Sprintf(" · %d 连接", telemetry.AgentCount)
			}
			ui.Text(c, reqHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
		})

		// Card 2: Request Success Rate
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "请求成功率").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				if telemetry.ErrorCount > 0 {
					SailBadge(c, fmt.Sprintf("%d 失败", telemetry.ErrorCount), ToneDanger)
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
				rateHint = "暂无请求记录"
			case telemetry.ErrorCount > 0:
				rateHint = fmt.Sprintf("%d 次调用异常 · %d 次未命中", telemetry.ErrorCount, unmatched)
			case unmatched > 0:
				rateHint = fmt.Sprintf("0 次失败 · %d 次未命中", unmatched)
			default:
				rateHint = "近 24h 无失败请求"
			}
			ui.Text(c, rateHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
		})

		// Card 3: Context Savings + Micro Comparison Bar
		ovMetricCard(c, func() {
			ui.Text(c, "上下文节省").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()

			compStr := "—"
			if telemetry.CompressionPct != nil {
				compStr = fmt.Sprintf("~%.0f%%", math.Round(*telemetry.CompressionPct))
			}
			ui.Text(c, compStr).FontSize(24).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

			if telemetry.AvgEligible != nil && telemetry.AvgSelectedOnHit != nil {
				keptPct := float32(math.Round(100.0 - *telemetry.CompressionPct))
				if keptPct < 4 {
					keptPct = 4
				}
				if keptPct > 100 {
					keptPct = 100
				}
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Textf(
						c,
						"平均 %d 候选 → %.1f 个工具",
						int(math.Round(*telemetry.AvgEligible)),
						*telemetry.AvgSelectedOnHit,
					).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()

					ui.Row(c).
						FillWidth().
						Height(4).
						Radius(999).
						Background(SailBgMuted).
						Clip().
						Tooltip(fmt.Sprintf("%d 候选工具中仅下发约 %.0f%%", int(math.Round(*telemetry.AvgEligible)), float64(keptPct))).
						Children(func() {
							ui.Box(c).WidthPercent(keptPct).FillHeight().Radius(999).Background(SailAccent)
						})
				})
			} else {
				ui.Textf(c, "单次上限 %d 个工具", settings.MaxTools).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			}
		})

		// Card 4: Upstream Services
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "上游服务").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				if errServerCount > 0 {
					SailBadge(c, fmt.Sprintf("%d 异常", errServerCount), ToneDanger)
				} else if pendingServerCount > 0 {
					SailBadge(c, fmt.Sprintf("%d 待验证", pendingServerCount), ToneWarning)
				}
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(5).Children(func() {
				ui.Textf(c, "%d", readyServerCount).
					FontSize(24).
					Bold().
					TextColor(SailInkStrong).
					FontFeatures("tnum").
					SingleLine()
				ui.Textf(c, "/ %d 就绪", realServerCount).
					FontSize(13).
					FontWeight(500).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})

			svcHint := fmt.Sprintf("%d/%d 工具可用", enabledRealToolCount, realToolCount)
			if errServerCount > 0 {
				svcHint += fmt.Sprintf(" · %d 需修复", errServerCount)
			} else if pendingServerCount > 0 {
				svcHint += fmt.Sprintf(" · %d 待验证", pendingServerCount)
			}
			ui.Text(c, svcHint).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
		})
	})
}

func ovMetricCard(c *ui.Context, fn func()) {
	ui.Column(c).
		FillWidth().
		MinWidth(0).
		Padding(18, 20).
		Gap(8).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Children(fn)
}

func (u *DesktopUI) renderOverviewPulseCard(
	c *ui.Context,
	ov *overviewLocalState,
	buckets []apitypes.PulseBucket,
) {
	maxBucket := 1
	peakIdx := -1
	for i, b := range buckets {
		if b.Total > maxBucket {
			maxBucket = b.Total
		}
		if b.Total > 0 && (peakIdx == -1 || b.Total >= buckets[peakIdx].Total) {
			peakIdx = i
		}
	}

	readout := "近 12 小时暂无请求"
	if ov.hoveredPulseIdx >= 0 && ov.hoveredPulseIdx < len(buckets) {
		readout = ovFormatPulseBucket(buckets[ov.hoveredPulseIdx])
	} else if peakIdx >= 0 {
		readout = "峰值 " + ovFormatPulseBucket(buckets[peakIdx])
	}

	ui.Column(c).
		FillWidth().
		Padding(14, 20).
		Gap(10).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, "请求脉冲").FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Text(c, "近 12 小时 · 每 15 分钟").FontSize(12).TextColor(SailInkMuted).SingleLine()
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
						slot := ui.Column(c.Key(fmt.Sprintf("ov-pulse-%d", i))).
							Grow(1).
							MinWidth(0).
							FillHeight().
							Justify(ui.End).
							Cursor(ui.CursorPointer).
							Tooltip(ovFormatPulseBucket(b))
						isHovered := slot.Hovered()
						if isHovered {
							hoveredNow = i
						}
						slot.Children(func() {
							if b.Total == 0 {
								col := SailBgMuted
								if isHovered {
									col = SailBorderStrong
								}
								ui.Box(c).FillWidth().Height(3).Radius(1.5).Background(col)
								return
							}

							barH := float32(math.Round((float64(b.Total) / float64(maxBucket)) * 42.0))
							if barH < 8 {
								barH = 8
							}
							if barH > 42 {
								barH = 42
							}
							okCount := b.Total - b.Error
							if okCount < 0 {
								okCount = 0
							}
							if b.Error == 0 {
								ui.Box(c).
									FillWidth().
									Height(barH).
									Radius(2.5, 2.5, 1.5, 1.5).
									Background(SailSuccessInk)
								return
							}
							if okCount == 0 {
								ui.Box(c).
									FillWidth().
									Height(barH).
									Radius(2.5, 2.5, 1.5, 1.5).
									Background(SailDangerInk)
								return
							}

							errH := float32(math.Round(float64(barH) * (float64(b.Error) / float64(b.Total))))
							if errH < 3 {
								errH = 3
							}
							if errH > barH-3 {
								errH = barH - 3
							}
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
					}
				})
			ov.hoveredPulseIdx = hoveredNow

			ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "12h 前").Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
				ui.Text(c, "6h 前").Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
				ui.Text(c, "现在").Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine()
			})
		})
}

func ovFormatPulseBucket(b apitypes.PulseBucket) string {
	slot := fmt.Sprintf("%s–%s", b.Start.Local().Format("15:04"), b.End.Local().Format("15:04"))
	if b.Total == 0 {
		return slot + " · 无请求"
	}
	if b.Error > 0 {
		ok := b.Total - b.Error
		if ok < 0 {
			ok = 0
		}
		return fmt.Sprintf("%s · %d 次（%d 成功 · %d 失败）", slot, b.Total, ok, b.Error)
	}
	return fmt.Sprintf("%s · %d 次（全部成功）", slot, b.Total)
}

func (u *DesktopUI) renderOverviewRecentLogsCard(
	c *ui.Context,
	ov *overviewLocalState,
	events []apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	SailFlushCard(c, func() {
		SailListCardHeader(c, "最近请求", "", func() {
			if ovRenderArrowLinkBtn(c, "ov-all-logs-link", "全部日志") {
				u.OpenLogsEvent("")
			}
		})

		if len(events) == 0 {
			ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
				SailEmptyState(c, "暂无网关请求记录", "", "", nil)
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
			agentLbl = "客户端"
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
			outcome = "调用失败"
		}
	case ev.Status == "selected":
		if ev.Kind == "discover" {
			outcome = fmt.Sprintf("命中 %d 个工具", len(parsed.Tools))
		} else {
			outcome = "执行成功"
		}
	case ev.Status == "needs_context":
		outcome = "待补充上下文"
	case ev.Status == "no_match":
		outcome = "未命中工具"
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
	state *apitypes.Bootstrap,
	realServers []apitypes.Server,
	realTools []apitypes.Tool,
	topTools []TopToolItem,
	maxTopHits int,
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
				ui.Segmented(c.Key("ov-right-tab"), &ov.rightTab, "上游服务", "高频工具").
					Label("切换上游服务与高频工具")

				if ov.rightTab == 0 {
					if ovRenderArrowLinkBtn(c, "ov-manage-upstreams", "管理") {
						u.Navigate(ViewServices)
					}
				} else {
					if ovRenderArrowLinkBtn(c, "ov-top-all-catalog", "目录") {
						u.OpenCatalogFilter("", "all")
					}
				}
			})

		if ov.rightTab == 0 {
			if len(realServers) == 0 {
				ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
					SailEmptyState(
						c,
						"尚未接入上游服务",
						"",
						"添加上游服务",
						func() { u.OpenAddServiceModal() },
					)
				})
				return
			}

			for idx, srv := range realServers {
				s := srv
				isLast := idx == len(realServers)-1
				srvToolTotal, openTotal, openWrite := countServerTools(s.ID, realTools)
				var srvTools []apitypes.Tool
				for _, t := range realTools {
					if t.ServerID == s.ID {
						srvTools = append(srvTools, t)
					}
				}
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

							ui.Text(c, s.Name).
								FontSize(13.5).
								FontWeight(600).
								TextColor(SailInkStrong).
								SingleLine()

							if readiness.BadgeLabel != "" {
								SailBadge(c, readiness.BadgeLabel, readiness.BadgeTone)
							} else if !s.Enabled {
								ui.Text(c, "已停用").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
							}
							if s.Enabled && openWrite > 0 {
								SailBadge(c, fmt.Sprintf("写 ×%d", openWrite), ToneWarning)
							}
						})

						if readiness.RowSummary != "" {
							sumColor := SailInkMuted
							if readiness.HasError() {
								sumColor = SailDangerInk
							}
							ui.Text(c, CleanDescriptionSummary(readiness.RowSummary)).FontSize(12).TextColor(sumColor).SingleLine()
						} else {
							ui.Textf(c, "%s · %d/%d 工具开放", ServerProtocolLabel(s), openTotal, srvToolTotal).
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
			return
		}

		// Right Tab 1: Top Tools
		if len(topTools) == 0 {
			ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
				SailEmptyState(c, "暂无工具命中统计", "", "", nil)
			})
			return
		}

		for idx, tool := range topTools {
			t := tool
			isLast := idx == len(topTools)-1
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
					ui.Text(c, t.Name).
						Font("monospace").
						FontSize(13).
						FontWeight(600).
						TextColor(SailInkStrong).
						SingleLine()
					ui.Text(c, t.ServerName).
						FontSize(12).
						TextColor(SailInkFaint).
						SingleLine()
					if !t.ReadOnly {
						SailBadge(c, "写", ToneWarning)
					}
				})

				pct := float32(math.Round((float64(t.Hits) / float64(maxTopHits)) * 100))
				if pct < 8 {
					pct = 8
				}
				if pct > 100 {
					pct = 100
				}
				ui.Row(c).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Row(c).
						Width(54).
						Height(5).
						Radius(999).
						Background(SailBgMuted).
						Clip().
						Children(func() {
							ui.Box(c).WidthPercent(pct).FillHeight().Radius(999).Background(SailInkBody)
						})
					ui.Textf(c, "%d 次", t.Hits).
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
	}).FillHeight()
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
	realTools []apitypes.Tool,
	toolByID map[string]apitypes.Tool,
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
		u.renderOverviewLogDrawer(c, ov, state, *target, toolByID)
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
		u.renderOverviewServiceDrawer(c, ov, state, *target, realTools)
	case "tool":
		t, ok := toolByID[ov.drawerID]
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
		backdrop.Background(ui.RGBA(10, 37, 64, 0.22)).
			Padding(12).
			AlignItems(ui.End).
			Justify(ui.Start)

		panel.Width(460).
			MaxWidthPercent(100).
			FillHeight().
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25)).
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
				if SailSmallButton(c.Key("ov-drawer-close"), "关闭").Clicked() {
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
				ui.Text(c, "按 Esc 关闭").FontSize(12).TextColor(SailInkMuted).SingleLine()
				if footerActions != nil {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(footerActions)
				}
			})
	})
}

func (u *DesktopUI) renderOverviewLogDrawer(
	c *ui.Context,
	ov *overviewLocalState,
	state *apitypes.Bootstrap,
	ev apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	parsed := logsParseEventDetail(ev, toolByID)
	stText := "已命中"
	if logsIsErrorStatus(ev.Status) {
		stText = "调用失败"
	} else if logsIsUnmatchedStatus(ev.Status) {
		stText = "未命中工具"
	}
	kindText := "工具执行"
	if ev.Kind == "discover" {
		kindText = "路由发现"
	} else if ev.Kind == "agent" || ev.Kind == "agent_tool" {
		kindText = "MCP 连接"
	}

	var canBind bool
	if sc := parsed.Scope; sc != nil {
		bound := logsMatchedBoundRepo(state, *sc)
		canBind = bound == nil && (sc.RepoID == "" || sc.RepoID == "*") && (sc.Root != "" || sc.GitCommonDir != "")
	}

	ovRenderSlideDrawerShell(
		c,
		ov,
		func() {
			SailStatusDot(c, logsStatusDotColor(ev.Status))
			ui.Text(c, "请求详情").FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
		},
		func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				titleText := strings.TrimSpace(ev.Title)
				if titleText == "" {
					titleText = ev.ID
				}
				ui.Text(c, titleText).FontSize(15).FontWeight(600).LineHeight(1.4).TextColor(SailInkStrong).Selectable()
				ui.Textf(c, "%s · %s · %d ms · %s", kindText, stText, ev.DurationMS, FormatTime(ev.Time)).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum")
			})

			SailSubtleCard(c, func() {
				SailKeyValueRow(c, "状态", stText, false)
				if sc := parsed.Scope; sc != nil {
					if sc.Agent != "" {
						SailKeyValueRow(c, "客户端", agentDisplayName(sc.Agent), false)
					}
					repoLbl := sc.RepoName
					if repoLbl == "" {
						repoLbl = sc.Root
					}
					if repoLbl != "" {
						SailKeyValueRow(c, "仓库", repoLbl, true)
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
					SailKeyValueRow(c, "路由 Token", fmt.Sprintf("%d tok (%d in · %d out)", pt+ot, pt, ot), true)
				} else if parsed.Model != "" {
					SailKeyValueRow(c, "路由模型", parsed.Model, true)
				}
			})

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
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					ui.Text(c, "命中工具").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					SailFlushCard(c, func() {
						for idx, mt := range parsed.Tools {
							isLast := idx == len(parsed.Tools)-1
							r := ui.Column(c.Key(fmt.Sprintf("ov-drawer-hit-%s-%d", ev.ID, idx))).
								FillWidth().
								Padding(11, 14).
								Gap(3)
							if !isLast {
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
		},
		func() {
			if canBind && parsed.Scope != nil {
				lbl := "绑定此仓库"
				if ov.drawerBindingRepo {
					lbl = "绑定中…"
				}
				if SailSmallButton(c.Key("ov-drawer-bind-repo"), lbl).Disabled(ov.drawerBindingRepo).Clicked() {
					u.bindRepoFromOverviewDrawer(ov, *parsed.Scope)
				}
			}
			if ev.Kind == "discover" && strings.TrimSpace(ev.Title) != "" {
				if SailSmallButton(c.Key("ov-drawer-replay"), "沙盒重放").Clicked() {
					ov.drawerOpen = false
					if parsed.Scope != nil {
						replayRepo := logsResolveReplayRepoKey(state, *parsed.Scope)
						u.OpenPlaygroundReplay(ev.Title, ev.Actor, replayRepo)
					} else {
						u.OpenPlaygroundReplay(ev.Title, ev.Actor)
					}
				}
			}
			if ovRenderDrawerArrowBtn(c, "ov-drawer-open-logs", "日志详情") {
				ov.drawerOpen = false
				u.OpenLogsEvent(ev.ID)
			}
		},
	)
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
		u.Notify("已绑定仓库 " + saved.Name)
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
	enableLabel := "已停用"
	if srv.Enabled {
		enableLabel = "已启用"
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
				SailKeyValueRow(c, "协议", ServerProtocolLabel(srv), false)
				SailKeyValueRow(c, "当前状态", readiness.StatusLabel, false)
				SailKeyValueRow(c, "启用开关", enableLabel, false)
				if readiness.CheckLabel != "" {
					SailKeyValueRow(c, "最近检查", readiness.CheckLabel, true)
				}
				SailKeyValueRow(c, "开放工具", fmt.Sprintf("%d/%d", open, total), true)
			})

			SailDescriptionDetail(c, "ov-srv-"+srv.ID, srv.Description)

			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, "下属工具").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				if len(srvTools) == 0 {
					ui.Text(c, "暂无已同步工具").FontSize(12.5).TextColor(SailInkFaint)
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
								SailBadge(c, "写", ToneWarning)
							}
						})
					}
				})
			})
		},
		func() {
			if ovRenderDrawerArrowBtn(c, "ov-drawer-open-srv", "管理此服务") {
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
	stateStr := "已禁用"
	if t.Enabled {
		stateStr = "已开放"
	}

	ovRenderSlideDrawerShell(
		c,
		ov,
		func() {
			ui.Text(c, "工具详情").FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
		},
		func() {
			ui.Column(c).FillWidth().Gap(4).Children(func() {
				ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, t.Name).Font("monospace").FontSize(15).FontWeight(600).TextColor(SailInkStrong)
					if !t.ReadOnly {
						SailBadge(c, "写操作", ToneWarning)
					} else {
						ui.Text(c, "只读").FontSize(12).TextColor(SailInkMuted)
					}
				})
				ui.Textf(c, "%s · 累计调用 %d 次 · %s", serverName, hits, stateStr).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum")
			})

			SailDescriptionDetail(c, "ov-tool-"+t.ID, t.Description)

			SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(t.InputSchema), func() {
				u.Notify("已复制 Schema")
			})
		},
		func() {
			if SailSmallButton(c.Key("ov-drawer-tool-pg"), "沙盒测试").Clicked() {
				ov.drawerOpen = false
				u.OpenPlaygroundReplay("使用 "+t.Name, "developer")
			}
			if ovRenderDrawerArrowBtn(c, "ov-drawer-open-cat", "工具目录") {
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
	btn := ui.Button(c.Key(key), "").Padding(5, 11).FontSize(12.5)
	btn.Children(func() {
		ui.Text(c, label).SingleLine()
		ui.Icon(c, IconArrowUpRight).Size(12, 12).Shrink(0)
	})
	return btn.Clicked()
}
