package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type serviceListState struct {
	subtab           int // 0=已接入, 1=市场
	listQuery        string
	listStatusFilter int // 0=全部, else serviceTriageBucket+1
	bulkProbeBusy    bool
	demoExpanded     bool
	syncingServerID  string
	toggleBusyID     string
}

type serviceListRow struct {
	server    apitypes.Server
	readiness serviceReadiness
	bucket    serviceTriageBucket
}

type serviceListRowItem struct {
	row       serviceListRow
	tools     []apitypes.Tool
	noDivider bool
}

type serviceToolCounts struct {
	total    int
	enabled  int
	readOnly int
	writable int
}

type serviceListData struct {
	toolsByServer map[string][]apitypes.Tool
	rows          []serviceListRow
	demoServers   []apitypes.Server
	probeTargets  []apitypes.Server
	bucketCounts  [triageBucketCount]int
	enabledCount  int
}

func groupToolsByServer(tools []apitypes.Tool) map[string][]apitypes.Tool {
	grouped := make(map[string][]apitypes.Tool)
	for _, t := range tools {
		grouped[t.ServerID] = append(grouped[t.ServerID], t)
	}
	return grouped
}

func summarizeServiceTools(tools []apitypes.Tool) serviceToolCounts {
	counts := serviceToolCounts{total: len(tools)}
	for _, t := range tools {
		if !t.Enabled {
			continue
		}
		counts.enabled++
		if t.ReadOnly {
			counts.readOnly++
		}
	}
	counts.writable = counts.enabled - counts.readOnly
	return counts
}

func readOnlyToolIDs(srvTools []apitypes.Tool) []string {
	var ids []string
	for _, t := range srvTools {
		if t.ReadOnly {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

func buildServiceListData(
	servers []apitypes.Server,
	tools []apitypes.Tool,
	readinessFn func(apitypes.Server, []apitypes.Tool) serviceReadiness,
) serviceListData {
	data := serviceListData{
		toolsByServer: groupToolsByServer(tools),
	}
	for _, s := range servers {
		if s.Demo {
			data.demoServers = append(data.demoServers, s)
			continue
		}
		r := readinessFn(s, data.toolsByServer[s.ID])
		b := r.TriageBucket()
		data.rows = append(data.rows, serviceListRow{server: s, readiness: r, bucket: b})
		data.bucketCounts[b]++
		if s.Enabled {
			data.enabledCount++
		}
		if r.Probeable() {
			data.probeTargets = append(data.probeTargets, s)
		}
	}
	// Stable sort keeps registry order inside each bucket.
	sort.SliceStable(data.rows, func(i, j int) bool { return data.rows[i].bucket < data.rows[j].bucket })
	return data
}

func filterServiceListRows(rows []serviceListRow, statusFilter int, query string) []serviceListRow {
	q := strings.ToLower(strings.TrimSpace(query))
	if statusFilter == 0 && q == "" {
		return rows
	}
	var filtered []serviceListRow
	for _, row := range rows {
		if statusFilter > 0 && int(row.bucket) != statusFilter-1 {
			continue
		}
		if q != "" && !serviceRowMatchesQuery(row, q) {
			continue
		}
		filtered = append(filtered, row)
	}
	return filtered
}

func serviceRowMatchesQuery(row serviceListRow, lowerQuery string) bool {
	s := row.server
	hay := strings.ToLower(strings.Join([]string{
		s.Name, s.ID, s.Description, ServerTargetText(s), serviceProtocolTag(s),
		strings.Join(row.readiness.MissingVars, " "),
	}, " "))
	return strings.Contains(hay, lowerQuery)
}

func serviceListFilterLabels(bucketCounts [triageBucketCount]int) []string {
	labels := make([]string, 0, int(triageBucketCount)+1)
	labels = append(labels, i18n.T("common.all"))
	for b := serviceTriageBucket(0); b < triageBucketCount; b++ {
		labels = append(labels, fmt.Sprintf("%s %d", b.Label(), bucketCounts[b]))
	}
	return labels
}

func serviceListEmptyTitle(statusFilter int, query string) string {
	if strings.TrimSpace(query) == "" && statusFilter == int(triageAttention)+1 {
		return i18n.T("services.list.nothing_to_fix")
	}
	return i18n.T("services.list.no_match")
}

func (u *DesktopUI) renderServiceList(c *ui.Context, st *servicesPageState) {
	data := buildServiceListData(u.State.Registry.Servers, u.State.Registry.Tools, u.readinessForServer)

	u.renderServiceListHeader(c, st, data)
	if st.subtab == 1 {
		u.renderMarketContent(c)
		return
	}

	u.renderServiceListCard(c, st, data)
	if len(data.demoServers) > 0 {
		u.renderDemoServicesCard(c, st, data.demoServers, data.toolsByServer)
	}
	u.renderSkillAllowlistSection(c, st)
}

func (u *DesktopUI) renderServiceListHeader(c *ui.Context, st *servicesPageState, data serviceListData) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("services.page.title")).FontSize(21).Bold().LetterSpacing(-0.2).TextColor(SailInkStrong)
			ui.Segmented(c.Key("svc-subtab"), &st.subtab, i18n.T("common.connected"), i18n.T("services.tab.market")).
				Label(i18n.T("services.tab.switch"))
			if st.subtab == 0 {
				if attention := data.bucketCounts[triageAttention]; attention > 0 {
					ui.Text(c, fmt.Sprintf(i18n.T("services.summary.attention"), attention)).FontSize(12.5).FontWeight(600).TextColor(SailDangerInk).SingleLine()
				}
				ui.Text(c, fmt.Sprintf(i18n.T("services.summary.ready"), data.bucketCounts[triageReady], data.enabledCount, len(data.rows))).
					FontSize(12.5).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()
			} else {
				SailQuietStatus(c, i18n.T("services.summary.market_hint"), SailSuccessInk, SailInkMuted)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			if st.subtab == 1 {
				mktSt := u.marketState()
				if SailSmallButton(c.Key("mkt-refresh"), i18n.T("common.refresh")).Disabled(mktSt.loading).Clicked() {
					u.fetchMarket(mktSt, mktSt.registryQuery)
				}
			}
			u.renderServiceAddDropdown(c, st)
		})
	})
}

