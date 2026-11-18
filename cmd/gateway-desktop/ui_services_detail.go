package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type serviceDetailState struct {
	detailServerID    string
	detailSubtab      int // 0=工具白名单, 1=连接与认证配置, 2=健康与维护概览
	toolQuery         string
	toolFilter        int // 0=全部, 1=已启用, 2=未启用, 3=仅只读
	toolPage          int
	toolBatchBusy     bool
	confirmDeactivate bool
	deactivateBusy    bool
}

type serviceConnFormState struct {
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
	connOAuth            bool
	connRedactEmails     bool
	connRedactFieldsText string
	connRawJSON          string
	connBusy             bool
	connError            string
}

type connSaveMode uint8

const (
	connSaveOnly connSaveMode = iota
	connSaveAndTest
)

func collectServerTools(tools []apitypes.Tool, serverID string) ([]apitypes.Tool, serviceToolCounts) {
	var srvTools []apitypes.Tool
	for _, t := range tools {
		if t.ServerID == serverID {
			srvTools = append(srvTools, t)
		}
	}
	return srvTools, summarizeServiceTools(srvTools)
}

func (u *DesktopUI) renderServiceDetail(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	st.enterDetail(server.ID)

	srvTools, counts := collectServerTools(u.State.Registry.Tools, server.ID)
	readiness := u.readinessForServer(server, srvTools)

	u.renderServiceDetailTopBar(c, st, server)

	SailSectionHeader(c, server.Name, "", func() {
		u.renderServiceDetailHeaderActions(c, st, server, readiness)
	})

	switch ui.AlertDialog(
		c.Key("deactivate-alert-"+server.ID),
		&st.confirmDeactivate,
		fmt.Sprintf(i18n.T("services.confirm.deactivate_title"), server.Name),
		i18n.T("services.confirm.deactivate_body"),
		i18n.T("common.cancel"),
		i18n.T("services.confirm.deactivate_confirm"),
	) {
	case 1:
		u.deactivateService(st, server)
	}

	u.renderServiceDetailSummaryCard(c, server, readiness, counts)
	u.renderServiceDetailReadinessBanner(c, st, server, readiness, srvTools)
	u.renderServiceDetailSubtab(c, st, server, srvTools, counts.enabled)
}

func (u *DesktopUI) renderServiceDetailTopBar(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		backBtn := ui.ButtonBase(c.Key("svc-back-btn")).
			Padding(5, 10).
			Radius(6).
			Background(SailBgSubtle).
			Cursor(ui.CursorPointer)
		if backBtn.Hovered() {
			backBtn.Background(SailBgHover)
		}
		backBtn.Children(func() {
			ui.Text(c, i18n.T("services.detail.back")).FontSize(12.5).FontWeight(500).TextColor(SailInkBody).SingleLine()
		})
		if backBtn.Clicked() {
			u.clearActiveService()
			st.detailServerID = ""
		}

		if SailSmallButton(c.Key("detail-open-catalog-"+server.ID), i18n.T("services.detail.open_in_catalog")).Clicked() {
			u.OpenCatalogFilter(server.ID, "")
		}
	})
}

func (u *DesktopUI) renderServiceDetailHeaderActions(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	readiness serviceReadiness,
) {
	ui.Text(c, serviceProtocolTag(server)).FontSize(12).TextColor(SailInkMuted).SingleLine()
	if server.Demo {
		ui.Text(c, i18n.T("common.demo")).FontSize(12).TextColor(SailInkFaint).SingleLine()
		return
	}

	if readiness.BadgeLabel != "" {
		SailBadgeDot(c, readiness.BadgeLabel, readiness.BadgeTone)
	} else {
		SailQuietStatus(c, readiness.StatusLabel, readiness.DotColor, SailInkBody)
	}

	enabledVal := server.Enabled
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		swLbl := i18n.T("common.disabled")
		if enabledVal {
			swLbl = i18n.T("common.enabled")
		}
		ui.Text(c, swLbl).FontSize(12).TextColor(SailInkMuted).SingleLine()
		sw := ui.Switch(c.Key("detail-srv-sw-"+server.ID), &enabledVal).
			Label(i18n.T("services.detail.enable_label")).
			Disabled(st.toggleBusyID == server.ID)
		if sw.Changed() {
			u.toggleServerEnabled(st, server, enabledVal)
		}
	})

	probeLabel := i18n.T("common.test_connection")
	if st.connBusy {
		probeLabel = i18n.T("common.testing")
	}
	if SailSmallButton(c.Key("detail-probe-"+server.ID), probeLabel).
		Disabled(st.connBusy || st.syncingServerID != "").
		Clicked() {
		u.probeServerConnection(st, server)
	}

	if server.Kind == "mcp" {
		syncLabel := i18n.T("services.detail.sync_tools")
		if st.syncingServerID == server.ID {
			syncLabel = i18n.T("common.syncing")
		}
		if SailSmallButton(c.Key("detail-sync-"+server.ID), syncLabel).
			Disabled(st.syncingServerID != "" || st.connBusy).
			Clicked() {
			u.syncServerTools(st, server)
		}
	}

	if server.Kind == "http" {
		if SailPrimaryButton(c.Key("detail-add-http-tool"), i18n.T("services.detail.add_http_tool")).
			Padding(5, 11).
			FontSize(12.5).
			Clicked() {
			st.httpTool.open()
		}
	}

	if SailDangerButton(c.Key("detail-del-btn-"+server.ID), i18n.T("common.disable")).
		Disabled(st.deactivateBusy).
		Clicked() {
		st.confirmDeactivate = true
	}
}

