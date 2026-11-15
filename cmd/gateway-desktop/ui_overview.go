package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"gatewaylab/internal/gateway"

	"github.com/egoist/mygo/ui"
)

// overviewLocalState holds UI-thread state local to the Overview page and Connect Client modal.
type overviewLocalState struct {
	bannerDismissed bool
	testingModel    bool
	testErrTitle    string
	testErrDetail   string
	connectTab      int
}

var (
	overviewStatesMu sync.Mutex
	overviewStates   = make(map[*DesktopUI]*overviewLocalState)

	ovIconClose = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>`))
	ovIconPlay  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"><polygon points="6 3 20 12 6 21 6 3"/></svg>`))
	ovIconPlus  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>`))
	ovIconCopy  = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>`))
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

	realServers := make([]gateway.Server, 0, len(state.Registry.Servers))
	activeRealServers := make([]gateway.Server, 0, len(state.Registry.Servers))
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

	realTools := make([]gateway.Tool, 0, len(state.Registry.Tools))
	enabledRealTools := make([]gateway.Tool, 0, len(state.Registry.Tools))
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

	// 1. Page Header
	SailSectionHeader(
		c,
		"运行大盘",
		"实时监控语义路由命中率、工具上下文收敛效率、上游连接健康度与未命中意图盲区。",
		func() {
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
				u.OpenConnectModal()
			}

			addSvcBtn := ui.PrimaryButton(c.Key("ov-hdr-add-svc"), "").Padding(5, 12).FontSize(12.5)
			addSvcBtn.Children(func() {
				ui.Icon(c, ovIconPlus).Size(13.5, 13.5)
				ui.Text(c, "添加上游服务").SingleLine()
			})
			if addSvcBtn.Clicked() {
				u.OpenAddServiceModal()
			}
		},
	)

	// 2. Day-0 / Unverified Setup Banner
	showSetupBanner := !ov.bannerDismissed && (!setup.ToolsReady || !modelReady)
	if showSetupBanner {
		u.renderOverviewSetupBanner(c, ov, state, realServers, modelReady)
	}

	if ov.testErrTitle != "" || ov.testErrDetail != "" {
		u.renderOverviewTestError(c, ov)
	}

	// 3. Row 1: 4 Enterprise Gateway Golden Signal KPI Cards
	u.renderOverviewKPIGrid(c, state, telemetry, len(activeRealServers), len(realServers), len(realTools), readOnlyEnabled, writeEnabled)

	// 4. Row 2: Traffic Outcome Proportion Bar & Legend
	u.renderOverviewTrafficStrip(c, telemetry)

	// 5. Row 3: Two-Column Operational Control Split
	ui.Grid(c).
		FillWidth().
		ColumnTracks(ui.Fr(1.42).Min(0), ui.Fr(0.98).Min(300)).
		Gap(18).
		AlignItems(ui.Start).
		Children(func() {
			// Left Column: Attention / Unmatched Blind Spots + Top Routed Tools
			ui.Column(c).FillWidth().MinWidth(0).Gap(18).Children(func() {
				u.renderOverviewAttentionCard(c, telemetry.AttentionItems)
				u.renderOverviewTopToolsCard(c, telemetry.TopTools, maxTopHits)
			})

			// Right Column: Upstream Services Health Topology + Data Plane Endpoint Card
			ui.Column(c).FillWidth().MinWidth(0).Gap(18).Children(func() {
				u.renderOverviewServersCard(c, state, realServers, realTools)
				u.renderOverviewEndpointCard(c, state)
			})
		})
}

