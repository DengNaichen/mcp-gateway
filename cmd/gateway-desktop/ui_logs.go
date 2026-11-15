package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"gatewaylab/internal/gateway"

	"github.com/egoist/mygo/ui"
)

const logsDefaultTimelineLimit = 150

var (
	logsStatesMu sync.Mutex
	logsStates   = make(map[*DesktopUI]*logsPageState)
)

type logsPageState struct {
	query         string
	kindIdx       int // 0: 全部, 1: discover, 2: invoke, 3: agent / agent_tool
	statusIdx     int // 0: 全部, 1: 成功, 2: 未命中, 3: 错误
	selectedID    string
	payloadTab    int // 0: 完整事件 JSON, 1: Detail 详情载荷
	timelineLimit int
	timelineScrl  ui.ScrollState
}

func logsStateFor(u *DesktopUI) *logsPageState {
	logsStatesMu.Lock()
	defer logsStatesMu.Unlock()
	st, ok := logsStates[u]
	if !ok {
		st = &logsPageState{
			timelineLimit: logsDefaultTimelineLimit,
		}
		logsStates[u] = st
	}
	return st
}

func (st *logsPageState) hasActiveFilters() bool {
	return strings.TrimSpace(st.query) != "" || st.kindIdx != 0 || st.statusIdx != 0
}

func (st *logsPageState) resetFilters() {
	st.query = ""
	st.kindIdx = 0
	st.statusIdx = 0
	st.timelineLimit = logsDefaultTimelineLimit
}

type logsMatchedTool struct {
	ID          string
	Name        string
	ServerID    string
	Description string
	Reason      string
	ReadOnly    bool
	HasReadOnly bool
}

type logsParsedDetail struct {
	Model         string
	Summary       string
	Question      string
	Error         string
	Eligible      *int
	PromptTokens  *int
	OutputTokens  *int
	Servers       []string
	Tools         []logsMatchedTool
	ToolID        string
	Fields        []string
	Client        string
	Method        string
	AgentToolName string
	DetailJSON    string
	FullEventJSON string
}

