package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type serviceToolEditState struct {
	show     bool
	tab      int // 0=属性与 Schema, 1=完整 JSON
	tool     *apitypes.Tool
	desc     string
	actors   string
	enabled  bool
	readOnly bool
	rawJSON  string
	busy     bool
	errMsg   string
}

func (s *serviceToolEditState) open(t apitypes.Tool) {
	copyTool := t
	*s = serviceToolEditState{
		show:     true,
		tool:     &copyTool,
		desc:     t.Description,
		actors:   strings.Join(t.Actors, ", "),
		enabled:  t.Enabled,
		readOnly: t.ReadOnly,
		rawJSON:  PrettyJSON(t),
	}
}

func (s *serviceToolEditState) buildPayload(orig apitypes.Tool) (apitypes.Tool, error) {
	if s.tab == 1 {
		var payload apitypes.Tool
		if err := json.Unmarshal([]byte(s.rawJSON), &payload); err != nil {
			return apitypes.Tool{}, errors.New(i18n.T("services.err.json_invalid", err.Error()))
		}
		return payload, nil
	}
	payload := orig
	payload.Description = strings.TrimSpace(s.desc)
	payload.Enabled = s.enabled
	payload.ReadOnly = s.readOnly
	payload.Actors = splitCommaList(s.actors)
	return payload, nil
}

type serviceHTTPToolState struct {
	show     bool
	name     string
	method   string
	path     string
	desc     string
	readOnly bool
	schema   string
	busy     bool
	errMsg   string
}

func defaultServiceHTTPToolState() serviceHTTPToolState {
	return serviceHTTPToolState{
		method:   "GET",
		readOnly: true,
		schema:   "{\n  \"type\": \"object\",\n  \"properties\": {},\n  \"required\": [],\n  \"additionalProperties\": false\n}",
	}
}

func (s *serviceHTTPToolState) open() {
	*s = defaultServiceHTTPToolState()
	s.show = true
}

func (s *serviceHTTPToolState) buildPayload(serverID string) (apitypes.Tool, error) {
	name := strings.TrimSpace(s.name)
	path := strings.TrimSpace(s.path)
	desc := strings.TrimSpace(s.desc)
	if name == "" || path == "" || desc == "" {
		return apitypes.Tool{}, errors.New(i18n.T("services.err.http_tool_required"))
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(s.schema), &schema); err != nil {
		return apitypes.Tool{}, errors.New(i18n.T("services.err.schema_invalid", err.Error()))
	}
	return apitypes.Tool{
		ID:          serverID + "." + name,
		ServerID:    serverID,
		Name:        name,
		Description: desc,
		Method:      s.method,
		Path:        path,
		ReadOnly:    s.readOnly,
		InputSchema: schema,
		Actors:      []string{"developer"},
		Tags:        []string{},
		Enabled:     true,
	}, nil
}

type serviceToolsPage struct {
	items      []apitypes.Tool
	page       int
	totalPages int
	start      int
	end        int
	hasFooter  bool
}

func allToolIDs(srvTools []apitypes.Tool) []string {
	ids := make([]string, 0, len(srvTools))
	for _, t := range srvTools {
		ids = append(ids, t.ID)
	}
	return ids
}

func filterServiceTools(srvTools []apitypes.Tool, filter int, query string) []apitypes.Tool {
	q := strings.ToLower(strings.TrimSpace(query))
	var visible []apitypes.Tool
	for _, t := range srvTools {
		if !toolMatchesFilter(t, filter) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(t.Name+" "+t.Description+" "+t.ID), q) {
			continue
		}
		visible = append(visible, t)
	}
	return visible
}

func toolMatchesFilter(t apitypes.Tool, filter int) bool {
	switch filter {
	case 1:
		return t.Enabled
	case 2:
		return !t.Enabled
	case 3:
		return t.ReadOnly
	default:
		return true
	}
}

