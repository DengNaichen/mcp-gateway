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
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

const logsDefaultTimelineLimit = 150

type logsPageState struct {
	query         string
	protocolIdx   int // index into logsProtocolValues
	decisionIdx   int // index into logsDecisionValues
	repoLabel     string
	repoKey       string // "": all, logsRepoUnbound, or Repository.ID
	serviceLabel  string
	serviceID     string
	remote        logsRemotePage
	selectedID    string
	payloadTab    int // 0: 完整事件 JSON, 1: Detail 详情载荷
	timelineLimit int
	timelineScrl  ui.ScrollState
	bindingRepo   bool
	approvals     approvalsState
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
	return strings.TrimSpace(st.query) != "" || st.protocolIdx != 0 || st.decisionIdx != 0 || st.repoKey != "" || st.serviceID != ""
}

func (st *logsPageState) resetFilters() {
	st.query = ""
	st.protocolIdx = 0
	st.decisionIdx = 0
	st.repoLabel, st.repoKey = "", ""
	st.serviceLabel, st.serviceID = "", ""
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
	opts := buildLogsFilterOptions(u.State)
	st.syncLabels(opts)
	toolByID := logsBuildToolIndex(u.State.Registry.Tools)

	// Consume cross-page navigation handoff (e.g. from Overview blind-spot row).
	if targetID := u.consumePendingEventID(); targetID != "" {
		logsApplyPendingEvent(u.State.Events, st, targetID)
		st.syncLabels(opts)
	}
	u.applyPendingApproval(st)
	events, remote := u.logsSourceEvents(st)

	filtered, errCount, activeEvent := logsPrepareEventsView(events, st)

	// 1. Page Header.
	SailSectionHeader(
		c,
		i18n.T("ops.logs-title"),
		"",
		func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				SailQuietStatus(c, i18n.T("ops.auto-sync"), SailSuccessInk, SailInkMuted)
				if errCount > 0 {
					SailBadge(c, i18n.T("ops.error-count", errCount), ToneDanger)
				}
				if SailSmallButton(c.Key("logs-refresh-btn"), i18n.T("ops.refresh")).Disabled(u.Refreshing).Clicked() {
					u.Refresh()
				}
			})
		},
	)

	// 2. Pending approvals review queue (above the ledger, highest priority).
	u.renderLogsApprovalQueue(c, st)

	// 3. Compact Filter Bar.
	renderLogsFilterBar(c, st, opts, len(filtered), len(events), st.remote.loading)

	// 4. Empty State or Split Master-Detail Workbench.
	if len(filtered) == 0 {
		u.renderLogsEmptyState(c, st, len(events))
		return
	}

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		u.renderLogsTimelinePane(c, st, filtered, activeEvent, remote)
		if activeEvent != nil {
			u.renderLogsInspectorPane(c, st, *activeEvent, toolByID)
		}
	})
}

func logsBuildToolIndex(tools []apitypes.Tool) map[string]apitypes.Tool {
	toolByID := make(map[string]apitypes.Tool, len(tools))
	for _, t := range tools {
		toolByID[t.ID] = t
	}
	return toolByID
}

func logsApplyPendingEvent(events []apitypes.Event, st *logsPageState, targetID string) {
	st.selectedID = targetID
	for idx, ev := range events {
		if ev.ID != targetID {
			continue
		}
		if !logsMatchesFilters(ev, st) {
			st.resetFilters()
		}
		if idx >= st.timelineLimit {
			st.timelineLimit = idx + 50
		}
		break
	}
}