func (u *DesktopUI) renderServiceDetailSummaryCard(
	c *ui.Context,
	server apitypes.Server,
	readiness serviceReadiness,
	counts serviceToolCounts,
) {
	target := ServerTargetText(server)
	SailCard(c, func() {
		if strings.TrimSpace(server.Description) != "" {
			SailDescriptionDetail(c, "svc-desc-"+server.ID, server.Description)
			ui.Box(c).FillWidth().Height(1).Background(SailDivider)
		}

		ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Center).Justify(ui.SpaceBetween).Wrap().Children(func() {
			ui.Row(c).Gap(24).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, i18n.T("services.field.service_id")).FontSize(11.5).TextColor(SailInkMuted)
					ui.Text(c, server.ID).Font("monospace").FontSize(12.5).TextColor(SailInkStrong).Selectable()
				})

				ui.Column(c).MinWidth(180).MaxWidth(480).Gap(3).Children(func() {
					lbl := i18n.T("services.field.endpoint")
					if server.Kind == "mcp" && server.Transport == "stdio" {
						lbl = i18n.T("services.field.command")
					}
					ui.Text(c, lbl).FontSize(11.5).TextColor(SailInkMuted)
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, target).Font("monospace").FontSize(12.5).TextColor(SailInkStrong).SingleLine().TruncateMiddle().Tooltip(target).Selectable()
						if SailSmallButton(c.Key("detail-copy-target"), i18n.T("common.copy")).Padding(2, 7).FontSize(11).Clicked() {
							c.WriteClipboard(target)
							u.Notify(i18n.T("services.toast.endpoint_copied"))
						}
					})
				})
			})

			renderServiceDetailMetrics(c, readiness, counts)
		})
	})
}

func renderServiceDetailMetrics(c *ui.Context, readiness serviceReadiness, counts serviceToolCounts) {
	ui.Row(c).Gap(18).AlignItems(ui.Center).Wrap().Children(func() {
		if readiness.CredSummary != "" {
			ui.Column(c).Gap(3).AlignItems(ui.End).Children(func() {
				ui.Text(c, i18n.T("services.field.credentials")).FontSize(11.5).TextColor(SailInkMuted)
				credColor := SailInkStrong
				if readiness.CredDanger {
					credColor = SailDangerInk
				}
				ui.Text(c, readiness.CredSummary).
					FontSize(12.5).
					FontWeight(600).
					TextColor(credColor).
					SingleLine()
			})
		}

		ui.Column(c).Gap(3).AlignItems(ui.End).Children(func() {
			ui.Text(c, i18n.T("services.field.open_tools")).FontSize(11.5).TextColor(SailInkMuted)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, fmt.Sprintf("%d / %d", counts.enabled, counts.total)).
					FontSize(13.5).
					FontWeight(600).
					FontFeatures("tnum").
					TextColor(SailInkStrong)
				if counts.writable > 0 {
					SailBadge(c, fmt.Sprintf(i18n.T("common.write_badge"), counts.writable), ToneWarning)
				}
			})
		})

		if readiness.CheckDisplay != checkDisplayNone && readiness.CheckLabel != "" {
			ui.Column(c).Gap(3).AlignItems(ui.End).Children(func() {
				ui.Text(c, i18n.T("common.last_check")).FontSize(11.5).TextColor(SailInkMuted)
				renderServiceCheckBadge(c, readiness)
			})
		}
	})
}