func paginateServiceTools(visible []apitypes.Tool, page, pageSize int) serviceToolsPage {
	if pageSize < 1 {
		pageSize = servicesToolPageSize
	}
	totalPages := (len(visible) + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pageSize
	if start > len(visible) {
		start = len(visible)
	}
	end := start + pageSize
	if end > len(visible) {
		end = len(visible)
	}
	return serviceToolsPage{
		items:      visible[start:end],
		page:       page,
		totalPages: totalPages,
		start:      start,
		end:        end,
		hasFooter:  len(visible) > pageSize,
	}
}

func buildWhitelistWithToolToggled(srvTools []apitypes.Tool, targetID string, enable bool) []string {
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
	return ids
}

// --- Detail Subtab 0: Tool Whitelist ---

func (u *DesktopUI) renderServiceToolsTab(c *ui.Context, st *servicesPageState, server apitypes.Server, srvTools []apitypes.Tool, enabledCount int) {
	SailFlushCard(c, func() {
		u.renderServiceToolsToolbar(c, st, server, srvTools, enabledCount)

		visible := filterServiceTools(srvTools, st.toolFilter, st.toolQuery)
		if len(visible) == 0 {
			u.renderServiceToolsEmptyState(c, st, server, len(srvTools) > 0)
			return
		}

		pg := paginateServiceTools(visible, st.toolPage, servicesToolPageSize)
		st.toolPage = pg.page

		// Hairline-divided tool rows (max 3 core anchors)
		for idx, item := range pg.items {
			isLast := idx == len(pg.items)-1 && !pg.hasFooter
			u.renderServiceToolRow(c, st, server, srvTools, item, isLast)
		}

		if pg.hasFooter {
			renderServiceToolsPagination(c, st, pg, len(visible))
		}
	})
}

func (u *DesktopUI) renderServiceToolsToolbar(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	srvTools []apitypes.Tool,
	enabledCount int,
) {
	ui.Row(c).
		FillWidth().
		Padding(14, 20).
		Gap(10).
		BorderWidth(0, 0, 1, 0).
		BorderColor(SailDivider).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Wrap().
		Children(func() {
			ui.Row(c).Grow(1).MinWidth(240).Gap(10).AlignItems(ui.Center).Children(func() {
				if SailSearchField(c.Key("detail-tool-search"), &st.toolQuery).
					Placeholder(i18n.T("services.tools.search")).
					Grow(1).
					MinWidth(0).
					Changed() {
					st.toolPage = 1
				}
				if SailSegmented(
					c.Key("detail-tool-filter"),
					&st.toolFilter,
					fmt.Sprintf(i18n.T("services.tools.filter_all"), len(srvTools)),
					fmt.Sprintf(i18n.T("services.tools.filter_enabled"), enabledCount),
					fmt.Sprintf(i18n.T("services.tools.filter_disabled"), len(srvTools)-enabledCount),
					i18n.T("services.tools.filter_read_only"),
				).Changed() {
					st.toolPage = 1
				}
			})

			if !server.Demo && len(srvTools) > 0 {
				u.renderServiceToolsBatchButtons(c, st, server.ID, srvTools)
			}
		})
}

func (u *DesktopUI) renderServiceToolsBatchButtons(c *ui.Context, st *servicesPageState, serverID string, srvTools []apitypes.Tool) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if SailSmallButton(c.Key("batch-ro"), i18n.T("services.tools.batch_read_only")).
			Disabled(st.toolBatchBusy).
			Clicked() {
			u.applyServerToolWhitelist(st, serverID, readOnlyToolIDs(srvTools), i18n.T("services.toast.opened_read_only"))
		}
		if SailSmallButton(c.Key("batch-all-on"), i18n.T("services.tools.batch_open_all")).
			Disabled(st.toolBatchBusy).
			Clicked() {
			u.applyServerToolWhitelist(st, serverID, allToolIDs(srvTools), i18n.T("services.toast.opened_all_tools"))
		}
		if SailSmallButton(c.Key("batch-all-off"), i18n.T("services.tools.batch_close_all")).
			Disabled(st.toolBatchBusy).
			Clicked() {
			u.applyServerToolWhitelist(st, serverID, []string{}, i18n.T("services.toast.closed_all_tools"))
		}
	})
}