func (u *DesktopUI) renderOverviewSetupBanner(
	c *ui.Context,
	ov *overviewLocalState,
	state *Bootstrap,
	realServers []gateway.Server,
	modelReady bool,
) {
	setup := state.Setup
	settings := state.Settings

	var title, desc string
	if !setup.ToolsReady {
		title = "网关尚未向 Agent 开放任何上游工具"
		desc = "请先接入至少一个 MCP 或 HTTP 服务，并在白名单中启用需要暴露的工具。"
	} else if !setup.ModelConfigured {
		title = "尚未配置 Decisions 语义路由模型"
		desc = "前往「访问与路由」配置模型 Base URL 与密钥后即可启用按需工具发现。"
	} else {
		title = fmt.Sprintf("路由模型 %s 尚未完成连通性校验", settings.Model)
		if state.KeyConfigured {
			desc = "API Key 已就绪，运行一次真实工具发现校验即可确认控制面就绪。"
		} else {
			desc = "当前进程未检测到模型 API Key，请前往「访问与路由」注入密钥并完成校验。"
		}
	}
	_ = modelReady

	ui.Row(c).
		FillWidth().
		Padding(13, 16).
		Gap(14).
		Radius(8).
		Background(SailBgSubtle).
		Border(1, SailWarningBorder).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Start).Children(func() {
				ui.Box(c).Margin(5, 0, 0, 0).Children(func() {
					SailStatusDot(c, SailWarningInk)
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(SailInkStrong)
					ui.Text(c, desc).FontSize(12).TextColor(SailInkMuted)
				})
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
					if SailSmallButton(c.Key("ov-banner-policy-btn"), "配置路由模型").Clicked() {
						u.Navigate(ViewSettings)
					}
				}

				dismissBtn := ui.ButtonBase(c.Key("ov-banner-dismiss-btn")).
					Size(26, 26).
					Radius(5).
					Center().
					Cursor(ui.CursorPointer).
					Label("收起提示").
					Tooltip("收起提示")
				if dismissBtn.Hovered() {
					dismissBtn.Background(SailBgMuted)
				}
				dismissBtn.Children(func() {
					ui.Icon(c, ovIconClose).Size(14, 14).TextColor(SailInkMuted)
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
		Padding(12, 14).
		Gap(12).
		Radius(6).
		Background(SailDangerBg).
		Border(1, SailDangerBorder).
		AlignItems(ui.Start).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				errTitle := ov.testErrTitle
				if errTitle == "" {
					errTitle = "路由模型连通性校验失败"
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

	ui.Grid(c).Columns(4).Gap(14).FillWidth().Children(func() {
		// Card 1: Semantic Routing Hit Rate
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "语义路由命中率").FontSize(12.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				ui.Text(c, fmt.Sprintf("近 %d 次发现", telemetry.DiscoverCount)).
					FontSize(11.5).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
				hitStr := "—"
				if telemetry.HitRatePct != nil {
					hitStr = fmt.Sprintf("%.1f%%", *telemetry.HitRatePct)
				}
				ui.Text(c, hitStr).FontSize(22).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()
				if telemetry.DiscoverCount > 0 {
					ui.Text(c, fmt.Sprintf("(%d / %d)", telemetry.SelectedCount, telemetry.DiscoverCount)).
						FontSize(12.5).
						FontWeight(500).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
				}
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, fmt.Sprintf("%d 未命中 · %d 待补上下文", telemetry.NoMatchCount, telemetry.NeedsContextCount)).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
				if telemetry.ErrorCount > 0 {
					ui.Row(c).Gap(5).AlignItems(ui.Center).Shrink(0).Children(func() {
						SailStatusDot(c, SailDangerInk)
						ui.Text(c, fmt.Sprintf("%d 错误", telemetry.ErrorCount)).
							FontSize(12).
							FontWeight(600).
							TextColor(SailDangerInk).
							FontFeatures("tnum").
							SingleLine()
					})
				}
			})
		})

		// Card 2: Tool Context Convergence Rate
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "工具上下文收敛率").FontSize(12.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				ui.Text(c, "按需暴露 ROI").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
				compStr := "—"
				if telemetry.CompressionPct != nil {
					compStr = fmt.Sprintf("%.1f%%", *telemetry.CompressionPct)
				}
				ui.Text(c, compStr).FontSize(22).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()
				ui.Text(c, "Schema 缩减").FontSize(12.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			})

			footText := fmt.Sprintf("单次下发上限 %d 个工具", settings.MaxTools)
			if telemetry.AvgEligible != nil && telemetry.AvgSelectedOnHit != nil {
				footText = fmt.Sprintf(
					"平均 %d 候选 → 下发 %.1f 个",
					int(math.Round(*telemetry.AvgEligible)),
					*telemetry.AvgSelectedOnHit,
				)
			}
			ui.Text(c, footText).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
		})

		// Card 3: Routing Decision Latency (P50 / P95)
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "路由决策延迟（P50）").FontSize(12.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				modelLabel := settings.Model
				if modelLabel == "" {
					modelLabel = "未配置"
				}
				ui.Text(c, modelLabel).
					Font("monospace").
					FontSize(11.5).
					TextColor(SailInkMuted).
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(6).Children(func() {
				p50Str := "—"
				if telemetry.P50DiscoverMs != nil {
					p50Str = fmt.Sprintf("%s ms", ovFormatIntComma(*telemetry.P50DiscoverMs))
				}
				ui.Text(c, p50Str).FontSize(22).Bold().TextColor(SailInkStrong).FontFeatures("tnum").SingleLine()
				if telemetry.P95DiscoverMs != nil {
					ui.Text(c, fmt.Sprintf("P95 %s ms", ovFormatIntComma(*telemetry.P95DiscoverMs))).
						FontSize(12.5).
						FontWeight(500).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
				}
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				foot := fmt.Sprintf("累计路由消耗 %s tok", FormatCompactTokens(telemetry.TotalTokens))
				if telemetry.AvgInvokeMs != nil {
					foot = fmt.Sprintf("下游调用均值 %d ms", *telemetry.AvgInvokeMs)
				}
				ui.Text(c, foot).Grow(1).MinWidth(0).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()
				if ovRenderLinkBtn(c, "ov-kpi-logs-link", "追踪明细") {
					u.OpenLogsEvent("")
				}
			})
		})

		// Card 4: Open Tool Surface & Read/Write Guardrails
		ovMetricCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, "开放工具面与护栏").FontSize(12.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				ui.Text(c, fmt.Sprintf("%d / %d 服务在线", activeServerCount, realServerCount)).
					FontSize(11.5).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Baseline).Gap(5).Children(func() {
				ui.Text(c, fmt.Sprintf("%d", setup.EnabledTools)).
					FontSize(22).
					Bold().
					TextColor(SailInkStrong).
					FontFeatures("tnum").
					SingleLine()
				ui.Text(c, fmt.Sprintf("/ %d 已开放", realToolCount)).
					FontSize(12.5).
					FontWeight(500).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(6).Children(func() {
				ui.Text(c, fmt.Sprintf("%d 只读 · %d 写操作", readOnlyEnabled, writeEnabled)).
					Grow(1).
					MinWidth(0).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
				if ovRenderLinkBtn(c, "ov-kpi-catalog-link", "治理目录") {
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
		Padding(16, 18).
		Gap(7).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Children(fn)
}

func (u *DesktopUI) renderOverviewTrafficStrip(c *ui.Context, telemetry GatewayTelemetry) {
	colorContext := ui.Hex("#94a3b8")
	colorNoMatch := ui.Hex("#cbd5e1")
	colorError := ui.Hex("#ef4444")
	colorInvokeOK := ui.Hex("#10b981")

	otherOK := telemetry.SampleSize - telemetry.SelectedCount - telemetry.NeedsContextCount - telemetry.NoMatchCount - telemetry.ErrorCount
	if otherOK < 0 {
		otherOK = 0
	}

	ui.Column(c).
		FillWidth().
		Padding(14, 18).
		Gap(10).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
				stripTitle := "最近网关请求状态分布（暂无请求记录）"
				if telemetry.SampleSize > 0 {
					stripTitle = fmt.Sprintf("最近 %d 次网关请求状态分布", telemetry.SampleSize)
				}
				ui.Text(c, stripTitle).FontSize(13).FontWeight(600).TextColor(SailInkStrong)

				ui.Row(c).Gap(14).AlignItems(ui.Center).Wrap().Children(func() {
					ovLegendItem(c, SailInkStrong, fmt.Sprintf("命中下发 %d", telemetry.SelectedCount), SailInkMuted)
					if otherOK > 0 {
						ovLegendItem(c, colorInvokeOK, fmt.Sprintf("调用成功 %d", otherOK), SailInkMuted)
					}
					ovLegendItem(c, colorContext, fmt.Sprintf("需补充上下文 %d", telemetry.NeedsContextCount), SailInkMuted)
					ovLegendItem(c, colorNoMatch, fmt.Sprintf("未命中 %d", telemetry.NoMatchCount), SailInkMuted)
					errInk := SailInkMuted
					if telemetry.ErrorCount > 0 {
						errInk = SailDangerInk
					}
					ovLegendItem(c, colorError, fmt.Sprintf("异常错误 %d", telemetry.ErrorCount), errInk)

					if ovRenderLinkBtn(c, "ov-dist-all-logs", "全部请求日志") {
						u.OpenLogsEvent("")
					}
				})
			})

			// Proportional horizontal status bar
			ui.Row(c).
				FillWidth().
				Height(8).
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
							Tooltip(fmt.Sprintf("命中下发 (selected): %d", telemetry.SelectedCount))
					}
					if otherOK > 0 {
						ui.Box(c).
							Grow(float32(otherOK)).
							FillHeight().
							Background(colorInvokeOK).
							Tooltip(fmt.Sprintf("调用成功 (invoke/agent): %d", otherOK))
					}
					if telemetry.NeedsContextCount > 0 {
						ui.Box(c).
							Grow(float32(telemetry.NeedsContextCount)).
							FillHeight().
							Background(colorContext).
							Tooltip(fmt.Sprintf("需补充上下文 (needs_context): %d", telemetry.NeedsContextCount))
					}
					if telemetry.NoMatchCount > 0 {
						ui.Box(c).
							Grow(float32(telemetry.NoMatchCount)).
							FillHeight().
							Background(colorNoMatch).
							Tooltip(fmt.Sprintf("未命中 (no_match): %d", telemetry.NoMatchCount))
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
							Tooltip(fmt.Sprintf("异常错误 (error): %d", telemetry.ErrorCount))
					}
				})
		})
}