func logsPrepareEventsView(events []apitypes.Event, st *logsPageState) ([]apitypes.Event, int, *apitypes.Event) {
	var errCount int
	filtered := make([]apitypes.Event, 0, len(events))
	for _, ev := range events {
		if logsIsErrorStatus(ev.Status) {
			errCount++
		}
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
	return filtered, errCount, activeEvent
}

func (u *DesktopUI) renderLogsEmptyState(c *ui.Context, st *logsPageState, totalCount int) {
	if totalCount == 0 {
		SailEmptyState(
			c,
			i18n.T("ops.no-requests"),
			"",
			i18n.T("ops.goto-playground"),
			func() {
				u.Navigate(ViewPlayground)
			},
		)
		return
	}
	var resetFn func()
	resetLbl := ""
	if st.hasActiveFilters() {
		resetLbl = i18n.T("ops.clear-filters")
		resetFn = st.resetFilters
	}
	SailEmptyState(c, i18n.T("ops.no-matching-logs"), "", resetLbl, resetFn)
}

type logsTimelineRowProps struct {
	rowKey   string
	ev       apitypes.Event
	selected bool
	isLast   bool
}

func (u *DesktopUI) renderLogsTimelinePane(
	c *ui.Context,
	st *logsPageState,
	filtered []apitypes.Event,
	activeEvent *apitypes.Event,
	remote bool,
) {
	visibleCount := len(filtered)
	if st.timelineLimit <= 0 {
		st.timelineLimit = logsDefaultTimelineLimit
	}
	hasMore := visibleCount > st.timelineLimit
	if hasMore {
		visibleCount = st.timelineLimit
	}

	applyCardElevation(
		ui.Column(c.Key("logs-timeline-card")).
			Width(376).
			Shrink(0).
			Radius(10).
			Background(SailBgSurface),
	).Clip().Children(func() {
		SailListCardHeader(c, i18n.T("ops.timeline"), i18n.T("ops.entries-count", len(filtered)), nil)

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
					renderLogsTimelineRow(c, st, logsTimelineRowProps{
						rowKey:   rowKey,
						ev:       ev,
						selected: activeEvent != nil && activeEvent.ID == ev.ID,
						isLast:   i == visibleCount-1 && !hasMore,
					})
				}

				if hasMore {
					ui.Row(c).FillWidth().Padding(10, 16).Justify(ui.Center).Children(func() {
						if SailSmallButton(c.Key("logs-load-more"), i18n.T("ops.load-more", len(filtered)-visibleCount)).Clicked() {
							st.timelineLimit += logsDefaultTimelineLimit
						}
					})
				} else if remote && st.remote.hasMore {
					ui.Row(c).FillWidth().Padding(10, 16).Justify(ui.Center).Children(func() {
						if SailSmallButton(c.Key("logs-load-older"), i18n.T("policy.load-older")).Disabled(st.remote.loading).Clicked() {
							filter, _ := logsServerFilter(st)
							u.fetchLogsPage(st, filter, st.remote.key, true)
						}
					})
				}
			})
	})
}