func (u *DesktopUI) renderServiceToolsEmptyState(c *ui.Context, st *servicesPageState, server apitypes.Server, hasTools bool) {
	ui.Column(c).FillWidth().Padding(20).Children(func() {
		if hasTools {
			SailEmptyState(c, i18n.T("services.tools.no_match"), "", i18n.T("services.tools.clear_filters"), func() {
				st.toolQuery = ""
				st.toolFilter = 0
				st.toolPage = 1
			})
			return
		}
		if server.Kind == "http" && !server.Demo {
			SailEmptyState(c, i18n.T("services.tools.empty"), "", i18n.T("services.detail.add_http_tool"), func() { st.httpTool.open() })
			return
		}
		SailEmptyState(c, i18n.T("services.tools.not_synced"), "", i18n.T("services.detail.sync_tools"), func() { u.syncServerTools(st, server) })
	})
}

func (u *DesktopUI) renderServiceToolRow(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	srvTools []apitypes.Tool,
	t apitypes.Tool,
	isLast bool,
) {
	row := ui.Row(c.Key("tool-row-"+t.ID)).
		FillWidth().
		Padding(13, 20).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(16)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	if row.Hovered() {
		row.Background(SailBgHover)
	}
	row.Children(func() {
		// Anchor 1 & 2: Name (+ RW risk badge if write) + Cleaned 1-sentence summary
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				nameBtn := ui.ButtonBase(c.Key("tool-open-" + t.ID)).Cursor(ui.CursorPointer)
				nameBtn.Children(func() {
					ui.Text(c, t.Name).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				})
				if nameBtn.Clicked() {
					st.toolEdit.open(t)
				}
				if !t.ReadOnly {
					SailBadge(c, i18n.T("common.write_operation"), ToneWarning)
				}
				if u.State != nil && toolMatchesApproveRule(t, u.State.Policies.Rules) {
					SailBadge(c, i18n.T("policy.needs-approval"), ToneWarning)
				}
			})
			SailDescSummary(c, t.Description, i18n.T("services.tools.no_description"), 1)
		})

		// Anchor 3: Enable Switch + Edit
		ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
			if !server.Demo {
				swVal := t.Enabled
				sw := ui.Switch(c.Key("tool-sw-"+t.ID), &swVal).
					Label(i18n.T("services.tools.toggle", t.Name)).
					Disabled(st.toolBatchBusy)
				if sw.Changed() {
					u.toggleSingleToolWhitelist(st, server.ID, srvTools, t.ID, swVal)
				}
			}
			if serverIsCLI(server) {
				return // controlled CLI contracts are gateway-generated; only enablement is editable
			}
			if SailSmallButton(c.Key("tool-edit-"+t.ID), i18n.T("common.edit")).Clicked() {
				st.toolEdit.open(t)
			}
		})
	})
}

func renderServiceToolsPagination(c *ui.Context, st *servicesPageState, pg serviceToolsPage, totalVisible int) {
	ui.Row(c).FillWidth().Padding(12, 20).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		ui.Text(c, fmt.Sprintf("%d–%d / %d", pg.start+1, pg.end, totalVisible)).
			FontSize(12).
			FontFeatures("tnum").
			TextColor(SailInkMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf(i18n.T("services.tools.pager"), st.toolPage, pg.totalPages)).
				FontSize(12).
				FontFeatures("tnum").
				TextColor(SailInkMuted)
			if SailSmallButton(c.Key("tool-prev"), i18n.T("common.prev_page")).Disabled(st.toolPage <= 1).Clicked() {
				st.toolPage--
			}
			if SailSmallButton(c.Key("tool-next"), i18n.T("common.next_page")).Disabled(st.toolPage >= pg.totalPages).Clicked() {
				st.toolPage++
			}
		})
	})
}

// --- Tool Edit Modal & Add HTTP Tool Modal ---