func (u *DesktopUI) renderServiceAddDropdown(c *ui.Context, st *servicesPageState) {
	ui.Row(c).Gap(1).AlignItems(ui.Center).Shrink(0).Children(func() {
		if SailPrimaryButton(c.Key("svc-btn-add"), i18n.T("services.action.add")).Clicked() {
			st.add.open(0)
		}
		SailPrimaryButton(c.Key("svc-btn-add-more"), "▾").
			Padding(0, 8).
			Label(i18n.T("services.action.more_add")).
			PopoverMenuButton(func(menu *ui.Menu) {
				if menu.Item(i18n.T("services.add.menu.manual")).Chosen() {
					st.add.open(0)
				}
				if menu.Item(i18n.T("services.add.menu.paste_json")).Chosen() {
					st.add.open(3)
				}
				if menu.Item(i18n.T("services.add.menu.openapi")).Chosen() {
					st.add.open(2)
				}
				if menu.Item(i18n.T("services.add.menu.gh_cli")).Chosen() {
					st.add.open(serviceAddTabCLI)
					st.add.applyGHPreset()
				}
				if st.subtab == 0 && menu.Item(i18n.T("services.add.menu.market")).Chosen() {
					st.subtab = 1
				}
			})
	})
}

func (u *DesktopUI) renderServiceListCard(c *ui.Context, st *servicesPageState, data serviceListData) {
	// Hairline-divided service list card, triaged by readiness.
	SailFlushCard(c, func() {
		if len(data.rows) == 0 {
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, i18n.T("services.empty.title"), "", i18n.T("services.action.add"), func() { st.add.open(0) })
			})
			return
		}

		u.renderServiceListToolbar(c, st, data)

		filtered := filterServiceListRows(data.rows, st.listStatusFilter, st.listQuery)
		if len(filtered) == 0 {
			title := serviceListEmptyTitle(st.listStatusFilter, st.listQuery)
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, title, "", i18n.T("services.list.view_all"), func() {
					st.listQuery = ""
					st.listStatusFilter = 0
				})
			})
			return
		}

		showGroups := st.listStatusFilter == 0
		for idx, item := range filtered {
			if showGroups && (idx == 0 || filtered[idx-1].bucket != item.bucket) {
				SailGroupHeader(c, item.bucket.Label(), data.bucketCounts[item.bucket], 20, idx == 0)
			}
			// No divider under the last row of a group: whitespace + the next header separate groups.
			endsGroup := idx == len(filtered)-1 || (showGroups && filtered[idx+1].bucket != item.bucket)
			u.renderServiceListRow(c, st, serviceListRowItem{
				row:       item,
				tools:     data.toolsByServer[item.server.ID],
				noDivider: endsGroup,
			})
		}
	})
}