func (u *DesktopUI) renderLogs(c *ui.Context) {
	if u.State == nil {
		return
	}
	st := logsStateFor(u)
	events := u.State.Events

	toolByID := make(map[string]gateway.Tool, len(u.State.Registry.Tools))
	for _, t := range u.State.Registry.Tools {
		toolByID[t.ID] = t
	}

	// Consume cross-page navigation handoff (e.g. from Overview blind-spot row).
	if u.SelectedEventID != "" {
		targetID := u.SelectedEventID
		u.SelectedEventID = ""
		st.selectedID = targetID
		for idx, ev := range events {
			if ev.ID == targetID {
				if !logsMatchesFilters(ev, st) {
					st.resetFilters()
				}
				if idx >= st.timelineLimit {
					st.timelineLimit = idx + 50
				}
				break
			}
		}
	}

	// Compute summary counts across all events for quick context.
	var (
		okCount        int
		unmatchedCount int
		errCount       int
	)
	for _, ev := range events {
		switch {
		case logsIsErrorStatus(ev.Status):
			errCount++
		case logsIsUnmatchedStatus(ev.Status):
			unmatchedCount++
		default:
			okCount++
		}
	}

	filtered := make([]gateway.Event, 0, len(events))
	for _, ev := range events {
		if logsMatchesFilters(ev, st) {
			filtered = append(filtered, ev)
		}
	}

	var activeEvent *gateway.Event
	if len(filtered) > 0 {
		for i := range filtered {
			if filtered[i].ID == st.selectedID {
				activeEvent = &filtered[i]
				break
			}
		}
		if activeEvent == nil {
			activeEvent = &filtered[0]
			st.selectedID = filtered[0].ID
		}
	}

	// 1. Page Header.
	SailSectionHeader(
		c,
		"请求日志",
		"实时追踪数据面语义路由发现（discover）、下游工具调用（invoke）与 MCP 客户端接入会话，支持一键重放复现。",
		func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				SailBadgeDot(c, "每 4s 自动同步", ToneSuccess)
				if errCount > 0 {
					SailBadge(c, fmt.Sprintf("错误 %d", errCount), ToneDanger)
				}
				if unmatchedCount > 0 {
					SailBadge(c, fmt.Sprintf("未命中 %d", unmatchedCount), ToneWarning)
				}
				if SailSmallButton(c.Key("logs-refresh-btn"), "立即刷新").Disabled(u.Refreshing).Clicked() {
					u.Refresh()
				}
			})
		},
	)

	// 2. Multi-dimensional Filter Bar.
	SailCard(c.Key("logs-filter-card"), func() {
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.SearchField(c.Key("logs-search"), &st.query).
					Placeholder("搜索意图、工具名、Actor、错误或 JSON 载荷…").
					Label("搜索请求日志").
					Width(256)

				ui.Segmented(
					c.Key("logs-kind-seg"),
					&st.kindIdx,
					"全部",
					"discover 语义发现",
					"invoke 工具调用",
					"agent_tool MCP 接入",
				).Label("事件类型筛选")

				ui.Segmented(
					c.Key("logs-status-seg"),
					&st.statusIdx,
					"全部",
					"成功",
					"未命中",
					"错误",
				).Label("事件状态筛选")
			})

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if st.hasActiveFilters() {
					if SailSmallButton(c.Key("logs-reset-btn"), "重置筛选").Clicked() {
						st.resetFilters()
					}
				}
				ui.Textf(c, "匹配 %d / %d 条", len(filtered), len(events)).
					FontSize(12.5).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})
		})
	})

	// 3. Empty State or Split Master-Detail Workbench.
	if len(filtered) == 0 {
		if len(events) == 0 {
			SailEmptyState(
				c,
				"暂无请求追踪记录",
				"通过 MCP 客户端发起工具检索与调用，或在「路由沙盒」执行一次测试后，完整请求轨迹与 JSON 载荷将实时呈现在此处。",
				"前往路由沙盒测试",
				func() {
					u.Navigate(ViewPlayground)
				},
			)
		} else {
			var resetFn func()
			resetLbl := ""
			if st.hasActiveFilters() {
				resetLbl = "清空筛选条件"
				resetFn = st.resetFilters
			}
			SailEmptyState(
				c,
				"没有匹配的请求日志",
				fmt.Sprintf("当前共有 %d 条历史记录（成功 %d · 未命中 %d · 错误 %d），请尝试放宽事件类型、状态或关键词过滤条件。", len(events), okCount, unmatchedCount, errCount),
				resetLbl,
				resetFn,
			)
		}
		return
	}

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(16).Children(func() {
		// Left Column: Timeline List.
		u.renderLogsTimelinePane(c, st, filtered, activeEvent)

		// Right Column: Selected Event Trace Inspector.
		if activeEvent != nil {
			u.renderLogsInspectorPane(c, st, *activeEvent, toolByID)
		}
	})
}