func (u *DesktopUI) renderToolEditModal(c *ui.Context, st *servicesPageState) {
	t := *st.toolEdit.tool
	ui.DialogBase(c.Key("tool-edit-dialog-"+t.ID), &st.toolEdit.show, func(backdrop, panel ui.Element) {
		backdrop.Background(SailModalBackdrop)
		panel.Width(640).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, t.Name).Font("monospace").FontSize(16).Bold().TextColor(SailInkStrong)
				ui.Text(c, t.ID).Font("monospace").FontSize(12).TextColor(SailInkMuted)
			})
			if SailSmallButton(c.Key("tool-modal-close"), i18n.T("common.close")).Clicked() {
				st.toolEdit.show = false
			}
		})

		if ui.Segmented(c.Key("tool-edit-tabs"), &st.toolEdit.tab, i18n.T("services.tool.tab_props"), i18n.T("services.tool.tab_json")).Changed() && st.toolEdit.tab == 1 {
			updated := t
			updated.Description = st.toolEdit.desc
			updated.Enabled = st.toolEdit.enabled
			updated.ReadOnly = st.toolEdit.readOnly
			updated.Actors = splitCommaList(st.toolEdit.actors)
			st.toolEdit.rawJSON = PrettyJSON(updated)
		}

		if st.toolEdit.errMsg != "" {
			SailAlertBox(c, ToneDanger, i18n.T("services.tool.error_title"), st.toolEdit.errMsg)
		}

		ui.Scroll(c.Key("tool-edit-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(14).Children(func() {
			if st.toolEdit.tab == 1 {
				ui.TextArea(c.Key("tool-raw-json"), &st.toolEdit.rawJSON).
					Font("monospace").
					FontSize(12).
					Height(340).
					FillWidth()
			} else {
				u.renderToolEditFormFields(c, st, t)
			}
		})

		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailButton(c.Key("tool-edit-cancel"), i18n.T("common.cancel")).Disabled(st.toolEdit.busy).Clicked() {
				st.toolEdit.show = false
			}
			lbl := i18n.T("services.tool.save")
			if st.toolEdit.busy {
				lbl = i18n.T("common.saving")
			}
			if SailPrimaryButton(c.Key("tool-edit-save"), lbl).Disabled(st.toolEdit.busy).Clicked() {
				u.saveEditedTool(st, t)
			}
		})
	})
}

func (u *DesktopUI) renderToolEditFormFields(c *ui.Context, st *servicesPageState, t apitypes.Tool) {
	ui.Row(c).FillWidth().Gap(14).Children(func() {
		subtlePanel(c, 160, 1, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, i18n.T("services.tool.open_section")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.Switch(c.Key("tool-edit-enabled"), &st.toolEdit.enabled).Label(i18n.T("services.field.open_tools"))
			})
		})
		subtlePanel(c, 160, 1, func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, i18n.T("services.tool.read_only_section")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.Switch(c.Key("tool-edit-ro"), &st.toolEdit.readOnly).Label(i18n.T("services.tool.read_only_label"))
			})
		})
	})

	if t.Method != "" && t.Path != "" {
		SailKeyValueRow(c, i18n.T("services.tool.http_endpoint"), t.Method+" "+t.Path, true)
	}

	if strings.TrimSpace(st.toolEdit.desc) != "" {
		SailSubtleCard(c, func() {
			ui.Text(c, i18n.T("services.tool.preview")).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
			SailDescriptionDetail(c, "modal-desc-"+t.ID, st.toolEdit.desc)
		})
	}

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.tool.description")).
			FontSize(12.5).
			FontWeight(600).
			TextColor(SailInkBody)
		ui.TextArea(c.Key("tool-edit-desc"), &st.toolEdit.desc).
			Height(88).
			FillWidth()
	})

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.tool.actors")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("tool-edit-actors"), &st.toolEdit.actors).
			Font("monospace").
			Placeholder("developer, observer").
			FillWidth()
	})

	SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(t.InputSchema), func() {
		u.Notify(i18n.T("services.tool.schema_copied"))
	})
}

func (u *DesktopUI) renderAddHTTPToolModal(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.DialogBase(c.Key("add-http-tool-dialog-"+server.ID), &st.httpTool.show, func(backdrop, panel ui.Element) {
		backdrop.Background(SailModalBackdrop)
		panel.Width(600).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("services.detail.add_http_tool")).FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("http-tool-close"), i18n.T("common.close")).Clicked() {
				st.httpTool.show = false
			}
		})

		if st.httpTool.errMsg != "" {
			SailAlertBox(c, ToneDanger, i18n.T("services.http_tool.error_title"), st.httpTool.errMsg)
		}

		ui.Scroll(c.Key("http-tool-scroll")).Grow(1).FillWidth().MaxHeight(440).Gap(10).Children(func() {
			renderAddHTTPToolFields(c, st)
		})

		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailButton(c.Key("http-tool-cancel"), i18n.T("common.cancel")).Disabled(st.httpTool.busy).Clicked() {
				st.httpTool.show = false
			}
			lbl := i18n.T("services.http_tool.save")
			if st.httpTool.busy {
				lbl = i18n.T("common.saving")
			}
			if SailPrimaryButton(c.Key("http-tool-submit"), lbl).Disabled(st.httpTool.busy).Clicked() {
				u.submitAddHTTPTool(st, server.ID)
			}
		})
	})
}