func ovLegendItem(c *ui.Context, dotColor ui.Color, label string, textColor ui.Color) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(6.5, 6.5).Radius(999).Background(dotColor).Shrink(0)
		ui.Text(c, label).FontSize(12).TextColor(textColor).FontFeatures("tnum").SingleLine()
	})
}

func (u *DesktopUI) renderOverviewAttentionCard(c *ui.Context, items []BlindSpotItem) {
	u.ovSectionTableCard(
		c,
		"异常告警与未命中意图盲区",
		"聚合近期网关错误及 Agent 发起但未匹配到开放工具的任务，支持一键带入沙盒复现。",
		func() {
			tone := ToneNeutral
			for _, it := range items {
				if it.Status == "error" || it.Status == "failed" {
					tone = ToneDanger
					break
				}
				tone = ToneWarning
			}
			SailBadge(c, fmt.Sprintf("%d 项待关注", len(items)), tone)
		},
		func() {
			if len(items) == 0 {
				ui.Box(c).Padding(16, 18).FillWidth().Children(func() {
					SailEmptyState(c, "当前无异常或未命中盲区", "近期所有工具发现与调用请求均正常命中。", "", nil)
				})
				return
			}

			// Table header
			ui.Row(c).
				FillWidth().
				Padding(9, 18).
				Gap(12).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailBorder).
				AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, "状态").Width(108).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "任务意图 / 诊断摘要").Grow(1).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "身份").Width(82).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "操作").Width(148).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted).TextAlign(ui.End)
				})

			for idx, item := range items {
				it := item
				isLast := idx == len(items)-1
				row := ui.Row(c.Key(fmt.Sprintf("ov-att-%d-%s-%s", idx, it.Status, it.LastEventID))).
					FillWidth().
					Padding(11, 18).
					Gap(12).
					AlignItems(ui.Center)
				if !isLast {
					row.BorderWidth(0, 0, 1, 0).BorderColor(SailBorder)
				}
				if row.Hovered() {
					row.Background(SailBgHover)
				}
				row.Children(func() {
					// Status badge
					ui.Row(c).Width(108).Shrink(0).AlignItems(ui.Center).Children(func() {
						ovRenderEventStatusBadge(c, it.Status)
					})

					// Intent + diagnostic summary
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
							ui.Text(c, it.Intent).FontSize(13).FontWeight(600).TextColor(SailInkStrong).MaxLines(1)
							if it.Count > 1 {
								ui.Text(c, fmt.Sprintf("×%d", it.Count)).
									Font("monospace").
									FontSize(11.5).
									FontWeight(600).
									TextColor(SailInkMuted).
									FontFeatures("tnum").
									Shrink(0).
									SingleLine()
							}
						})
						sub := it.ErrorSummary
						if sub == "" {
							sub = fmt.Sprintf("%s · 当前开放工具池未匹配到合适工具（或对应写操作已被护栏拦截）", FormatTime(it.LastTime))
						}
						ui.Text(c, sub).FontSize(11.5).TextColor(SailInkMuted).MaxLines(2)
					})

					// Actor
					actor := it.Actor
					if actor == "" {
						actor = "—"
					}
					ui.Text(c, actor).
						Width(82).
						Shrink(0).
						Font("monospace").
						FontSize(11.5).
						TextColor(SailInkMuted).
						SingleLine()

					// Actions: Logs + Sandbox Replay
					ui.Row(c).Width(148).Shrink(0).Gap(6).Justify(ui.End).AlignItems(ui.Center).Children(func() {
						if ui.Button(c.Key("log-"+it.LastEventID), "日志").Padding(4, 9).FontSize(12).Clicked() {
							u.OpenLogsEvent(it.LastEventID)
						}
						replayBtn := ui.Button(c.Key("replay-"+it.LastEventID), "").Padding(4, 9).FontSize(12)
						replayBtn.Children(func() {
							ui.Icon(c, ovIconPlay).Size(11, 11).TextColor(SailAccent)
							ui.Text(c, "沙盒重放").SingleLine()
						})
						if replayBtn.Clicked() {
							u.OpenPlaygroundReplay(it.Intent, it.Actor)
						}
					})
				})
			}
		},
	)
}

