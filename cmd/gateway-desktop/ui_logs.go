package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo/ui"
)

const logsDefaultTimelineLimit = 150

type logsPageState struct {
	query         string
	kindIdx       int // 0: 全部, 1: discover, 2: invoke, 3: agent / agent_tool
	statusIdx     int // 0: 全部, 1: 成功, 2: 未命中, 3: 错误
	selectedID    string
	payloadTab    int // 0: 完整事件 JSON, 1: Detail 详情载荷
	timelineLimit int
	timelineScrl  ui.ScrollState
	bindingRepo   bool
}

func (u *DesktopUI) logsState() *logsPageState {
	if u.logs == nil {
		u.logs = &logsPageState{
			timelineLimit: logsDefaultTimelineLimit,
		}
	}
	return u.logs
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

type logsParsedScope struct {
	Agent        string
	RepoID       string
	RepoName     string
	Kind         string
	Root         string
	GitCommonDir string
	RemoteHint   string
	MatchedRule  string
	Status       string
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
	Scope         *logsParsedScope
	DetailJSON    string
	FullEventJSON string
}

func (u *DesktopUI) renderLogs(c *ui.Context) {
	if u.State == nil {
		return
	}
	st := u.logsState()
	events := u.State.Events

	toolByID := make(map[string]apitypes.Tool, len(u.State.Registry.Tools))
	for _, t := range u.State.Registry.Tools {
		toolByID[t.ID] = t
	}

	// Consume cross-page navigation handoff (e.g. from Overview blind-spot row).
	if targetID := u.consumePendingEventID(); targetID != "" {
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

	filtered := make([]apitypes.Event, 0, len(events))
	for _, ev := range events {
		if logsMatchesFilters(ev, st) {
			filtered = append(filtered, ev)
		}
	}

	var activeEvent *apitypes.Event
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
		"",
		func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				SailQuietStatus(c, "4s 自动同步", SailSuccessInk, SailInkMuted)
				if errCount > 0 {
					SailBadge(c, fmt.Sprintf("错误 %d", errCount), ToneDanger)
				}
				if SailSmallButton(c.Key("logs-refresh-btn"), "刷新").Disabled(u.Refreshing).Clicked() {
					u.Refresh()
				}
			})
		},
	)

	// 2. Compact Filter Bar.
	SailCard(c.Key("logs-filter-card"), func() {
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				SailSearchField(c.Key("logs-search"), &st.query).
					Placeholder("搜索意图、工具名或载荷…").
					Label("搜索请求日志").
					Width(236)

				SailSegmented(
					c.Key("logs-kind-seg"),
					&st.kindIdx,
					"全部",
					"discover",
					"invoke",
					"agent_tool",
				).Label("事件类型筛选")

				SailSegmented(
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
					if SailButton(c.Key("logs-reset-btn"), "重置").Clicked() {
						st.resetFilters()
					}
				}
				ui.Textf(c, "%d / %d", len(filtered), len(events)).
					FontSize(12).
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
				"暂无请求记录",
				"",
				"前往路由沙盒",
				func() {
					u.Navigate(ViewPlayground)
				},
			)
		} else {
			var resetFn func()
			resetLbl := ""
			if st.hasActiveFilters() {
				resetLbl = "清空筛选"
				resetFn = st.resetFilters
			}
			SailEmptyState(
				c,
				"无匹配日志",
				"",
				resetLbl,
				resetFn,
			)
		}
		return
	}

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		// Left Column: Hairline Timeline List.
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
	filtered []apitypes.Event,
	activeEvent *apitypes.Event,
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
		Width(376).
		Shrink(0).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailSurfaceBorder).
		Shadow(0, 3, 10, 0, SailCardShadow).
		Clip().
		Children(func() {
			SailListCardHeader(c, "时间线", fmt.Sprintf("%d 条", len(filtered)), nil)

			// Scrollable Hairline Timeline Rows (max 3 anchors per row, zero per-row borders).
			ui.Scroll(c.Key("logs-timeline-scroll")).
				TrackScroll(&st.timelineScrl).
				FillWidth().
				MaxHeight(660).
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
						isLast := i == visibleCount-1 && !hasMore

						rowBg := SailBgCanvas
						if selected {
							rowBg = SailAccentSoft
						}

						rowBtn := ui.ButtonBase(c.Key("logs-item-"+rowKey)).
							Column().
							FillWidth().
							Padding(12, 18).
							Gap(5).
							AlignItems(ui.Stretch).
							Background(rowBg).
							Cursor(ui.CursorPointer)
						if !isLast {
							rowBtn.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
						}
						if rowBtn.Hovered() && !selected {
							rowBtn.Background(SailBgHover)
						}

						rowBtn.Children(func() {
							// Anchor 1 & 3: Status dot + quiet kind (+ Error badge if error) on left; latency + time on right.
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
								ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
									SailStatusDot(c, logsStatusDotColor(ev.Status))
									ui.Text(c, logsKindBadgeLabel(ev.Kind)).
										Font("monospace").
										FontSize(11.5).
										TextColor(SailInkMuted).
										SingleLine()
									if logsIsErrorStatus(ev.Status) {
										SailBadge(c, "错误", ToneDanger)
									}
								})

								ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
									ui.Textf(c, "%d ms", ev.DurationMS).
										Font("monospace").
										FontSize(11).
										TextColor(SailInkMuted).
										FontFeatures("tnum").
										SingleLine()
									ui.Text(c, FormatTime(ev.Time)).
										FontSize(11).
										TextColor(SailInkFaint).
										FontFeatures("tnum").
										SingleLine()
								})
							})

							// Anchor 2: Primary Intent or Tool ID.
							titleText := strings.TrimSpace(ev.Title)
							if titleText == "" {
								titleText = ev.ID
							}
							titleColor := SailInkStrong
							if selected {
								titleColor = SailAccent
							}
							titleElem := ui.Text(c, titleText).
								FontSize(13).
								FontWeight(500).
								TextColor(titleColor).
								SingleLine()
							if ev.Kind == "invoke" || ev.Kind == "agent" || ev.Kind == "agent_tool" {
								titleElem.Font("monospace").FontSize(12.5)
							}
						})

						if rowBtn.Clicked() {
							st.selectedID = ev.ID
						}
					}

					if hasMore {
						ui.Row(c).FillWidth().Padding(10, 16).Justify(ui.Center).Children(func() {
							if SailSmallButton(c.Key("logs-load-more"), fmt.Sprintf("加载更多 (%d)", len(filtered)-visibleCount)).Clicked() {
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
	ev apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	parsed := logsParseEventDetail(ev, toolByID)

	ui.Column(c.Key("logs-inspector-"+ev.ID)).
		Grow(1).
		MinWidth(0).
		Padding(20, 22).
		Gap(18).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailSurfaceBorder).
		Shadow(0, 3, 10, 0, SailCardShadow).
		Children(func() {
			// 1. Inspector Header + Quick Actions.
			ui.Row(c).FillWidth().AlignItems(ui.Start).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
					ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
						if logsIsErrorStatus(ev.Status) {
							SailBadgeDot(c, ev.Status, ToneDanger)
						} else if ev.Status == "needs_context" {
							SailBadgeDot(c, ev.Status, ToneWarning)
						} else {
							SailQuietStatus(c, ev.Status, logsStatusDotColor(ev.Status), SailInkStrong)
						}
						ui.Text(c, logsKindBadgeLabel(ev.Kind)).
							Font("monospace").
							FontSize(12).
							TextColor(SailInkMuted).
							SingleLine()
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
						if SailSmallButton(c.Key("logs-replay-"+ev.ID), "沙盒重放").Clicked() {
							if parsed.Scope != nil {
								replayRepo := logsResolveReplayRepoKey(u.State, *parsed.Scope)
								u.OpenPlaygroundReplay(ev.Title, ev.Actor, replayRepo)
							} else {
								u.OpenPlaygroundReplay(ev.Title, ev.Actor)
							}
						}
					}
					if ev.Kind == "invoke" {
						targetToolID := parsed.ToolID
						if targetToolID == "" {
							targetToolID = ev.Title
						}
						if regTool, ok := toolByID[targetToolID]; ok {
							if SailSmallButton(c.Key("logs-jump-cat-"+ev.ID), "查看契约").Clicked() {
								u.OpenCatalogTool(regTool.ID, regTool.ServerID)
							}
						}
					}
					if SailSmallButton(c.Key("logs-copy-json-"+ev.ID), "复制 JSON").Clicked() {
						c.WriteClipboard(parsed.FullEventJSON)
						u.Notify("已复制事件完整 JSON")
					}
				})
			})

			// 2. Borderless Key Trace Metrics Strip (no chatty captions).
			ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
				logsMetricTile(c, "耗时", fmt.Sprintf("%d ms", ev.DurationMS), "", true)

				tokMain := "—"
				tokSub := ""
				if parsed.PromptTokens != nil || parsed.OutputTokens != nil {
					pt, ot := 0, 0
					if parsed.PromptTokens != nil {
						pt = *parsed.PromptTokens
					}
					if parsed.OutputTokens != nil {
						ot = *parsed.OutputTokens
					}
					tokMain = fmt.Sprintf("%d tok", pt+ot)
					tokSub = fmt.Sprintf("%d in · %d out", pt, ot)
				}
				logsMetricTile(c, "Token", tokMain, tokSub, true)

				actorStr := ev.Actor
				if actorStr == "" {
					actorStr = "anonymous"
				}
				logsMetricTile(c, "身份", actorStr, "", true)

				logsMetricTile(c, "时间", FormatTime(ev.Time), ev.Time.Local().Format("2006-01-02"), true)
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
						SailKeyValueRow(c, "路由模型", parsed.Model, true)
					}
					if parsed.Eligible != nil {
						SailKeyValueRow(c, "候选收敛", fmt.Sprintf("%d → %d", *parsed.Eligible, len(parsed.Tools)), true)
					}
					if len(parsed.Servers) > 0 {
						SailKeyValueRow(c, "命中服务", strings.Join(parsed.Servers, ", "), true)
					}
					if parsed.Method != "" {
						SailKeyValueRow(c, "协议方法", parsed.Method, true)
					}
					if parsed.AgentToolName != "" {
						SailKeyValueRow(c, "元工具", parsed.AgentToolName, true)
					}
					if parsed.Client != "" {
						SailKeyValueRow(c, "客户端", parsed.Client, true)
					}
					if len(parsed.Fields) > 0 {
						SailKeyValueRow(c, "裁剪字段", strings.Join(parsed.Fields, ", "), true)
					}
				})
			}

			// Scope Trace Card when detail.scope is present.
			if sc := parsed.Scope; sc != nil {
				u.renderLogsScopeCard(c, st, ev.ID, *sc)
			}

			// 3. Semantic Summary / Question / Error.
			if parsed.Summary != "" {
				SailSubtleCard(c, func() {
					ui.Text(c, parsed.Summary).FontSize(13).LineHeight(1.5).TextColor(SailInkStrong).Selectable()
				})
			}
			if parsed.Question != "" {
				SailAlertBox(c, ToneWarning, "需补充上下文", parsed.Question)
			}
			if parsed.Error != "" {
				SailAlertBox(c, ToneDanger, "执行异常", parsed.Error)
			}

			// 4. Matched Candidate Tools (hairline rows, no nested bordered cards).
			if len(parsed.Tools) > 0 {
				SailSubtleCard(c, func() {
					ui.Textf(c, "命中工具 (%d)", len(parsed.Tools)).
						FontSize(12).
						FontWeight(600).
						TextColor(SailInkMuted)

					ui.Column(c).FillWidth().Children(func() {
						for idx, mt := range parsed.Tools {
							item := mt
							isLast := idx == len(parsed.Tools)-1
							r := ui.Column(c.Key(fmt.Sprintf("logs-hit-%s-%d", ev.ID, idx))).
								FillWidth().
								Padding(10, 0).
								Gap(4)
							if !isLast {
								r.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
							}
							r.Children(func() {
								ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
									ui.Row(c).Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
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

										if item.HasReadOnly && !item.ReadOnly {
											SailBadge(c, "写操作", ToneWarning)
										}
									})

									if item.ID != "" {
										if _, exists := toolByID[item.ID]; exists {
											if SailSmallButton(c.Key(fmt.Sprintf("logs-open-tool-%s-%d", ev.ID, idx)), "目录详情").Clicked() {
												u.OpenCatalogTool(item.ID, item.ServerID)
											}
										}
									}
								})

								if item.Reason != "" {
									ui.Text(c, item.Reason).
										FontSize(12.5).
										LineHeight(1.48).
										TextColor(SailInkBody).
										Selectable()
								} else if item.Description != "" {
									SailDescSummary(c, item.Description, "", 2)
								}
							})
						}
					})
				})
			} else if ev.Kind == "discover" && parsed.Error == "" {
				SailSubtleCard(c, func() {
					ui.Text(c, "未命中候选工具").FontSize(12.5).TextColor(SailInkMuted)
				})
			}

			// 5. Formatted JSON Payload Viewer.
			ui.Column(c).FillWidth().Gap(10).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Start).Children(func() {
					ui.Segmented(c.Key("logs-payload-tab"), &st.payloadTab, "完整事件 JSON", "Detail 详情载荷").
						Label("JSON 载荷视图")
				})

				if st.payloadTab == 1 {
					SailCodeBlock(c, "DETAIL PAYLOAD", parsed.DetailJSON, func() {
						u.Notify("已复制 Detail 载荷 JSON")
					})
				} else {
					SailCodeBlock(c, "EVENT TRACE", parsed.FullEventJSON, func() {
						u.Notify("已复制完整事件 JSON")
					})
				}
			})
		})
}

