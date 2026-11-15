package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/gateway"

	"github.com/egoist/mygo/ui"
)

const servicesToolPageSize = 30

var (
	servicesStateMu  sync.Mutex
	servicesStateMap = map[*DesktopUI]*servicesPageState{}
	nonServiceIDRe   = regexp.MustCompile(`[^a-z0-9._-]+`)
)

type servicesPageState struct {
	// List view state
	listQuery       string
	listProtoFilter int // 0=全部, 1=MCP Stdio, 2=MCP HTTP, 3=HTTP REST
	demoExpanded    bool
	syncingServerID string
	toggleBusyID    string

	// Add / Import Service modal
	showAddModal        bool
	addTab              int // 0=MCP Stdio, 1=MCP HTTP/SSE, 2=OpenAPI 导入, 3=粘贴 JSON
	addBusy             bool
	addError            string
	addID               string
	addName             string
	addDescription      string
	addCommand          string
	addArgsText         string
	addEnvText          string
	addWorkDir          string
	addEndpoint         string
	addRemoteProto      string // "MCP Streamable HTTP" or "MCP SSE (Streamable 兼容)"
	addHeadersText      string
	addOpenAPIBaseURL   string
	addOpenAPISpecURL   string
	addOpenAPISpecText  string
	addOpenAPIOpsText   string
	addOpenAPIHeaders   string
	addPasteText        string

	// Detail view state
	detailServerID    string
	detailSubtab      int // 0=工具白名单, 1=连接与认证配置, 2=健康与维护概览
	toolQuery         string
	toolFilter        int // 0=全部, 1=已启用, 2=未启用, 3=仅只读
	toolPage          int
	toolBatchBusy     bool
	confirmDeactivate bool
	deactivateBusy    bool

	// Tool edit modal
	showToolModal     bool
	toolEditTab       int // 0=属性与 Schema, 1=完整 JSON
	editingTool       *gateway.Tool
	toolEditDesc      string
	toolEditActors    string
	toolEditEnabled   bool
	toolEditReadOnly  bool
	toolEditRawJSON   string
	toolEditBusy      bool
	toolEditError     string

	// Add HTTP tool modal
	showAddHTTPTool  bool
	httpToolName     string
	httpToolMethod   string
	httpToolPath     string
	httpToolDesc     string
	httpToolReadOnly bool
	httpToolSchema   string
	httpToolBusy     bool
	httpToolError    string

	// Connection edit form
	connHydratedID       string
	connMode             int // 0=表单视图, 1=JSON 定义
	connName             string
	connDesc             string
	connEndpoint         string
	connCommand          string
	connWorkDir          string
	connArgsText         string
	connEnvText          string
	connHeadersText      string
	connRedactEmails     bool
	connRedactFieldsText string
	connRawJSON          string
	connBusy             bool
	connError            string

	// Maintenance & health subtab
	maintLoading          bool
	maintError            string
	maintBusy             bool
	maintOverview         *MaintenanceOverview
	maintHydratedServerID string
	maintFormatIdx        int // 0=原生/OpenAPI, 1=Pi 编程适配
	maintFilesText        string
	maintURLsText         string
	maintOpsText          string
	maintInstructions     string
	maintIntervalStr      string
	maintTimeoutStr       string
	maintEnabled          bool
	maintAutoPublish      bool
	selectedRunID         string
}

func getServicesState(u *DesktopUI) *servicesPageState {
	servicesStateMu.Lock()
	defer servicesStateMu.Unlock()
	st, ok := servicesStateMap[u]
	if !ok {
		st = &servicesPageState{
			addArgsText:       "[]",
			addEnvText:        "{}",
			addHeadersText:    "{}",
			addOpenAPIHeaders: "{}",
			addRemoteProto:    "MCP Streamable HTTP",
			httpToolMethod:    "GET",
			httpToolReadOnly:  true,
			httpToolSchema:    "{\n  \"type\": \"object\",\n  \"properties\": {},\n  \"required\": [],\n  \"additionalProperties\": false\n}",
			maintIntervalStr:  "60",
			maintTimeoutStr:   "900",
			maintAutoPublish:  true,
		}
		servicesStateMap[u] = st
	}
	return st
}

func (st *servicesPageState) openAddModal(tab int) {
	st.showAddModal = true
	st.addTab = tab
	st.addError = ""
	if st.addArgsText == "" {
		st.addArgsText = "[]"
	}
	if st.addEnvText == "" {
		st.addEnvText = "{}"
	}
	if st.addHeadersText == "" {
		st.addHeadersText = "{}"
	}
	if st.addOpenAPIHeaders == "" {
		st.addOpenAPIHeaders = "{}"
	}
	if st.addRemoteProto == "" {
		st.addRemoteProto = "MCP Streamable HTTP"
	}
}

func (st *servicesPageState) resetAddFields() {
	st.addID = ""
	st.addName = ""
	st.addDescription = ""
	st.addCommand = ""
	st.addArgsText = "[]"
	st.addEnvText = "{}"
	st.addWorkDir = ""
	st.addEndpoint = ""
	st.addRemoteProto = "MCP Streamable HTTP"
	st.addHeadersText = "{}"
	st.addOpenAPIBaseURL = ""
	st.addOpenAPISpecURL = ""
	st.addOpenAPISpecText = ""
	st.addOpenAPIOpsText = ""
	st.addOpenAPIHeaders = "{}"
	st.addPasteText = ""
	st.addError = ""
}

func serviceProtocolTag(s gateway.Server) string {
	if s.Kind == "http" {
		return "HTTP REST"
	}
	switch strings.ToLower(strings.TrimSpace(s.Transport)) {
	case "stdio":
		return "MCP Stdio"
	case "sse":
		return "MCP SSE"
	default:
		return "MCP Streamable HTTP"
	}
}

func serviceProtocolTone(s gateway.Server) BadgeTone {
	if s.Kind == "http" {
		return ToneInfo
	}
	if strings.EqualFold(s.Transport, "stdio") {
		return ToneAccent
	}
	return ToneNeutral
}

func slugServiceID(name, fallbackPrefix string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = nonServiceIDRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-._")
	if s == "" {
		return fmt.Sprintf("%s-%d", fallbackPrefix, time.Now().Unix()%100000)
	}
	return s
}

func (u *DesktopUI) renderServices(c *ui.Context) {
	st := getServicesState(u)

	// Consume cross-page trigger to open Add Service modal.
	if u.OpenAddService {
		u.OpenAddService = false
		st.openAddModal(0)
	}

	var selectedServer *gateway.Server
	if u.SelectedServerID != "" {
		for i := range u.State.Registry.Servers {
			if u.State.Registry.Servers[i].ID == u.SelectedServerID {
				selectedServer = &u.State.Registry.Servers[i]
				break
			}
		}
		if selectedServer == nil {
			u.SelectedServerID = ""
		}
	}

	if selectedServer != nil {
		u.renderServiceDetail(c, st, *selectedServer)
	} else {
		u.renderServiceList(c, st)
	}

	if st.showAddModal {
		u.renderAddServiceModal(c, st)
	}
	if st.showToolModal && st.editingTool != nil {
		u.renderToolEditModal(c, st)
	}
	if st.showAddHTTPTool && selectedServer != nil {
		u.renderAddHTTPToolModal(c, st, *selectedServer)
	}
}

// --- Service List View ---