func (u *DesktopUI) renderLogsTimelinePane(
	c *ui.Context,
	st *logsPageState,
	filtered []gateway.Event,
	activeEvent *gateway.Event,
) {
	visibleCount := len(filtered)
	if st.timelineLimit <= 0 {
		st.timelineLimit = logsDefaultTimelineLimit
	}
	hasMore := false
	if visibleCount > st.timelineLimit {
		visibleCount = st.timelineLimit
		hasMore = true
	}

	ui.Column(c.Key("logs-timeline-card")).
		Width(384).
		Shrink(0).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Clip().
		Children(func() {
			// Timeline Header.
			ui.Row(c).
				FillWidth().
				Padding(10, 14).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailBorder).
				Children(func() {
					ui.Text(c, "请求时间线").FontSize(12).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Textf(c, "%d 条", len(filtered)).
						FontSize(11.5).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
				})

			// Scrollable Timeline Rows.
			ui.Scroll(c.Key("logs-timeline-scroll")).
				TrackScroll(&st.timelineScrl).
				FillWidth().
				MaxHeight(640).
				Padding(8).
				Gap(6).
				Children(func() {
					seenRowKeys := make(map[string]int, visibleCount)
					for i := 0; i < visibleCount; i++ {
						ev := filtered[i]
						rowKey := ev.ID
						if rowKey == "" || seenRowKeys[rowKey] > 0 {
							rowKey = fmt.Sprintf("%s#%d", ev.ID, i)
						}
						seenRowKeys[ev.ID]++
						selected := activeEvent != nil && activeEvent.ID == ev.ID

						cardBg := SailBgCanvas
						cardBorder := SailBorder
						if selected {
							cardBg = SailAccentSoft
							cardBorder = SailAccentBorder
						}

						rowBtn := ui.ButtonBase(c.Key("logs-item-" + rowKey)).
							Column().
							FillWidth().
							Padding(10, 12).
							Gap(5).
							Radius(6).
							AlignItems(ui.Stretch).
							Background(cardBg).
							Border(1, cardBorder).
							Cursor(ui.CursorPointer)
						if rowBtn.Hovered() && !selected {
							rowBtn.Background(SailBgHover).Border(1, SailBorderStrong)
						}

						rowBtn.Children(func() {
							// Row 1: Status dot + Kind badge + Status code + Duration ms.
							ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
									SailStatusDot(c, logsStatusDotColor(ev.Status))
									SailBadge(c, logsKindBadgeLabel(ev.Kind), logsKindTone(ev.Kind))
									ui.Text(c, ev.Status).
										Font("monospace").
										FontSize(11.5).
										TextColor(logsStatusInkColor(ev.Status)).
										SingleLine()
								})

								ui.Textf(c, "%d ms", ev.DurationMS).
									Shrink(0).
									Font("monospace").
									FontSize(11.5).
									FontWeight(600).
									TextColor(SailInkMuted).
									FontFeatures("tnum").
									SingleLine()
							})

							// Row 2: Intent summary or Tool ID/name.
							titleText := strings.TrimSpace(ev.Title)
							if titleText == "" {
								titleText = "（无标题事件）"
							}
							titleColor := SailInkStrong
							if selected {
								titleColor = SailAccent
							}
							titleElem := ui.Text(c, titleText).
								FontSize(13).
								FontWeight(600).
								TextColor(titleColor).
								MaxLines(2)
							if ev.Kind == "invoke" || ev.Kind == "agent" || ev.Kind == "agent_tool" {
								titleElem.Font("monospace").FontSize(12.5)
							}

							// Row 3: Timestamp + Actor.
							actorLabel := ev.Actor
							if actorLabel == "" {
								actorLabel = "anonymous"
							}
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								ui.Text(c, FormatTime(ev.Time)).
									FontSize(11.5).
									TextColor(SailInkMuted).
									FontFeatures("tnum").
									SingleLine()

								ui.Text(c, "Actor: "+actorLabel).
									Font("monospace").
									FontSize(11.5).
									TextColor(SailInkFaint).
									SingleLine()
							})
						})

						if rowBtn.Clicked() {
							st.selectedID = ev.ID
						}
					}

					if hasMore {
						ui.Row(c).FillWidth().Padding(6, 0).Justify(ui.Center).Children(func() {
							if SailSmallButton(c.Key("logs-load-more"), fmt.Sprintf("加载更多（剩余 %d 条）", len(filtered)-visibleCount)).Clicked() {
								st.timelineLimit += logsDefaultTimelineLimit
							}
						})
					}
				})
		})
}