func (u *DesktopUI) renderOverviewTopToolsCard(c *ui.Context, topTools []TopToolItem, maxTopHits int) {
	u.ovSectionTableCard(
		c,
		"高频命中工具（Top Routed Tools）",
		"按近期语义路由选中频次排序，识别团队高频依赖能力与冗余长尾工具。",
		func() {
			if SailSmallButton(c.Key("ov-top-all-catalog"), "全部工具目录").Clicked() {
				u.OpenCatalogFilter("", "all")
			}
		},
		func() {
			if len(topTools) == 0 {
				ui.Box(c).Padding(16, 18).FillWidth().Children(func() {
					SailEmptyState(c, "暂无路由命中统计", "当 Agent 或路由沙盒发起工具发现后，命中工具将按频次排行展示。", "", nil)
				})
				return
			}

			ui.Row(c).
				FillWidth().
				Padding(9, 18).
				Gap(12).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailBorder).
				AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, "工具名称").Grow(1).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "所属上游").Width(120).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "权限属性").Width(74).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "命中频次").Width(156).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted).TextAlign(ui.End)
				})

			for idx, tool := range topTools {
				t := tool
				isLast := idx == len(topTools)-1
				row := ui.Row(c.Key("ov-toptool-" + t.ID)).
					FillWidth().
					Padding(10, 18).
					Gap(12).
					AlignItems(ui.Center)
				if !isLast {
					row.BorderWidth(0, 0, 1, 0).BorderColor(SailBorder)
				}
				if row.Hovered() {
					row.Background(SailBgHover)
				}
				row.Children(func() {
					ui.Text(c, t.Name).
						Grow(1).
						MinWidth(0).
						Font("monospace").
						FontSize(12.5).
						FontWeight(600).
						TextColor(SailInkStrong).
						SingleLine()

					ui.Row(c).Width(120).Shrink(0).AlignItems(ui.Center).Children(func() {
						if ovRenderLinkBtn(c, "ov-toptool-srv-"+t.ID, t.ServerName) {
							u.OpenServiceDetail(t.ServerID)
						}
					})

					ui.Row(c).Width(74).Shrink(0).AlignItems(ui.Center).Children(func() {
						if t.ReadOnly {
							SailBadge(c, "只读", ToneNeutral)
						} else {
							SailBadge(c, "写操作", ToneWarning)
						}
					})

					pct := float32(math.Round((float64(t.Hits) / float64(maxTopHits)) * 100))
					if pct < 8 {
						pct = 8
					}
					if pct > 100 {
						pct = 100
					}
					ui.Row(c).Width(156).Shrink(0).Gap(10).AlignItems(ui.Center).Justify(ui.End).Children(func() {
						ui.Row(c).
							Width(76).
							Height(6).
							Radius(999).
							Background(SailBgMuted).
							Clip().
							Children(func() {
								ui.Box(c).WidthPercent(pct).FillHeight().Radius(999).Background(SailInkBody)
							})
						ui.Text(c, fmt.Sprintf("%d 次", t.Hits)).
							Width(46).
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
		},
	)
}

func (u *DesktopUI) renderOverviewServersCard(
	c *ui.Context,
	state *Bootstrap,
	realServers []gateway.Server,
	realTools []gateway.Tool,
) {
	u.ovSectionTableCard(
		c,
		"上游服务拓扑",
		"已注册的下游 MCP 与 HTTP 服务实例健康度与工具暴露概览。",
		func() {
			if SailSmallButton(c.Key("ov-manage-upstreams"), "管理上游").Clicked() {
				u.Navigate(ViewServices)
			}
		},
		func() {
			if len(realServers) == 0 {
				ui.Box(c).Padding(16, 18).FillWidth().Children(func() {
					SailEmptyState(
						c,
						"尚未接入上游服务",
						"支持远程 Streamable HTTP MCP、stdio 进程或 REST/OpenAPI 服务。",
						"添加第一个上游",
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

				row := ui.ButtonBase(c.Key("ov-srv-row-" + s.ID)).
					FillWidth().
					Padding(12, 18).
					Gap(12).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Cursor(ui.CursorPointer)
				if !isLast {
					row.BorderWidth(0, 0, 1, 0).BorderColor(SailBorder)
				}
				if row.Hovered() {
					row.Background(SailBgHover)
				}

				row.Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).FillWidth().Gap(7).AlignItems(ui.Center).Wrap().Children(func() {
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

							ui.Box(c).
								Padding(1, 6).
								Radius(4).
								Background(SailBgSubtle).
								Border(1, SailBorder).
								Children(func() {
									ui.Text(c, ServerProtocolLabel(s)).
										Font("monospace").
										FontSize(11).
										TextColor(SailInkMuted).
										SingleLine()
								})

							if hasErr {
								SailBadge(c, "连接异常", ToneDanger)
							} else if s.Enabled {
								SailBadge(c, "已启用", ToneSuccess)
							} else {
								SailBadge(c, "已停用", ToneNeutral)
							}
						})

						ui.Text(
							c,
							fmt.Sprintf("开放 %d / %d 工具 · %d 只读 / %d 写操作", openTotal, srvToolTotal, openTotal-openWrite, openWrite),
						).FontSize(12).TextColor(SailInkMuted).FontFeatures("tnum").SingleLine()

						if hasErr && check.Message != "" {
							ui.Text(c, check.Message).FontSize(11.5).TextColor(SailDangerInk).SingleLine()
						}
					})

					ui.Text(c, "详情 →").
						FontSize(12).
						FontWeight(600).
						TextColor(SailAccent).
						Shrink(0).
						SingleLine()
				})

				if row.Clicked() {
					u.OpenServiceDetail(s.ID)
				}
			}
		},
	)
}