func (u *DesktopUI) renderServiceList(c *ui.Context, st *servicesPageState) {
	servers := u.State.Registry.Servers
	tools := u.State.Registry.Tools

	var realServers []gateway.Server
	var demoServers []gateway.Server
	onlineCount := 0
	for _, s := range servers {
		if s.Demo {
			demoServers = append(demoServers, s)
			continue
		}
		realServers = append(realServers, s)
		if s.Enabled {
			onlineCount++
		}
	}

	toolsByServer := map[string][]gateway.Tool{}
	for _, t := range tools {
		toolsByServer[t.ServerID] = append(toolsByServer[t.ServerID], t)
	}

	SailSectionHeader(
		c,
		"上游服务",
		"管理下游 MCP 与 HTTP REST 服务连接、工具白名单、OpenAPI 导入与健康维护。",
		func() {
			if SailSmallButton(c.Key("svc-btn-paste"), "粘贴 MCP JSON").Clicked() {
				st.openAddModal(3)
			}
			if SailSmallButton(c.Key("svc-btn-openapi"), "OpenAPI 导入").Clicked() {
				st.openAddModal(2)
			}
			if ui.PrimaryButton(c.Key("svc-btn-add"), "添加上游服务").Padding(6, 13).FontSize(12.5).Clicked() {
				st.openAddModal(0)
			}
		},
	)

	// Filter & Search toolbar card
	SailCard(c, func() {
		SailCardTitle(
			c,
			"已接入上游服务",
			fmt.Sprintf("%d / %d 在线启用 · 共 %d 个服务", onlineCount, len(realServers), len(realServers)),
			func() {
				ui.Segmented(c.Key("svc-proto-filter"), &st.listProtoFilter, "全部", "MCP Stdio", "MCP HTTP", "HTTP REST")
			},
		)

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.SearchField(c.Key("svc-list-search"), &st.listQuery).
				Placeholder("按服务名称、ID、描述、命令或 URL 搜索…").
				Grow(1)
			if strings.TrimSpace(st.listQuery) != "" || st.listProtoFilter != 0 {
				if SailSmallButton(c.Key("svc-filter-reset"), "重置筛选").Clicked() {
					st.listQuery = ""
					st.listProtoFilter = 0
				}
			}
		})

		if len(realServers) == 0 {
			SailEmptyState(
				c,
				"尚未接入任何上游服务",
				"支持接入本地 MCP Stdio 进程、远程 MCP Streamable HTTP / SSE 服务，或一键导入 OpenAPI / Swagger 规范。",
				"添加第一个服务",
				func() { st.openAddModal(0) },
			)
			return
		}

		q := strings.ToLower(strings.TrimSpace(st.listQuery))
		var filtered []gateway.Server
		for _, s := range realServers {
			switch st.listProtoFilter {
			case 1:
				if !(s.Kind == "mcp" && s.Transport == "stdio") {
					continue
				}
			case 2:
				if !(s.Kind == "mcp" && s.Transport != "stdio") {
					continue
				}
			case 3:
				if s.Kind != "http" {
					continue
				}
			}
			if q != "" {
				hay := strings.ToLower(s.Name + " " + s.ID + " " + s.Description + " " + ServerTargetText(s))
				if !strings.Contains(hay, q) {
					continue
				}
			}
			filtered = append(filtered, s)
		}

		if len(filtered) == 0 {
			SailEmptyState(
				c,
				"没有匹配筛选条件的服务",
				"尝试清空搜索词或切换协议筛选标签。",
				"显示全部服务",
				func() {
					st.listQuery = ""
					st.listProtoFilter = 0
				},
			)
			return
		}

		ui.Column(c).FillWidth().Gap(10).Children(func() {
			for _, srv := range filtered {
				s := srv
				srvTools := toolsByServer[s.ID]
				onCount, roCount := 0, 0
				for _, t := range srvTools {
					if t.Enabled {
						onCount++
						if t.ReadOnly {
							roCount++
						}
					}
				}
				rwCount := onCount - roCount
				check, hasCheck := u.State.Setup.SourceChecks[s.ID]

				card := ui.Column(c.Key("svc-card-"+s.ID)).
					FillWidth().
					Padding(14, 16).
					Gap(10).
					Radius(7).
					Background(SailBgCanvas).
					Border(1, SailBorder)
				if card.Hovered() {
					card.Background(SailBgSubtle).Border(1, SailBorderStrong)
				}
				card.Children(func() {
					ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
						// Left: Title, ID, Protocol badge, Status badge
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
								nameBtn := ui.ButtonBase(c.Key("svc-open-"+s.ID)).Cursor(ui.CursorPointer)
								nameBtn.Children(func() {
									ui.Text(c, s.Name).FontSize(14.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
								})
								if nameBtn.Clicked() {
									st.enterDetail(s.ID)
									u.OpenServiceDetail(s.ID)
								}

								ui.Text(c, s.ID).Font("monospace").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
								SailBadge(c, serviceProtocolTag(s), serviceProtocolTone(s))

								if s.Enabled {
									SailBadgeDot(c, "已启用", ToneSuccess)
								} else {
									SailBadgeDot(c, "已停用", ToneNeutral)
								}

								if hasCheck && check.Status == "error" {
									SailBadgeDot(c, "连接异常", ToneDanger)
								} else if hasCheck && check.Status == "success" && !check.Stale {
									SailBadge(c, "健康", ToneSuccess)
								}
							})

							if s.Description != "" {
								ui.Text(c, s.Description).FontSize(12.5).TextColor(SailInkMuted)
							}

							ui.Row(c).Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
								ui.Text(c, ServerTargetText(s)).
									Font("monospace").
									FontSize(12).
									TextColor(SailInkMuted).
									SingleLine().
									MaxWidth(520)

								ui.Text(c, fmt.Sprintf("开放工具 %d / %d", onCount, len(srvTools))).
									FontSize(12).
									FontWeight(600).
									FontFeatures("tnum").
									TextColor(SailInkBody).
									SingleLine()

								ui.Text(c, fmt.Sprintf("(%d 只读 · %d 写)", roCount, rwCount)).
									FontSize(11.5).
									FontFeatures("tnum").
									TextColor(SailInkMuted).
									SingleLine()
							})
						})

						// Right: Switch + Sync + Catalog + Detail
						ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
							enabledVal := s.Enabled
							sw := ui.Switch(c.Key("svc-sw-"+s.ID), &enabledVal).
								Label(s.Name + " 启用开关").
								Disabled(st.toggleBusyID == s.ID)
							if sw.Changed() {
								u.toggleServerEnabled(st, s, enabledVal)
							}

							if s.Kind == "mcp" {
								syncLabel := "同步工具"
								if st.syncingServerID == s.ID {
									syncLabel = "同步中…"
								}
								if SailSmallButton(c.Key("svc-sync-"+s.ID), syncLabel).
									Disabled(st.syncingServerID != "").
									Clicked() {
									u.syncServerTools(st, s)
								}
							}

							if SailSmallButton(c.Key("svc-cat-"+s.ID), "工具目录").Clicked() {
								u.OpenCatalogFilter(s.ID, "")
							}

							if ui.PrimaryButton(c.Key("svc-detail-"+s.ID), "管理详情").
								Padding(5, 11).
								FontSize(12.5).
								Clicked() {
								st.enterDetail(s.ID)
								u.OpenServiceDetail(s.ID)
							}
						})
					})
				})
			}
		})
	})

	// Built-in Demo Services collapsible
	if len(demoServers) > 0 {
		SailCard(c, func() {
			ui.Collapsible(
				c.Key("svc-demo-collapsible"),
				fmt.Sprintf("内置演示服务（%d 个 · 仅在路由沙盒勾选包含演示工具时参与路由）", len(demoServers)),
				&st.demoExpanded,
				func() {
					ui.Column(c).FillWidth().Gap(8).Padding(6, 0, 0, 0).Children(func() {
						for _, dsrv := range demoServers {
							s := dsrv
							srvTools := toolsByServer[s.ID]
							ui.Row(c.Key("demo-row-"+s.ID)).
								FillWidth().
								Padding(10, 12).
								Radius(6).
								Background(SailBgSubtle).
								Border(1, SailBorder).
								AlignItems(ui.Center).
								Justify(ui.SpaceBetween).
								Gap(12).
								Children(func() {
									ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
										ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
											ui.Text(c, s.Name).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong)
											SailBadge(c, "DEMO", ToneNeutral)
											SailBadge(c, serviceProtocolTag(s), serviceProtocolTone(s))
										})
										if s.Description != "" {
											ui.Text(c, s.Description).FontSize(12).TextColor(SailInkMuted)
										}
										ui.Text(c, fmt.Sprintf("%s · %d 个工具", ServerTargetText(s), len(srvTools))).
											Font("monospace").
											FontSize(11.5).
											TextColor(SailInkMuted).
											SingleLine()
									})
									if SailSmallButton(c.Key("demo-view-"+s.ID), "查看工具").Clicked() {
										st.enterDetail(s.ID)
										u.OpenServiceDetail(s.ID)
									}
								})
						}
					})
				},
			)
		})
	}
}

func (st *servicesPageState) enterDetail(serverID string) {
	if st.detailServerID != serverID {
		st.detailServerID = serverID
		st.detailSubtab = 0
		st.toolQuery = ""
		st.toolFilter = 0
		st.toolPage = 1
		st.confirmDeactivate = false
		st.connHydratedID = ""
		st.maintHydratedServerID = ""
		st.maintError = ""
		st.selectedRunID = ""
	}
}

// --- Service Detail View ---

func (u *DesktopUI) renderServiceDetail(c *ui.Context, st *servicesPageState, server gateway.Server) {
	st.enterDetail(server.ID)

	var srvTools []gateway.Tool
	enabledCount := 0
	roCount := 0
	for _, t := range u.State.Registry.Tools {
		if t.ServerID == server.ID {
			srvTools = append(srvTools, t)
			if t.Enabled {
				enabledCount++
				if t.ReadOnly {
					roCount++
				}
			}
		}
	}
	check, hasCheck := u.State.Setup.SourceChecks[server.ID]

	// Top back button + header
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		backBtn := ui.ButtonBase(c.Key("svc-back-btn")).
			Padding(5, 10).
			Radius(6).
			Background(SailBgSubtle).
			Border(1, SailBorder).
			Cursor(ui.CursorPointer)
		if backBtn.Hovered() {
			backBtn.Background(SailBgHover)
		}
		backBtn.Children(func() {
			ui.Text(c, "← 返回上游服务列表").FontSize(12.5).FontWeight(500).TextColor(SailInkBody).SingleLine()
		})
		if backBtn.Clicked() {
			u.SelectedServerID = ""
			st.detailServerID = ""
		}
	})

	SailSectionHeader(
		c,
		server.Name,
		server.Description,
		func() {
			SailBadge(c, serviceProtocolTag(server), serviceProtocolTone(server))
			if server.Demo {
				SailBadge(c, "DEMO", ToneNeutral)
			} else {
				enabledVal := server.Enabled
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "启用服务").FontSize(12.5).TextColor(SailInkMuted).SingleLine()
					sw := ui.Switch(c.Key("detail-srv-sw-"+server.ID), &enabledVal).
						Label("启用服务").
						Disabled(st.toggleBusyID == server.ID)
					if sw.Changed() {
						u.toggleServerEnabled(st, server, enabledVal)
					}
				})
			}

			if server.Kind == "mcp" && !server.Demo {
				syncLabel := "同步下游工具"
				if st.syncingServerID == server.ID {
					syncLabel = "同步中…"
				}
				if SailSmallButton(c.Key("detail-sync-"+server.ID), syncLabel).
					Disabled(st.syncingServerID != "").
					Clicked() {
					u.syncServerTools(st, server)
				}
			}

			if server.Kind == "http" && !server.Demo {
				if ui.PrimaryButton(c.Key("detail-add-http-tool"), "添加 HTTP 工具").
					Padding(5, 11).
					FontSize(12.5).
					Clicked() {
					st.openAddHTTPToolModal()
				}
			}

			if !server.Demo {
				if SailDangerButton(c.Key("detail-del-btn-"+server.ID), "删除 / 停用").
					Disabled(st.deactivateBusy).
					Clicked() {
					st.confirmDeactivate = true
				}
			}
		},
	)

	// Confirmation dialog for deleting / deactivating service
	switch ui.AlertDialog(
		c.Key("deactivate-alert-"+server.ID),
		&st.confirmDeactivate,
		fmt.Sprintf("确认停用并移除服务「%s」的开放工具？", server.Name),
		"后端将停用该服务（enabled=false）并关闭其全部开放工具，Agent 将无法再路由或调用此服务。历史审计日志保留在本地数据库中。",
		"取消",
		"确认停用",
	) {
	case 1:
		u.deactivateService(st, server)
	}

	// Property Metadata Strip
	target := ServerTargetText(server)
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Stretch).Children(func() {
		subtlePanel(c, 160, 1, func() {
			ui.Text(c, "服务 ID").FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
			ui.Text(c, server.ID).Font("monospace").FontSize(13).TextColor(SailInkStrong).Selectable()
		})

		subtlePanel(c, 240, 2, func() {
			lbl := "连接地址"
			if server.Kind == "mcp" && server.Transport == "stdio" {
				lbl = "启动命令"
			}
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, lbl).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				if SailSmallButton(c.Key("detail-copy-target"), "复制").Padding(2, 7).FontSize(11).Clicked() {
					c.WriteClipboard(target)
					u.Notify("已复制连接目标")
				}
			})
			ui.Text(c, target).Font("monospace").FontSize(12.5).TextColor(SailInkStrong).SingleLine().Selectable()
		})

		subtlePanel(c, 180, 1, func() {
			ui.Text(c, "开放工具与健康").FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
			ui.Text(c, fmt.Sprintf("%d / %d 已启用 (%d 只读)", enabledCount, len(srvTools), roCount)).
				FontSize(13).
				FontWeight(600).
				FontFeatures("tnum").
				TextColor(SailInkStrong)
			if hasCheck {
				statusText := "最近检查"
				if check.Status == "error" {
					statusText = "检查异常"
				}
				ui.Text(c, fmt.Sprintf("%s · %s", statusText, FormatTime(check.CheckedAt))).
					FontSize(11.5).
					TextColor(SailInkMuted)
			} else {
				ui.Text(c, "支持按需同步检测").FontSize(11.5).TextColor(SailInkMuted)
			}
		})
	})

	if hasCheck && check.Status == "error" {
		SailAlertBox(c, ToneDanger, check.Message, check.Detail)
	}

	// Sub-tabs
	if !server.Demo {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Segmented(
				c.Key("detail-subtab-"+server.ID),
				&st.detailSubtab,
				fmt.Sprintf("工具白名单 (%d)", len(srvTools)),
				"连接与认证配置",
				"健康与维护概览",
			)
		})
	} else {
		st.detailSubtab = 0
	}

	switch st.detailSubtab {
	case 0:
		u.renderServiceToolsTab(c, st, server, srvTools, enabledCount)
	case 1:
		u.renderServiceConnectionTab(c, st, server)
	case 2:
		u.renderServiceMaintenanceTab(c, st, server)
	}
}