func renderLogsTimelineRow(c *ui.Context, st *logsPageState, p logsTimelineRowProps) {
	ev := p.ev
	rowBg := SailBgSurface
	if p.selected {
		rowBg = SailAccentSoft
	}

	rowBtn := ui.ButtonBase(c.Key("logs-item-"+p.rowKey)).
		Column().
		FillWidth().
		Padding(12, 18).
		Gap(5).
		AlignItems(ui.Stretch).
		Background(rowBg).
		Cursor(ui.CursorPointer)
	if !p.isLast {
		rowBtn.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	if rowBtn.Hovered() && !p.selected {
		rowBtn.Background(SailBgHover)
	}

	rowBtn.Children(func() {
		// Anchor 1 & 3: Status dot + quiet kind (+ Error badge if error) on left; latency + time on right.
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				SailStatusDot(c, logsRowDotColor(ev))
				ui.Text(c, logsRowKindLabel(ev)).
					Font("monospace").
					FontSize(11.5).
					TextColor(SailInkMuted).
					SingleLine()
				if ev.Decision == "deny" || ev.Decision == "approve" {
					policyEffectBadge(c, ev.Decision)
				} else if logsIsErrorStatus(ev.Status) {
					SailBadge(c, i18n.T("ops.status-error"), ToneDanger)
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
		if p.selected {
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

func (u *DesktopUI) renderLogsInspectorPane(
	c *ui.Context,
	st *logsPageState,
	ev apitypes.Event,
	toolByID map[string]apitypes.Tool,
) {
	parsed := logsParseEventDetail(ev, toolByID)

	applyCardElevation(
		ui.Column(c.Key("logs-inspector-"+ev.ID)).
			Grow(1).
			MinWidth(0).
			Padding(20, 22).
			Gap(18).
			Radius(10).
			Background(SailBgSurface),
	).Children(func() {
		u.renderLogsInspectorHeader(c, ev, parsed, toolByID)
		renderLogsInspectorMetrics(c, ev, parsed)
		renderLogsInspectorAuditCard(c, ev)
		u.renderLogsLinkedApproval(c, st, ev)
		renderLogsInspectorMetaCard(c, parsed)

		if sc := parsed.Scope; sc != nil {
			u.renderLogsScopeCard(c, st, ev.ID, *sc)
		}

		if parsed.Summary != "" {
			SailSubtleCard(c, func() {
				ui.Text(c, parsed.Summary).FontSize(13).LineHeight(1.5).TextColor(SailInkStrong).Selectable()
			})
		}
		if parsed.Question != "" {
			SailAlertBox(c, ToneWarning, i18n.T("ops.needs-context"), parsed.Question)
		}
		if parsed.Error != "" {
			SailAlertBox(c, ToneDanger, i18n.T("ops.execution-error"), parsed.Error)
		}

		u.renderLogsMatchedToolsCard(c, ev, parsed, toolByID)
		u.renderLogsPayloadViewer(c, st, parsed)
	})
}

func (u *DesktopUI) renderLogsInspectorHeader(
	c *ui.Context,
	ev apitypes.Event,
	parsed logsParsedDetail,
	toolByID map[string]apitypes.Tool,
) {
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
				if SailSmallButton(c.Key("logs-replay-"+ev.ID), i18n.T("ops.replay-in-sandbox")).Clicked() {
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
					if SailSmallButton(c.Key("logs-jump-cat-"+ev.ID), i18n.T("ops.view-contract")).Clicked() {
						u.OpenCatalogTool(regTool.ID, regTool.ServerID)
					}
				}
			}
			if SailSmallButton(c.Key("logs-copy-json-"+ev.ID), i18n.T("ops.copy-json")).Clicked() {
				c.WriteClipboard(parsed.FullEventJSON)
				u.Notify(i18n.T("ops.copied-event-json"))
			}
		})
	})
}

func renderLogsInspectorMetrics(c *ui.Context, ev apitypes.Event, parsed logsParsedDetail) {
	ui.Row(c).FillWidth().Wrap().Gap(12).Children(func() {
		logsMetricTile(c, i18n.T("ops.duration"), fmt.Sprintf("%d ms", ev.DurationMS), "", true)

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
		logsMetricTile(c, i18n.T("ops.actor"), actorStr, "", true)
		logsMetricTile(c, i18n.T("ops.time"), FormatTime(ev.Time), ev.Time.Local().Format("2006-01-02"), true)
	})
}

func renderLogsInspectorMetaCard(c *ui.Context, parsed logsParsedDetail) {
	hasExtraMeta := parsed.Model != "" ||
		parsed.Eligible != nil ||
		len(parsed.Servers) > 0 ||
		parsed.Client != "" ||
		parsed.Method != "" ||
		len(parsed.Fields) > 0
	if !hasExtraMeta {
		return
	}
	SailSubtleCard(c, func() {
		if parsed.Model != "" {
			SailKeyValueRow(c, i18n.T("ops.routing-model"), parsed.Model, true)
		}
		if parsed.Eligible != nil {
			SailKeyValueRow(c, i18n.T("ops.candidate-narrowing"), fmt.Sprintf("%d → %d", *parsed.Eligible, len(parsed.Tools)), true)
		}
		if len(parsed.Servers) > 0 {
			SailKeyValueRow(c, i18n.T("ops.matched-services"), strings.Join(parsed.Servers, ", "), true)
		}
		if parsed.Method != "" {
			SailKeyValueRow(c, i18n.T("ops.protocol-method"), parsed.Method, true)
		}
		if parsed.AgentToolName != "" {
			SailKeyValueRow(c, i18n.T("ops.meta-tool"), parsed.AgentToolName, true)
		}
		if parsed.Client != "" {
			SailKeyValueRow(c, i18n.T("ops.client-label"), parsed.Client, true)
		}
		if len(parsed.Fields) > 0 {
			SailKeyValueRow(c, i18n.T("ops.trimmed-fields"), strings.Join(parsed.Fields, ", "), true)
		}
	})
}

type logsMatchedToolRowProps struct {
	evID           string
	idx            int
	item           logsMatchedTool
	isLast         bool
	hasCatalogTool bool
}

func (u *DesktopUI) renderLogsMatchedToolsCard(
	c *ui.Context,
	ev apitypes.Event,
	parsed logsParsedDetail,
	toolByID map[string]apitypes.Tool,
) {
	if len(parsed.Tools) == 0 {
		if ev.Kind == "discover" && parsed.Error == "" {
			SailSubtleCard(c, func() {
				ui.Text(c, i18n.T("ops.no-candidate-tools")).FontSize(12.5).TextColor(SailInkMuted)
			})
		}
		return
	}

	SailSubtleCard(c, func() {
		ui.Text(c, i18n.T("ops.matched-tools-count", len(parsed.Tools))).
			FontSize(12).
			FontWeight(600).
			TextColor(SailInkMuted)

		ui.Column(c).FillWidth().Children(func() {
			for idx, mt := range parsed.Tools {
				_, exists := toolByID[mt.ID]
				u.renderLogsMatchedToolRow(c, logsMatchedToolRowProps{
					evID:           ev.ID,
					idx:            idx,
					item:           mt,
					isLast:         idx == len(parsed.Tools)-1,
					hasCatalogTool: mt.ID != "" && exists,
				})
			}
		})
	})
}

func (u *DesktopUI) renderLogsMatchedToolRow(c *ui.Context, p logsMatchedToolRowProps) {
	item := p.item
	r := ui.Column(c.Key(fmt.Sprintf("logs-hit-%s-%d", p.evID, p.idx))).
		FillWidth().
		Padding(10, 0).
		Gap(4)
	if !p.isLast {
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
					SailBadge(c, i18n.T("ops.write-access"), ToneWarning)
				}
			})

			if p.hasCatalogTool {
				if SailSmallButton(c.Key(fmt.Sprintf("logs-open-tool-%s-%d", p.evID, p.idx)), i18n.T("ops.catalog-detail")).Clicked() {
					u.OpenCatalogTool(item.ID, item.ServerID)
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

func (u *DesktopUI) renderLogsPayloadViewer(c *ui.Context, st *logsPageState, parsed logsParsedDetail) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.Start).Children(func() {
			ui.Segmented(c.Key("logs-payload-tab"), &st.payloadTab, i18n.T("ops.raw-event-json"), i18n.T("ops.detail-payload")).
				Label(i18n.T("ops.payload-view"))
		})

		if st.payloadTab == 1 {
			SailCodeBlock(c, "DETAIL PAYLOAD", parsed.DetailJSON, func() {
				u.Notify(i18n.T("ops.copied-detail-payload"))
			})
		} else {
			SailCodeBlock(c, "EVENT TRACE", parsed.FullEventJSON, func() {
				u.Notify(i18n.T("ops.copied-full-event-json"))
			})
		}
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

	detailAny := logsParseDetailPayload(ev.Detail)
	if len(ev.Detail) == 0 {
		out.DetailJSON = "{}"
	} else if detailAny != nil {
		out.DetailJSON = PrettyJSON(detailAny)
	} else {
		out.DetailJSON = string(ev.Detail)
	}
	out.FullEventJSON = logsBuildFullEventJSON(ev, detailAny)

	m, ok := detailAny.(map[string]any)
	if !ok || m == nil {
		return out
	}

	logsParseDetailScalars(m, &out)
	out.Scope = logsParseDetailScope(m["scope"])
	out.Servers = logsParseStringList(m, "servers")
	out.Fields = logsParseStringList(m, "fields")
	if rawTools, ok := m["tools"].([]any); ok {
		out.Tools = logsParseMatchedTools(rawTools, toolByID)
	}

	return out
}