func renderServiceCheckBadge(c *ui.Context, readiness serviceReadiness) {
	switch readiness.CheckDisplay {
	case checkDisplayDanger:
		SailBadge(c, readiness.CheckLabel, ToneDanger)
	case checkDisplayWarn:
		SailBadge(c, readiness.CheckLabel, ToneWarning)
	case checkDisplaySuccess:
		SailQuietStatus(c, readiness.CheckLabel, SailSuccessInk, SailInkBody)
	default:
		ui.Text(c, readiness.CheckLabel).FontSize(12).Font("monospace").TextColor(SailInkMuted).SingleLine()
	}
}

func (u *DesktopUI) renderServiceDetailReadinessBanner(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	readiness serviceReadiness,
	srvTools []apitypes.Tool,
) {
	renderReadinessActionBanner(
		c,
		"detail-readiness-"+server.ID,
		readiness,
		st.connBusy || st.toolBatchBusy || st.credentialBusy,
		func() {
			switch readiness.AlertAction {
			case readinessActionFillCreds:
				st.detailSubtab = 1
				st.connMode = 0
			case readinessActionOAuthSignIn, readinessActionProbe:
				u.probeServerConnection(st, server)
			case readinessActionEnableReadOnly:
				u.applyServerToolWhitelist(st, server.ID, readOnlyToolIDs(srvTools), i18n.T("services.toast.opened_read_only"))
			}
		},
		func(upg apitypes.OAuthUpgradeInfo) {
			u.switchServerToOAuth(st, server, upg)
		},
	)
}

func (u *DesktopUI) renderServiceDetailSubtab(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	srvTools []apitypes.Tool,
	enabledCount int,
) {
	if !server.Demo {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Segmented(
				c.Key("detail-subtab-"+server.ID),
				&st.detailSubtab,
				fmt.Sprintf(i18n.T("services.tools.whitelist_tab"), len(srvTools)),
				i18n.T("services.tabs.connection"),
				i18n.T("services.tabs.health"),
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

// --- Detail Subtab 1: Connection & Auth Config ---

func (st *serviceConnFormState) hydrateConnForm(server apitypes.Server) {
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
	st.connOAuth = server.OAuth
	st.connRedactEmails = server.RedactEmails
	st.connRedactFieldsText = strings.Join(server.RedactFields, ", ")
	st.connRawJSON = PrettyJSON(server)
	st.connError = ""
}

func (st *serviceConnFormState) buildServerFromConnForm(orig apitypes.Server) (apitypes.Server, error) {
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
			return out, fmt.Errorf("%s: %w", i18n.T("services.err.args_format"), err)
		}
		env, err := parseStringMapJSON(st.connEnvText)
		if err != nil {
			return out, fmt.Errorf("%s: %w", i18n.T("services.err.env_format"), err)
		}
		out.Args = args
		out.Env = env
		out.OAuth = false
		out.Endpoint = ""
		out.Headers = nil
		return out, nil
	}

	if out.Kind == "http" {
		out.Transport = "http"
		out.OAuth = false
	} else {
		if out.Transport == "" {
			out.Transport = "http"
		}
		out.OAuth = st.connOAuth
	}
	out.Endpoint = strings.TrimSpace(st.connEndpoint)
	headers, err := parseStringMapJSON(st.connHeadersText)
	if err != nil {
		return out, fmt.Errorf("%s: %w", i18n.T("services.err.headers_format"), err)
	}
	out.Headers = headers
	out.Args = nil
	out.Env = nil
	return out, nil
}

func (u *DesktopUI) renderServiceConnectionTab(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	if st.connHydratedID != server.ID {
		st.hydrateConnForm(server)
	}
	cachedCreds := u.cachedServerCredentials(server)
	credsFirst := len(serverReferencedEnvVars(server)) > 0 || serverUsesOAuth(server) || (cachedCreds != nil && cachedCreds.OAuthUpgrade != nil)
	if credsFirst {
		u.renderServerCredentials(c, st, server)
	}

	SailCard(c, func() {
		SailCardTitle(
			c,
			i18n.T("services.tabs.connection"),
			i18n.T("services.conn.subtitle"),
			func() {
				if ui.Segmented(c.Key("conn-mode-seg"), &st.connMode, i18n.T("services.conn.mode_form"), i18n.T("services.conn.mode_json")).Changed() && st.connMode == 1 {
					if built, err := st.buildServerFromConnForm(server); err == nil {
						st.connRawJSON = PrettyJSON(built)
					} else {
						st.connRawJSON = PrettyJSON(server)
					}
				}
			},
		)

		if st.connError != "" {
			SailAlertBox(c, ToneDanger, i18n.T("services.conn.error_title"), st.connError)
		}

		if st.connMode == 1 {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, i18n.T("services.conn.json_section")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("conn-raw-json-"+server.ID), &st.connRawJSON).
					Font("monospace").
					FontSize(12).
					Height(300).
					FillWidth()
			})
		} else {
			renderServiceConnectionForm(c, st, server)
		}

		u.renderServiceConnectionFooter(c, st, server)
	})

	if !credsFirst {
		u.renderServerCredentials(c, st, server)
	}
}