// --- Detail Subtab 0: Tool Whitelist ---

func (u *DesktopUI) renderServiceToolsTab(c *ui.Context, st *servicesPageState, server gateway.Server, srvTools []gateway.Tool, enabledCount int) {
	SailCard(c, func() {
		// Search & filter bar
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Wrap().Children(func() {
			ui.Row(c).Grow(1).MinWidth(240).Gap(10).AlignItems(ui.Center).Children(func() {
				if ui.SearchField(c.Key("detail-tool-search"), &st.toolQuery).
					Placeholder("搜索工具名称、ID 或说明…").
					Grow(1).
					MinWidth(0).
					Changed() {
					st.toolPage = 1
				}
				if ui.Segmented(
					c.Key("detail-tool-filter"),
					&st.toolFilter,
					fmt.Sprintf("全部 (%d)", len(srvTools)),
					fmt.Sprintf("已启用 (%d)", enabledCount),
					fmt.Sprintf("未启用 (%d)", len(srvTools)-enabledCount),
					"仅只读",
				).Changed() {
					st.toolPage = 1
				}
			})

			if !server.Demo && len(srvTools) > 0 {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if SailSmallButton(c.Key("batch-ro"), "仅开放只读").
						Disabled(st.toolBatchBusy).
						Clicked() {
						var roIDs []string
						for _, t := range srvTools {
							if t.ReadOnly {
								roIDs = append(roIDs, t.ID)
							}
						}
						u.applyServerToolWhitelist(st, server.ID, roIDs, "已仅开放全部只读工具")
					}
					if SailSmallButton(c.Key("batch-all-on"), "全选开放").
						Disabled(st.toolBatchBusy).
						Clicked() {
						allIDs := make([]string, 0, len(srvTools))
						for _, t := range srvTools {
							allIDs = append(allIDs, t.ID)
						}
						u.applyServerToolWhitelist(st, server.ID, allIDs, "已全选开放全部工具")
					}
					if SailSmallButton(c.Key("batch-all-off"), "全部停用").
						Disabled(st.toolBatchBusy).
						Clicked() {
						u.applyServerToolWhitelist(st, server.ID, []string{}, "已停用全部工具")
					}
				})
			}
		})

		q := strings.ToLower(strings.TrimSpace(st.toolQuery))
		var visible []gateway.Tool
		for _, t := range srvTools {
			switch st.toolFilter {
			case 1:
				if !t.Enabled {
					continue
				}
			case 2:
				if t.Enabled {
					continue
				}
			case 3:
				if !t.ReadOnly {
					continue
				}
			}
			if q != "" {
				hay := strings.ToLower(t.Name + " " + t.Description + " " + t.ID)
				if !strings.Contains(hay, q) {
					continue
				}
			}
			visible = append(visible, t)
		}

		if len(visible) == 0 {
			if len(srvTools) == 0 {
				if server.Kind == "http" && !server.Demo {
					SailEmptyState(
						c,
						"尚未为此 HTTP 服务定义工具",
						"你可以手动添加 HTTP 工具，或通过「添加上游服务 -> OpenAPI 导入」批量生成接口工具。",
						"手动添加 HTTP 工具",
						func() { st.openAddHTTPToolModal() },
					)
				} else {
					SailEmptyState(
						c,
						"尚未读取到下游工具",
						"点击右上角「同步下游工具」拉取 MCP Server 暴露的 tools/list。",
						"立即同步工具",
						func() { u.syncServerTools(st, server) },
					)
				}
			} else {
				SailEmptyState(
					c,
					"没有匹配筛选条件的工具",
					"尝试清空搜索词或切换启用状态筛选。",
					"清空筛选",
					func() {
						st.toolQuery = ""
						st.toolFilter = 0
						st.toolPage = 1
					},
				)
			}
			return
		}

		totalPages := (len(visible) + servicesToolPageSize - 1) / servicesToolPageSize
		if totalPages < 1 {
			totalPages = 1
		}
		if st.toolPage < 1 {
			st.toolPage = 1
		}
		if st.toolPage > totalPages {
			st.toolPage = totalPages
		}
		start := (st.toolPage - 1) * servicesToolPageSize
		end := start + servicesToolPageSize
		if end > len(visible) {
			end = len(visible)
		}
		paged := visible[start:end]

		// Tool rows
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			for _, item := range paged {
				t := item
				row := ui.Row(c.Key("tool-row-"+t.ID)).
					FillWidth().
					Padding(10, 12).
					Radius(6).
					Background(SailBgSubtle).
					Border(1, SailBorder).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(12)
				if row.Hovered() {
					row.Background(SailBgHover)
				}
				row.Children(func() {
					// Checkbox + Name & Desc
					ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Start).Children(func() {
						if !server.Demo {
							checked := t.Enabled
							cb := ui.Checkbox(c.Key("tool-cb-"+t.ID), &checked, "").
								Label("开放工具 " + t.Name).
								Disabled(st.toolBatchBusy)
							if cb.Changed() {
								u.toggleSingleToolWhitelist(st, server.ID, srvTools, t.ID, checked)
							}
						}

						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
								ui.Text(c, t.Name).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong)
								if t.Method != "" && t.Path != "" {
									SailBadge(c, t.Method+" "+t.Path, ToneNeutral)
								}
								if t.ReadOnly {
									SailBadge(c, "只读", ToneSuccess)
								} else {
									SailBadge(c, "写操作", ToneWarning)
								}
								if len(t.Actors) > 0 {
									ui.Text(c, strings.Join(t.Actors, ", ")).Font("monospace").FontSize(11).TextColor(SailInkFaint)
								}
							})
							desc := t.Description
							if desc == "" {
								desc = "下游未提供描述，建议点击「编辑」补充说明以提高路由准确率。"
							}
							ui.Text(c, desc).FontSize(12).TextColor(SailInkMuted)
						})
					})

					// Right: Switch + Edit
					ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
						if !server.Demo {
							swVal := t.Enabled
							sw := ui.Switch(c.Key("tool-sw-"+t.ID), &swVal).
								Label(t.Name + " 开关").
								Disabled(st.toolBatchBusy)
							if sw.Changed() {
								u.toggleSingleToolWhitelist(st, server.ID, srvTools, t.ID, swVal)
							}
						}
						if SailSmallButton(c.Key("tool-edit-"+t.ID), "查看 / 编辑").Clicked() {
							st.openToolEditModal(t)
						}
					})
				})
			}
		})

		// Pagination footer
		if len(visible) > servicesToolPageSize {
			ui.Row(c).FillWidth().Padding(6, 0, 0, 0).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, fmt.Sprintf("显示 %d–%d / 共 %d 个工具", start+1, end, len(visible))).
					FontSize(12).
					FontFeatures("tnum").
					TextColor(SailInkMuted)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("第 %d / %d 页", st.toolPage, totalPages)).
						FontSize(12).
						FontFeatures("tnum").
						TextColor(SailInkMuted)
					if SailSmallButton(c.Key("tool-prev"), "上一页").Disabled(st.toolPage <= 1).Clicked() {
						st.toolPage--
					}
					if SailSmallButton(c.Key("tool-next"), "下一页").Disabled(st.toolPage >= totalPages).Clicked() {
						st.toolPage++
					}
				})
			})
		}
	})
}