func (u *DesktopUI) renderServiceListToolbar(c *ui.Context, st *servicesPageState, data serviceListData) {
	ui.Row(c).
		FillWidth().
		Padding(12, 20).
		Gap(10).
		BorderWidth(0, 0, 1, 0).
		BorderColor(SailDivider).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			SailSegmented(c.Key("svc-status-filter"), &st.listStatusFilter, serviceListFilterLabels(data.bucketCounts)...)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Grow(1).MinWidth(0).Justify(ui.End).Children(func() {
				if len(data.probeTargets) > 1 {
					label := fmt.Sprintf(i18n.T("services.action.test_all"), len(data.probeTargets))
					if st.bulkProbeBusy {
						label = i18n.T("common.testing")
					}
					if SailButton(c.Key("svc-probe-all"), label).Disabled(st.bulkProbeBusy || st.connBusy).Clicked() {
						u.probeServersBulk(st, data.probeTargets)
					}
				}
				SailSearchField(c.Key("svc-list-search"), &st.listQuery).
					Placeholder(i18n.T("services.search.placeholder")).
					Width(240)
				if strings.TrimSpace(st.listQuery) != "" || st.listStatusFilter != 0 {
					if SailButton(c.Key("svc-filter-reset"), i18n.T("common.reset")).Clicked() {
						st.listQuery = ""
						st.listStatusFilter = 0
					}
				}
			})
		})
}

func (u *DesktopUI) renderDemoServicesCard(c *ui.Context, st *servicesPageState, demoServers []apitypes.Server, toolsByServer map[string][]apitypes.Tool) {
	SailCard(c, func() {
		ui.Collapsible(
			c.Key("svc-demo-collapsible"),
			fmt.Sprintf(i18n.T("services.demo.section"), len(demoServers)),
			&st.demoExpanded,
			func() {
				ui.Column(c).FillWidth().Padding(6, 0, 0, 0).Children(func() {
					for idx, dsrv := range demoServers {
						u.renderDemoServiceRow(c, st, dsrv, toolsByServer[dsrv.ID], idx == len(demoServers)-1)
					}
				})
			},
		)
	})
}

func (u *DesktopUI) renderDemoServiceRow(c *ui.Context, st *servicesPageState, s apitypes.Server, srvTools []apitypes.Tool, isLast bool) {
	r := ui.Row(c.Key("demo-row-"+s.ID)).
		FillWidth().
		Padding(11, 4).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(12)
	if !isLast {
		r.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	r.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, s.Name).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				ui.Text(c, fmt.Sprintf(i18n.T("services.row.tool_count"), len(srvTools))).FontSize(11.5).TextColor(SailInkFaint).FontFeatures("tnum")
			})
			SailDescSummary(c, s.Description, ServerTargetText(s), 1)
		})
		if SailSmallButton(c.Key("demo-view-"+s.ID), i18n.T("services.row.view_tools")).Clicked() {
			st.enterDetail(s.ID)
			u.OpenServiceDetail(s.ID)
		}
	})
}