func (u *DesktopUI) renderOverviewEndpointCard(c *ui.Context, state *Bootstrap) {
	ui.Column(c).
		FillWidth().
		Radius(8).
		Background(SailNavyBg).
		Border(1, SailNavyBorder).
		Shadow(0, 4, 12, 0, ui.RGBA(10, 37, 64, 0.14)).
		Clip().
		Children(func() {
			// Header
			ui.Row(c).
				FillWidth().
				Padding(10, 14).
				Background(SailNavyHeader).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailNavyBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(7, 7).Radius(999).Background(ui.Hex("#10b981")).Shrink(0)
						ui.Text(c, "DATA PLANE ENDPOINT").
							Font("monospace").
							FontSize(11.5).
							FontWeight(600).
							TextColor(SailNavyMuted).
							SingleLine()
					})

					if ovRenderNavyBtn(c, "ov-ep-copy", "复制地址") {
						c.WriteClipboard(state.MCPURL)
						u.Notify("已复制 MCP 网关地址")
					}
				})

			// Endpoint URL
			ui.Box(c).FillWidth().Padding(12, 14).Children(func() {
				ui.Text(c, state.MCPURL).
					Font("monospace").
					FontSize(12.5).
					TextColor(SailNavyText).
					Selectable()
			})

			// Footer
			ui.Row(c).
				FillWidth().
				Padding(9, 14).
				BorderWidth(1, 0, 0, 0).
				BorderColor(SailNavyBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Gap(8).
				Children(func() {
					ui.Text(c, "统一暴露 3 个元工具收敛上下文").
						FontSize(12).
						TextColor(SailNavyMuted).
						SingleLine()

					if ovRenderNavyBtn(c, "ov-ep-connect-modal", "客户端接入指令") {
						u.OpenConnectModal()
					}
				})
		})
}