func (u *DesktopUI) renderLogsInspectorPane(
	c *ui.Context,
	st *logsPageState,
	ev gateway.Event,
	toolByID map[string]gateway.Tool,
) {
	parsed := logsParseEventDetail(ev, toolByID)

	ui.Column(c.Key("logs-inspector-" + ev.ID)).
		Grow(1).
		MinWidth(0).
		Padding(20).
		Gap(16).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Children(func() {
			// 1. Inspector Header + Quick Actions.
			ui.Row(c).FillWidth().AlignItems(ui.Start).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
						SailBadgeDot(c, ev.Status, logsStatusTone(ev.Status))
						SailBadge(c, logsKindFullLabel(ev.Kind), logsKindTone(ev.Kind))
						ui.Text(c, ev.ID).
							Font("monospace").
							FontSize(11.5).
							TextColor(SailInkFaint).
							Selectable().
							SingleLine()
					})

					titleText := strings.TrimSpace(ev.Title)
					if titleText == "" {
						titleText = ev.ID
					}
					titleElem := ui.Text(c, titleText).
						FontSize(16.5).
						Bold().
						TextColor(SailInkStrong).
						Selectable()
					if ev.Kind == "invoke" {
						titleElem.Font("monospace").FontSize(15.5)
					}
				})

				ui.Row(c).Shrink(0).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					if ev.Kind == "discover" && strings.TrimSpace(ev.Title) != "" {
						if SailSmallButton(c.Key("logs-replay-"+ev.ID), "在沙盒中重放").Clicked() {
							u.OpenPlaygroundReplay(ev.Title, ev.Actor)
						}
					}
					if ev.Kind == "invoke" {
						targetToolID := parsed.ToolID
						if targetToolID == "" {
							targetToolID = ev.Title
						}
						if regTool, ok := toolByID[targetToolID]; ok {
							if SailSmallButton(c.Key("logs-jump-cat-"+ev.ID), "查看工具契约").Clicked() {
								catalogSelectToolFromLogs(u, regTool.ID, regTool.ServerID)
								u.Navigate(ViewCatalog)
							}
						}
					}
					if ui.PrimaryButton(c.Key("logs-copy-json-"+ev.ID), "复制事件 JSON").Clicked() {
						c.WriteClipboard(parsed.FullEventJSON)
						u.Notify("已复制事件完整 JSON")
					}
				})
			})

			// 2. Key Trace Metrics Strip (耗时、Token 消耗、Actor、时间、模型、候选池).
			ui.Row(c).FillWidth().Wrap().Gap(10).Children(func() {
				logsMetricTile(
					c,
					"执行耗时",
					fmt.Sprintf("%d ms", ev.DurationMS),
					logsDurationHint(ev.DurationMS),
					true,
				)

				tokMain := "—"
				tokSub := "未触发模型推理计费"
				if parsed.PromptTokens != nil || parsed.OutputTokens != nil {
					pt, ot := 0, 0
					if parsed.PromptTokens != nil {
						pt = *parsed.PromptTokens
					}
					if parsed.OutputTokens != nil {
						ot = *parsed.OutputTokens
					}
					tokMain = fmt.Sprintf("%d tok", pt+ot)
					tokSub = fmt.Sprintf("输入 %d · 输出 %d", pt, ot)
				}
				logsMetricTile(c, "Token 消耗", tokMain, tokSub, true)

				actorStr := ev.Actor
				if actorStr == "" {
					actorStr = "anonymous"
				}
				logsMetricTile(c, "调用身份 (Actor)", actorStr, "请求授权上下文", true)

				logsMetricTile(c, "发生时间", FormatTime(ev.Time), ev.Time.Local().Format("2006-01-02"), true)
			})

			// Secondary metadata row when model / candidate pool / protocol details exist.
			hasExtraMeta := parsed.Model != "" ||
				parsed.Eligible != nil ||
				len(parsed.Servers) > 0 ||
				parsed.Client != "" ||
				parsed.Method != "" ||
				len(parsed.Fields) > 0
			if hasExtraMeta {
				SailSubtleCard(c, func() {
					if parsed.Model != "" {
						SailKeyValueRow(c, "路由决策模型", parsed.Model, true)
					}
					if parsed.Eligible != nil {
						SailKeyValueRow(c, "候选工具池收敛", fmt.Sprintf("%d 个授权候选工具 → 命中 %d 个", *parsed.Eligible, len(parsed.Tools)), false)
					}
					if len(parsed.Servers) > 0 {
						SailKeyValueRow(c, "分层路由命中服务", strings.Join(parsed.Servers, ", "), true)
					}
					if parsed.Method != "" {
						SailKeyValueRow(c, "MCP 协议方法", parsed.Method, true)
					}
					if parsed.AgentToolName != "" {
						SailKeyValueRow(c, "MCP 元工具", parsed.AgentToolName, true)
					}
					if parsed.Client != "" {
						SailKeyValueRow(c, "客户端标识", parsed.Client, true)
					}
					if len(parsed.Fields) > 0 {
						SailKeyValueRow(c, "响应投影字段", strings.Join(parsed.Fields, ", "), true)
					}
				})
			}

			// 3. Semantic Summary / Question / Error Banners.
			if parsed.Summary != "" {
				SailAlertBox(c, ToneInfo, "路由决策摘要", parsed.Summary)
			}
			if parsed.Question != "" {
				SailAlertBox(c, ToneWarning, "模型请求补充上下文 (needs_context)", parsed.Question)
			}
			if parsed.Error != "" {
				SailAlertBox(c, ToneDanger, "执行异常与错误详情", parsed.Error)
			}

			// 4. Matched Candidate Tools & Selection Reasons.
			if len(parsed.Tools) > 0 {
				SailSubtleCard(c, func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
						ui.Textf(c, "命中的候选工具列表（%d）", len(parsed.Tools)).
							FontSize(13).
							FontWeight(600).
							TextColor(SailInkStrong)
						ui.Text(c, "含模型精选理由与读写权限").
							FontSize(11.5).
							TextColor(SailInkMuted)
					})

					ui.Column(c).FillWidth().Gap(8).Children(func() {
						for idx, mt := range parsed.Tools {
							item := mt
							ui.Column(c.Key(fmt.Sprintf("logs-hit-%s-%d", ev.ID, idx))).
								FillWidth().
								Padding(10, 12).
								Gap(6).
								Radius(6).
								Background(SailBgCanvas).
								Border(1, SailBorder).
								Children(func() {
									ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
										ui.Row(c).Wrap().Gap(6).AlignItems(ui.Center).Children(func() {
											displayID := item.ID
											if displayID == "" {
												displayID = item.Name
											}
											ui.Text(c, displayID).
												Font("monospace").
												FontSize(12.5).
												FontWeight(600).
												TextColor(SailInkStrong).
												Selectable()

											if item.ServerID != "" {
												SailBadge(c, item.ServerID, ToneNeutral)
											}
											if item.HasReadOnly {
												if item.ReadOnly {
													SailBadge(c, "只读 ro", ToneSuccess)
												} else {
													SailBadge(c, "写操作 rw", ToneWarning)
												}
											}
										})

										if item.ID != "" {
											if _, exists := toolByID[item.ID]; exists {
												if SailSmallButton(c.Key(fmt.Sprintf("logs-open-tool-%s-%d", ev.ID, idx)), "在目录中查看").Clicked() {
													catalogSelectToolFromLogs(u, item.ID, item.ServerID)
													u.Navigate(ViewCatalog)
												}
											}
										}
									})

									if item.Reason != "" {
										ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
											ui.Text(c, "命中原因：").
												Shrink(0).
												FontSize(12).
												FontWeight(600).
												TextColor(SailAccent)
											ui.Text(c, item.Reason).
												Grow(1).
												MinWidth(0).
												FontSize(12.5).
												LineHeight(1.45).
												TextColor(SailInkBody).
												Selectable()
										})
									} else if item.Description != "" {
										ui.Text(c, item.Description).
											FontSize(12).
											TextColor(SailInkMuted).
											Selectable()
									}
								})
						}
					})
				})
			} else if ev.Kind == "discover" && parsed.Error == "" {
				SailSubtleCard(c, func() {
					ui.Text(c, "本次发现未返回候选工具（0 命中）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.Text(c, "若期望某工具被召回，可前往「工具目录」检查该工具是否已开放白名单、当前 Actor 是否有权访问，或优化其功能语义描述。").
						FontSize(12).
						TextColor(SailInkMuted)
				})
			}

			// 5. Formatted JSON Payload Viewer + One-click Copy.
			ui.Column(c).FillWidth().Gap(8).Children(func() {
				ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					ui.Segmented(c.Key("logs-payload-tab"), &st.payloadTab, "完整事件 JSON", "Detail 详情载荷").
						Label("JSON 载荷视图")

					activePayload := parsed.FullEventJSON
					copyNotice := "已复制完整事件 JSON"
					if st.payloadTab == 1 {
						activePayload = parsed.DetailJSON
						copyNotice = "已复制 Detail 载荷 JSON"
					}
					if SailSmallButton(c.Key("logs-copy-active-payload-"+ev.ID), "一键复制 JSON").Clicked() {
						c.WriteClipboard(activePayload)
						u.Notify(copyNotice)
					}
				})

				if st.payloadTab == 1 {
					SailCodeBlock(c, "DETAIL PAYLOAD (JSON)", parsed.DetailJSON, func() {
						u.Notify("已复制 Detail 载荷 JSON")
					})
				} else {
					SailCodeBlock(c, "EVENT TRACE PAYLOAD (JSON)", parsed.FullEventJSON, func() {
						u.Notify("已复制完整事件 JSON")
					})
				}
			})
		})
}

