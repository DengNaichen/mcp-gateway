package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

const catalogPageSize = 40

var catalogIconChevron = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`))

type catalogSelectOption struct {
	Value string
	Label string
}

type catalogEditFormState struct {
	loadedID       string
	loadedRev      string
	desc           string
	readOnly       bool
	actors         string
	tags           string
	savingContract bool
}

func (f *catalogEditFormState) sync(t apitypes.Tool) {
	sig := fmt.Sprintf("%s|%s|%t|%s|%s", t.Revision, t.Description, t.ReadOnly, strings.Join(t.Actors, ","), strings.Join(t.Tags, ","))
	if f.loadedID == t.ID && f.loadedRev == sig {
		return
	}
	f.loadedID = t.ID
	f.loadedRev = sig
	f.desc = t.Description
	f.readOnly = t.ReadOnly
	f.actors = strings.Join(t.Actors, ", ")
	f.tags = strings.Join(t.Tags, ", ")
}

func (f *catalogEditFormState) reset(t apitypes.Tool) {
	f.loadedID = ""
	f.sync(t)
}

type catalogPageState struct {
	query          string
	serverFilter   string // "all" or server.ID
	statusIdx      int    // 0: 全部, 1: 已开放, 2: 已关闭
	accessIdx      int    // 0: 全部, 1: 只读 ro, 2: 写操作 rw
	actorFilter    string // "all" or actor name
	sortIdx        int    // 0: 按近期命中, 1: 按名称 A–Z
	showDemo       bool
	page           int
	selectedToolID string
	panelCollapsed bool
	lastFilterSig  string

	detailTab   int // 0: 属性与 Schema, 1: 编辑契约, 2: 完整 JSON
	edit        catalogEditFormState
	togglingIDs map[string]bool
}

func (u *DesktopUI) catalogState() *catalogPageState {
	if u.catalog == nil {
		u.catalog = &catalogPageState{
			serverFilter: "all",
			actorFilter:  "all",
			page:         1,
			togglingIDs:  make(map[string]bool),
		}
	}
	return u.catalog
}

func (st *catalogPageState) focusTool(toolID, serverID string, serverByID map[string]apitypes.Server) {
	st.query = ""
	st.statusIdx = 0
	st.accessIdx = 0
	st.actorFilter = "all"
	st.page = 1
	if serverID != "" {
		st.serverFilter = serverID
		if srv, ok := serverByID[serverID]; ok && srv.Demo {
			st.showDemo = true
		}
	} else {
		st.serverFilter = "all"
	}
	if toolID != "" {
		st.selectedToolID = toolID
		st.panelCollapsed = false
		st.detailTab = 0
	}
}

func (st *catalogPageState) applyFilterIntent(filterServerID, filterAccess string, serverByID map[string]apitypes.Server) {
	if filterServerID != "" {
		st.serverFilter = filterServerID
		if srv, found := serverByID[filterServerID]; found && srv.Demo {
			st.showDemo = true
		}
		if filterAccess == "" {
			st.accessIdx = 0
		}
	}
	if filterAccess != "" {
		switch filterAccess {
		case "ro":
			st.accessIdx = 1
		case "rw":
			st.accessIdx = 2
		default:
			st.accessIdx = 0
		}
	}
	st.page = 1
}

func (st *catalogPageState) hasActiveFilters() bool {
	return strings.TrimSpace(st.query) != "" ||
		st.serverFilter != "all" ||
		st.statusIdx != 0 ||
		st.accessIdx != 0 ||
		st.actorFilter != "all"
}

func (st *catalogPageState) resetFilters() {
	st.query = ""
	st.serverFilter = "all"
	st.statusIdx = 0
	st.accessIdx = 0
	st.actorFilter = "all"
	st.page = 1
}

type catalogGovernanceStats struct {
	realToolCount    int
	enabledRealCount int
	closedRealCount  int
	roEnabledCount   int
	rwEnabledCount   int
	hitRealToolCount int
}

type catalogViewModel struct {
	serverByID    map[string]apitypes.Server
	realServers   []apitypes.Server
	demoServers   []apitypes.Server
	stats         catalogGovernanceStats
	actorOptions  []catalogSelectOption
	serverOptions []catalogSelectOption
	filtered      []apitypes.Tool
	pagedTools    []apitypes.Tool
	totalPages    int
	startIdx      int
	endIdx        int
	selectedTool  *apitypes.Tool
}

func catalogIndexServers(servers []apitypes.Server) (
	serverByID map[string]apitypes.Server,
	realServerIDs map[string]bool,
	realServers []apitypes.Server,
	demoServers []apitypes.Server,
) {
	serverByID = make(map[string]apitypes.Server, len(servers))
	realServerIDs = make(map[string]bool, len(servers))
	for _, s := range servers {
		serverByID[s.ID] = s
		if s.Demo {
			demoServers = append(demoServers, s)
		} else {
			realServerIDs[s.ID] = true
			realServers = append(realServers, s)
		}
	}
	return serverByID, realServerIDs, realServers, demoServers
}

func catalogCollectStatsAndActors(
	tools []apitypes.Tool,
	realServerIDs map[string]bool,
	hitCounts map[string]int,
	showDemo bool,
) (catalogGovernanceStats, map[string]bool, []catalogSelectOption) {
	var stats catalogGovernanceStats
	actorSeen := make(map[string]bool)

	for _, t := range tools {
		isReal := realServerIDs[t.ServerID]
		if isReal {
			stats.realToolCount++
			if t.Enabled {
				stats.enabledRealCount++
				if t.ReadOnly {
					stats.roEnabledCount++
				} else {
					stats.rwEnabledCount++
				}
			}
			if hitCounts[t.ID] > 0 {
				stats.hitRealToolCount++
			}
		}
		if isReal || showDemo {
			for _, a := range t.Actors {
				if a != "" && a != "*" {
					actorSeen[a] = true
				}
			}
		}
	}
	stats.closedRealCount = max(0, stats.realToolCount-stats.enabledRealCount)

	sortedActors := make([]string, 0, len(actorSeen))
	for a := range actorSeen {
		sortedActors = append(sortedActors, a)
	}
	sort.Strings(sortedActors)

	actorOptions := make([]catalogSelectOption, 0, 1+len(sortedActors))
	actorOptions = append(actorOptions, catalogSelectOption{Value: "all", Label: i18n.T("ops.all-actors")})
	for _, a := range sortedActors {
		actorOptions = append(actorOptions, catalogSelectOption{Value: a, Label: a})
	}
	return stats, actorSeen, actorOptions
}

func catalogBuildServerOptions(servers, realServers []apitypes.Server, showDemo bool) []catalogSelectOption {
	visibleServers := realServers
	if showDemo {
		visibleServers = servers
	}
	opts := make([]catalogSelectOption, 0, 1+len(visibleServers))
	opts = append(opts, catalogSelectOption{Value: "all", Label: i18n.T("ops.all-services")})
	for _, s := range visibleServers {
		lbl := s.Name
		if lbl == "" {
			lbl = s.ID
		}
		if s.Demo {
			lbl += " (DEMO)"
		}
		opts = append(opts, catalogSelectOption{Value: s.ID, Label: lbl})
	}
	return opts
}

func catalogMatchesTool(t apitypes.Tool, srv apitypes.Server, qLower string, st *catalogPageState) bool {
	if !st.showDemo && srv.Demo {
		return false
	}
	if st.serverFilter != "all" && t.ServerID != st.serverFilter {
		return false
	}
	if st.statusIdx == 1 && !t.Enabled {
		return false
	}
	if st.statusIdx == 2 && t.Enabled {
		return false
	}
	if st.accessIdx == 1 && !t.ReadOnly {
		return false
	}
	if st.accessIdx == 2 && t.ReadOnly {
		return false
	}
	if st.actorFilter != "all" && !slices.Contains(t.Actors, "*") && !slices.Contains(t.Actors, st.actorFilter) {
		return false
	}
	if qLower == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		t.ID,
		t.Name,
		t.Description,
		t.ServerID,
		srv.Name,
		strings.Join(t.Tags, " "),
		t.Method,
		t.Path,
	}, " "))
	return strings.Contains(haystack, qLower)
}

func catalogFilterAndSortTools(
	tools []apitypes.Tool,
	serverByID map[string]apitypes.Server,
	hitCounts map[string]int,
	st *catalogPageState,
) []apitypes.Tool {
	qLower := strings.ToLower(strings.TrimSpace(st.query))
	filtered := make([]apitypes.Tool, 0, len(tools))
	for _, t := range tools {
		if catalogMatchesTool(t, serverByID[t.ServerID], qLower, st) {
			filtered = append(filtered, t)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		if st.sortIdx == 0 {
			ha, hb := hitCounts[a.ID], hitCounts[b.ID]
			if ha != hb {
				return ha > hb
			}
			if a.Enabled != b.Enabled {
				return a.Enabled
			}
		}
		if nameCmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); nameCmp != 0 {
			return nameCmp < 0
		}
		return a.ID < b.ID
	})
	return filtered
}

func catalogResolveSelectedTool(
	tools, filtered []apitypes.Tool,
	serverByID map[string]apitypes.Server,
	st *catalogPageState,
) *apitypes.Tool {
	if st.panelCollapsed || len(filtered) == 0 {
		return nil
	}
	for i := range filtered {
		if filtered[i].ID == st.selectedToolID {
			return &filtered[i]
		}
	}
	// Also check full registry in case user toggled a status filter while inspecting a tool.
	for i := range tools {
		if tools[i].ID == st.selectedToolID {
			srv := serverByID[tools[i].ServerID]
			if st.showDemo || !srv.Demo {
				return &tools[i]
			}
			break
		}
	}
	st.selectedToolID = filtered[0].ID
	return &filtered[0]
}

func catalogPaginateTools(filtered []apitypes.Tool, page, pageSize int) (paged []apitypes.Tool, clampedPage, totalPages, startIdx, endIdx int) {
	if len(filtered) == 0 {
		return nil, 1, 1, 0, 0
	}
	totalPages = max(1, (len(filtered)+pageSize-1)/pageSize)
	clampedPage = max(1, min(page, totalPages))
	startIdx = (clampedPage - 1) * pageSize
	endIdx = min(len(filtered), startIdx+pageSize)
	return filtered[startIdx:endIdx], clampedPage, totalPages, startIdx, endIdx
}

func buildCatalogViewModel(
	servers []apitypes.Server,
	tools []apitypes.Tool,
	hitCounts map[string]int,
	st *catalogPageState,
) catalogViewModel {
	serverByID, realServerIDs, realServers, demoServers := catalogIndexServers(servers)

	// If showDemo was turned off while filtering on a demo server, reset serverFilter.
	if !st.showDemo && st.serverFilter != "all" {
		if srv, ok := serverByID[st.serverFilter]; !ok || srv.Demo {
			st.serverFilter = "all"
		}
	}

	stats, actorSeen, actorOptions := catalogCollectStatsAndActors(tools, realServerIDs, hitCounts, st.showDemo)
	if st.actorFilter != "all" && !actorSeen[st.actorFilter] {
		st.actorFilter = "all"
	}

	serverOptions := catalogBuildServerOptions(servers, realServers, st.showDemo)

	// Reset pagination when any filter or sort parameter changes.
	filterSig := fmt.Sprintf("%s|%s|%d|%d|%s|%d|%t",
		strings.TrimSpace(st.query), st.serverFilter, st.statusIdx, st.accessIdx, st.actorFilter, st.sortIdx, st.showDemo)
	if filterSig != st.lastFilterSig {
		st.lastFilterSig = filterSig
		st.page = 1
	}

	filtered := catalogFilterAndSortTools(tools, serverByID, hitCounts, st)
	selectedTool := catalogResolveSelectedTool(tools, filtered, serverByID, st)
	paged, clampedPage, totalPages, startIdx, endIdx := catalogPaginateTools(filtered, st.page, catalogPageSize)
	st.page = clampedPage

	return catalogViewModel{
		serverByID:    serverByID,
		realServers:   realServers,
		demoServers:   demoServers,
		stats:         stats,
		actorOptions:  actorOptions,
		serverOptions: serverOptions,
		filtered:      filtered,
		pagedTools:    paged,
		totalPages:    totalPages,
		startIdx:      startIdx,
		endIdx:        endIdx,
		selectedTool:  selectedTool,
	}
}

func (u *DesktopUI) renderCatalog(c *ui.Context) {
	if u.State == nil {
		return
	}
	st := u.catalogState()
	servers := u.State.Registry.Servers
	tools := u.State.Registry.Tools
	hitCounts := u.Telemetry.ToolHitCounts

	serverByID, _, _, _ := catalogIndexServers(servers)
	if toolID, serverID, ok := u.consumePendingCatalogTool(); ok {
		st.focusTool(toolID, serverID, serverByID)
	}
	if filterServerID, filterAccess, ok := u.consumePendingCatalogFilter(); ok {
		st.applyFilterIntent(filterServerID, filterAccess, serverByID)
	}

	vm := buildCatalogViewModel(servers, tools, hitCounts, st)

	// 1. Page Header.
	SailSectionHeader(c, i18n.T("ops.catalog-title"), "", func() {
		if SailSmallButton(c.Key("cat-nav-services"), i18n.T("ops.services")).Clicked() {
			u.Navigate(ViewServices)
		}
		if SailSmallButton(c.Key("cat-nav-playground"), i18n.T("ops.playground")).Clicked() {
			u.Navigate(ViewPlayground)
		}
	})

	// 2. Interactive Governance KPI Cards.
	renderCatalogKPIRow(c, st, vm.stats)

	// 3. Compact Filter Bar.
	renderCatalogFilterBar(c, st, vm)

	// 4. Empty State or Master-Detail Workbench.
	if len(vm.filtered) == 0 {
		renderCatalogEmptyState(c, st, len(tools), vm.stats.realToolCount)
		return
	}

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		u.renderCatalogTableCard(c, st, vm, hitCounts)
		if vm.selectedTool != nil {
			u.renderCatalogDetailPanel(c, st, *vm.selectedTool, vm.serverByID[vm.selectedTool.ServerID], hitCounts[vm.selectedTool.ID])
		}
	})
}

func renderCatalogKPIRow(c *ui.Context, st *catalogPageState, stats catalogGovernanceStats) {
	rwTone := ToneNeutral
	if stats.rwEnabledCount > 0 {
		rwTone = ToneWarning
	}
	ui.Row(c).FillWidth().Wrap().Gap(16).AlignItems(ui.Stretch).Children(func() {
		SailKPICard(
			c.Key("cat-kpi-open"),
			i18n.T("ops.enabled-over-total"),
			fmt.Sprintf("%d / %d", stats.enabledRealCount, stats.realToolCount),
			i18n.T("ops.disabled-count", stats.closedRealCount),
			ToneNeutral,
			func() {
				if st.statusIdx == 1 {
					st.statusIdx = 0
				} else {
					st.statusIdx = 1
				}
			},
		)
		SailKPICard(
			c.Key("cat-kpi-ro"),
			i18n.T("ops.read-only-tools"),
			fmt.Sprintf("%d", stats.roEnabledCount),
			"",
			ToneNeutral,
			func() {
				if st.accessIdx == 1 {
					st.accessIdx = 0
				} else {
					st.accessIdx = 1
				}
			},
		)
		SailKPICard(
			c.Key("cat-kpi-rw"),
			i18n.T("ops.write-tools"),
			fmt.Sprintf("%d", stats.rwEnabledCount),
			"",
			rwTone,
			func() {
				if st.accessIdx == 2 {
					st.accessIdx = 0
				} else {
					st.accessIdx = 2
				}
			},
		)
		SailKPICard(
			c.Key("cat-kpi-hits"),
			i18n.T("ops.recent-hits"),
			fmt.Sprintf("%d", stats.hitRealToolCount),
			"",
			ToneNeutral,
			func() {
				st.sortIdx = 0
			},
		)
	})
}

func renderCatalogFilterBar(c *ui.Context, st *catalogPageState, vm catalogViewModel) {
	SailCard(c.Key("cat-filter-card"), func() {
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				SailSearchField(c.Key("cat-search"), &st.query).
					Placeholder(i18n.T("ops.search-tools-placeholder")).
					Label(i18n.T("ops.search-catalog")).
					Width(230)

				catalogRenderSelect(c, "cat-srv-select", &st.serverFilter, vm.serverOptions, 156)

				SailSegmented(c.Key("cat-status-seg"), &st.statusIdx, i18n.T("ops.filter-all"), i18n.T("ops.filter-enabled"), i18n.T("ops.filter-disabled")).
					Label(i18n.T("ops.enabled-filter"))

				SailSegmented(c.Key("cat-access-seg"), &st.accessIdx, i18n.T("ops.filter-all"), i18n.T("ops.read-only"), i18n.T("ops.write-access")).
					Label(i18n.T("ops.access-filter"))
			})

			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				if len(vm.actorOptions) > 1 {
					catalogRenderSelect(c, "cat-actor-select", &st.actorFilter, vm.actorOptions, 124)
				}

				SailSegmented(c.Key("cat-sort-seg"), &st.sortIdx, i18n.T("ops.sort-by-hits"), i18n.T("ops.sort-by-name")).
					Label(i18n.T("ops.sort-mode"))

				if len(vm.demoServers) > 0 {
					ui.Checkbox(c.Key("cat-show-demo"), &st.showDemo, i18n.T("ops.demo-tools"))
				}

				if st.hasActiveFilters() {
					if SailButton(c.Key("cat-reset-filters"), i18n.T("ops.reset")).Clicked() {
						st.resetFilters()
					}
				}
				if st.panelCollapsed && len(vm.filtered) > 0 {
					if SailButton(c.Key("cat-reopen-panel"), i18n.T("ops.expand-details")).Clicked() {
						st.panelCollapsed = false
					}
				}
			})
		})
	})
}

func renderCatalogEmptyState(c *ui.Context, st *catalogPageState, totalTools, realToolCount int) {
	if !st.showDemo && realToolCount == 0 && totalTools > 0 {
		SailEmptyState(c, i18n.T("ops.no-real-upstream-tools"), "", i18n.T("ops.show-demo-tools"), func() {
			st.showDemo = true
		})
		return
	}
	actionLbl := ""
	var actionFn func()
	if st.hasActiveFilters() {
		actionLbl = i18n.T("ops.clear-filters")
		actionFn = st.resetFilters
	}
	SailEmptyState(c, i18n.T("ops.no-matching-tools"), "", actionLbl, actionFn)
}

type catalogToolRowProps struct {
	rowKey       string
	tool         apitypes.Tool
	srv          apitypes.Server
	hits         int
	maxDescLines int
	isSelected   bool
	isLast       bool
}

func (u *DesktopUI) renderCatalogTableCard(
	c *ui.Context,
	st *catalogPageState,
	vm catalogViewModel,
	hitCounts map[string]int,
) {
	applyCardElevation(
		ui.Column(c.Key("cat-table-card")).
			Grow(1).
			MinWidth(0).
			Radius(10).
			Background(SailBgSurface),
	).Clip().Children(func() {
		SailListCardHeader(c, i18n.T("ops.tool-list"), i18n.T("ops.items-count", len(vm.filtered)), nil)

		maxDescLines := 1
		if vm.selectedTool == nil {
			maxDescLines = 2
		}

		seenRowKeys := make(map[string]int, len(vm.pagedTools))
		for idx, tool := range vm.pagedTools {
			rowKey := tool.ID
			if rowKey == "" || seenRowKeys[rowKey] > 0 {
				rowKey = fmt.Sprintf("%s#%d", tool.ID, idx)
			}
			seenRowKeys[tool.ID]++
			u.renderCatalogToolRow(c, st, catalogToolRowProps{
				rowKey:       rowKey,
				tool:         tool,
				srv:          vm.serverByID[tool.ServerID],
				hits:         hitCounts[tool.ID],
				maxDescLines: maxDescLines,
				isSelected:   vm.selectedTool != nil && vm.selectedTool.ID == tool.ID,
				isLast:       idx == len(vm.pagedTools)-1 && vm.totalPages <= 1,
			})
		}

		if vm.totalPages > 1 {
			renderCatalogPagination(c, st, vm)
		}
	})
}

func (u *DesktopUI) renderCatalogToolRow(c *ui.Context, st *catalogPageState, p catalogToolRowProps) {
	t := p.tool
	rowBg := SailBgSurface
	if p.isSelected {
		rowBg = SailAccentSoft
	}

	row := ui.Row(c.Key("cat-row-"+p.rowKey)).
		FillWidth().
		Padding(13, 20).
		Gap(14).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Background(rowBg)
	if !p.isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	if row.Hovered() && !p.isSelected {
		row.Background(SailBgHover)
	}

	row.Children(func() {
		infoBtn := ui.ButtonBase(c.Key("cat-info-" + p.rowKey)).
			Column().
			Grow(1).
			MinWidth(0).
			Gap(4).
			AlignItems(ui.Start).
			Cursor(ui.CursorPointer)
		infoBtn.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				nameColor := SailInkStrong
				if p.isSelected {
					nameColor = SailAccent
				}
				ui.Text(c, t.Name).
					Font("monospace").
					FontSize(13).
					FontWeight(600).
					TextColor(nameColor).
					SingleLine()

				srvName := p.srv.Name
				if srvName == "" {
					srvName = t.ServerID
				}
				ui.Text(c, srvName).
					FontSize(11.5).
					TextColor(SailInkFaint).
					SingleLine()

				if !t.ReadOnly {
					SailBadge(c, i18n.T("ops.write-access"), ToneWarning)
				}
			})

			SailDescSummary(c, t.Description, i18n.T("ops.no-description"), p.maxDescLines).FillWidth()
		})
		if infoBtn.Clicked() {
			st.selectedToolID = t.ID
			st.panelCollapsed = false
		}

		ui.Row(c).Shrink(0).Gap(12).AlignItems(ui.Center).Children(func() {
			if p.hits > 0 {
				ui.Text(c, i18n.T("ops.hits-count", p.hits)).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			}

			enabledVal := t.Enabled
			if ui.Switch(c.Key("cat-sw-"+p.rowKey), &enabledVal).
				Disabled(st.togglingIDs[t.ID]).
				Label(i18n.T("ops.open-state-for", t.Name)).
				Changed() {
				u.catalogToggleToolWhitelist(st, t)
			}

			btnLabel := i18n.T("ops.details")
			if p.isSelected {
				btnLabel = i18n.T("ops.collapse")
			}
			if SailSmallButton(c.Key("cat-inspect-"+p.rowKey), btnLabel).Clicked() {
				if p.isSelected {
					st.panelCollapsed = true
				} else {
					st.selectedToolID = t.ID
					st.panelCollapsed = false
				}
			}
		})
	})
}

func renderCatalogPagination(c *ui.Context, st *catalogPageState, vm catalogViewModel) {
	ui.Row(c).
		FillWidth().
		Padding(12, 20).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Textf(c, "%d–%d / %d", vm.startIdx+1, vm.endIdx, len(vm.filtered)).
				FontSize(12).
				TextColor(SailInkMuted).
				FontFeatures("tnum")

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("ops.page-indicator", st.page, vm.totalPages)).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum")
				if SailSmallButton(c.Key("cat-prev-page"), i18n.T("ops.prev-page")).Disabled(st.page <= 1).Clicked() {
					st.page--
				}
				if SailSmallButton(c.Key("cat-next-page"), i18n.T("ops.next-page")).Disabled(st.page >= vm.totalPages).Clicked() {
					st.page++
				}
			})
		})
}

func (u *DesktopUI) renderCatalogDetailPanel(
	c *ui.Context,
	st *catalogPageState,
	tool apitypes.Tool,
	srv apitypes.Server,
	hits int,
) {
	st.edit.sync(tool)

	applyCardElevation(
		ui.Column(c.Key("cat-detail-"+tool.ID)).
			Width(420).
			Shrink(0).
			Padding(20, 22).
			Gap(16).
			Radius(10).
			Background(SailBgSurface),
	).Children(func() {
		// Top Status Row + Collapse Button (Badge Diet).
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				if tool.Enabled {
					SailQuietStatus(c, i18n.T("ops.filter-enabled"), SailSuccessInk, SailInkStrong)
				} else {
					SailQuietStatus(c, i18n.T("ops.filter-disabled"), SailInkFaint, SailInkMuted)
				}
				if tool.ReadOnly {
					ui.Text(c, i18n.T("ops.read-only")).FontSize(12).TextColor(SailInkMuted).SingleLine()
				} else {
					SailBadge(c, i18n.T("ops.write-access"), ToneWarning)
				}
				if srv.Demo {
					ui.Text(c, i18n.T("ops.demo-badge")).FontSize(12).TextColor(SailInkFaint).SingleLine()
				}
			})

			if SailSmallButton(c.Key("cat-detail-close"), i18n.T("ops.collapse")).Clicked() {
				st.panelCollapsed = true
			}
		})

		// Tool Name & Copy ID.
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, tool.Name).
				Font("monospace").
				FontSize(16).
				Bold().
				TextColor(SailInkStrong).
				Selectable()

			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, tool.ID).
					Grow(1).
					MinWidth(0).
					Font("monospace").
					FontSize(12).
					TextColor(SailInkMuted).
					Selectable().
					SingleLine()

				if SailSmallButton(c.Key("cat-copy-id-"+tool.ID), i18n.T("ops.copy-id")).Padding(2, 8).FontSize(11).Clicked() {
					c.WriteClipboard(tool.ID)
					u.Notify(i18n.T("ops.copied-tool-id", tool.ID))
				}
			})
		})

		// Borderless Quick Governance Switch + Sandbox Jump Surface.
		SailSubtleCard(c, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(10).Children(func() {
				ui.Text(c, i18n.T("ops.allowlist")).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
				enabledVal := tool.Enabled
				if ui.Switch(c.Key("cat-detail-sw-"+tool.ID), &enabledVal).
					Disabled(st.togglingIDs[tool.ID]).
					Label(i18n.T("ops.toggle-allowlist")).
					Changed() {
					u.catalogToggleToolWhitelist(st, tool)
				}
			})

			ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
				if SailSmallPrimaryButton(c.Key("cat-sandbox-jump-"+tool.ID), i18n.T("ops.sandbox-test")).Grow(1).Clicked() {
					intent := catalogBuildTestIntent(tool)
					actor := catalogPickTestActor(tool)
					u.OpenPlaygroundReplay(intent, actor)
				}
				if SailSmallButton(c.Key("cat-open-srv-"+tool.ID), i18n.T("ops.owning-service")).Clicked() {
					u.OpenServiceDetail(tool.ServerID)
				}
			})
		})

		// Detail Sub-tabs: 属性与 Schema | 编辑契约 | 完整 JSON.
		ui.Segmented(c.Key("cat-detail-tab"), &st.detailTab, i18n.T("ops.props-schema"), i18n.T("ops.edit-contract"), i18n.T("ops.raw-json")).
			FillEqually().
			Label(i18n.T("ops.tool-detail-view"))

		switch st.detailTab {
		case 1:
			u.renderCatalogEditTab(c, st, tool)
		case 2:
			SailCodeBlock(c, "TOOL CONTRACT JSON", PrettyJSON(tool), func() {
				u.Notify(i18n.T("ops.copied-contract-json"))
			})
		default:
			u.renderCatalogOverviewTab(c, tool, srv, hits)
		}
	})
}

func (u *DesktopUI) renderCatalogOverviewTab(c *ui.Context, tool apitypes.Tool, srv apitypes.Server, hits int) {
	srvDisplay := srv.Name
	if srvDisplay == "" {
		srvDisplay = tool.ServerID
	} else if srv.ID != "" && srv.Name != srv.ID {
		srvDisplay = fmt.Sprintf("%s (%s)", srv.Name, srv.ID)
	}

	accessDisplay := i18n.T("ops.access-rw")
	if tool.ReadOnly {
		accessDisplay = i18n.T("ops.access-ro")
	}

	actorsDisplay := strings.Join(tool.Actors, ", ")
	if actorsDisplay == "" {
		actorsDisplay = "—"
	}

	// Structured Description Section (prominent lead + bullets + params + collapsible raw).
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, i18n.T("ops.description")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
		SailDescriptionDetail(c, "cat-desc-"+tool.ID, tool.Description)
	})

	ui.Box(c).FillWidth().Height(1).Background(SailDivider)

	// Metadata rows (progressive disclosure of secondary fields).
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		SailKeyValueRow(c, i18n.T("ops.full-tool-id"), tool.ID, true)
		SailKeyValueRow(c, i18n.T("ops.owning-service"), srvDisplay, false)
		SailKeyValueRow(c, i18n.T("ops.access-mode"), accessDisplay, false)
		SailKeyValueRow(c, i18n.T("ops.allowed-actors"), actorsDisplay, true)
		SailKeyValueRow(c, i18n.T("ops.recent-hits"), i18n.T("ops.hits-count", hits), false)
		if tool.Method != "" || tool.Path != "" {
			SailKeyValueRow(c, i18n.T("ops.http-mapping"), strings.TrimSpace(tool.Method+" "+tool.Path), true)
		}
		if len(tool.Tags) > 0 {
			var cleanTags []string
			for _, tg := range tool.Tags {
				if v := strings.TrimSpace(tg); v != "" {
					cleanTags = append(cleanTags, "#"+v)
				}
			}
			if len(cleanTags) > 0 {
				SailKeyValueRow(c, i18n.T("ops.tags"), strings.Join(cleanTags, "  "), true)
			}
		}
	})

	// input_schema formatted JSON preview with one-click copy.
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, i18n.T("ops.input-schema")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
		schemaObj := any(tool.InputSchema)
		if len(tool.InputSchema) == 0 {
			schemaObj = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(schemaObj), func() {
			u.Notify(i18n.T("ops.copied-input-schema"))
		})
	})

	if len(tool.Example) > 0 {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, i18n.T("ops.param-example")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
			SailCodeBlock(c, "EXAMPLE", PrettyJSON(tool.Example), func() {
				u.Notify(i18n.T("ops.copied-example-args"))
			})
		})
	}
}

func (u *DesktopUI) renderCatalogEditTab(c *ui.Context, st *catalogPageState, tool apitypes.Tool) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("ops.read-only-mode")).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
			ui.Switch(c.Key("cat-edit-ro-"+tool.ID), &st.edit.readOnly).Label(i18n.T("ops.read-only-mode"))
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, i18n.T("ops.description")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("cat-edit-desc-"+tool.ID), &st.edit.desc).
				Lines(4, 8).
				FillWidth()
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, i18n.T("ops.actors-label")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("cat-edit-actors-"+tool.ID), &st.edit.actors).
				Font("monospace").
				Placeholder("developer, observer").
				FillWidth()
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, i18n.T("ops.tags-label")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("cat-edit-tags-"+tool.ID), &st.edit.tags).
				Placeholder("github, pr").
				FillWidth()
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailButton(c.Key("cat-edit-reset-"+tool.ID), i18n.T("ops.reset")).Disabled(st.edit.savingContract).Clicked() {
				st.edit.reset(tool)
			}
			saveLabel := i18n.T("ops.save-contract")
			if st.edit.savingContract {
				saveLabel = i18n.T("ops.saving")
			}
			if SailPrimaryButton(c.Key("cat-edit-save-"+tool.ID), saveLabel).Disabled(st.edit.savingContract).Clicked() {
				u.catalogSaveToolMetadata(st, tool)
			}
		})
	})
}

// catalogToggleToolWhitelist toggles a single tool's enablement via POST /api/servers/{server_id}/tools.
func (u *DesktopUI) catalogToggleToolWhitelist(st *catalogPageState, target apitypes.Tool) {
	if u.Client == nil || u.State == nil || st.togglingIDs[target.ID] {
		return
	}
	wantEnabled := !target.Enabled
	nextIDs := make([]string, 0, 16)
	for _, t := range u.State.Registry.Tools {
		if t.ServerID != target.ServerID {
			continue
		}
		if t.ID == target.ID {
			if wantEnabled {
				nextIDs = append(nextIDs, t.ID)
			}
		} else if t.Enabled {
			nextIDs = append(nextIDs, t.ID)
		}
	}

	st.togglingIDs[target.ID] = true
	toolID := target.ID
	toolName := target.Name
	serverID := target.ServerID

	u.RunAsync(func() error {
		defer u.updateUI(func() {
			delete(st.togglingIDs, toolID)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.EnableServerTools(ctx, serverID, nextIDs); err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		if wantEnabled {
			u.Notify(i18n.T("ops.tool-enabled-toast", toolName))
		} else {
			u.Notify(i18n.T("ops.tool-disabled-toast", toolName))
		}
		return nil
	})
}

func (u *DesktopUI) catalogSaveToolMetadata(st *catalogPageState, baseTool apitypes.Tool) {
	if u.Client == nil || st.edit.savingContract {
		return
	}
	updated := baseTool
	updated.Description = strings.TrimSpace(st.edit.desc)
	updated.ReadOnly = st.edit.readOnly
	updated.Actors = catalogSplitCSV(st.edit.actors)
	updated.Tags = catalogSplitCSV(st.edit.tags)
	if len(updated.Actors) == 0 {
		updated.Actors = []string{"developer"}
	}

	st.edit.savingContract = true
	u.RunAsync(func() error {
		defer u.updateUI(func() {
			st.edit.savingContract = false
		})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveTool(ctx, updated); err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		u.updateUI(func() {
			st.detailTab = 0
		})
		u.Notify(i18n.T("ops.contract-saved-toast", updated.Name))
		return nil
	})
}

func catalogSplitCSV(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，'
	})
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func catalogBuildTestIntent(tool apitypes.Tool) string {
	desc := CleanDescriptionSummary(tool.Description)
	if desc != "" {
		return i18n.T("ops.test-intent-with-desc", tool.Name, desc)
	}
	return i18n.T("ops.test-intent-call", tool.Name, tool.ID)
}

func catalogPickTestActor(tool apitypes.Tool) string {
	if len(tool.Actors) == 0 || slices.Contains(tool.Actors, "*") || slices.Contains(tool.Actors, "developer") {
		return "developer"
	}
	if slices.Contains(tool.Actors, "observer") {
		return "observer"
	}
	return tool.Actors[0]
}

func catalogRenderSelect(c *ui.Context, key string, selected *string, options []catalogSelectOption, width float32) {
	currentLabel := *selected
	for _, opt := range options {
		if opt.Value == *selected {
			currentLabel = opt.Label
			break
		}
	}
	sel := ui.SelectBase(c.Key(key), selected)
	sel.Trigger.
		Width(width).
		Height(SailControlHeightMd).
		Padding(0, 10).
		Gap(6).
		Radius(6).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Background(SailBgSurface).
		Border(1, SailBorder).
		Cursor(ui.CursorPointer)
	if sel.Trigger.Hovered() {
		sel.Trigger.Background(SailBgHover).Border(1, SailBorderStrong)
	}
	sel.Trigger.Children(func() {
		ui.Text(c, currentLabel).Grow(1).MinWidth(0).FontSize(12.5).TextColor(SailInkStrong).SingleLine()
		ui.Icon(c, catalogIconChevron).Size(13, 13).TextColor(SailInkMuted).Shrink(0)
	})
	sel.Popup(func(panel ui.Element) {
		panel.
			MinWidth(width).
			Margin(4, 0, 0, 0).
			Padding(4).
			Radius(8).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 6, 18, 0, SailFloatingShadow)
		for _, opt := range options {
			o := opt
			item := sel.Item(o.Value).Padding(6, 10).Radius(5).Cursor(ui.CursorPointer)
			txtColor := SailInkBody
			if item.Highlighted() {
				item.Background(SailAccent)
				txtColor = ui.Hex("#ffffff")
			} else if o.Value == *selected {
				item.Background(SailAccentSoft)
				txtColor = SailAccent
			}
			item.Children(func() {
				ui.Text(c, o.Label).FontSize(12.5).TextColor(txtColor).SingleLine()
			})
		}
	})
}