func (u *DesktopUI) ovSectionTableCard(
	c *ui.Context,
	title string,
	subtitle string,
	right func(),
	body func(),
) {
	ui.Column(c).
		FillWidth().
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Clip().
		Children(func() {
			ui.Row(c).
				FillWidth().
				Padding(15, 18).
				Gap(12).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, title).FontSize(14.5).FontWeight(600).TextColor(SailInkStrong)
						if subtitle != "" {
							ui.Text(c, subtitle).FontSize(12).TextColor(SailInkMuted)
						}
					})
					if right != nil {
						ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(right)
					}
				})
			body()
		})
}

// --- Connect Client Configuration Modal ---

type ovClientTabSpec struct {
	id       string
	label    string
	hint     string
	wrapText bool
}

var ovClientTabs = []ovClientTabSpec{
	{
		id:    "codex",
		label: "Codex CLI",
		hint:  "在终端执行以下命令挂载网关：",
	},
	{
		id:    "claude-code",
		label: "Claude Code",
		hint:  "在终端执行以下命令挂载 Streamable HTTP 网关：",
	},
	{
		id:    "cursor",
		label: "Cursor",
		hint:  "写入 ~/.cursor/mcp.json 或工作区 .cursor/mcp.json：",
	},
	{
		id:    "claude-desktop",
		label: "Claude Desktop",
		hint:  "写入 ~/Library/Application Support/Claude/claude_desktop_config.json（兼容通用 mcpServers 客户端）：",
	},
	{
		id:       "guide",
		label:    "系统提示词规范",
		hint:     "推荐注入到 Agent 系统提示词（AGENTS.md / CLAUDE.md）以引导按需工具发现：",
		wrapText: true,
	},
}

func ovBuildConnectSnippet(tabID string, mcpURL string) string {
	switch tabID {
	case "codex":
		return fmt.Sprintf("codex mcp add mcp-gateway --url %s", mcpURL)
	case "claude-code":
		return fmt.Sprintf("claude mcp add --transport http mcp-gateway %s", mcpURL)
	case "cursor":
		return PrettyJSON(map[string]any{
			"mcpServers": map[string]any{
				"mcp-gateway": map[string]any{
					"url": mcpURL,
				},
			},
		})
	case "claude-desktop":
		return PrettyJSON(map[string]any{
			"mcpServers": map[string]any{
				"mcp-gateway": map[string]any{
					"type": "http",
					"url":  mcpURL,
				},
			},
		})
	default:
		return "需要外部工具时，先向 mcp-gateway 的 discover_tools 提交当前意图与上下文；网关按相关性返回最小候选工具 Schema 后，再调用 invoke_tool 执行。任务目标切换时重新调用 discover_tools。"
	}
}