// renderServiceListRow renders one triaged service row: identity, a single
// readiness line (status word + variables or reason), quiet tool counts, the
// state-specific fix action, the enable switch, and a whole-row detail link.
func (u *DesktopUI) renderServiceListRow(c *ui.Context, st *servicesPageState, item serviceListRowItem) {
	s := item.row.server
	readiness := item.row.readiness
	counts := summarizeServiceTools(item.tools)
	live := readiness.Kind == readinessReady || readiness.Kind == readinessNoOpenTools

	row := ui.Row(c.Key("svc-card-"+s.ID)).
		FillWidth().
		Padding(13, 20).
		Gap(14).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Cursor(ui.CursorPointer).
		Label(i18n.T("services.row.open_detail", s.Name))
	if !item.noDivider {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	hovered := row.Hovered()
	if hovered {
		row.Background(SailBgHover)
	}
	nameInk, bodyInk := SailInkStrong, SailInkMuted
	if !s.Enabled {
		nameInk, bodyInk = SailInkFaint, SailInkFaint
	}

	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				SailStatusDot(c, readiness.DotColor)
				ui.Text(c, s.Name).FontSize(13.5).FontWeight(600).TextColor(nameInk).SingleLine()
				ui.Text(c, serviceProtocolTag(s)).FontSize(11.5).TextColor(SailInkFaint).SingleLine()
				if serverIsCLI(s) && s.CommandSHA256 != "" {
					ui.Text(c, "sha256:"+cliDigestShort(s.CommandSHA256)).Font("monospace").FontSize(11).TextColor(SailInkFaint).SingleLine().Tooltip(strings.ToLower(s.CommandSHA256))
				}
			})
			ui.Row(c).Padding(0, 0, 0, 14.5).Gap(6).AlignItems(ui.Baseline).MinWidth(0).Children(func() {
				renderServiceRowStatusLine(c, s, readiness, bodyInk)
			})
		})

		u.renderServiceRowTrailing(c, st, item, counts, live, hovered)
	})

	if row.Clicked() {
		st.enterDetail(s.ID)
		u.OpenServiceDetail(s.ID)
	}
}

func (u *DesktopUI) renderServiceRowTrailing(
	c *ui.Context,
	st *servicesPageState,
	item serviceListRowItem,
	counts serviceToolCounts,
	live, hovered bool,
) {
	s := item.row.server
	readiness := item.row.readiness
	ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
		countText := fmt.Sprintf(i18n.T("services.row.tools_open"), counts.enabled, counts.total)
		if s.Enabled && counts.writable > 0 {
			countText += fmt.Sprintf(i18n.T("services.row.writable"), counts.writable)
		}
		countInk, countTip := SailInkMuted, i18n.T("services.row.count_tooltip_open")
		if !live {
			countInk, countTip = SailInkFaint, i18n.T("services.row.count_tooltip_stale")
		}
		ui.Text(c, countText).FontSize(12).FontFeatures("tnum").TextColor(countInk).SingleLine().Tooltip(countTip)

		// Fixed-width slot keeps switches aligned whether or not a row has an action.
		ui.Row(c).Width(94).Justify(ui.End).Children(func() {
			action, label := readiness.RowAction()
			if action == readinessActionNone {
				return
			}
			busy := st.connBusy || st.toolBatchBusy || st.bulkProbeBusy
			if SailSmallButton(c.Key("svc-act-"+s.ID), label).Disabled(busy).Clicked() {
				u.runServiceRowAction(st, s, item.tools, action)
			}
		})

		enabledVal := s.Enabled
		sw := ui.Switch(c.Key("svc-sw-"+s.ID), &enabledVal).
			Label(i18n.T("services.row.enable_switch", s.Name)).
			Disabled(st.toggleBusyID == s.ID)
		if sw.Changed() {
			u.toggleServerEnabled(st, s, enabledVal)
		}

		chevronInk := SailInkFaint
		if hovered {
			chevronInk = SailInkMuted
		}
		ui.Icon(c, IconChevronRight).Size(14, 14).TextColor(chevronInk).Shrink(0)
	})
}