func logsMetricTile(c *ui.Context, label, value, sub string, mono bool) {
	ui.Column(c).
		Grow(1).
		MinWidth(136).
		Padding(12, 14).
		Gap(4).
		Radius(8).
		Background(SailBgSubtle).
		Children(func() {
			ui.Text(c, label).FontSize(11.5).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			valElem := ui.Text(c, value).
				FontSize(15).
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

func logsMatchesFilters(ev apitypes.Event, st *logsPageState) bool {
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

func logsParseEventDetail(ev apitypes.Event, toolByID map[string]apitypes.Tool) logsParsedDetail {
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
	if rawScope, ok := m["scope"].(map[string]any); ok && len(rawScope) > 0 {
		sc := &logsParsedScope{}
		if s, ok := rawScope["agent"].(string); ok {
			sc.Agent = strings.TrimSpace(s)
		}
		if s, ok := rawScope["repo_id"].(string); ok {
			sc.RepoID = strings.TrimSpace(s)
		}
		if s, ok := rawScope["repo_name"].(string); ok {
			sc.RepoName = strings.TrimSpace(s)
		}
		if s, ok := rawScope["kind"].(string); ok {
			sc.Kind = strings.TrimSpace(s)
		}
		if s, ok := rawScope["root"].(string); ok {
			sc.Root = strings.TrimSpace(s)
		} else if s, ok := rawScope["cwd"].(string); ok {
			sc.Root = strings.TrimSpace(s)
		}
		if s, ok := rawScope["git_common_dir"].(string); ok {
			sc.GitCommonDir = strings.TrimSpace(s)
		}
		if s, ok := rawScope["remote_hint"].(string); ok {
			sc.RemoteHint = strings.TrimSpace(s)
		}
		if s, ok := rawScope["matched_rule"].(string); ok {
			sc.MatchedRule = strings.TrimSpace(s)
		}
		if s, ok := rawScope["status"].(string); ok {
			sc.Status = strings.TrimSpace(s)
		} else if s, ok := rawScope["context_status"].(string); ok {
			sc.Status = strings.TrimSpace(s)
		}
		if sc.Agent != "" || sc.RepoID != "" || sc.RepoName != "" || sc.Root != "" || sc.MatchedRule != "" || sc.Status != "" {
			out.Scope = sc
		}
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

func hasSegmentPathPrefix(target, anchor string) bool {
	t := filepath.Clean(strings.TrimSpace(target))
	a := filepath.Clean(strings.TrimSpace(anchor))
	if t == "" || a == "" || a == "." || t == "." {
		return false
	}
	if t == a {
		return true
	}
	sep := string(filepath.Separator)
	if strings.HasSuffix(a, sep) {
		return strings.HasPrefix(t, a)
	}
	return strings.HasPrefix(t, a+sep)
}

func logsMatchedBoundRepo(state *apitypes.Bootstrap, sc logsParsedScope) *apitypes.Repository {
	if state == nil {
		return nil
	}
	if sc.RepoID != "" && sc.RepoID != "*" {
		for i := range state.Scopes.Repositories {
			if state.Scopes.Repositories[i].ID == sc.RepoID {
				return &state.Scopes.Repositories[i]
			}
		}
	}
	effectiveCommonDir := sc.GitCommonDir
	effectiveKind := sc.Kind
	if effectiveCommonDir == "" && sc.Root != "" {
		for _, obs := range state.Scopes.Observed {
			if obs.Root == sc.Root {
				if obs.GitCommonDir != "" {
					effectiveCommonDir = obs.GitCommonDir
				}
				if effectiveKind == "" && obs.Kind != "" {
					effectiveKind = obs.Kind
				}
				break
			}
		}
	}
	isGit := effectiveKind == "git" || effectiveCommonDir != ""
	if isGit {
		for i := range state.Scopes.Repositories {
			repo := &state.Scopes.Repositories[i]
			if effectiveCommonDir != "" && slices.Contains(repo.GitCommonDirs, effectiveCommonDir) {
				return repo
			}
			if effectiveCommonDir == "" && sc.Root != "" {
				for _, cd := range repo.GitCommonDirs {
					if filepath.Base(cd) == ".git" && filepath.Dir(cd) == sc.Root {
						return repo
					}
				}
			}
		}
		return nil
	}
	if sc.Root != "" {
		var best *apitypes.Repository
		bestLen := -1
		for i := range state.Scopes.Repositories {
			repo := &state.Scopes.Repositories[i]
			for _, rp := range repo.RootPaths {
				if hasSegmentPathPrefix(sc.Root, rp) && len(rp) > bestLen {
					best = repo
					bestLen = len(rp)
				}
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

func logsResolveReplayRepoKey(state *apitypes.Bootstrap, sc logsParsedScope) string {
	if repo := logsMatchedBoundRepo(state, sc); repo != nil {
		return repo.ID
	}
	if sc.RepoID != "" && sc.RepoID != "*" {
		return sc.RepoID
	}
	if sc.RepoID == "*" || sc.Root != "" || sc.Agent != "" {
		return "*"
	}
	return ""
}

func logsScopeIsErrorStatus(status string) bool {
	switch status {
	case "scope_required", "scope_invalid", "ambiguous_workspace", "denied", "error":
		return true
	default:
		return false
	}
}

func (u *DesktopUI) renderLogsScopeCard(c *ui.Context, st *logsPageState, evID string, sc logsParsedScope) {
	boundRepo := logsMatchedBoundRepo(u.State, sc)
	canBind := boundRepo == nil && (sc.RepoID == "" || sc.RepoID == "*") && (sc.Root != "" || sc.GitCommonDir != "")

	SailSubtleCard(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "仓库作用域").FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
				if sc.Status != "" {
					if logsScopeIsErrorStatus(sc.Status) {
						SailBadge(c, sc.Status, ToneDanger)
					} else {
						SailQuietStatus(c, sc.Status, SailSuccessInk, SailInkStrong)
					}
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if boundRepo != nil {
					if SailSmallButton(c.Key("logs-open-repo-"+evID), "仓库规则").Clicked() {
						u.OpenRepos(boundRepo.ID)
					}
				} else if canBind {
					bindLbl := "绑定此仓库"
					if st.bindingRepo {
						bindLbl = "绑定中…"
					}
					if SailSmallButton(c.Key("logs-bind-repo-"+evID), bindLbl).Disabled(st.bindingRepo).Clicked() {
						u.bindRepoFromLogScope(st, sc)
					}
				}
			})
		})

		if sc.Agent != "" {
			SailKeyValueRow(c, "调用客户端", fmt.Sprintf("%s (%s)", agentDisplayName(sc.Agent), sc.Agent), false)
		}
		repoDisplay := sc.RepoName
		if boundRepo != nil && boundRepo.Name != "" {
			repoDisplay = boundRepo.Name
		}
		if repoDisplay == "" && sc.Root != "" {
			repoDisplay = filepath.Base(sc.Root)
		}
		if repoDisplay != "" {
			if boundRepo == nil && (sc.RepoID == "" || sc.RepoID == "*") {
				repoDisplay += " (未绑定)"
			}
			SailKeyValueRow(c, "仓库", repoDisplay, false)
		}
		if sc.Root != "" {
			SailKeyValueRow(c, "工作目录", sc.Root, true)
		}
		if sc.MatchedRule != "" {
			SailKeyValueRow(c, "命中规则", sc.MatchedRule, true)
		}
	})
}

func (u *DesktopUI) bindRepoFromLogScope(st *logsPageState, sc logsParsedScope) {
	if st.bindingRepo || u.Client == nil {
		return
	}
	st.bindingRepo = true

	name := strings.TrimSpace(sc.RepoName)
	if name == "" && sc.Root != "" {
		name = filepath.Base(sc.Root)
	}
	in := apitypes.SaveRepositoryRequest{
		Name: name,
	}
	// Check if this repo is in state.Scopes.Observed to reuse verified GitCommonDir & RemoteHint.
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
			u.updateUI(func() { st.bindingRepo = false })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.bindingRepo = false
		})
		u.Notify("已绑定仓库 " + saved.Name)
		return nil
	})
}