func renderServiceConnectionForm(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Row(c).FillWidth().Gap(14).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.display_name")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("conn-name-"+server.ID), &st.connName).FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.description")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("conn-desc-"+server.ID), &st.connDesc).FillWidth()
		})
	})

	if server.Kind == "mcp" && server.Transport == "stdio" {
		renderServiceConnectionStdioFields(c, st, server.ID)
	} else {
		renderServiceConnectionHTTPFields(c, st, server)
	}

	// Redaction controls
	ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c.Key("conn-redact-emails-"+server.ID), &st.connRedactEmails, i18n.T("services.conn.redact_emails"))
		ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("services.conn.redact_fields")).FontSize(12).TextColor(SailInkMuted).Shrink(0)
			ui.TextInput(c.Key("conn-redact-fields-"+server.ID), &st.connRedactFieldsText).
				Placeholder("user.ssn, token").
				Font("monospace").
				Grow(1).
				MinWidth(0)
		})
	})
}

func renderServiceConnectionStdioFields(c *ui.Context, st *servicesPageState, serverID string) {
	ui.Row(c).FillWidth().Gap(14).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.command")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("conn-cmd-"+serverID), &st.connCommand).
				Font("monospace").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.workdir")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("conn-cwd-"+serverID), &st.connWorkDir).
				Font("monospace").
				FillWidth()
		})
	})

	ui.Row(c).FillWidth().Gap(14).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.args")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("conn-args-"+serverID), &st.connArgsText).
				Font("monospace").
				FontSize(12).
				Height(104).
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.env")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("conn-env-"+serverID), &st.connEnvText).
				Font("monospace").
				FontSize(12).
				Height(104).
				FillWidth()
		})
	})
}

func renderServiceConnectionHTTPFields(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		lbl := "Base URL"
		if server.Kind == "mcp" {
			lbl = "Endpoint URL"
		}
		ui.Text(c, lbl).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("conn-ep-"+server.ID), &st.connEndpoint).
			Font("monospace").
			FillWidth()
	})

	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.field.headers")).
			FontSize(12.5).
			FontWeight(600).
			TextColor(SailInkBody)
		ui.TextArea(c.Key("conn-hdrs-"+server.ID), &st.connHeadersText).
			Font("monospace").
			FontSize(12).
			Height(104).
			FillWidth()
	})

	if server.Kind == "mcp" {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Checkbox(c.Key("conn-oauth-"+server.ID), &st.connOAuth, i18n.T("services.conn.oauth_checkbox"))
			if st.connOAuth {
				ui.Text(c, i18n.T("services.conn.oauth_header_hint")).FontSize(11.5).TextColor(SailInkMuted)
			}
		})
	}
}

func (u *DesktopUI) renderServiceConnectionFooter(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Row(c).FillWidth().Padding(6, 0, 0, 0).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if SailButton(c.Key("conn-reset-"+server.ID), i18n.T("common.reset")).
			Disabled(st.connBusy).
			Clicked() {
			st.hydrateConnForm(server)
		}
		if st.connMode == 1 {
			if SailPrimaryButton(c.Key("conn-save-json-"+server.ID), i18n.T("services.conn.save_json")).
				Disabled(st.connBusy).
				Clicked() {
				u.saveConnJSON(st, server)
			}
			return
		}
		if SailButton(c.Key("conn-save-only-"+server.ID), i18n.T("services.conn.save_only")).
			Disabled(st.connBusy).
			Clicked() {
			u.saveConnForm(st, server, connSaveOnly)
		}
		btnLabel := i18n.T("services.conn.save_and_test")
		if st.connBusy {
			btnLabel = i18n.T("services.conn.testing")
		}
		if SailPrimaryButton(c.Key("conn-save-test-"+server.ID), btnLabel).
			Disabled(st.connBusy).
			Clicked() {
			u.saveConnForm(st, server, connSaveAndTest)
		}
	})
}

