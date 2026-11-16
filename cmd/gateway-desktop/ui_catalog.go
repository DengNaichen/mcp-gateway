package main

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/registry"
)

const catalogPageSize = 40

var (
	catalogIconChevron = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>`))

	catalogStatesMu sync.Mutex
	catalogStates   = make(map[*DesktopUI]*catalogPageState)
)

type catalogSelectOption struct {
	Value string
	Label string
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

	// Detail panel sub-tab & contract edit state.
	detailTab      int // 0: 属性与 Schema, 1: 编辑契约, 2: 完整 JSON
	editLoadedID   string
	editLoadedRev  string
	editDesc       string
	editReadOnly   bool
	editActors     string
	editTags       string
	savingContract bool

	togglingIDs map[string]bool
}

func catalogStateFor(u *DesktopUI) *catalogPageState {
	catalogStatesMu.Lock()
	defer catalogStatesMu.Unlock()
	st, ok := catalogStates[u]
	if !ok {
		st = &catalogPageState{
			serverFilter: "all",
			actorFilter:  "all",
			page:         1,
			togglingIDs:  make(map[string]bool),
		}
		catalogStates[u] = st
	}
	return st
}

// catalogSelectToolFromLogs allows the Logs trace inspector to jump directly to a tool in Catalog.
func catalogSelectToolFromLogs(u *DesktopUI, toolID, serverID string) {
	st := catalogStateFor(u)
	st.query = ""
	st.statusIdx = 0
	st.accessIdx = 0
	st.actorFilter = "all"
	st.page = 1
	if serverID != "" {
		st.serverFilter = serverID
		if u.State != nil {
			for _, s := range u.State.Registry.Servers {
				if s.ID == serverID && s.Demo {
					st.showDemo = true
					break
				}
			}
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

func (st *catalogPageState) syncEditForm(t registry.Tool) {
	sig := fmt.Sprintf("%s|%s|%t|%s|%s", t.Revision, t.Description, t.ReadOnly, strings.Join(t.Actors, ","), strings.Join(t.Tags, ","))
	if st.editLoadedID == t.ID && st.editLoadedRev == sig {
		return
	}
	st.editLoadedID = t.ID
	st.editLoadedRev = sig
	st.editDesc = t.Description
	st.editReadOnly = t.ReadOnly
	st.editActors = strings.Join(t.Actors, ", ")
	st.editTags = strings.Join(t.Tags, ", ")
}

func (u *DesktopUI) renderCatalog(c *ui.Context) {
	if u.State == nil {
		return
	}
	st := catalogStateFor(u)
	servers := u.State.Registry.Servers
	tools := u.State.Registry.Tools
	hitCounts := u.Telemetry.ToolHitCounts

	serverByID := make(map[string]registry.Server, len(servers))
	realServerIDs := make(map[string]bool, len(servers))
	var realServers []registry.Server
	var demoServers []registry.Server
	for _, s := range servers {
		serverByID[s.ID] = s
		if s.Demo {
			demoServers = append(demoServers, s)
		} else {
			realServerIDs[s.ID] = true
			realServers = append(realServers, s)
		}
	}

	// Consume cross-page navigation filters (e.g. from Overview rw card or Services page).
	if u.CatalogFilterServerID != "" || u.CatalogFilterAccess != "" {
		if u.CatalogFilterServerID != "" {
			st.serverFilter = u.CatalogFilterServerID
			if srv, ok := serverByID[u.CatalogFilterServerID]; ok && srv.Demo {
				st.showDemo = true
			}
			if u.CatalogFilterAccess == "" {
				st.accessIdx = 0
			}
			u.CatalogFilterServerID = ""
		}
		if u.CatalogFilterAccess != "" {
			switch u.CatalogFilterAccess {
			case "ro":
				st.accessIdx = 1
			case "rw":
				st.accessIdx = 2
			default:
				st.accessIdx = 0
			}
			u.CatalogFilterAccess = ""
		}
		st.page = 1
	}

	// If showDemo was turned off while filtering on a demo server, reset serverFilter.
	if !st.showDemo && st.serverFilter != "all" {
		if srv, ok := serverByID[st.serverFilter]; !ok || srv.Demo {
			st.serverFilter = "all"
		}
	}

	// Compute top-level governance statistics on real tools.
	var (
		realToolCount    int
		enabledRealCount int
		roEnabledCount   int
		rwEnabledCount   int
		hitRealToolCount int
	)
	actorSeen := make(map[string]bool)
	var actorOptions []catalogSelectOption
	actorOptions = append(actorOptions, catalogSelectOption{Value: "all", Label: "全部身份"})

	for _, t := range tools {
		isReal := realServerIDs[t.ServerID]
		if isReal {
			realToolCount++
			if t.Enabled {
				enabledRealCount++
				if t.ReadOnly {
					roEnabledCount++
				} else {
					rwEnabledCount++
				}
			}
			if hitCounts[t.ID] > 0 {
				hitRealToolCount++
			}
		}
		if isReal || st.showDemo {
			for _, a := range t.Actors {
				if a != "" && a != "*" && !actorSeen[a] {
					actorSeen[a] = true
				}
			}
		}
	}
	sortedActors := make([]string, 0, len(actorSeen))
	for a := range actorSeen {
		sortedActors = append(sortedActors, a)
	}
	sort.Strings(sortedActors)
	for _, a := range sortedActors {
		actorOptions = append(actorOptions, catalogSelectOption{Value: a, Label: a})
	}
	if st.actorFilter != "all" && !actorSeen[st.actorFilter] {
		st.actorFilter = "all"
	}

	// Build service dropdown options.
	srvOptions := make([]catalogSelectOption, 0, 1+len(servers))
	srvOptions = append(srvOptions, catalogSelectOption{Value: "all", Label: "全部服务"})
	visibleServers := realServers
	if st.showDemo {
		visibleServers = servers
	}
	for _, s := range visibleServers {
		lbl := s.Name
		if lbl == "" {
			lbl = s.ID
		}
		if s.Demo {
			lbl += " (DEMO)"
		}
		srvOptions = append(srvOptions, catalogSelectOption{Value: s.ID, Label: lbl})
	}

	// Reset pagination when any filter or sort parameter changes.
	filterSig := fmt.Sprintf("%s|%s|%d|%d|%s|%d|%t",
		strings.TrimSpace(st.query), st.serverFilter, st.statusIdx, st.accessIdx, st.actorFilter, st.sortIdx, st.showDemo)
	if filterSig != st.lastFilterSig {
		st.lastFilterSig = filterSig
		st.page = 1
	}

	// Filter & sort tools.
	qLower := strings.ToLower(strings.TrimSpace(st.query))
	filtered := make([]registry.Tool, 0, len(tools))
	for _, t := range tools {
		srv := serverByID[t.ServerID]
		if !st.showDemo && srv.Demo {
			continue
		}
		if st.serverFilter != "all" && t.ServerID != st.serverFilter {
			continue
		}
		if st.statusIdx == 1 && !t.Enabled {
			continue
		}
		if st.statusIdx == 2 && t.Enabled {
			continue
		}
		if st.accessIdx == 1 && !t.ReadOnly {
			continue
		}
		if st.accessIdx == 2 && t.ReadOnly {
			continue
		}
		if st.actorFilter != "all" && !slices.Contains(t.Actors, "*") && !slices.Contains(t.Actors, st.actorFilter) {
			continue
		}
		if qLower != "" {
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
			if !strings.Contains(haystack, qLower) {
				continue
			}
		}
		filtered = append(filtered, t)
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
		nameCmp := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		if nameCmp != 0 {
			return nameCmp < 0
		}
		return a.ID < b.ID
	})

	// Resolve selected tool for right-side inspector panel.
	var selectedTool *registry.Tool
	if !st.panelCollapsed && len(filtered) > 0 {
		for i := range filtered {
			if filtered[i].ID == st.selectedToolID {
				selectedTool = &filtered[i]
				break
			}
		}
		if selectedTool == nil {
			// Also check full registry in case user toggled a status filter while inspecting a tool.
			for i := range tools {
				if tools[i].ID == st.selectedToolID {
					srv := serverByID[tools[i].ServerID]
					if st.showDemo || !srv.Demo {
						selectedTool = &tools[i]
					}
					break
				}
			}
		}
		if selectedTool == nil {
			st.selectedToolID = filtered[0].ID
			selectedTool = &filtered[0]
		}
	}

	// 1. Page Header.
	SailSectionHeader(
		c,
		"工具目录",
		"",
		func() {
			if SailSmallButton(c.Key("cat-nav-services"), "上游服务").Clicked() {
				u.Navigate(ViewServices)
			}
			if SailSmallButton(c.Key("cat-nav-playground"), "路由沙盒").Clicked() {
				u.Navigate(ViewPlayground)
			}
		},
	)

	// 2. Interactive Governance KPI Cards.
	closedRealCount := realToolCount - enabledRealCount
	if closedRealCount < 0 {
		closedRealCount = 0
	}
	rwTone := ToneNeutral
	if rwEnabledCount > 0 {
		rwTone = ToneWarning
	}
	ui.Row(c).FillWidth().Wrap().Gap(16).Children(func() {
		SailKPICard(
			c.Key("cat-kpi-open"),
			"已开放 / 总数",
			fmt.Sprintf("%d / %d", enabledRealCount, realToolCount),
			fmt.Sprintf("已关闭 %d", closedRealCount),
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
			"只读工具",
			fmt.Sprintf("%d", roEnabledCount),
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
			"写操作工具",
			fmt.Sprintf("%d", rwEnabledCount),
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
			"近期命中",
			fmt.Sprintf("%d", hitRealToolCount),
			"",
			ToneNeutral,
			func() {
				st.sortIdx = 0
			},
		)
	})

	// 3. Compact Filter Bar.
	SailCard(c.Key("cat-filter-card"), func() {
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.SearchField(c.Key("cat-search"), &st.query).
					Placeholder("搜索名称、ID 或描述…").
					Label("搜索工具目录").
					Width(230)

				catalogRenderSelect(c, "cat-srv-select", &st.serverFilter, srvOptions, 156)

				ui.Segmented(c.Key("cat-status-seg"), &st.statusIdx, "全部", "已开放", "已关闭").
					Label("开放状态筛选")

				ui.Segmented(c.Key("cat-access-seg"), &st.accessIdx, "全部", "只读", "写操作").
					Label("读写属性筛选")
			})

			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				if len(actorOptions) > 1 {
					catalogRenderSelect(c, "cat-actor-select", &st.actorFilter, actorOptions, 124)
				}

				ui.Segmented(c.Key("cat-sort-seg"), &st.sortIdx, "按命中", "名称 A–Z").
					Label("排序方式")

				if len(demoServers) > 0 {
					ui.Checkbox(c.Key("cat-show-demo"), &st.showDemo, "演示工具")
				}

				if st.hasActiveFilters() {
					if SailSmallButton(c.Key("cat-reset-filters"), "重置").Clicked() {
						st.resetFilters()
					}
				}
				if st.panelCollapsed && len(filtered) > 0 {
					if SailSmallButton(c.Key("cat-reopen-panel"), "展开详情").Clicked() {
						st.panelCollapsed = false
					}
				}
			})
		})
	})

	// 4. Empty State or Master-Detail Workbench.
	if len(filtered) == 0 {
		if !st.showDemo && realToolCount == 0 && len(tools) > 0 {
			SailEmptyState(
				c,
				"暂无真实上游工具",
				"",
				"显示演示工具",
				func() {
					st.showDemo = true
				},
			)
		} else {
			actionLbl := ""
			var actionFn func()
			if st.hasActiveFilters() {
				actionLbl = "清空筛选"
				actionFn = st.resetFilters
			}
			SailEmptyState(
				c,
				"无匹配工具",
				"",
				actionLbl,
				actionFn,
			)
		}
		return
	}

	totalPages := (len(filtered) + catalogPageSize - 1) / catalogPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if st.page < 1 {
		st.page = 1
	}
	if st.page > totalPages {
		st.page = totalPages
	}
	startIdx := (st.page - 1) * catalogPageSize
	endIdx := startIdx + catalogPageSize
	if endIdx > len(filtered) {
		endIdx = len(filtered)
	}
	pagedTools := filtered[startIdx:endIdx]

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		// Left Column: Hairline-divided Tool List Card (max 3 anchors per row).
		ui.Column(c.Key("cat-table-card")).
			Grow(1).
			MinWidth(0).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 1, 2, 0, SailCardShadow).
			Clip().
			Children(func() {
				SailListCardHeader(c, "工具列表", fmt.Sprintf("%d 项", len(filtered)), nil)

				maxDescLines := 1
				if selectedTool == nil {
					maxDescLines = 2
				}

				seenRowKeys := make(map[string]int, len(pagedTools))
				for idx, tool := range pagedTools {
					t := tool
					rowKey := t.ID
					if rowKey == "" || seenRowKeys[rowKey] > 0 {
						rowKey = fmt.Sprintf("%s#%d", t.ID, idx)
					}
					seenRowKeys[t.ID]++
					srv := serverByID[t.ServerID]
					hits := hitCounts[t.ID]
					isSelected := selectedTool != nil && selectedTool.ID == t.ID
					isLast := idx == len(pagedTools)-1 && totalPages <= 1

					rowBg := SailBgCanvas
					if isSelected {
						rowBg = SailAccentSoft
					}

					row := ui.Row(c.Key("cat-row-"+rowKey)).
						FillWidth().
						Padding(13, 20).
						Gap(14).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Background(rowBg)
					if !isLast {
						row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
					}
					if row.Hovered() && !isSelected {
						row.Background(SailBgHover)
					}

					row.Children(func() {
						// Anchor 1 & 2: Primary Identity + Cleaned 1–2 line Summary.
						infoBtn := ui.ButtonBase(c.Key("cat-info-" + rowKey)).
							Column().
							Grow(1).
							MinWidth(0).
							Gap(4).
							AlignItems(ui.Start).
							Cursor(ui.CursorPointer)
						infoBtn.Children(func() {
							ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
								nameColor := SailInkStrong
								if isSelected {
									nameColor = SailAccent
								}
								ui.Text(c, t.Name).
									Font("monospace").
									FontSize(13).
									FontWeight(600).
									TextColor(nameColor).
									SingleLine()

								srvName := srv.Name
								if srvName == "" {
									srvName = t.ServerID
								}
								ui.Text(c, srvName).
									FontSize(11.5).
									TextColor(SailInkFaint).
									SingleLine()

								if !t.ReadOnly {
									SailBadge(c, "写操作", ToneWarning)
								}
							})

							SailDescSummary(c, t.Description, "暂无描述", maxDescLines).FillWidth()
						})
						if infoBtn.Clicked() {
							st.selectedToolID = t.ID
							st.panelCollapsed = false
						}

						// Anchor 3: Quiet hit count + Enable Switch + Detail toggle.
						ui.Row(c).Shrink(0).Gap(12).AlignItems(ui.Center).Children(func() {
							if hits > 0 {
								ui.Textf(c, "%d 次", hits).
									FontSize(12).
									TextColor(SailInkMuted).
									FontFeatures("tnum").
									SingleLine()
							}

							enabledVal := t.Enabled
							busy := st.togglingIDs[t.ID]
							if ui.Switch(c.Key("cat-sw-"+rowKey), &enabledVal).
								Disabled(busy).
								Label(t.Name + " 开放状态").
								Changed() {
								u.catalogToggleToolWhitelist(st, t)
							}

							btnLabel := "详情"
							if isSelected {
								btnLabel = "收起"
							}
							if SailSmallButton(c.Key("cat-inspect-"+rowKey), btnLabel).Clicked() {
								if isSelected {
									st.panelCollapsed = true
								} else {
									st.selectedToolID = t.ID
									st.panelCollapsed = false
								}
							}
						})
					})
				}

				// Pagination Footer (shown only when multi-page).
				if totalPages > 1 {
					ui.Row(c).
						FillWidth().
						Padding(12, 20).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Children(func() {
							ui.Textf(c, "%d–%d / %d", startIdx+1, endIdx, len(filtered)).
								FontSize(12).
								TextColor(SailInkMuted).
								FontFeatures("tnum")

							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Textf(c, "%d / %d 页", st.page, totalPages).
									FontSize(12).
									TextColor(SailInkMuted).
									FontFeatures("tnum")
								if SailSmallButton(c.Key("cat-prev-page"), "上一页").Disabled(st.page <= 1).Clicked() {
									st.page--
								}
								if SailSmallButton(c.Key("cat-next-page"), "下一页").Disabled(st.page >= totalPages).Clicked() {
									st.page++
								}
							})
						})
				}
			})

		// Right Column: Tool Detail Panel / Inspector.
		if selectedTool != nil {
			u.renderCatalogDetailPanel(c, st, *selectedTool, serverByID[selectedTool.ServerID], hitCounts[selectedTool.ID])
		}
	})
}

func (u *DesktopUI) renderCatalogDetailPanel(
	c *ui.Context,
	st *catalogPageState,
	tool registry.Tool,
	srv registry.Server,
	hits int,
) {
	st.syncEditForm(tool)

	ui.Column(c.Key("cat-detail-"+tool.ID)).
		Width(420).
		Shrink(0).
		Padding(20, 22).
		Gap(16).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Children(func() {
			// Top Status Row + Collapse Button (Badge Diet).
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
					if tool.Enabled {
						SailQuietStatus(c, "已开放", SailSuccessInk, SailInkStrong)
					} else {
						SailQuietStatus(c, "已关闭", SailInkFaint, SailInkMuted)
					}
					if tool.ReadOnly {
						ui.Text(c, "只读").FontSize(12).TextColor(SailInkMuted).SingleLine()
					} else {
						SailBadge(c, "写操作", ToneWarning)
					}
					if srv.Demo {
						ui.Text(c, "演示").FontSize(12).TextColor(SailInkFaint).SingleLine()
					}
				})

				if SailSmallButton(c.Key("cat-detail-close"), "收起").Clicked() {
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

					if SailSmallButton(c.Key("cat-copy-id-"+tool.ID), "复制 ID").Padding(2, 8).FontSize(11).Clicked() {
						c.WriteClipboard(tool.ID)
						u.Notify("已复制工具 ID: " + tool.ID)
					}
				})
			})

			// Borderless Quick Governance Switch + Sandbox Jump Surface.
			SailSubtleCard(c, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(10).Children(func() {
					ui.Text(c, "开放白名单").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
					enabledVal := tool.Enabled
					if ui.Switch(c.Key("cat-detail-sw-"+tool.ID), &enabledVal).
						Disabled(st.togglingIDs[tool.ID]).
						Label("切换工具启用白名单").
						Changed() {
						u.catalogToggleToolWhitelist(st, tool)
					}
				})

				ui.Row(c).FillWidth().Wrap().Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.PrimaryButton(c.Key("cat-sandbox-jump-"+tool.ID), "沙盒测试").Grow(1).Clicked() {
						intent := catalogBuildTestIntent(tool)
						actor := catalogPickTestActor(tool)
						u.OpenPlaygroundReplay(intent, actor)
					}
					if SailSmallButton(c.Key("cat-open-srv-"+tool.ID), "所属服务").Clicked() {
						u.OpenServiceDetail(tool.ServerID)
					}
				})
			})

			// Detail Sub-tabs: 属性与 Schema | 编辑契约 | 完整 JSON.
			ui.Segmented(c.Key("cat-detail-tab"), &st.detailTab, "属性与 Schema", "编辑契约", "完整 JSON").
				FillEqually().
				Label("工具详情视图")

			switch st.detailTab {
			case 1:
				u.renderCatalogEditTab(c, st, tool)
			case 2:
				SailCodeBlock(c, "TOOL CONTRACT JSON", PrettyJSON(tool), func() {
					u.Notify("已复制完整工具契约 JSON")
				})
			default:
				u.renderCatalogOverviewTab(c, tool, srv, hits)
			}
		})
}

func (u *DesktopUI) renderCatalogOverviewTab(c *ui.Context, tool registry.Tool, srv registry.Server, hits int) {
	srvDisplay := srv.Name
	if srvDisplay == "" {
		srvDisplay = tool.ServerID
	} else if srv.ID != "" && srv.Name != srv.ID {
		srvDisplay = fmt.Sprintf("%s (%s)", srv.Name, srv.ID)
	}

	accessDisplay := "写操作 (rw)"
	if tool.ReadOnly {
		accessDisplay = "只读 (ro)"
	}

	actorsDisplay := strings.Join(tool.Actors, ", ")
	if actorsDisplay == "" {
		actorsDisplay = "—"
	}

	// Structured Description Section (prominent lead + bullets + params + collapsible raw).
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Text(c, "功能描述").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
		SailDescriptionDetail(c, "cat-desc-"+tool.ID, tool.Description)
	})

	ui.Box(c).FillWidth().Height(1).Background(SailDivider)

	// Metadata rows (progressive disclosure of secondary fields).
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		SailKeyValueRow(c, "工具完整 ID", tool.ID, true)
		SailKeyValueRow(c, "所属服务", srvDisplay, false)
		SailKeyValueRow(c, "读写属性", accessDisplay, false)
		SailKeyValueRow(c, "允许身份", actorsDisplay, true)
		SailKeyValueRow(c, "近期命中", fmt.Sprintf("%d 次", hits), false)
		if tool.Method != "" || tool.Path != "" {
			SailKeyValueRow(c, "HTTP 映射", strings.TrimSpace(tool.Method+" "+tool.Path), true)
		}
		if len(tool.Tags) > 0 {
			var cleanTags []string
			for _, tg := range tool.Tags {
				if v := strings.TrimSpace(tg); v != "" {
					cleanTags = append(cleanTags, "#"+v)
				}
			}
			if len(cleanTags) > 0 {
				SailKeyValueRow(c, "分类标签", strings.Join(cleanTags, "  "), true)
			}
		}
	})

	// input_schema formatted JSON preview with one-click copy.
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, "输入参数 Schema").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
		schemaObj := any(tool.InputSchema)
		if len(tool.InputSchema) == 0 {
			schemaObj = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(schemaObj), func() {
			u.Notify("已复制 input_schema JSON")
		})
	})

	if len(tool.Example) > 0 {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, "参数示例").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
			SailCodeBlock(c, "EXAMPLE", PrettyJSON(tool.Example), func() {
				u.Notify("已复制示例参数 JSON")
			})
		})
	}
}

func (u *DesktopUI) renderCatalogEditTab(c *ui.Context, st *catalogPageState, tool registry.Tool) {
	ui.Column(c).FillWidth().Gap(12).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "只读模式").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
			ui.Switch(c.Key("cat-edit-ro-"+tool.ID), &st.editReadOnly).Label("只读模式")
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, "功能描述").FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("cat-edit-desc-"+tool.ID), &st.editDesc).
				Lines(4, 8).
				FillWidth()
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, "允许身份 (Actors)").FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("cat-edit-actors-"+tool.ID), &st.editActors).
				Font("monospace").
				Placeholder("developer, observer").
				FillWidth()
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, "分类标签 (Tags)").FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("cat-edit-tags-"+tool.ID), &st.editTags).
				Placeholder("github, pr").
				FillWidth()
		})

		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("cat-edit-reset-"+tool.ID), "重置").Disabled(st.savingContract).Clicked() {
				st.editLoadedID = ""
				st.syncEditForm(tool)
			}
			saveLabel := "保存契约"
			if st.savingContract {
				saveLabel = "保存中…"
			}
			if ui.PrimaryButton(c.Key("cat-edit-save-"+tool.ID), saveLabel).Disabled(st.savingContract).Clicked() {
				u.catalogSaveToolMetadata(st, tool)
			}
		})
	})
}

// catalogToggleToolWhitelist toggles a single tool's enablement via POST /api/servers/{server_id}/tools.
func (u *DesktopUI) catalogToggleToolWhitelist(st *catalogPageState, target registry.Tool) {
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
			u.Notify("已开放工具: " + toolName)
		} else {
			u.Notify("已关闭工具: " + toolName)
		}
		return nil
	})
}

func (u *DesktopUI) catalogSaveToolMetadata(st *catalogPageState, baseTool registry.Tool) {
	if u.Client == nil || st.savingContract {
		return
	}
	updated := baseTool
	updated.Description = strings.TrimSpace(st.editDesc)
	updated.ReadOnly = st.editReadOnly
	updated.Actors = catalogSplitCSV(st.editActors)
	updated.Tags = catalogSplitCSV(st.editTags)
	if len(updated.Actors) == 0 {
		updated.Actors = []string{"developer"}
	}

	st.savingContract = true
	u.RunAsync(func() error {
		defer u.updateUI(func() {
			st.savingContract = false
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
		u.Notify("工具契约已保存: " + updated.Name)
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

func catalogBuildTestIntent(tool registry.Tool) string {
	desc := CleanDescriptionSummary(tool.Description)
	if desc != "" {
		return fmt.Sprintf("使用 %s：%s", tool.Name, desc)
	}
	return fmt.Sprintf("调用 %s (%s)", tool.Name, tool.ID)
}

func catalogPickTestActor(tool registry.Tool) string {
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
		Padding(6, 10).
		Gap(6).
		Radius(6).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Background(SailBgCanvas).
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
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 6, 18, 0, SailCardShadow)
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