// --- Detail Subtab 1: Connection & Auth Config ---

func (st *servicesPageState) hydrateConnForm(server gateway.Server) {
	st.connHydratedID = server.ID
	st.connName = server.Name
	st.connDesc = server.Description
	st.connEndpoint = server.Endpoint
	st.connCommand = server.Command
	st.connWorkDir = server.WorkDir
	if len(server.Args) > 0 {
		st.connArgsText = PrettyJSON(server.Args)
	} else {
		st.connArgsText = "[]"
	}
	if len(server.Env) > 0 {
		st.connEnvText = PrettyJSON(server.Env)
	} else {
		st.connEnvText = "{}"
	}
	if len(server.Headers) > 0 {
		st.connHeadersText = PrettyJSON(server.Headers)
	} else {
		st.connHeadersText = "{}"
	}
	st.connRedactEmails = server.RedactEmails
	st.connRedactFieldsText = strings.Join(server.RedactFields, ", ")
	st.connRawJSON = PrettyJSON(server)
	st.connError = ""
}

func (u *DesktopUI) renderServiceConnectionTab(c *ui.Context, st *servicesPageState, server gateway.Server) {
	if st.connHydratedID != server.ID {
		st.hydrateConnForm(server)
	}

	SailCard(c, func() {
		SailCardTitle(
			c,
			"连接与认证配置",
			"请求头与环境变量仅支持引用宿主环境变量（例如 ${SERVICE_TOKEN}），明文密钥不落盘。",
			func() {
				if ui.Segmented(c.Key("conn-mode-seg"), &st.connMode, "表单视图", "JSON 定义").Changed() {
					if st.connMode == 1 {
						if built, err := st.buildServerFromConnForm(server); err == nil {
							st.connRawJSON = PrettyJSON(built)
						} else {
							st.connRawJSON = PrettyJSON(server)
						}
					}
				}
			},
		)

		if st.connError != "" {
			SailAlertBox(c, ToneDanger, "保存或连接测试失败", st.connError)
		}

		if st.connMode == 1 {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, "完整服务 JSON 定义").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("conn-raw-json-"+server.ID), &st.connRawJSON).
					Font("monospace").
					FontSize(12).
					Height(300).
					FillWidth()
			})
		} else {
			// Name + Description
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "显示名称").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("conn-name-"+server.ID), &st.connName).FillWidth()
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "服务描述").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("conn-desc-"+server.ID), &st.connDesc).
						Placeholder("简要说明此服务提供的能力范围").
						FillWidth()
				})
			})

			if server.Kind == "mcp" && server.Transport == "stdio" {
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "启动命令 (command)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("conn-cmd-"+server.ID), &st.connCommand).
							Font("monospace").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "工作目录 (cwd，可选)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("conn-cwd-"+server.ID), &st.connWorkDir).
							Font("monospace").
							FillWidth()
					})
				})

				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "启动参数 args（JSON 字符串数组）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextArea(c.Key("conn-args-"+server.ID), &st.connArgsText).
							Font("monospace").
							FontSize(12).
							Height(110).
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "环境变量 env（JSON 对象，值使用 ${ENV_VAR}）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextArea(c.Key("conn-env-"+server.ID), &st.connEnvText).
							Font("monospace").
							FontSize(12).
							Height(110).
							FillWidth()
					})
				})
			} else {
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					lbl := "HTTP API 基地址 (Base URL)"
					if server.Kind == "mcp" {
						lbl = "MCP Endpoint URL (Streamable HTTP)"
					}
					ui.Text(c, lbl).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("conn-ep-"+server.ID), &st.connEndpoint).
						Font("monospace").
						FillWidth()
				})

				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "请求头 headers（JSON 对象，如 {\"Authorization\": \"Bearer ${SERVICE_TOKEN}\"}）").
						FontSize(12.5).
						FontWeight(600).
						TextColor(SailInkBody)
					ui.TextArea(c.Key("conn-hdrs-"+server.ID), &st.connHeadersText).
						Font("monospace").
						FontSize(12).
						Height(110).
						FillWidth()
				})
			}

			// Redaction controls
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Checkbox(c.Key("conn-redact-emails-"+server.ID), &st.connRedactEmails, "自动脱敏返回结果中的邮箱地址")
				ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "脱敏字段").FontSize(12).TextColor(SailInkMuted).Shrink(0)
					ui.TextInput(c.Key("conn-redact-fields-"+server.ID), &st.connRedactFieldsText).
						Placeholder("逗号分隔，如 user.ssn, token").
						Font("monospace").
						Grow(1).
						MinWidth(0)
				})
			})
		}

		// Footer buttons
		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "「保存并测试连接」会立即探测端点并在 MCP 服务上同步工具定义。").
				FontSize(12).
				TextColor(SailInkMuted)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if SailSmallButton(c.Key("conn-reset-"+server.ID), "重置修改").
					Disabled(st.connBusy).
					Clicked() {
					st.hydrateConnForm(server)
				}
				if st.connMode == 1 {
					if ui.PrimaryButton(c.Key("conn-save-json-"+server.ID), "保存 JSON 定义").
						Disabled(st.connBusy).
						Clicked() {
						u.saveConnJSON(st, server)
					}
				} else {
					if SailSmallButton(c.Key("conn-save-only-"+server.ID), "仅保存配置").
						Disabled(st.connBusy).
						Clicked() {
						u.saveConnForm(st, server, false)
					}
					btnLabel := "保存并测试连接"
					if st.connBusy {
						btnLabel = "测试连接中…"
					}
					if ui.PrimaryButton(c.Key("conn-save-test-"+server.ID), btnLabel).
						Disabled(st.connBusy).
						Clicked() {
						u.saveConnForm(st, server, true)
					}
				}
			})
		})
	})
}

func (st *servicesPageState) buildServerFromConnForm(orig gateway.Server) (gateway.Server, error) {
	out := orig
	out.Name = strings.TrimSpace(st.connName)
	out.Description = strings.TrimSpace(st.connDesc)
	out.RedactEmails = st.connRedactEmails
	out.RedactFields = splitCommaList(st.connRedactFieldsText)

	if out.Kind == "mcp" && out.Transport == "stdio" {
		out.Command = strings.TrimSpace(st.connCommand)
		out.WorkDir = strings.TrimSpace(st.connWorkDir)
		args, err := parseStringSliceJSON(st.connArgsText)
		if err != nil {
			return out, fmt.Errorf("启动参数格式错误: %w", err)
		}
		env, err := parseStringMapJSON(st.connEnvText)
		if err != nil {
			return out, fmt.Errorf("环境变量格式错误: %w", err)
		}
		out.Args = args
		out.Env = env
		out.Endpoint = ""
		out.Headers = nil
	} else {
		if out.Kind == "http" {
			out.Transport = "http"
		} else if out.Transport == "" {
			out.Transport = "http"
		}
		out.Endpoint = strings.TrimSpace(st.connEndpoint)
		headers, err := parseStringMapJSON(st.connHeadersText)
		if err != nil {
			return out, fmt.Errorf("请求头格式错误: %w", err)
		}
		out.Headers = headers
		out.Args = nil
		out.Env = nil
	}
	return out, nil
}

// --- Detail Subtab 2: Health & Maintenance Overview ---