func (u *DesktopUI) renderConnectModal(c *ui.Context) {
	if u.State == nil {
		return
	}
	state := u.State
	ov := u.overviewState()
	if ov.connectTab < 0 || ov.connectTab >= len(ovClientTabs) {
		ov.connectTab = 0
	}

	// Build verification prompt using first enabled real tool if available.
	activeReal := make(map[string]bool, len(state.Registry.Servers))
	for _, s := range state.Registry.Servers {
		if !s.Demo && s.Enabled {
			activeReal[s.ID] = true
		}
	}
	var firstTool *gateway.Tool
	for i := range state.Registry.Tools {
		t := &state.Registry.Tools[i]
		if t.Enabled && activeReal[t.ServerID] {
			firstTool = t
			break
		}
	}
	samplePrompt := "请先调用 mcp-gateway 的 discover_tools 描述当前任务查找工具，再按返回的 input_schema 调用 invoke_tool 执行。"
	if firstTool != nil {
		taskDesc := firstTool.Description
		if taskDesc == "" {
			taskDesc = firstTool.Name
		}
		samplePrompt = fmt.Sprintf(
			"请通过 mcp-gateway 完成任务：%s。先调用 discover_tools 查找匹配工具，再按返回的 input_schema 调用 invoke_tool。",
			taskDesc,
		)
	}

	activeSpec := ovClientTabs[ov.connectTab]
	snippet := ovBuildConnectSnippet(activeSpec.id, state.MCPURL)

	ui.DialogBase(c.Key("connect-client-modal"), &u.ShowConnectModal, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(26, 31, 54, 0.38)).Padding(20)
		panel.
			Width(660).
			MaxWidthPercent(94).
			MaxHeightPercent(90).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 16, 36, 0, ui.RGBA(26, 31, 54, 0.22)).
			Label("接入 MCP 客户端").
			Clip()

		// Modal Header
		ui.Row(c).
			FillWidth().
			Shrink(0).
			Padding(15, 20).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailBorder).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Text(c, "接入 MCP 客户端").FontSize(16).Bold().TextColor(SailInkStrong)
				closeBtn := ui.ButtonBase(c.Key("connect-modal-close")).
					Size(28, 28).
					Radius(6).
					Center().
					Cursor(ui.CursorPointer).
					Label("关闭弹窗")
				if closeBtn.Hovered() {
					closeBtn.Background(SailBgMuted)
				}
				closeBtn.Children(func() {
					ui.Icon(c, ovIconClose).Size(15, 15).TextColor(SailInkMuted)
				})
				if closeBtn.Clicked() {
					u.ShowConnectModal = false
				}
			})

		// Modal Body
		ui.Scroll(c.Key("connect-modal-body")).
			FillWidth().
			Shrink(1).
			MinHeight(0).
			MaxHeight(480).
			Padding(18, 20).
			Gap(14).
			Children(func() {
				ui.Text(
					c,
					"所有 Agent 客户端只需挂载下方单一 Streamable HTTP 端点。网关仅向客户端暴露固定的 3 个元工具（discover_tools、get_tool_schema、invoke_tool），避免下游上百个工具撑爆上下文。",
				).FontSize(12.5).LineHeight(1.5).TextColor(SailInkMuted)

				// Deep Navy Client Config Card
				ui.Column(c).
					FillWidth().
					Radius(8).
					Background(SailNavyBg).
					Border(1, SailNavyBorder).
					Clip().
					Children(func() {
						// Top Endpoint Row
						ui.Row(c).
							FillWidth().
							Padding(10, 14).
							Background(SailNavyHeader).
							BorderWidth(0, 0, 1, 0).
							BorderColor(SailNavyBorder).
							AlignItems(ui.Center).
							Justify(ui.SpaceBetween).
							Gap(10).
							Wrap().
							Children(func() {
								ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
									ui.Box(c).Size(7, 7).Radius(999).Background(ui.Hex("#10b981")).Shrink(0)
									ui.Text(c, "MCP ENDPOINT").
										Font("monospace").
										FontSize(11.5).
										FontWeight(600).
										TextColor(SailNavyMuted).
										SingleLine()
								})

								ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Text(c, state.MCPURL).
										Font("monospace").
										FontSize(12).
										TextColor(ui.Hex("#ffffff")).
										Selectable().
										SingleLine()
									if ovRenderNavyBtn(c, "connect-copy-url", "复制地址") {
										c.WriteClipboard(state.MCPURL)
										u.Notify("已复制 MCP 地址")
									}
								})
							})

						// Client Selector Tabs + Copy Config Button
						ui.Row(c).
							FillWidth().
							Padding(8, 12).
							Background(ui.RGBA(255, 255, 255, 0.03)).
							BorderWidth(0, 0, 1, 0).
							BorderColor(SailNavyBorder).
							AlignItems(ui.Center).
							Justify(ui.SpaceBetween).
							Gap(8).
							Wrap().
							Children(func() {
								tabs := ui.TabsBase(c.Key("connect-client-tabs"), &ov.connectTab, len(ovClientTabs))
								tabs.List.Gap(4).Wrap().Children(func() {
									for i, spec := range ovClientTabs {
										active := ov.connectTab == i
										tab := tabs.Tab(i).
											Padding(4, 10).
											Radius(5).
											Cursor(ui.CursorPointer)
										if active {
											tab.Background(ui.RGBA(99, 91, 255, 0.62))
										} else if tab.Hovered() {
											tab.Background(ui.RGBA(255, 255, 255, 0.09))
										}
										tabColor := SailNavyMuted
										weight := 500
										if active {
											tabColor = ui.Hex("#ffffff")
											weight = 600
										}
										tab.Children(func() {
											ui.Text(c, spec.label).
												FontSize(12).
												FontWeight(weight).
												TextColor(tabColor).
												SingleLine()
										})
									}
								})

								if ovRenderNavyBtn(c, "connect-copy-snippet", "复制配置") {
									c.WriteClipboard(snippet)
									u.Notify("已复制到剪贴板")
								}
							})

						// Target config path / usage hint
						ui.Box(c).
							FillWidth().
							Padding(8, 14, 0, 14).
							Children(func() {
								ui.Text(c, activeSpec.hint).FontSize(11.5).TextColor(SailNavyMuted)
							})

						// Snippet Preview
						if activeSpec.wrapText {
							ui.Box(c).FillWidth().Padding(10, 14, 14, 14).Children(func() {
								ui.Text(c, snippet).
									Font("monospace").
									FontSize(12.5).
									LineHeight(1.55).
									TextColor(SailNavyText).
									Selectable()
							})
						} else {
							ui.ScrollBoth(c.Key("connect-snippet-scroll")).
								FillWidth().
								MaxHeight(240).
								Padding(10, 14, 14, 14).
								Children(func() {
									ui.Text(c, snippet).
										Font("monospace").
										FontSize(12.5).
										LineHeight(1.55).
										TextColor(SailNavyText).
										Selectable().
										NoWrap()
								})
						}
					})

				if state.Access.Shared {
					SailAlertBox(
						c,
						ToneWarning,
						"共享鉴权模式提示",
						"当前实例处于共享鉴权模式，客户端请求需携带管理员签发的 Authorization: Bearer <token> 请求头。",
					)
				}
			})

		// Modal Footer
		ui.Row(c).
			FillWidth().
			Shrink(0).
			Padding(12, 20).
			Background(SailBgSubtle).
			BorderWidth(1, 0, 0, 0).
			BorderColor(SailBorder).
			AlignItems(ui.Center).
			Justify(ui.End).
			Gap(8).
			Children(func() {
				promptBtn := ui.Button(c.Key("connect-copy-prompt"), "").Padding(6, 12).FontSize(12.5)
				promptBtn.Children(func() {
					ui.Icon(c, ovIconCopy).Size(13, 13)
					ui.Text(c, "复制连通性验证 Prompt").SingleLine()
				})
				if promptBtn.Clicked() {
					c.WriteClipboard(samplePrompt)
					u.Notify("已复制验证 Prompt")
				}

				if ui.PrimaryButton(c.Key("connect-done-btn"), "完成").Padding(6, 16).FontSize(12.5).Clicked() {
					u.ShowConnectModal = false
				}
			})
	})
}

