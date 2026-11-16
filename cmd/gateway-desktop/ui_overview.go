package main

import (
	"context"
	"errors"
	"fmt"
	"gatewaylab/internal/registry"
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// overviewLocalState holds UI-thread state local to the Overview page.
type overviewLocalState struct {
	bannerDismissed bool
	testingModel    bool
	testErrTitle    string
	testErrDetail   string
}

var (
	overviewStatesMu sync.Mutex
	overviewStates   = make(map[*DesktopUI]*overviewLocalState)

	ovIconClose = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
	ovIconPlay  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><polygon points="6 3 20 12 6 21 6 3"/></svg>`))
	ovIconPlus  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`))
	ovIconTerm  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><polyline points="4 17 10 11 4 5"/><line x1="12" x2="20" y1="19" y2="19"/></svg>`))
)

func (u *DesktopUI) overviewState() *overviewLocalState {
	overviewStatesMu.Lock()
	defer overviewStatesMu.Unlock()
	st, ok := overviewStates[u]
	if !ok {
		st = &overviewLocalState{}
		overviewStates[u] = st
	}
	return st
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
			if IsUnauthorized(err) {
				u.updateUI(func() {
					ov.testingModel = false
				})
				return err
			}
			title := err.Error()
			detail := ""
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				if apiErr.Message != "" {
					title = apiErr.Message
				}
				detail = apiErr.Detail
			}
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

	realServers := make([]registry.Server, 0, len(state.Registry.Servers))
	activeRealServers := make([]registry.Server, 0, len(state.Registry.Servers))
	realServerMap := make(map[string]bool, len(state.Registry.Servers))
	activeRealMap := make(map[string]bool, len(state.Registry.Servers))
	for _, s := range state.Registry.Servers {
		if !s.Demo {
			realServers = append(realServers, s)
			realServerMap[s.ID] = true
			if s.Enabled {
				activeRealServers = append(activeRealServers, s)
				activeRealMap[s.ID] = true
			}
		}
	}

	realTools := make([]registry.Tool, 0, len(state.Registry.Tools))
	enabledRealTools := make([]registry.Tool, 0, len(state.Registry.Tools))
	readOnlyEnabled := 0
	for _, t := range state.Registry.Tools {
		if realServerMap[t.ServerID] {
			realTools = append(realTools, t)
			if t.Enabled && activeRealMap[t.ServerID] {
				enabledRealTools = append(enabledRealTools, t)
				if t.ReadOnly {
					readOnlyEnabled++
				}
			}
		}
	}
	writeEnabled := len(enabledRealTools) - readOnlyEnabled

	setup := state.Setup
	modelReady := setup.ModelReady && (setup.ModelCheck == nil || !setup.ModelCheck.Stale)
	telemetry := u.Telemetry

	maxTopHits := 1
	if len(telemetry.TopTools) > 0 && telemetry.TopTools[0].Hits > 0 {
		maxTopHits = telemetry.TopTools[0].Hits
	}

	// 1. Clean Page Header (zero instructional prose)
	SailSectionHeader(c, "运行大盘", "", func() {
		sandboxBtn := ui.Button(c.Key("ov-hdr-sandbox"), "").Padding(5, 11).FontSize(12.5)
		sandboxBtn.Children(func() {
			ui.Icon(c, IconWaypoints).Size(13.5, 13.5)
			ui.Text(c, "路由沙盒").SingleLine()
		})
		if sandboxBtn.Clicked() {
			u.OpenPlaygroundReplay("", "")
		}

		connectBtn := ui.Button(c.Key("ov-hdr-connect"), "").Padding(5, 11).FontSize(12.5)
		connectBtn.Children(func() {
			ui.Icon(c, ovIconTerm).Size(13.5, 13.5)
			ui.Text(c, "接入客户端").SingleLine()
		})
		if connectBtn.Clicked() {
			u.OpenClients()
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

	// 2. Day-0 / Unverified Setup Banner
	showSetupBanner := !ov.bannerDismissed && (!setup.ToolsReady || !modelReady)
	if showSetupBanner {
		u.renderOverviewSetupBanner(c, ov, state, realServers)
	}

	if ov.testErrTitle != "" || ov.testErrDetail != "" {
		u.renderOverviewTestError(c, ov)
	}

	// 3. Golden Signal KPI Grid
	u.renderOverviewKPIGrid(c, state, telemetry, len(activeRealServers), len(realServers), len(realTools), readOnlyEnabled, writeEnabled)

	// 4. Traffic Outcome Proportion Bar
	u.renderOverviewTrafficStrip(c, telemetry)

	// 5. Two-Column Operational Split
	ui.Grid(c).
		FillWidth().
		ColumnTracks(ui.Fr(1.38).Min(0), ui.Fr(1.0).Min(300)).
		Gap(24).
		AlignItems(ui.Start).
		Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(24).Children(func() {
				u.renderOverviewAttentionCard(c, telemetry.AttentionItems)
				u.renderOverviewTopToolsCard(c, telemetry.TopTools, maxTopHits)
			})

			ui.Column(c).FillWidth().MinWidth(0).Gap(24).Children(func() {
				u.renderOverviewServersCard(c, state, realServers, realTools)
				u.renderOverviewEndpointCard(c, state)
			})
		})
}

func (u *DesktopUI) renderOverviewSetupBanner(
	c *ui.Context,
	ov *overviewLocalState,
	state *Bootstrap,
	realServers []registry.Server,
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
	state *Bootstrap,
	telemetry GatewayTelemetry,
	activeServerCount int,
	realServerCount int,
	realToolCount int,
	readOnlyEnabled int,
	writeEnabled int,
) {
	settings := state.Settings
	setup := state.Setup

	ui.Grid(c).Columns(4).Gap(16).FillWidth().Children(func() {
		// Card 1: Hit Rate
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "路由命中率").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				if telemetry.DiscoverCount > 0 {
					ui.Textf(c, "%d/%d", telemetry.SelectedCount, telemetry.DiscoverCount).
						FontSize(11.5).
						TextColor(SailInkFaint).
						FontFeatures("tnum").
						SingleLine()
				}
			})

			hitStr := "—"
			if telemetry.HitRatePct != nil {
				hitStr = fmt.Sprintf("%.1f%%", *telemetry.HitRatePct)
			}
			ui.Text(c, hitStr).FontSize(25).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Textf(c, "%d 未命中", telemetry.NoMatchCount+telemetry.NeedsContextCount).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
				if telemetry.ErrorCount > 0 {
					SailBadge(c, fmt.Sprintf("%d 错误", telemetry.ErrorCount), ToneDanger)
				}
			})
		})

		// Card 2: Context Convergence
		ovMetricCard(c, func() {
			ui.Text(c, "上下文收敛率").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()

			compStr := "—"
			if telemetry.CompressionPct != nil {
				compStr = fmt.Sprintf("%.1f%%", *telemetry.CompressionPct)
			}
			ui.Text(c, compStr).FontSize(25).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

			footText := fmt.Sprintf("上限 %d 个工具", settings.MaxTools)
			if telemetry.AvgEligible != nil && telemetry.AvgSelectedOnHit != nil {
				footText = fmt.Sprintf(
					"%d 候选 → %.1f 下发",
					int(math.Round(*telemetry.AvgEligible)),
					*telemetry.AvgSelectedOnHit,
				)
			}
			ui.Text(c, footText).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
		})

		// Card 3: Latency
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "决策延迟 P50").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				if telemetry.P95DiscoverMs != nil {
					ui.Textf(c, "P95 %s ms", ovFormatIntComma(*telemetry.P95DiscoverMs)).
						FontSize(11.5).
						TextColor(SailInkFaint).
						FontFeatures("tnum").
						SingleLine()
				}
			})

			p50Str := "—"
			if telemetry.P50DiscoverMs != nil {
				p50Str = fmt.Sprintf("%s ms", ovFormatIntComma(*telemetry.P50DiscoverMs))
			}
			ui.Text(c, p50Str).FontSize(25).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				foot := fmt.Sprintf("累计 %s tok", FormatCompactTokens(telemetry.TotalTokens))
				if telemetry.AvgInvokeMs != nil {
					foot = fmt.Sprintf("调用均值 %d ms", *telemetry.AvgInvokeMs)
				}
				ui.Text(c, foot).Grow(1).MinWidth(0).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
				if ovRenderLinkBtn(c, "ov-kpi-logs-link", "日志") {
					u.OpenLogsEvent("")
				}
			})
		})

		// Card 4: Open Tool Surface
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "开放工具").FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				ui.Textf(c, "%d/%d 服务在线", activeServerCount, realServerCount).
					FontSize(11.5).
					TextColor(SailInkFaint).
					FontFeatures("tnum").
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(5).Children(func() {
				ui.Textf(c, "%d", setup.EnabledTools).
					FontSize(25).
					Bold().
					TextColor(SailInkStrong).
					FontFeatures("tnum").
					SingleLine()
				ui.Textf(c, "/ %d", realToolCount).
					FontSize(13).
					FontWeight(500).
					TextColor(SailInkFaint).
					FontFeatures("tnum").
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				if writeEnabled > 0 {
					SailBadge(c, fmt.Sprintf("写操作 %d", writeEnabled), ToneWarning)
				} else {
					ui.Textf(c, "%d 只读", readOnlyEnabled).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
				}
				if ovRenderLinkBtn(c, "ov-kpi-catalog-link", "目录") {
					if writeEnabled > 0 {
						u.OpenCatalogFilter("", "rw")
					} else {
						u.OpenCatalogFilter("", "all")
					}
				}
			})
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

func (u *DesktopUI) renderOverviewTrafficStrip(c *ui.Context, telemetry GatewayTelemetry) {
	colorContext := ui.Hex("#94a3b8")
	colorNoMatch := SailBorderStrong
	colorError := ui.Hex("#ef4444")
	colorInvokeOK := ui.Hex("#10b981")

	otherOK := telemetry.SampleSize - telemetry.SelectedCount - telemetry.NeedsContextCount - telemetry.NoMatchCount - telemetry.ErrorCount
	if otherOK < 0 {
		otherOK = 0
	}

	ui.Column(c).
		FillWidth().
		Padding(16, 20).
		Gap(12).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, "请求状态分布").FontSize(13.5).FontWeight(600).TextColor(SailInkStrong)
					if telemetry.SampleSize > 0 {
						ui.Textf(c, "近 %d 次", telemetry.SampleSize).FontSize(12).TextColor(SailInkFaint).FontFeatures("tnum")
					}
				})

				ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
					ovLegendItem(c, SailInkStrong, fmt.Sprintf("命中 %d", telemetry.SelectedCount), SailInkMuted)
					if otherOK > 0 {
						ovLegendItem(c, colorInvokeOK, fmt.Sprintf("调用 %d", otherOK), SailInkMuted)
					}
					if telemetry.NeedsContextCount > 0 {
						ovLegendItem(c, colorContext, fmt.Sprintf("待补上下文 %d", telemetry.NeedsContextCount), SailInkMuted)
					}
					ovLegendItem(c, colorNoMatch, fmt.Sprintf("未命中 %d", telemetry.NoMatchCount), SailInkMuted)
					if telemetry.ErrorCount > 0 {
						ovLegendItem(c, colorError, fmt.Sprintf("错误 %d", telemetry.ErrorCount), SailDangerInk)
					}

					if ovRenderLinkBtn(c, "ov-dist-all-logs", "全部日志") {
						u.OpenLogsEvent("")
					}
				})
			})

			ui.Row(c).
				FillWidth().
				Height(6).
				Radius(999).
				Background(SailBgMuted).
				Gap(2).
				Clip().
				Children(func() {
					if telemetry.SampleSize <= 0 {
						return
					}
					if telemetry.SelectedCount > 0 {
						ui.Box(c).
							Grow(float32(telemetry.SelectedCount)).
							FillHeight().
							Background(SailInkStrong).
							Tooltip(fmt.Sprintf("命中下发: %d", telemetry.SelectedCount))
					}
					if otherOK > 0 {
						ui.Box(c).
							Grow(float32(otherOK)).
							FillHeight().
							Background(colorInvokeOK).
							Tooltip(fmt.Sprintf("调用成功: %d", otherOK))
					}
					if telemetry.NeedsContextCount > 0 {
						ui.Box(c).
							Grow(float32(telemetry.NeedsContextCount)).
							FillHeight().
							Background(colorContext).
							Tooltip(fmt.Sprintf("待补上下文: %d", telemetry.NeedsContextCount))
					}
					if telemetry.NoMatchCount > 0 {
						ui.Box(c).
							Grow(float32(telemetry.NoMatchCount)).
							FillHeight().
							Background(colorNoMatch).
							Tooltip(fmt.Sprintf("未命中: %d", telemetry.NoMatchCount))
					}
					if telemetry.ErrorCount > 0 {
						grow := float32(telemetry.ErrorCount)
						if grow < 1.5 {
							grow = 1.5
						}
						ui.Box(c).
							Grow(grow).
							FillHeight().
							Background(colorError).
							Tooltip(fmt.Sprintf("错误: %d", telemetry.ErrorCount))
					}
				})
		})
}

func ovLegendItem(c *ui.Context, dotColor ui.Color, label string, textColor ui.Color) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(6, 6).Radius(999).Background(dotColor).Shrink(0)
		ui.Text(c, label).FontSize(12).TextColor(textColor).FontFeatures("tnum").SingleLine()
	})
}

func (u *DesktopUI) renderOverviewAttentionCard(c *ui.Context, items []BlindSpotItem) {
	SailFlushCard(c, func() {
		SailListCardHeader(c, "异常与未命中意图", "", func() {
			if len(items) == 0 {
				return
			}
			tone := ToneWarning
			for _, it := range items {
				if it.Status == "error" || it.Status == "failed" {
					tone = ToneDanger
					break
				}
			}
			SailBadge(c, fmt.Sprintf("%d", len(items)), tone)
		})

		if len(items) == 0 {
			ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
				SailEmptyState(c, "无异常或未命中记录", "", "", nil)
			})
			return
		}

		for idx, item := range items {
			it := item
			isLast := idx == len(items)-1
			isErr := it.Status == "error" || it.Status == "failed"

			row := ui.Row(c.Key(fmt.Sprintf("ov-att-%d-%s-%s", idx, it.Status, it.LastEventID))).
				FillWidth().
				Padding(13, 20).
				Gap(14).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			if row.Hovered() {
				row.Background(SailBgHover)
			}
			row.Children(func() {
				// Anchor 1 & 2: Primary Intent + 1-line Summary
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						if isErr {
							SailBadge(c, "错误", ToneDanger)
						} else {
							dotCol := ui.Hex("#94a3b8")
							if it.Status == "needs_context" {
								dotCol = SailWarningInk
							}
							SailStatusDot(c, dotCol)
						}
						ui.Text(c, it.Intent).Grow(1).MinWidth(0).FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						if it.Count > 1 {
							ui.Textf(c, "×%d", it.Count).
								Font("monospace").
								FontSize(11.5).
								TextColor(SailInkFaint).
								FontFeatures("tnum").
								Shrink(0).
								SingleLine()
						}
					})

					if summary := CleanDescriptionSummary(it.ErrorSummary); summary != "" {
						ui.Text(c, summary).FontSize(12).TextColor(SailInkMuted).SingleLine()
					} else {
						ui.Textf(c, "%s · %s", it.Status, FormatTime(it.LastTime)).
							Font("monospace").
							FontSize(11.5).
							TextColor(SailInkFaint).
							SingleLine()
					}
				})

				// Anchor 3: Compact right actions
				ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
					if ui.Button(c.Key("log-"+it.LastEventID), "日志").Padding(4, 9).FontSize(12).Clicked() {
						u.OpenLogsEvent(it.LastEventID)
					}
					replayBtn := ui.Button(c.Key("replay-"+it.LastEventID), "").Padding(4, 9).FontSize(12)
					replayBtn.Children(func() {
						ui.Icon(c, ovIconPlay).Size(10.5, 10.5).TextColor(SailAccent)
						ui.Text(c, "重放").SingleLine()
					})
					if replayBtn.Clicked() {
						u.OpenPlaygroundReplay(it.Intent, it.Actor)
					}
				})
			})
		}
	})
}

func (u *DesktopUI) renderOverviewTopToolsCard(c *ui.Context, topTools []TopToolItem, maxTopHits int) {
	SailFlushCard(c, func() {
		SailListCardHeader(c, "高频命中工具", "", func() {
			if ovRenderLinkBtn(c, "ov-top-all-catalog", "全部目录") {
				u.OpenCatalogFilter("", "all")
			}
		})

		if len(topTools) == 0 {
			ui.Box(c).Padding(18, 20).FillWidth().Children(func() {
				SailEmptyState(c, "暂无命中记录", "", "", nil)
			})
			return
		}

		for idx, tool := range topTools {
			t := tool
			isLast := idx == len(topTools)-1
			row := ui.Row(c.Key("ov-toptool-"+t.ID)).
				FillWidth().
				Padding(12, 20).
				Gap(14).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			if row.Hovered() {
				row.Background(SailBgHover)
			}
			row.Children(func() {
				// Anchor 1: Tool Name (+ RW warning badge only when writable)
				ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, t.Name).
						Font("monospace").
						FontSize(13).
						FontWeight(600).
						TextColor(SailInkStrong).
						SingleLine()
					if !t.ReadOnly {
						SailBadge(c, "写操作", ToneWarning)
					}
				})

				// Anchor 2: Quiet Upstream Name
				if ovRenderLinkBtn(c, "ov-toptool-srv-"+t.ID, t.ServerName) {
					u.OpenServiceDetail(t.ServerID)
				}

				// Anchor 3: Hit bar & count
				pct := float32(math.Round((float64(t.Hits) / float64(maxTopHits)) * 100))
				if pct < 8 {
					pct = 8
				}
				if pct > 100 {
					pct = 100
				}
				ui.Row(c).Width(124).Shrink(0).Gap(10).AlignItems(ui.Center).Justify(ui.End).Children(func() {
					ui.Row(c).
						Width(60).
						Height(5).
						Radius(999).
						Background(SailBgMuted).
						Clip().
						Children(func() {
							ui.Box(c).WidthPercent(pct).FillHeight().Radius(999).Background(SailInkBody)
						})
					ui.Textf(c, "%d 次", t.Hits).
						Width(44).
						Font("monospace").
						FontSize(12).
						FontWeight(600).
						TextColor(SailInkStrong).
						FontFeatures("tnum").
						TextAlign(ui.End).
						SingleLine()
				})
			})
		}
	})
}

func (u *DesktopUI) renderOverviewServersCard(
	c *ui.Context,
	state *Bootstrap,
	realServers []registry.Server,
	realTools []registry.Tool,
) {
	SailFlushCard(c, func() {
		SailListCardHeader(c, "上游服务", "", func() {
			if ovRenderLinkBtn(c, "ov-manage-upstreams", "管理") {
				u.Navigate(ViewServices)
			}
		})

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

			srvToolTotal := 0
			openTotal := 0
			openWrite := 0
			for _, t := range realTools {
				if t.ServerID == s.ID {
					srvToolTotal++
					if t.Enabled {
						openTotal++
						if !t.ReadOnly {
							openWrite++
						}
					}
				}
			}

			check, hasCheck := state.Setup.SourceChecks[s.ID]
			hasErr := hasCheck && check.Status == "error"

			row := ui.ButtonBase(c.Key("ov-srv-row-"+s.ID)).
				FillWidth().
				Padding(13, 20).
				Gap(12).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Cursor(ui.CursorPointer)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			hovered := row.Hovered()
			if hovered {
				row.Background(SailBgHover)
			}

			row.Children(func() {
				// Anchor 1 & 2: Dot + Service Name + 1-line summary
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						dotColor := SailInkFaint
						if hasErr {
							dotColor = SailDangerInk
						} else if s.Enabled {
							dotColor = SailSuccessInk
						}
						SailStatusDot(c, dotColor)

						ui.Text(c, s.Name).
							FontSize(13.5).
							FontWeight(600).
							TextColor(SailInkStrong).
							SingleLine()

						if hasErr {
							SailBadge(c, "连接异常", ToneDanger)
						} else if openWrite > 0 {
							SailBadge(c, fmt.Sprintf("写 ×%d", openWrite), ToneWarning)
						} else if !s.Enabled {
							ui.Text(c, "已停用").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
						}
					})

					if hasErr && check.Message != "" {
						ui.Text(c, CleanDescriptionSummary(check.Message)).FontSize(12).TextColor(SailDangerInk).SingleLine()
					} else {
						ui.Textf(c, "%s · %d/%d 工具开放", ServerProtocolLabel(s), openTotal, srvToolTotal).
							FontSize(12).
							TextColor(SailInkMuted).
							FontFeatures("tnum").
							SingleLine()
					}
				})

				// Anchor 3: Subtle right arrow
				arrowColor := SailInkFaint
				if hovered {
					arrowColor = SailAccent
				}
				ui.Icon(c, IconChevronRight).Size(15, 15).TextColor(arrowColor).Shrink(0)
			})

			if row.Clicked() {
				u.OpenServiceDetail(s.ID)
			}
		}
	})
}

func (u *DesktopUI) renderOverviewEndpointCard(c *ui.Context, state *Bootstrap) {
	ui.Column(c).
		FillWidth().
		Radius(10).
		Background(SailNavyBg).
		Border(1, SailNavyBorder).
		Clip().
		Children(func() {
			ui.Row(c).
				FillWidth().
				Padding(10, 16).
				Background(SailNavyHeader).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailNavyBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6.5, 6.5).Radius(999).Background(ui.Hex("#10b981")).Shrink(0)
						ui.Text(c, "MCP ENDPOINT").
							Font("monospace").
							FontSize(11).
							FontWeight(600).
							TextColor(SailNavyMuted).
							SingleLine()
					})

					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						if ovRenderNavyBtn(c, "ov-ep-copy", "复制") {
							c.WriteClipboard(state.MCPURL)
							u.Notify("已复制 MCP 网关地址")
						}
						if ovRenderNavyBtn(c, "ov-ep-connect-modal", "接入客户端") {
							u.OpenClients()
						}
					})
				})

			ui.Box(c).FillWidth().Padding(14, 16).Children(func() {
				ui.Text(c, state.MCPURL).
					Font("monospace").
					FontSize(12.5).
					TextColor(SailNavyText).
					Selectable()
			})
		})
}

// --- Local Styling & Formatting Helpers ---

func ovRenderLinkBtn(c *ui.Context, key, label string) bool {
	btn := ui.ButtonBase(c.Key(key)).
		Padding(2, 5).
		Radius(4).
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
	})
	return btn.Clicked()
}

func ovRenderNavyBtn(c *ui.Context, key, label string) bool {
	btn := ui.ButtonBase(c.Key(key)).
		Padding(3, 9).
		Radius(5).
		Background(ui.RGBA(255, 255, 255, 0.09)).
		Border(1, SailNavyBorder).
		Cursor(ui.CursorPointer)
	if btn.Hovered() {
		btn.Background(ui.RGBA(255, 255, 255, 0.16))
	}
	btn.Children(func() {
		ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(SailNavyText).SingleLine()
	})
	return btn.Clicked()
}

func ovFormatIntComma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var out []byte
	rem := len(s) % 3
	if rem > 0 {
		out = append(out, s[:rem]...)
		if len(s) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		out = append(out, s[i:i+3]...)
		if i+3 < len(s) {
			out = append(out, ',')
		}
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