func (u *DesktopUI) renderServiceMaintenanceTab(c *ui.Context, st *servicesPageState, server gateway.Server) {
	if st.maintOverview == nil && !st.maintLoading && st.maintError == "" {
		u.loadMaintenanceOverview(st, server)
	}
	if st.maintOverview != nil && st.maintHydratedServerID != server.ID {
		st.hydrateMaintSource(*st.maintOverview, server)
	}

	check, hasCheck := u.State.Setup.SourceChecks[server.ID]

	// Health & process summary card
	SailCard(c, func() {
		SailCardTitle(
			c,
			"服务运行状态与健康检查",
			"展示连接探活结果、进程/协议运行时状态以及最近相关网关活动。",
			func() {
				refreshLabel := "刷新状态"
				if st.maintLoading {
					refreshLabel = "加载中…"
				}
				if SailSmallButton(c.Key("maint-refresh-"+server.ID), refreshLabel).
					Disabled(st.maintLoading).
					Clicked() {
					u.loadMaintenanceOverview(st, server)
				}
				if SailSmallButton(c.Key("health-probe-"+server.ID), "立即探活测试").
					Disabled(st.connBusy).
					Clicked() {
					u.probeServerConnection(st, server)
				}
			},
		)

		ui.Row(c).FillWidth().Gap(12).Children(func() {
			subtlePanel(c, 220, 1, func() {
				ui.Text(c, "最近连接检查").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				if !hasCheck {
					ui.Text(c, "尚无探活记录").FontSize(13).TextColor(SailInkBody)
					ui.Text(c, "点击「立即探活测试」或「同步下游工具」更新健康快照。").FontSize(12).TextColor(SailInkMuted)
				} else {
					tone := ToneSuccess
					statusLabel := "正常"
					if check.Status == "error" {
						tone = ToneDanger
						statusLabel = "异常"
					} else if check.Stale {
						tone = ToneWarning
						statusLabel = "配置已变更（待复测）"
					}
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						SailBadgeDot(c, statusLabel, tone)
						ui.Text(c, FormatTime(check.CheckedAt)).FontSize(12).TextColor(SailInkMuted)
					})
					ui.Text(c, check.Message).FontSize(12.5).TextColor(SailInkStrong)
					if check.Detail != "" {
						ui.Text(c, check.Detail).Font("monospace").FontSize(11.5).TextColor(SailDangerInk).Selectable()
					}
				}
			})

			subtlePanel(c, 220, 1, func() {
				ui.Text(c, "维护运行时与执行环境").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				if st.maintOverview != nil && st.maintOverview.Runtime != nil {
					avail, _ := st.maintOverview.Runtime["available"].(bool)
					cmdStr, _ := st.maintOverview.Runtime["command"].(string)
					execMode := "local"
					if execMap, ok := st.maintOverview.Runtime["execution"].(map[string]any); ok {
						if m, ok := execMap["mode"].(string); ok && m != "" {
							execMode = m
						}
					}
					if avail {
						SailBadgeDot(c, "Pi 运行时就绪 ("+cmdStr+")", ToneSuccess)
					} else {
						SailBadgeDot(c, "仅确定性同步 (无 Pi)", ToneNeutral)
					}
					ui.Text(c, fmt.Sprintf("执行隔离模式：%s · 协议：%s", execMode, serviceProtocolTag(server))).
						FontSize(12).
						TextColor(SailInkMuted)
				} else {
					ui.Text(c, "正在读取维护运行时信息…").FontSize(12).TextColor(SailInkMuted)
				}
			})
		})
	})

	if st.maintError != "" {
		SailAlertBox(c, ToneDanger, "维护操作失败", st.maintError)
	}

	// Maintenance Source Policy & Run History
	SailCard(c, func() {
		SailCardTitle(
			c,
			"自动维护策略",
			"配置上游规格同步来源与定期检查周期，自动检测工具变更。",
			nil,
		)

		modeOpt0 := "原生 MCP 同步"
		if server.Kind == "http" {
			modeOpt0 = "OpenAPI 确定性导入"
		}
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "维护方式").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.Segmented(c.Key("maint-fmt-"+server.ID), &st.maintFormatIdx, modeOpt0, "Pi 编程适配")
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, "工具 / operationId 范围过滤（可选，逗号分隔）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("maint-ops-"+server.ID), &st.maintOpsText).
				Font("monospace").
				Placeholder("留空则导入全部工具").
				FillWidth()
		})

		if server.Kind == "http" || st.maintFormatIdx == 1 {
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "本地规格文件路径（每行一个绝对路径）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("maint-files-"+server.ID), &st.maintFilesText).
						Font("monospace").
						FontSize(12).
						Height(72).
						FillWidth()
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "在线规格文档 URL（每行一个 HTTP/HTTPS 地址）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("maint-urls-"+server.ID), &st.maintURLsText).
						Font("monospace").
						FontSize(12).
						Height(72).
						FillWidth()
				})
			})
		}

		if st.maintFormatIdx == 1 {
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Text(c, "Pi 维护指引（Prompt）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("maint-inst-"+server.ID), &st.maintInstructions).
					FontSize(12.5).
					Height(72).
					FillWidth()
			})
		}

		ui.Row(c).FillWidth().Gap(12).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, "检查周期（分钟）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("maint-interval-"+server.ID), &st.maintIntervalStr).Font("monospace").FillWidth()
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, "单次任务超时（秒）").FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("maint-timeout-"+server.ID), &st.maintTimeoutStr).Font("monospace").FillWidth()
			})
		})

		ui.Row(c).FillWidth().Gap(16).Wrap().Children(func() {
			ui.Checkbox(c.Key("maint-enabled-"+server.ID), &st.maintEnabled, "启用后台定期自动检查")
			ui.Checkbox(c.Key("maint-autopub-"+server.ID), &st.maintAutoPublish, "独立验收通过后自动发布兼容更新")
		})

		ui.Row(c).FillWidth().Padding(6, 0, 0, 0).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("maint-save-"+server.ID), "保存策略").
				Disabled(st.maintBusy).
				Clicked() {
				u.saveMaintenanceConfig(st, server, false, false)
			}
			if SailSmallButton(c.Key("maint-force-run-"+server.ID), "强制重跑").
				Disabled(st.maintBusy).
				Clicked() {
				u.saveMaintenanceConfig(st, server, true, true)
			}
			if ui.PrimaryButton(c.Key("maint-save-run-"+server.ID), "保存并立即检查").
				Disabled(st.maintBusy).
				Clicked() {
				u.saveMaintenanceConfig(st, server, true, false)
			}
		})
	})

	// Maintenance runs list
	SailCard(c, func() {
		var runs []gateway.MaintenanceRun
		if st.maintOverview != nil {
			for _, r := range st.maintOverview.Runs {
				if r.ServerID == server.ID {
					runs = append(runs, r)
				}
			}
		}

		SailCardTitle(
			c,
			"维护运行记录",
			fmt.Sprintf("共 %d 条历史维护任务记录", len(runs)),
			nil,
		)

		if len(runs) == 0 {
			SailEmptyState(c, "尚未运行过维护检查", "配置上方维护策略后，可点击「保存并立即检查」。", "", nil)
			return
		}

		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for _, runItem := range runs {
				r := runItem
				tone := ToneInfo
				switch r.Status {
				case "published", "no_change":
					tone = ToneSuccess
				case "failed", "interrupted":
					tone = ToneDanger
				case "ready":
					tone = ToneWarning
				case "cancelled", "rolled_back":
					tone = ToneNeutral
				}

				ui.Row(c.Key("maint-run-"+r.ID)).
					FillWidth().
					Padding(10, 12).
					Radius(6).
					Background(SailBgSubtle).
					Border(1, SailBorder).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Gap(10).
					Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								SailBadge(c, maintenanceStatusLabel(r.Status), tone)
								ui.Text(c, r.Stage).Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
								ui.Text(c, FormatTime(r.CreatedAt)).FontSize(11.5).TextColor(SailInkFaint)
							})
							summary := r.Error
							if summary == "" {
								summary = r.Summary
							}
							if summary == "" {
								summary = "检查来源快照并验证工具定义"
							}
							ui.Text(c, summary).FontSize(12.5).TextColor(SailInkBody)
						})

						ui.Row(c).Gap(6).Shrink(0).Children(func() {
							if r.Status == "ready" && r.Acceptance.Passed {
								if ui.PrimaryButton(c.Key("run-pub-"+r.ID), "发布版本").
									Padding(4, 10).
									FontSize(12).
									Disabled(st.maintBusy).
									Clicked() {
									u.runMaintenanceAction(st, server, "新版本已发布", func(ctx context.Context) error {
										return u.Client.PublishMaintenanceRun(ctx, r.ID)
									})
								}
							}
							if r.Status == "published" {
								if SailSmallButton(c.Key("run-rb-"+r.ID), "回退").
									Disabled(st.maintBusy).
									Clicked() {
									u.runMaintenanceAction(st, server, "已回退本次发布", func(ctx context.Context) error {
										return u.Client.RollbackMaintenanceRun(ctx, r.ID)
									})
								}
							}
							if r.Status == "queued" || r.Status == "running" {
								if SailSmallButton(c.Key("run-cancel-"+r.ID), "取消").
									Disabled(st.maintBusy).
									Clicked() {
									u.runMaintenanceAction(st, server, "已取消任务", func(ctx context.Context) error {
										return u.Client.CancelMaintenanceRun(ctx, r.ID)
									})
								}
							}
						})
					})
			}
		})
	})
}

func maintenanceStatusLabel(status string) string {
	switch status {
	case "queued":
		return "排队中"
	case "running":
		return "运行中"
	case "ready":
		return "待确认发布"
	case "published":
		return "已发布"
	case "no_change":
		return "无变更"
	case "failed":
		return "失败"
	case "cancelled":
		return "已取消"
	case "interrupted":
		return "已中断"
	case "rolled_back":
		return "已回退"
	default:
		return status
	}
}

func (st *servicesPageState) hydrateMaintSource(ov MaintenanceOverview, server gateway.Server) {
	st.maintHydratedServerID = server.ID
	var found *gateway.MaintenanceSource
	for i := range ov.Sources {
		if ov.Sources[i].ServerID == server.ID {
			found = &ov.Sources[i]
			break
		}
	}
	if found == nil {
		st.maintFormatIdx = 0
		st.maintFilesText = ""
		st.maintURLsText = ""
		st.maintOpsText = ""
		st.maintInstructions = ""
		st.maintIntervalStr = "60"
		st.maintTimeoutStr = "900"
		st.maintEnabled = false
		st.maintAutoPublish = true
		return
	}
	if found.Format == "pi" {
		st.maintFormatIdx = 1
	} else {
		st.maintFormatIdx = 0
	}
	st.maintFilesText = strings.Join(found.Files, "\n")
	st.maintURLsText = strings.Join(found.URLs, "\n")
	st.maintOpsText = strings.Join(found.OperationIDs, ", ")
	st.maintInstructions = found.Instructions
	if found.IntervalMinutes > 0 {
		st.maintIntervalStr = strconv.Itoa(found.IntervalMinutes)
	} else {
		st.maintIntervalStr = "60"
	}
	if found.TimeoutSeconds > 0 {
		st.maintTimeoutStr = strconv.Itoa(found.TimeoutSeconds)
	} else {
		st.maintTimeoutStr = "900"
	}
	st.maintEnabled = found.Enabled
	st.maintAutoPublish = found.AutoPublish
}

// --- Add / Import Service Modal ---