// --- Local Styling & Formatting Helpers ---

func ovRenderEventStatusBadge(c *ui.Context, status string) {
	isErr := status == "error" || status == "failed"
	isWarn := status == "needs_context"
	isNeutral := status == "no_match"

	dotColor := SailSuccessInk
	textColor := SailInkBody
	if isErr {
		dotColor = SailDangerInk
		textColor = SailDangerInk
	} else if isWarn {
		dotColor = SailWarningInk
		textColor = SailInkBody
	} else if isNeutral {
		dotColor = ui.Hex("#94a3b8")
		textColor = SailInkMuted
	}

	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(6.5, 6.5).Radius(999).Background(dotColor).Shrink(0)
		ui.Text(c, status).Font("monospace").FontSize(12).TextColor(textColor).SingleLine()
	})
}

func ovRenderLinkBtn(c *ui.Context, key, label string) bool {
	btn := ui.ButtonBase(c.Key(key)).
		Padding(2, 4).
		Radius(4).
		Cursor(ui.CursorPointer)
	hovered := btn.Hovered()
	if hovered {
		btn.Background(SailAccentSoft)
	}
	btn.Children(func() {
		t := ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(SailAccent).SingleLine()
		if hovered {
			t.TextColor(SailAccentHover).Underline()
		}
	})
	return btn.Clicked()
}

func ovRenderNavyBtn(c *ui.Context, key, label string) bool {
	btn := ui.ButtonBase(c.Key(key)).
		Padding(4, 10).
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