func logsMetricTile(c *ui.Context, label, value, sub string, mono bool) {
	ui.Column(c).
		Grow(1).
		MinWidth(145).
		Padding(12, 14).
		Gap(4).
		Radius(6).
		Background(SailBgSubtle).
		Border(1, SailBorder).
		Children(func() {
			ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted).SingleLine()
			valElem := ui.Text(c, value).
				FontSize(15.5).
				Bold().
				TextColor(SailInkStrong).
				FontFeatures("tnum").
				SingleLine()
			if mono {
				valElem.Font("monospace")
			}
			if sub != "" {
				ui.Text(c, sub).FontSize(11).TextColor(SailInkFaint).SingleLine()
			}
		})
}

func logsMatchesFilters(ev gateway.Event, st *logsPageState) bool {
	switch st.kindIdx {
	case 1:
		if ev.Kind != "discover" {
			return false
		}
	case 2:
		if ev.Kind != "invoke" {
			return false
		}
	case 3:
		if ev.Kind != "agent" && ev.Kind != "agent_tool" {
			return false
		}
	}

	switch st.statusIdx {
	case 1: // 成功
		if logsIsErrorStatus(ev.Status) || logsIsUnmatchedStatus(ev.Status) {
			return false
		}
	case 2: // 未命中
		if !logsIsUnmatchedStatus(ev.Status) {
			return false
		}
	case 3: // 错误
		if !logsIsErrorStatus(ev.Status) {
			return false
		}
	}

	q := strings.ToLower(strings.TrimSpace(st.query))
	if q == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		ev.ID,
		ev.Title,
		ev.Actor,
		ev.Kind,
		ev.Status,
		string(ev.Detail),
	}, " "))
	return strings.Contains(haystack, q)
}