func (u *DesktopUI) renderAddServiceModal(c *ui.Context, st *servicesPageState) {
	ui.DialogBase(c.Key("add-service-dialog"), &st.showAddModal, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42))
		panel.Width(680).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25))

		// Header
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "添加上游服务").FontSize(17).Bold().TextColor(SailInkStrong)
				ui.Text(c, "接入本地 MCP Stdio 进程、远程 MCP HTTP / SSE 服务，或从 OpenAPI 自动导入工具。").
					FontSize(12).
					TextColor(SailInkMuted)
			})
			if SailSmallButton(c.Key("add-modal-close"), "关闭").Clicked() {
				st.showAddModal = false
			}
		})

		ui.Segmented(
			c.Key("add-svc-tabs"),
			&st.addTab,
			"MCP Stdio 本地进程",
			"MCP 远程 HTTP / SSE",
			"OpenAPI / Swagger 导入",
			"粘贴 mcpServers JSON",
		).FillEqually()

		if st.addError != "" {
			SailAlertBox(c, ToneDanger, "操作失败", st.addError)
		}

		// Scrollable form body
		ui.Scroll(c.Key("add-svc-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			switch st.addTab {
			case 0: // MCP Stdio
				u.renderAddCommonIdentityFields(c, st, "stdio")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "启动命令 (command) *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-cmd"), &st.addCommand).
							Font("monospace").
							Placeholder("npx、uvx 或可执行程序绝对路径").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "工作目录 (cwd，可选)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-cwd"), &st.addWorkDir).
							Font("monospace").
							Placeholder("/path/to/workspace").
							FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "启动参数 args（JSON 字符串数组）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-args"), &st.addArgsText).
						Font("monospace").
						FontSize(12).
						Placeholder("[\"-y\", \"@modelcontextprotocol/server-filesystem\", \"/tmp\"]").
						Height(88).
						FillWidth()
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "环境变量 env（JSON 对象，值需使用 ${ENV_VAR} 引用）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-env"), &st.addEnvText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\"GITHUB_TOKEN\": \"${GITHUB_TOKEN}\"}").
						Height(88).
						FillWidth()
				})

			case 1: // MCP Remote HTTP / SSE
				u.renderAddCommonIdentityFields(c, st, "remote")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(2).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "服务地址 (url) *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-url"), &st.addEndpoint).
							Font("monospace").
							Placeholder("https://api.example.com/mcp").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "协议类型").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.Select(
							c.Key("add-remote-proto"),
							&st.addRemoteProto,
							[]string{"MCP Streamable HTTP", "MCP SSE (Streamable 兼容)"},
						).FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "请求头 headers（JSON 对象，凭据需使用 ${ENV_VAR} 引用）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-headers"), &st.addHeadersText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\"Authorization\": \"Bearer ${SERVICE_TOKEN}\"}").
						Height(100).
						FillWidth()
				})

			case 2: // OpenAPI / Swagger Import
				u.renderAddCommonIdentityFields(c, st, "openapi")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "API Base URL *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-base"), &st.addOpenAPIBaseURL).
							Font("monospace").
							Placeholder("https://api.internal.example/v1").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "OpenAPI 文档 URL（与下方内容二选一）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-url"), &st.addOpenAPISpecURL).
							Font("monospace").
							Placeholder("https://api.internal.example/openapi.json").
							FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "粘贴 OpenAPI 3.0 / 3.1 JSON 或 YAML 规范").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-oapi-spec"), &st.addOpenAPISpecText).
						Font("monospace").
						FontSize(12).
						Placeholder("openapi: 3.0.3\ninfo:\n  title: Internal API\npaths: ...").
						Height(140).
						FillWidth()
				})
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "过滤 operationId（可选，逗号分隔）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-ops"), &st.addOpenAPIOpsText).
							Font("monospace").
							Placeholder("留空导入全部接口").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "认证请求头 JSON（可选）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-hdrs"), &st.addOpenAPIHeaders).
							Font("monospace").
							Placeholder("{\"Authorization\": \"Bearer ${API_TOKEN}\"}").
							FillWidth()
					})
				})

			case 3: // Paste mcpServers JSON
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Text(c, "粘贴单个 mcpServers 配置条目或网关服务 JSON").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-paste-json"), &st.addPasteText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\n  \"mcpServers\": {\n    \"github\": {\n      \"type\": \"http\",\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  }\n}").
						Height(220).
						FillWidth()
					ui.Text(c, "点击下方「解析并填入表单」后会自动跳转到对应协议表单供核对并连接。").
						FontSize(12).
						TextColor(SailInkMuted)
				})
			}
		})

		// Footer buttons
		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			if SailSmallButton(c.Key("add-reset"), "清空表单").Disabled(st.addBusy).Clicked() {
				st.resetAddFields()
			}
			ui.Row(c).Gap(8).Children(func() {
				if SailSmallButton(c.Key("add-cancel"), "取消").Disabled(st.addBusy).Clicked() {
					st.showAddModal = false
				}
				switch st.addTab {
				case 0, 1:
					lbl := "连接并读取工具"
					if st.addBusy {
						lbl = "正在连接…"
					}
					if ui.PrimaryButton(c.Key("add-submit-mcp"), lbl).Disabled(st.addBusy).Clicked() {
						u.submitAddMCPService(st)
					}
				case 2:
					lbl := "解析并注册 OpenAPI 服务"
					if st.addBusy {
						lbl = "正在导入…"
					}
					if ui.PrimaryButton(c.Key("add-submit-oapi"), lbl).Disabled(st.addBusy).Clicked() {
						u.submitAddOpenAPIService(st)
					}
				case 3:
					if ui.PrimaryButton(c.Key("add-parse-paste"), "解析并填入表单").
						Disabled(strings.TrimSpace(st.addPasteText) == "").
						Clicked() {
						st.parsePasteJSON()
					}
				}
			})
		})
	})
}

func (u *DesktopUI) renderAddCommonIdentityFields(c *ui.Context, st *servicesPageState, keyPrefix string) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, "服务名称 *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-name-"+keyPrefix), &st.addName).
				Placeholder("例如：GitHub 或 内部订单平台").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, "服务 ID（留空自动生成）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-id-"+keyPrefix), &st.addID).
				Font("monospace").
				Placeholder("github").
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, "服务描述（可选）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("add-desc-"+keyPrefix), &st.addDescription).
			Placeholder("帮助路由模型理解此服务的职责范围").
			FillWidth()
	})
}

func (st *servicesPageState) parsePasteJSON() {
	st.addError = ""
	var raw map[string]any
	if err := json.Unmarshal([]byte(st.addPasteText), &raw); err != nil {
		st.addError = "JSON 解析失败: " + err.Error()
		return
	}

	// Gateway Server format
	if kind, ok := raw["kind"].(string); ok && kind != "" {
		if id, _ := raw["id"].(string); id != "" {
			st.addID = id
		}
		if name, _ := raw["name"].(string); name != "" {
			st.addName = name
		} else {
			st.addName = st.addID
		}
		st.addDescription, _ = raw["description"].(string)
		transport, _ := raw["transport"].(string)
		if kind == "http" {
			st.addOpenAPIBaseURL, _ = raw["endpoint"].(string)
			st.addOpenAPIHeaders = marshalMapOrDefault(raw["headers"])
			st.addTab = 2
			return
		}
		if transport == "stdio" {
			st.addCommand, _ = raw["command"].(string)
			st.addWorkDir, _ = raw["work_dir"].(string)
			st.addArgsText = marshalSliceOrDefault(raw["args"])
			st.addEnvText = marshalMapOrDefault(raw["env"])
			st.addTab = 0
			return
		}
		st.addEndpoint, _ = raw["endpoint"].(string)
		st.addHeadersText = marshalMapOrDefault(raw["headers"])
		st.addTab = 1
		return
	}

	// Standard mcpServers format
	var srvName string
	var entry map[string]any
	if mcpServers, ok := raw["mcpServers"].(map[string]any); ok {
		if len(mcpServers) != 1 {
			st.addError = "请一次粘贴单个 mcpServers 服务条目"
			return
		}
		for k, v := range mcpServers {
			srvName = k
			entry, _ = v.(map[string]any)
		}
	} else {
		srvName = "my-server"
		entry = raw
	}
	if entry == nil {
		st.addError = "未能识别 mcpServers 服务配置对象"
		return
	}

	st.addName = srvName
	st.addID = slugServiceID(srvName, "mcp")
	if cmd, _ := entry["command"].(string); cmd != "" {
		st.addCommand = cmd
		st.addWorkDir, _ = entry["cwd"].(string)
		st.addArgsText = marshalSliceOrDefault(entry["args"])
		st.addEnvText = marshalMapOrDefault(entry["env"])
		st.addTab = 0
		return
	}
	st.addEndpoint, _ = entry["url"].(string)
	st.addHeadersText = marshalMapOrDefault(entry["headers"])
	if typ, _ := entry["type"].(string); strings.EqualFold(typ, "sse") {
		st.addRemoteProto = "MCP SSE (Streamable 兼容)"
	} else {
		st.addRemoteProto = "MCP Streamable HTTP"
	}
	st.addTab = 1
}

// --- Tool Edit Modal & Add HTTP Tool Modal ---

func (st *servicesPageState) openToolEditModal(t gateway.Tool) {
	copyTool := t
	st.editingTool = &copyTool
	st.showToolModal = true
	st.toolEditTab = 0
	st.toolEditDesc = t.Description
	st.toolEditEnabled = t.Enabled
	st.toolEditReadOnly = t.ReadOnly
	st.toolEditActors = strings.Join(t.Actors, ", ")
	st.toolEditRawJSON = PrettyJSON(t)
	st.toolEditError = ""
}