func (u *DesktopUI) deactivateService(st *servicesPageState, server apitypes.Server) {
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
			u.clearActiveService()
			st.detailServerID = ""
		})
		u.Notify(i18n.T("services.toast.service_deactivated", server.Name))
		return nil
	})
}

func (u *DesktopUI) saveConnForm(st *servicesPageState, orig apitypes.Server, mode connSaveMode) {
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
	u.RunAsync(func() error {
		timeout := 35 * time.Second
		if mode == connSaveAndTest && payload.OAuth {
			timeout = 305 * time.Second
			u.Notify(i18n.T("services.toast.oauth_opened", payload.Name))
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		msg, callErr := u.persistConnPayload(ctx, payload, mode)
		if callErr != nil {
			return u.handleConnSaveError(st, callErr)
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.connBusy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.connBusy = false
			st.connHydratedID = ""
			st.credentialServerID = ""
		})
		u.Notify(msg)
		return nil
	})
}

func (u *DesktopUI) persistConnPayload(ctx context.Context, payload apitypes.Server, mode connSaveMode) (string, error) {
	if mode == connSaveAndTest {
		check, err := u.Client.TestSource(ctx, payload)
		if err != nil {
			return "", err
		}
		if check.Message != "" {
			return check.Message, nil
		}
		return i18n.T("services.toast.conn_saved_tested"), nil
	}
	if _, err := u.Client.SaveServer(ctx, payload); err != nil {
		return "", err
	}
	return i18n.T("services.toast.conn_saved"), nil
}

func (u *DesktopUI) handleConnSaveError(st *servicesPageState, callErr error) error {
	if apiclient.IsUnauthorized(callErr) {
		u.updateUI(func() { st.connBusy = false })
		return callErr
	}
	u.updateUI(func() {
		st.connBusy = false
		st.connError = formatAPIError(callErr)
	})
	return nil
}

func (u *DesktopUI) saveConnJSON(st *servicesPageState, orig apitypes.Server) {
	if u.Client == nil || st.connBusy {
		return
	}
	st.connError = ""
	var payload apitypes.Server
	if err := json.Unmarshal([]byte(st.connRawJSON), &payload); err != nil {
		st.connError = i18n.T("services.err.json_invalid", err.Error())
		return
	}
	if payload.ID == "" {
		payload.ID = orig.ID
	}
	st.connBusy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveServer(ctx, payload); err != nil {
			return u.handleConnSaveError(st, err)
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.connBusy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.connBusy = false
			st.connHydratedID = ""
			st.credentialServerID = ""
		})
		u.Notify(i18n.T("services.toast.json_saved"))
		return nil
	})
}

func (u *DesktopUI) probeServerConnection(st *servicesPageState, server apitypes.Server) {
	if u.Client == nil || st.connBusy || (u.market != nil && u.market.installingID == server.ID) {
		return
	}
	st.connBusy = true
	u.RunAsync(func() error {
		timeout := 30 * time.Second
		if server.OAuth {
			timeout = 305 * time.Second
			u.Notify(i18n.T("services.toast.oauth_opened", server.Name))
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		check, err := u.Client.TestSource(ctx, server)
		if err != nil {
			u.updateUI(func() { st.connBusy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
			_ = u.RefreshSync()
			u.Notify(i18n.T("services.err.probe_failed", formatAPIError(err)))
			return nil
		}
		if syncErr := u.RefreshSync(); syncErr != nil && apiclient.IsUnauthorized(syncErr) {
			u.updateUI(func() { st.connBusy = false })
			return syncErr
		}
		u.updateUI(func() {
			st.connBusy = false
		})
		msg := check.Message
		if msg == "" {
			msg = i18n.T("services.toast.probe_passed")
		}
		u.Notify(msg)
		return nil
	})
}