func logsIsErrorStatus(status string) bool {
	return status == "error" || status == "failed"
}

func logsIsUnmatchedStatus(status string) bool {
	return status == "no_match" || status == "needs_context"
}

func logsStatusTone(status string) BadgeTone {
	switch {
	case logsIsErrorStatus(status):
		return ToneDanger
	case status == "needs_context":
		return ToneWarning
	case status == "no_match":
		return ToneNeutral
	default:
		return ToneSuccess
	}
}

func logsStatusDotColor(status string) ui.Color {
	switch {
	case logsIsErrorStatus(status):
		return SailDangerInk
	case logsIsUnmatchedStatus(status):
		return SailWarningInk
	default:
		return SailSuccessInk
	}
}

func logsStatusInkColor(status string) ui.Color {
	switch {
	case logsIsErrorStatus(status):
		return SailDangerInk
	case status == "needs_context":
		return SailWarningInk
	case status == "no_match":
		return SailInkMuted
	default:
		return SailInkBody
	}
}

func logsKindBadgeLabel(kind string) string {
	switch kind {
	case "discover":
		return "discover"
	case "invoke":
		return "invoke"
	case "agent", "agent_tool":
		return "agent_tool"
	case "maintenance":
		return "maintenance"
	default:
		return kind
	}
}

func logsKindFullLabel(kind string) string {
	switch kind {
	case "discover":
		return "discover · 语义发现"
	case "invoke":
		return "invoke · 工具调用"
	case "agent", "agent_tool":
		return "agent_tool · MCP 接入调用"
	case "maintenance":
		return "maintenance · 自动维护"
	default:
		return kind
	}
}

func logsKindTone(kind string) BadgeTone {
	switch kind {
	case "discover":
		return ToneAccent
	case "invoke":
		return ToneInfo
	case "agent", "agent_tool":
		return ToneSuccess
	default:
		return ToneNeutral
	}
}

func logsDurationHint(ms int64) string {
	switch {
	case ms < 50:
		return "极速本地/缓存级响应"
	case ms < 800:
		return "常规端到端延迟"
	default:
		return "较长模型或下游耗时"
	}
}