func (u *DesktopUI) renderToolEditModal(c *ui.Context, st *servicesPageState) {
	t := *st.editingTool
	ui.DialogBase(c.Key("tool-edit-dialog-"+t.ID), &st.showToolModal, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42))
		panel.Width(640).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25))

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, t.Name).Font("monospace").FontSize(16).Bold().TextColor(SailInkStrong)
				ui.Text(c, t.ID).Font("monospace").FontSize(12).TextColor(SailInkMuted)
			})
			if SailSmallButton(c.Key("tool-modal-close"), "关闭").Clicked() {
				st.showToolModal = false
			}
		})

		if ui.Segmented(c.Key("tool-edit-tabs"), &st.toolEditTab, "属性与 Schema", "完整 JSON").Changed() {
			if st.toolEditTab == 1 {
				updated := t
				updated.Description = st.toolEditDesc
				updated.Enabled = st.toolEditEnabled
				updated.ReadOnly = st.toolEditReadOnly
				updated.Actors = splitCommaList(st.toolEditActors)
				st.toolEditRawJSON = PrettyJSON(updated)
			}
		}

		if st.toolEditError != "" {
			SailAlertBox(c, ToneDanger, "保存失败", st.toolEditError)
		}

		ui.Scroll(c.Key("tool-edit-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			if st.toolEditTab == 1 {
				ui.TextArea(c.Key("tool-raw-json"), &st.toolEditRawJSON).
					Font("monospace").
					FontSize(12).
					Height(340).
					FillWidth()
			} else {
				ui.Row(c).FillWidth().Gap(16).Children(func() {
					subtlePanel(c, 160, 1, func() {
						ui.Text(c, "开放给路由模型").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
						ui.Switch(c.Key("tool-edit-enabled"), &st.toolEditEnabled).Label("开放工具")
					})
					subtlePanel(c, 160, 1, func() {
						ui.Text(c, "只读安全标记").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
						ui.Switch(c.Key("tool-edit-ro"), &st.toolEditReadOnly).Label("只读工具")
					})
				})

				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "工具用途描述（Decisions 路由模型依据此描述评估相关性）").
						FontSize(12.5).
						FontWeight(600).
						TextColor(SailInkBody)
					ui.TextArea(c.Key("tool-edit-desc"), &st.toolEditDesc).
						Height(96).
						FillWidth()
				})

				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "允许访问身份（Actors，逗号分隔）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("tool-edit-actors"), &st.toolEditActors).
						Font("monospace").
						Placeholder("developer, observer").
						FillWidth()
				})

				SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(t.InputSchema), func() {
					u.Notify("已复制 Input Schema")
				})
			}
		})

		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("tool-edit-cancel"), "取消").Disabled(st.toolEditBusy).Clicked() {
				st.showToolModal = false
			}
			lbl := "保存更改"
			if st.toolEditBusy {
				lbl = "保存中…"
			}
			if ui.PrimaryButton(c.Key("tool-edit-save"), lbl).Disabled(st.toolEditBusy).Clicked() {
				u.saveEditedTool(st, t)
			}
		})
	})
}

func (st *servicesPageState) openAddHTTPToolModal() {
	st.showAddHTTPTool = true
	st.httpToolName = ""
	st.httpToolMethod = "GET"
	st.httpToolPath = ""
	st.httpToolDesc = ""
	st.httpToolReadOnly = true
	st.httpToolSchema = "{\n  \"type\": \"object\",\n  \"properties\": {},\n  \"required\": [],\n  \"additionalProperties\": false\n}"
	st.httpToolError = ""
}

func (u *DesktopUI) renderAddHTTPToolModal(c *ui.Context, st *servicesPageState, server gateway.Server) {
	ui.DialogBase(c.Key("add-http-tool-dialog-"+server.ID), &st.showAddHTTPTool, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42))
		panel.Width(600).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25))

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "添加 HTTP 工具").FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("http-tool-close"), "关闭").Clicked() {
				st.showAddHTTPTool = false
			}
		})

		if st.httpToolError != "" {
			SailAlertBox(c, ToneDanger, "创建失败", st.httpToolError)
		}

		ui.Scroll(c.Key("http-tool-scroll")).Grow(1).FillWidth().MaxHeight(440).Gap(10).Children(func() {
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(2).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "工具名称 *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("http-tool-name"), &st.httpToolName).
						Font("monospace").
						Placeholder("search_documents").
						FillWidth()
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, "HTTP 方法").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					if ui.Select(c.Key("http-tool-method"), &st.httpToolMethod, []string{"GET", "POST", "PUT", "PATCH", "DELETE"}).
						FillWidth().
						Changed() {
						st.httpToolReadOnly = st.httpToolMethod == "GET"
					}
				})
			})

			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Text(c, "相对路径 *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("http-tool-path"), &st.httpToolPath).
					Font("monospace").
					Placeholder("/documents/search").
					FillWidth()
			})

			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Text(c, "工具用途描述 *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("http-tool-desc"), &st.httpToolDesc).
					Placeholder("说明该工具解决什么问题、适用哪些查询场景").
					FillWidth()
			})

			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Text(c, "输入参数 JSON Schema").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("http-tool-schema"), &st.httpToolSchema).
					Font("monospace").
					FontSize(12).
					Height(150).
					FillWidth()
			})

			ui.Checkbox(c.Key("http-tool-ro"), &st.httpToolReadOnly, "声明为只读工具（不修改上游状态）")
		})

		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("http-tool-cancel"), "取消").Disabled(st.httpToolBusy).Clicked() {
				st.showAddHTTPTool = false
			}
			lbl := "保存并启用工具"
			if st.httpToolBusy {
				lbl = "保存中…"
			}
			if ui.PrimaryButton(c.Key("http-tool-submit"), lbl).Disabled(st.httpToolBusy).Clicked() {
				u.submitAddHTTPTool(st, server.ID)
			}
		})
	})
}

// --- Async API Operations ---

func (u *DesktopUI) toggleServerEnabled(st *servicesPageState, server gateway.Server, enabled bool) {
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
			u.Notify("已启用 " + server.Name)
		} else {
			u.Notify("已停用 " + server.Name)
		}
		return nil
	})
}

func (u *DesktopUI) syncServerTools(st *servicesPageState, server gateway.Server) {
	if u.Client == nil || st.syncingServerID != "" {
		return
	}
	st.syncingServerID = server.ID
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.syncingServerID = "" })
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		imported, err := u.Client.SyncServer(ctx, server.ID)
		if err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		u.Notify(fmt.Sprintf("已同步 %d 个工具定义（新增或变更工具默认关闭）", imported))
		return nil
	})
}

func (u *DesktopUI) applyServerToolWhitelist(st *servicesPageState, serverID string, ids []string, msg string) {
	if u.Client == nil || st.toolBatchBusy {
		return
	}
	st.toolBatchBusy = true
	if ids == nil {
		ids = []string{}
	}
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.toolBatchBusy = false })
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.EnableServerTools(ctx, serverID, ids); err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		u.Notify(fmt.Sprintf("%s（当前开放 %d 个工具）", msg, len(ids)))
		return nil
	})
}

func (u *DesktopUI) toggleSingleToolWhitelist(st *servicesPageState, serverID string, srvTools []gateway.Tool, targetID string, enable bool) {
	ids := make([]string, 0, len(srvTools))
	for _, t := range srvTools {
		on := t.Enabled
		if t.ID == targetID {
			on = enable
		}
		if on {
			ids = append(ids, t.ID)
		}
	}
	label := "已关闭工具"
	if enable {
		label = "已开放工具"
	}
	u.applyServerToolWhitelist(st, serverID, ids, label)
}

func (u *DesktopUI) deactivateService(st *servicesPageState, server gateway.Server) {
	if u.Client == nil || st.deactivateBusy {
		return
	}
	st.deactivateBusy = true
	updated := server
	updated.Enabled = false
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.deactivateBusy = false })
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.EnableServerTools(ctx, server.ID, []string{}); err != nil {
			return err
		}
		if _, err := u.Client.SaveServer(ctx, updated); err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		u.updateUI(func() {
			u.SelectedServerID = ""
			st.detailServerID = ""
		})
		u.Notify("已停用服务「" + server.Name + "」并关闭全部开放工具")
		return nil
	})
}

func (u *DesktopUI) submitAddMCPService(st *servicesPageState) {
	if u.Client == nil || st.addBusy {
		return
	}
	st.addError = ""

	name := strings.TrimSpace(st.addName)
	id := strings.TrimSpace(st.addID)
	if id == "" {
		id = slugServiceID(name, "mcp")
	}
	if name == "" {
		name = id
	}

	payload := gateway.Server{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(st.addDescription),
		Kind:        "mcp",
		Enabled:     true,
	}

	if st.addTab == 0 {
		cmd := strings.TrimSpace(st.addCommand)
		if cmd == "" {
			st.addError = "stdio 服务需要填写启动命令"
			return
		}
		args, err := parseStringSliceJSON(st.addArgsText)
		if err != nil {
			st.addError = "启动参数格式错误: " + err.Error()
			return
		}
		env, err := parseStringMapJSON(st.addEnvText)
		if err != nil {
			st.addError = "环境变量格式错误: " + err.Error()
			return
		}
		payload.Transport = "stdio"
		payload.Command = cmd
		payload.Args = args
		payload.Env = env
		payload.WorkDir = strings.TrimSpace(st.addWorkDir)
	} else {
		ep := strings.TrimSpace(st.addEndpoint)
		if ep == "" {
			st.addError = "请填写 MCP 服务地址 URL"
			return
		}
		headers, err := parseStringMapJSON(st.addHeadersText)
		if err != nil {
			st.addError = "请求头格式错误: " + err.Error()
			return
		}
		payload.Transport = "http"
		payload.Endpoint = ep
		payload.Headers = headers
	}

	st.addBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		check, err := u.Client.TestSource(ctx, payload)
		if err != nil {
			u.updateUI(func() {
				st.addBusy = false
				st.addError = formatAPIError(err)
			})
			return
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.addBusy = false
			st.showAddModal = false
			st.resetAddFields()
			st.enterDetail(id)
			u.OpenServiceDetail(id)
		})
		msg := check.Message
		if msg == "" {
			msg = "服务已连接并同步工具"
		}
		u.Notify(msg)
	}()
}

func (u *DesktopUI) submitAddOpenAPIService(st *servicesPageState) {
	if u.Client == nil || st.addBusy {
		return
	}
	st.addError = ""

	name := strings.TrimSpace(st.addName)
	id := strings.TrimSpace(st.addID)
	if id == "" {
		id = slugServiceID(name, "api")
	}
	if name == "" {
		name = id
	}
	baseURL := strings.TrimSpace(st.addOpenAPIBaseURL)
	if baseURL == "" {
		st.addError = "请填写 API Base URL"
		return
	}
	headers, err := parseStringMapJSON(st.addOpenAPIHeaders)
	if err != nil {
		st.addError = "请求头格式错误: " + err.Error()
		return
	}
	specBytes := []byte(strings.TrimSpace(st.addOpenAPISpecText))
	specURL := strings.TrimSpace(st.addOpenAPISpecURL)
	if len(specBytes) == 0 && specURL == "" {
		st.addError = "请粘贴 OpenAPI JSON/YAML 内容或填写文档 URL"
		return
	}

	srv := gateway.Server{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(st.addDescription),
		Kind:        "http",
		Transport:   "http",
		Endpoint:    baseURL,
		Headers:     headers,
		Enabled:     true,
	}
	opIDs := splitCommaList(st.addOpenAPIOpsText)

	st.addBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		count, err := u.Client.ImportOpenAPI(ctx, srv, specBytes, specURL, opIDs)
		if err != nil {
			u.updateUI(func() {
				st.addBusy = false
				st.addError = formatAPIError(err)
			})
			return
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.addBusy = false
			st.showAddModal = false
			st.resetAddFields()
			st.enterDetail(id)
			u.OpenServiceDetail(id)
		})
		u.Notify(fmt.Sprintf("已从 OpenAPI 导入 %d 个工具到 %s", count, name))
	}()
}