func logsParseDetailPayload(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func logsBuildFullEventJSON(ev apitypes.Event, detailAny any) string {
	fullEnvelope := map[string]any{
		"id":          ev.ID,
		"time":        ev.Time,
		"kind":        ev.Kind,
		"actor":       ev.Actor,
		"status":      ev.Status,
		"title":       ev.Title,
		"duration_ms": ev.DurationMS,
	}
	for k, v := range map[string]string{
		"repo_id":     ev.RepoID,
		"service_id":  ev.ServiceID,
		"protocol":    ev.Protocol,
		"action":      ev.Action,
		"decision":    ev.Decision,
		"rule_id":     ev.RuleID,
		"approval_id": ev.ApprovalID,
		"skill_id":    ev.SkillID,
	} {
		if v != "" {
			fullEnvelope[k] = v
		}
	}
	if detailAny != nil {
		fullEnvelope["detail"] = detailAny
	}
	return PrettyJSON(fullEnvelope)
}

func logsStringField(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func logsParseDetailScalars(m map[string]any, out *logsParsedDetail) {
	out.Model = logsStringField(m, "model")
	out.Summary = logsStringField(m, "summary")
	out.Question = logsStringField(m, "question")
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
	out.ToolID = logsStringField(m, "tool_id")
	out.Client = logsStringField(m, "client")
	out.Method = logsStringField(m, "method")
	out.AgentToolName = logsStringField(m, "tool")
}

func logsParseDetailScope(raw any) *logsParsedScope {
	rawScope, ok := raw.(map[string]any)
	if !ok || len(rawScope) == 0 {
		return nil
	}
	sc := &logsParsedScope{
		Agent:        logsStringField(rawScope, "agent"),
		RepoID:       logsStringField(rawScope, "repo_id"),
		RepoName:     logsStringField(rawScope, "repo_name"),
		Kind:         logsStringField(rawScope, "kind"),
		Root:         logsStringField(rawScope, "root"),
		GitCommonDir: logsStringField(rawScope, "git_common_dir"),
		RemoteHint:   logsStringField(rawScope, "remote_hint"),
		MatchedRule:  logsStringField(rawScope, "matched_rule"),
		Status:       logsStringField(rawScope, "status"),
	}
	if sc.Root == "" {
		sc.Root = logsStringField(rawScope, "cwd")
	}
	if sc.Status == "" {
		sc.Status = logsStringField(rawScope, "context_status")
	}
	if sc.Agent != "" || sc.RepoID != "" || sc.RepoName != "" || sc.Root != "" || sc.MatchedRule != "" || sc.Status != "" {
		return sc
	}
	return nil
}

func logsParseStringList(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, item := range raw {
		if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func logsParseMatchedTools(rawTools []any, toolByID map[string]apitypes.Tool) []logsMatchedTool {
	out := make([]logsMatchedTool, 0, len(rawTools))
	for _, raw := range rawTools {
		switch item := raw.(type) {
		case string:
			if mt, ok := logsParseMatchedToolString(item, toolByID); ok {
				out = append(out, mt)
			}
		case map[string]any:
			if mt, ok := logsParseMatchedToolMap(item, toolByID); ok {
				out = append(out, mt)
			}
		}
	}
	return out
}

func logsEnrichMatchedTool(mt *logsMatchedTool, toolByID map[string]apitypes.Tool) {
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
		return
	}
	if mt.ServerID == "" && strings.Contains(mt.ID, ".") {
		parts := strings.SplitN(mt.ID, ".", 2)
		mt.ServerID = parts[0]
		if mt.Name == "" {
			mt.Name = parts[1]
		}
	}
}

func logsParseMatchedToolString(raw string, toolByID map[string]apitypes.Tool) (logsMatchedTool, bool) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return logsMatchedTool{}, false
	}
	mt := logsMatchedTool{ID: id}
	logsEnrichMatchedTool(&mt, toolByID)
	if mt.Name == "" {
		mt.Name = id
	}
	return mt, true
}

func logsParseMatchedToolMap(item map[string]any, toolByID map[string]apitypes.Tool) (logsMatchedTool, bool) {
	var mt logsMatchedTool
	if s, ok := item["id"].(string); ok {
		mt.ID = strings.TrimSpace(s)
	} else if s, ok := item["tool_id"].(string); ok {
		mt.ID = strings.TrimSpace(s)
	}
	mt.Name = logsStringField(item, "name")
	mt.ServerID = logsStringField(item, "server_id")
	mt.Description = logsStringField(item, "description")
	mt.Reason = logsStringField(item, "reason")
	if ro, ok := item["read_only"].(bool); ok {
		mt.ReadOnly = ro
		mt.HasReadOnly = true
	}
	logsEnrichMatchedTool(&mt, toolByID)
	if mt.ID != "" || mt.Name != "" {
		return mt, true
	}
	return logsMatchedTool{}, false
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
	repos := state.Scopes.Repositories
	if repo := logsFindRepoByID(repos, sc.RepoID); repo != nil {
		return repo
	}
	effectiveCommonDir, isGit := logsEffectiveGitScope(state.Scopes.Observed, sc)
	if isGit {
		return logsMatchGitRepo(repos, effectiveCommonDir, sc.Root)
	}
	return logsMatchRootPrefixRepo(repos, sc.Root)
}

func logsFindRepoByID(repos []apitypes.Repository, repoID string) *apitypes.Repository {
	if repoID == "" || repoID == "*" {
		return nil
	}
	for i := range repos {
		if repos[i].ID == repoID {
			return &repos[i]
		}
	}
	return nil
}

func logsEffectiveGitScope(observed []apitypes.ObservedRepo, sc logsParsedScope) (commonDir string, isGit bool) {
	commonDir = sc.GitCommonDir
	kind := sc.Kind
	if commonDir == "" && sc.Root != "" {
		for _, obs := range observed {
			if obs.Root != sc.Root {
				continue
			}
			if obs.GitCommonDir != "" {
				commonDir = obs.GitCommonDir
			}
			if kind == "" && obs.Kind != "" {
				kind = obs.Kind
			}
			break
		}
	}
	return commonDir, kind == "git" || commonDir != ""
}

func logsMatchGitRepo(repos []apitypes.Repository, commonDir, root string) *apitypes.Repository {
	for i := range repos {
		repo := &repos[i]
		if commonDir != "" && slices.Contains(repo.GitCommonDirs, commonDir) {
			return repo
		}
		if commonDir == "" && root != "" && repoHasDotGitAtRoot(repo.GitCommonDirs, root) {
			return repo
		}
	}
	return nil
}

func repoHasDotGitAtRoot(commonDirs []string, root string) bool {
	for _, cd := range commonDirs {
		if filepath.Base(cd) == ".git" && filepath.Dir(cd) == root {
			return true
		}
	}
	return false
}

func logsMatchRootPrefixRepo(repos []apitypes.Repository, root string) *apitypes.Repository {
	if root == "" {
		return nil
	}
	var best *apitypes.Repository
	bestLen := -1
	for i := range repos {
		repo := &repos[i]
		for _, rp := range repo.RootPaths {
			if hasSegmentPathPrefix(root, rp) && len(rp) > bestLen {
				best = repo
				bestLen = len(rp)
			}
		}
	}
	return best
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
				ui.Text(c, i18n.T("ops.repo-scope")).FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
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
					if SailSmallButton(c.Key("logs-open-repo-"+evID), i18n.T("ops.repo-rule")).Clicked() {
						u.OpenRepos(boundRepo.ID)
					}
				} else if canBind {
					bindLbl := i18n.T("ops.bind-this-repo")
					if st.bindingRepo {
						bindLbl = i18n.T("ops.binding")
					}
					if SailSmallButton(c.Key("logs-bind-repo-"+evID), bindLbl).Disabled(st.bindingRepo).Clicked() {
						u.bindRepoFromLogScope(st, sc)
					}
				}
			})
		})

		if sc.Agent != "" {
			SailKeyValueRow(c, i18n.T("ops.calling-client"), fmt.Sprintf("%s (%s)", agentDisplayName(sc.Agent), sc.Agent), false)
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
				repoDisplay += i18n.T("ops.unbound-suffix")
			}
			SailKeyValueRow(c, i18n.T("ops.repo-label"), repoDisplay, false)
		}
		if sc.Root != "" {
			SailKeyValueRow(c, i18n.T("ops.workdir"), sc.Root, true)
		}
		if sc.MatchedRule != "" {
			SailKeyValueRow(c, i18n.T("ops.matched-rule"), sc.MatchedRule, true)
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
		u.Notify(i18n.T("ops.repo-bound-toast", saved.Name))
		return nil
	})
}