// renderServiceRowStatusLine expresses readiness once: a colored status word
// plus the missing variables or failure reason, instead of dot + badge + red text.
func renderServiceRowStatusLine(c *ui.Context, s apitypes.Server, r serviceReadiness, bodyInk ui.Color) {
	statusWord := func(label string, ink ui.Color) {
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(ink).SingleLine().Shrink(0)
	}
	detail := func(text string, mono bool) {
		t := ui.Text(c, text).FontSize(12.5).LineHeight(1.42).TextColor(bodyInk).SingleLine().Grow(1).MinWidth(0)
		if mono {
			t.Font("monospace").FontSize(12).TextColor(SailInkStrong)
		}
	}
	switch r.Kind {
	case readinessMissingCreds:
		statusWord(i18n.T("common.missing_credentials"), SailDangerInk)
		ui.Text(c, strings.Join(r.MissingVars, ", ")).Font("monospace").FontSize(12).TextColor(SailInkStrong).SingleLine().Shrink(0)
		if r.OAuthUpgrade != nil {
			ui.Text(c, i18n.T("services.row.supports_oauth")).FontSize(12.5).TextColor(SailInkMuted).SingleLine().MinWidth(0)
		} else {
			ui.Text(c, i18n.T("services.row.agent_will_fail")).FontSize(12.5).TextColor(SailInkFaint).SingleLine().MinWidth(0)
		}
	case readinessOAuthRequired:
		ink := SailWarningInk
		if r.BadgeTone == ToneDanger {
			ink = SailDangerInk
		}
		statusWord(r.BadgeLabel, ink)
		detail(r.RowSummary, false)
	case readinessConnError, readinessInvokeError:
		statusWord(r.BadgeLabel, SailDangerInk)
		detail(r.RowSummary, false)
	case readinessUnverified:
		statusWord(r.BadgeLabel, SailWarningInk)
		if strings.HasPrefix(r.RowSummary, i18n.T("readiness.unverified.row_prefix")) {
			detail(i18n.T("readiness.unverified.row_prefix"), false)
		} else {
			detail(r.RowSummary, false)
		}
	case readinessNoOpenTools:
		statusWord(r.BadgeLabel, SailWarningInk)
		detail(r.RowSummary, false)
	case readinessDisabled:
		if len(r.MissingVars) > 0 {
			ui.Text(c, i18n.T("services.row.missing_creds_hint")).FontSize(12.5).TextColor(SailInkFaint).SingleLine().Shrink(0)
			ui.Text(c, strings.Join(r.MissingVars, ", ")).Font("monospace").FontSize(12).TextColor(SailInkFaint).SingleLine().MinWidth(0)
			return
		}
		SailDescSummary(c, s.Description, ServerTargetText(s), 1).Grow(1).MinWidth(0)
	default:
		if serverIsCLI(s) {
			ui.Text(c, ServerTargetText(s)).Font("monospace").FontSize(12).TextColor(bodyInk).SingleLine().TruncateMiddle().Grow(1).MinWidth(0).Tooltip(ServerTargetText(s))
			return
		}
		SailDescSummary(c, s.Description, ServerTargetText(s), 1).Grow(1).MinWidth(0)
	}
}

func (u *DesktopUI) runServiceRowAction(st *servicesPageState, s apitypes.Server, srvTools []apitypes.Tool, action readinessActionKind) {
	switch action {
	case readinessActionFillCreds:
		st.enterDetail(s.ID)
		u.OpenServiceDetail(s.ID)
		st.detailSubtab = 1
		st.connMode = 0
	case readinessActionOAuthSignIn, readinessActionProbe:
		u.probeServerConnection(st, s)
	case readinessActionEnableReadOnly:
		u.applyServerToolWhitelist(st, s.ID, readOnlyToolIDs(srvTools), i18n.T("services.toast.opened_read_only"))
	}
}

// probeServersBulk tests every given service sequentially, refreshes once,
// and reports a single summary instead of one notification per service.
func (u *DesktopUI) probeServersBulk(st *servicesPageState, targets []apitypes.Server) {
	if u.Client == nil || st.bulkProbeBusy || len(targets) == 0 {
		return
	}
	st.bulkProbeBusy = true
	u.RunAsync(func() error {
		passed := 0
		for _, s := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			check, err := u.Client.TestSource(ctx, s)
			cancel()
			if err != nil && apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.bulkProbeBusy = false })
				return err
			}
			if err == nil && check.Status != "error" {
				passed++
			}
		}
		syncErr := u.RefreshSync()
		u.updateUI(func() { st.bulkProbeBusy = false })
		if syncErr != nil && apiclient.IsUnauthorized(syncErr) {
			return syncErr
		}
		u.Notify(fmt.Sprintf(i18n.T("services.toast.bulk_test"), len(targets), passed, len(targets)-passed))
		return nil
	})
}

func (u *DesktopUI) toggleServerEnabled(st *servicesPageState, server apitypes.Server, enabled bool) {
	if u.Client == nil {
		return
	}
	st.toggleBusyID = server.ID
	updated := server
	updated.Enabled = enabled
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.toggleBusyID = "" })
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveServer(ctx, updated); err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		if enabled {
			u.Notify(i18n.T("services.toast.server_enabled", server.Name))
		} else {
			u.Notify(i18n.T("services.toast.server_disabled", server.Name))
		}
		return nil
	})
}