func renderAddHTTPToolFields(c *ui.Context, st *servicesPageState) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(2).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.http_tool.name_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			SailTextInput(c.Key("http-tool-name"), &st.httpTool.name).
				Font("monospace").
				Placeholder("search_documents").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.http_tool.method")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			if SailSelect(c.Key("http-tool-method"), &st.httpTool.method, []string{"GET", "POST", "PUT", "PATCH", "DELETE"}).
				FillWidth().
				Changed() {
				st.httpTool.readOnly = st.httpTool.method == "GET"
			}
		})
	})

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.http_tool.path_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("http-tool-path"), &st.httpTool.path).
			Font("monospace").
			Placeholder("/documents/search").
			FillWidth()
	})

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.http_tool.description_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("http-tool-desc"), &st.httpTool.desc).FillWidth()
	})

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.http_tool.schema")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextArea(c.Key("http-tool-schema"), &st.httpTool.schema).
			Font("monospace").
			FontSize(12).
			Height(150).
			FillWidth()
	})

	ui.Checkbox(c.Key("http-tool-ro"), &st.httpTool.readOnly, i18n.T("services.tool.read_only_label"))
}

func (u *DesktopUI) syncServerTools(st *servicesPageState, server apitypes.Server) {
	if u.Client == nil || st.syncingServerID != "" {
		return
	}
	st.syncingServerID = server.ID
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.syncingServerID = "" })
		timeout := 35 * time.Second
		if server.OAuth {
			timeout = 305 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		imported, err := u.Client.SyncServer(ctx, server.ID)
		if err != nil {
			return err
		}
		if err := u.RefreshSync(); err != nil {
			return err
		}
		u.Notify(fmt.Sprintf(i18n.T("services.toast.synced"), imported))
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
		u.Notify(fmt.Sprintf(i18n.T("services.toast.whitelist_applied"), msg, len(ids)))
		return nil
	})
}

func (u *DesktopUI) toggleSingleToolWhitelist(st *servicesPageState, serverID string, srvTools []apitypes.Tool, targetID string, enable bool) {
	ids := buildWhitelistWithToolToggled(srvTools, targetID, enable)
	label := i18n.T("services.toast.tools_closed")
	if enable {
		label = i18n.T("services.toast.tools_opened")
	}
	u.applyServerToolWhitelist(st, serverID, ids, label)
}

func (u *DesktopUI) saveEditedTool(st *servicesPageState, orig apitypes.Tool) {
	if u.Client == nil || st.toolEdit.busy {
		return
	}
	st.toolEdit.errMsg = ""
	payload, err := st.toolEdit.buildPayload(orig)
	if err != nil {
		st.toolEdit.errMsg = err.Error()
		return
	}

	st.toolEdit.busy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveTool(ctx, payload); err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.toolEdit.busy = false })
				return err
			}
			u.updateUI(func() {
				st.toolEdit.busy = false
				st.toolEdit.errMsg = formatAPIError(err)
			})
			return nil
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.toolEdit.busy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.toolEdit.busy = false
			st.toolEdit.show = false
		})
		u.Notify(i18n.T("services.toast.tool_saved"))
		return nil
	})
}

func (u *DesktopUI) submitAddHTTPTool(st *servicesPageState, serverID string) {
	if u.Client == nil || st.httpTool.busy {
		return
	}
	st.httpTool.errMsg = ""
	payload, err := st.httpTool.buildPayload(serverID)
	if err != nil {
		st.httpTool.errMsg = err.Error()
		return
	}

	st.httpTool.busy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveTool(ctx, payload); err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.httpTool.busy = false })
				return err
			}
			u.updateUI(func() {
				st.httpTool.busy = false
				st.httpTool.errMsg = formatAPIError(err)
			})
			return nil
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.httpTool.busy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.httpTool.busy = false
			st.httpTool.show = false
		})
		u.Notify(i18n.T("services.toast.http_tool_created"))
		return nil
	})
}