func logsParseEventDetail(ev gateway.Event, toolByID map[string]gateway.Tool) logsParsedDetail {
	var out logsParsedDetail

	var detailAny any
	if len(ev.Detail) > 0 {
		if err := json.Unmarshal(ev.Detail, &detailAny); err == nil && detailAny != nil {
			out.DetailJSON = PrettyJSON(detailAny)
		} else {
			out.DetailJSON = string(ev.Detail)
		}
	} else {
		out.DetailJSON = "{}"
	}

	fullEnvelope := map[string]any{
		"id":          ev.ID,
		"time":        ev.Time,
		"kind":        ev.Kind,
		"actor":       ev.Actor,
		"status":      ev.Status,
		"title":       ev.Title,
		"duration_ms": ev.DurationMS,
	}
	if detailAny != nil {
		fullEnvelope["detail"] = detailAny
	}
	out.FullEventJSON = PrettyJSON(fullEnvelope)

	m, ok := detailAny.(map[string]any)
	if !ok || m == nil {
		return out
	}

	if v, ok := m["model"].(string); ok {
		out.Model = strings.TrimSpace(v)
	}
	if v, ok := m["summary"].(string); ok {
		out.Summary = strings.TrimSpace(v)
	}
	if v, ok := m["question"].(string); ok {
		out.Question = strings.TrimSpace(v)
	}
	if v, ok := m["error"]; ok && v != nil {
		errStr := strings.TrimSpace(fmt.Sprint(v))
		if errStr != "" && errStr != "<nil>" {
			out.Error = errStr
		}
	}
	if v, ok := jsonFloat(m["eligible"]); ok {
		iv := int(v)
		out.Eligible = &iv
	}
	if v, ok := jsonFloat(m["prompt_tokens"]); ok {
		iv := int(v)
		out.PromptTokens = &iv
	}
	if v, ok := jsonFloat(m["output_tokens"]); ok {
		iv := int(v)
		out.OutputTokens = &iv
	}
	if v, ok := m["tool_id"].(string); ok {
		out.ToolID = strings.TrimSpace(v)
	}
	if v, ok := m["client"].(string); ok {
		out.Client = strings.TrimSpace(v)
	}
	if v, ok := m["method"].(string); ok {
		out.Method = strings.TrimSpace(v)
	}
	if v, ok := m["tool"].(string); ok {
		out.AgentToolName = strings.TrimSpace(v)
	}
	if rawServers, ok := m["servers"].([]any); ok {
		for _, item := range rawServers {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out.Servers = append(out.Servers, strings.TrimSpace(s))
			}
		}
	}
	if rawFields, ok := m["fields"].([]any); ok {
		for _, item := range rawFields {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out.Fields = append(out.Fields, strings.TrimSpace(s))
			}
		}
	}

	if rawTools, ok := m["tools"].([]any); ok {
		for _, raw := range rawTools {
			switch item := raw.(type) {
			case string:
				id := strings.TrimSpace(item)
				if id == "" {
					continue
				}
				mt := logsMatchedTool{ID: id, Name: id}
				if reg, exists := toolByID[id]; exists {
					mt.Name = reg.Name
					mt.ServerID = reg.ServerID
					mt.Description = reg.Description
					mt.ReadOnly = reg.ReadOnly
					mt.HasReadOnly = true
				} else if parts := strings.SplitN(id, ".", 2); len(parts) == 2 {
					mt.ServerID = parts[0]
					mt.Name = parts[1]
				}
				out.Tools = append(out.Tools, mt)

			case map[string]any:
				var mt logsMatchedTool
				if s, ok := item["id"].(string); ok {
					mt.ID = strings.TrimSpace(s)
				} else if s, ok := item["tool_id"].(string); ok {
					mt.ID = strings.TrimSpace(s)
				}
				if s, ok := item["name"].(string); ok {
					mt.Name = strings.TrimSpace(s)
				}
				if s, ok := item["server_id"].(string); ok {
					mt.ServerID = strings.TrimSpace(s)
				}
				if s, ok := item["description"].(string); ok {
					mt.Description = strings.TrimSpace(s)
				}
				if s, ok := item["reason"].(string); ok {
					mt.Reason = strings.TrimSpace(s)
				}
				if ro, ok := item["read_only"].(bool); ok {
					mt.ReadOnly = ro
					mt.HasReadOnly = true
				}
				if reg, exists := toolByID[mt.ID]; exists {
					if mt.Name == "" {
						mt.Name = reg.Name
					}
					if mt.ServerID == "" {
						mt.ServerID = reg.ServerID
					}
					if mt.Description == "" {
						mt.Description = reg.Description
					}
					if !mt.HasReadOnly {
						mt.ReadOnly = reg.ReadOnly
						mt.HasReadOnly = true
					}
				} else if mt.ServerID == "" && strings.Contains(mt.ID, ".") {
					parts := strings.SplitN(mt.ID, ".", 2)
					mt.ServerID = parts[0]
					if mt.Name == "" {
						mt.Name = parts[1]
					}
				}
				if mt.ID != "" || mt.Name != "" {
					out.Tools = append(out.Tools, mt)
				}
			}
		}
	}

	return out
}