func (u *DesktopUI) saveConnForm(st *servicesPageState, orig gateway.Server, testSource bool) {
	if u.Client == nil || st.connBusy {
		return
	}
	st.connError = ""
	payload, err := st.buildServerFromConnForm(orig)
	if err != nil {
		st.connError = err.Error()
		return
	}
	st.connBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		var msg string
		if testSource {
			check, callErr := u.Client.TestSource(ctx, payload)
			if callErr != nil {
				u.updateUI(func() {
					st.connBusy = false
					st.connError = formatAPIError(callErr)
				})
				return
			}
			msg = check.Message
			if msg == "" {
				msg = "连接配置已保存并测试通过"
			}
		} else {
			if _, callErr := u.Client.SaveServer(ctx, payload); callErr != nil {
				u.updateUI(func() {
					st.connBusy = false
					st.connError = formatAPIError(callErr)
				})
				return
			}
			msg = "连接配置已保存"
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.connBusy = false
			st.connHydratedID = ""
		})
		u.Notify(msg)
	}()
}

func (u *DesktopUI) saveConnJSON(st *servicesPageState, orig gateway.Server) {
	if u.Client == nil || st.connBusy {
		return
	}
	st.connError = ""
	var payload gateway.Server
	if err := json.Unmarshal([]byte(st.connRawJSON), &payload); err != nil {
		st.connError = "JSON 格式无效: " + err.Error()
		return
	}
	if payload.ID == "" {
		payload.ID = orig.ID
	}
	st.connBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveServer(ctx, payload); err != nil {
			u.updateUI(func() {
				st.connBusy = false
				st.connError = formatAPIError(err)
			})
			return
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.connBusy = false
			st.connHydratedID = ""
		})
		u.Notify("服务 JSON 定义已保存")
	}()
}

func (u *DesktopUI) probeServerConnection(st *servicesPageState, server gateway.Server) {
	if u.Client == nil || st.connBusy {
		return
	}
	st.connBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		check, err := u.Client.TestSource(ctx, server)
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.connBusy = false
		})
		if err != nil {
			u.Notify("探活失败: " + formatAPIError(err))
			return
		}
		msg := check.Message
		if msg == "" {
			msg = "连接探活通过"
		}
		u.Notify(msg)
	}()
}

func (u *DesktopUI) saveEditedTool(st *servicesPageState, orig gateway.Tool) {
	if u.Client == nil || st.toolEditBusy {
		return
	}
	st.toolEditError = ""
	var payload gateway.Tool
	if st.toolEditTab == 1 {
		if err := json.Unmarshal([]byte(st.toolEditRawJSON), &payload); err != nil {
			st.toolEditError = "JSON 格式无效: " + err.Error()
			return
		}
	} else {
		payload = orig
		payload.Description = strings.TrimSpace(st.toolEditDesc)
		payload.Enabled = st.toolEditEnabled
		payload.ReadOnly = st.toolEditReadOnly
		payload.Actors = splitCommaList(st.toolEditActors)
	}

	st.toolEditBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveTool(ctx, payload); err != nil {
			u.updateUI(func() {
				st.toolEditBusy = false
				st.toolEditError = formatAPIError(err)
			})
			return
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.toolEditBusy = false
			st.showToolModal = false
		})
		u.Notify("工具定义已保存")
	}()
}

func (u *DesktopUI) submitAddHTTPTool(st *servicesPageState, serverID string) {
	if u.Client == nil || st.httpToolBusy {
		return
	}
	st.httpToolError = ""
	name := strings.TrimSpace(st.httpToolName)
	path := strings.TrimSpace(st.httpToolPath)
	desc := strings.TrimSpace(st.httpToolDesc)
	if name == "" || path == "" || desc == "" {
		st.httpToolError = "工具名称、相对路径与描述均为必填项"
		return
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(st.httpToolSchema), &schema); err != nil {
		st.httpToolError = "JSON Schema 格式无效: " + err.Error()
		return
	}
	payload := gateway.Tool{
		ID:          serverID + "." + name,
		ServerID:    serverID,
		Name:        name,
		Description: desc,
		Method:      st.httpToolMethod,
		Path:        path,
		ReadOnly:    st.httpToolReadOnly,
		InputSchema: schema,
		Actors:      []string{"developer"},
		Tags:        []string{},
		Enabled:     true,
	}

	st.httpToolBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveTool(ctx, payload); err != nil {
			u.updateUI(func() {
				st.httpToolBusy = false
				st.httpToolError = formatAPIError(err)
			})
			return
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.httpToolBusy = false
			st.showAddHTTPTool = false
		})
		u.Notify("HTTP 工具已创建并启用")
	}()
}

func (u *DesktopUI) loadMaintenanceOverview(st *servicesPageState, server gateway.Server) {
	if u.Client == nil || st.maintLoading {
		return
	}
	st.maintLoading = true
	st.maintError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ov, err := u.Client.Maintenance(ctx)
		u.updateUI(func() {
			st.maintLoading = false
			if err != nil {
				st.maintError = formatAPIError(err)
				return
			}
			st.maintOverview = &ov
			st.hydrateMaintSource(ov, server)
		})
	}()
}

func (u *DesktopUI) saveMaintenanceConfig(st *servicesPageState, server gateway.Server, runNow, force bool) {
	if u.Client == nil || st.maintBusy {
		return
	}
	st.maintError = ""
	interval, err := strconv.Atoi(strings.TrimSpace(st.maintIntervalStr))
	if err != nil || interval < 1 {
		interval = 60
	}
	timeout, err := strconv.Atoi(strings.TrimSpace(st.maintTimeoutStr))
	if err != nil || timeout < 30 {
		timeout = 900
	}
	format := "native"
	if server.Kind == "http" {
		format = "openapi"
	}
	if st.maintFormatIdx == 1 {
		format = "pi"
	}

	src := gateway.MaintenanceSource{
		ServerID:        server.ID,
		Format:          format,
		Files:           splitLines(st.maintFilesText),
		URLs:            splitLines(st.maintURLsText),
		OperationIDs:    splitCommaOrLineList(st.maintOpsText),
		Instructions:    strings.TrimSpace(st.maintInstructions),
		Enabled:         st.maintEnabled,
		AutoPublish:     st.maintAutoPublish,
		IntervalMinutes: interval,
		TimeoutSeconds:  timeout,
	}

	st.maintBusy = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := u.Client.SaveMaintenanceSource(ctx, src); err != nil {
			u.updateUI(func() {
				st.maintBusy = false
				st.maintError = formatAPIError(err)
			})
			return
		}
		if runNow {
			if _, err := u.Client.RunMaintenance(ctx, server.ID, force); err != nil {
				u.updateUI(func() {
					st.maintBusy = false
					st.maintError = formatAPIError(err)
				})
				return
			}
		}
		ov, ovErr := u.Client.Maintenance(ctx)
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.maintBusy = false
			if ovErr == nil {
				st.maintOverview = &ov
				st.hydrateMaintSource(ov, server)
			}
		})
		if runNow {
			u.Notify("已保存维护策略并加入检查队列")
		} else {
			u.Notify("维护策略已保存")
		}
	}()
}

func (u *DesktopUI) runMaintenanceAction(st *servicesPageState, server gateway.Server, okMsg string, fn func(context.Context) error) {
	if u.Client == nil || st.maintBusy {
		return
	}
	st.maintBusy = true
	st.maintError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := fn(ctx); err != nil {
			u.updateUI(func() {
				st.maintBusy = false
				st.maintError = formatAPIError(err)
			})
			return
		}
		ov, ovErr := u.Client.Maintenance(ctx)
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.maintBusy = false
			if ovErr == nil {
				st.maintOverview = &ov
			}
		})
		u.Notify(okMsg)
	}()
}

// --- Parsing helpers ---

func parseStringSliceJSON(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

func parseStringMapJSON(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]string{}, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]string{}
	}
	return out, nil
}

func splitCommaList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if v := strings.TrimSpace(l); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func splitCommaOrLineList(s string) []string {
	s = strings.ReplaceAll(s, "\n", ",")
	return splitCommaList(s)
}

func marshalMapOrDefault(v any) string {
	m, ok := v.(map[string]any)
	if !ok || len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	strMap := make(map[string]string, len(m))
	for _, k := range keys {
		strMap[k] = fmt.Sprint(m[k])
	}
	return PrettyJSON(strMap)
}

func marshalSliceOrDefault(v any) string {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return "[]"
	}
	strSlice := make([]string, 0, len(arr))
	for _, item := range arr {
		strSlice = append(strSlice, fmt.Sprint(item))
	}
	return PrettyJSON(strSlice)
}

func subtlePanel(c *ui.Context, minWidth, grow float32, fn func()) ui.Element {
	return ui.Column(c).
		Grow(grow).
		MinWidth(minWidth).
		Padding(14).
		Gap(8).
		Radius(6).
		Background(SailBgSubtle).
		Border(1, SailBorder).
		Children(fn)
}

func formatAPIError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Detail != "" {
		return apiErr.Message + "\n" + apiErr.Detail
	}
	return err.Error()
}
