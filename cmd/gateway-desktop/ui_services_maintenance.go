package main

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type serviceMaintState struct {
	maintLoading          bool
	maintError            string
	maintBusy             bool
	maintOverview         *apitypes.MaintenanceStatus
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

type maintRuntimeInfo struct {
	ready     bool
	available bool
	command   string
	execMode  string
}

func defaultServiceMaintState() serviceMaintState {
	return serviceMaintState{
		maintIntervalStr: "60",
		maintTimeoutStr:  "900",
		maintAutoPublish: true,
	}
}

func filterMaintenanceRunsForServer(ov *apitypes.MaintenanceStatus, serverID string) []apitypes.MaintenanceRun {
	if ov == nil {
		return nil
	}
	var runs []apitypes.MaintenanceRun
	for _, r := range ov.Runs {
		if r.ServerID == serverID {
			runs = append(runs, r)
		}
	}
	return runs
}

func parseMaintenanceRuntimeInfo(ov *apitypes.MaintenanceStatus) maintRuntimeInfo {
	if ov == nil || ov.Runtime == nil {
		return maintRuntimeInfo{}
	}
	avail, _ := ov.Runtime["available"].(bool)
	cmdStr, _ := ov.Runtime["command"].(string)
	execMode := "local"
	if execMap, ok := ov.Runtime["execution"].(map[string]any); ok {
		if m, ok := execMap["mode"].(string); ok && m != "" {
			execMode = m
		}
	}
	return maintRuntimeInfo{
		ready:     true,
		available: avail,
		command:   cmdStr,
		execMode:  execMode,
	}
}

func maintenanceRunSummary(r apitypes.MaintenanceRun) string {
	if r.Error != "" {
		return r.Error
	}
	return r.Summary
}

var maintenanceStatusKeys = map[string]string{
	"queued":      "services.run_status.queued",
	"running":     "services.run_status.running",
	"ready":       "services.run_status.ready",
	"published":   "services.run_status.published",
	"no_change":   "services.run_status.no_change",
	"failed":      "common.failed",
	"cancelled":   "services.run_status.cancelled",
	"interrupted": "services.run_status.interrupted",
	"rolled_back": "services.run_status.rolled_back",
}

func maintenanceStatusLabel(status string) string {
	if key, ok := maintenanceStatusKeys[status]; ok {
		return i18n.T(key)
	}
	return status
}

func (st *serviceMaintState) hydrateMaintSource(ov apitypes.MaintenanceStatus, server apitypes.Server) {
	st.maintHydratedServerID = server.ID
	var found *apitypes.MaintenanceSource
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

func (st *serviceMaintState) buildMaintenanceSource(server apitypes.Server) apitypes.MaintenanceSource {
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

	var src apitypes.MaintenanceSource
	if st.maintOverview != nil {
		for i := range st.maintOverview.Sources {
			if st.maintOverview.Sources[i].ServerID == server.ID {
				src = st.maintOverview.Sources[i]
				if src.Repository != nil {
					repoCopy := *src.Repository
					if src.Repository.Files != nil {
						repoCopy.Files = slices.Clone(src.Repository.Files)
					}
					src.Repository = &repoCopy
				}
				break
			}
		}
	}

	src.ServerID = server.ID
	src.Format = format
	src.Files = splitLines(st.maintFilesText)
	src.URLs = splitLines(st.maintURLsText)
	src.OperationIDs = splitCommaOrLineList(st.maintOpsText)
	src.Instructions = strings.TrimSpace(st.maintInstructions)
	src.Enabled = st.maintEnabled
	src.AutoPublish = st.maintAutoPublish
	src.IntervalMinutes = interval
	src.TimeoutSeconds = timeout
	return src
}

// --- Detail Subtab 2: Health & Maintenance Overview ---

func (u *DesktopUI) renderServiceMaintenanceTab(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	if st.maintOverview == nil && !st.maintLoading && st.maintError == "" {
		u.loadMaintenanceOverview(st, server)
	}
	if st.maintOverview != nil && st.maintHydratedServerID != server.ID {
		st.hydrateMaintSource(*st.maintOverview, server)
	}

	var srvTools []apitypes.Tool
	if u.State != nil {
		srvTools, _ = collectServerTools(u.State.Registry.Tools, server.ID)
	}
	readiness := u.readinessForServer(server, srvTools)

	u.renderServiceHealthCard(c, st, server, readiness)
	if st.maintError != "" {
		SailAlertBox(c, ToneDanger, i18n.T("services.health.error_title"), st.maintError)
	}
	u.renderMaintenancePolicyCard(c, st, server)
	u.renderMaintenanceRunsCard(c, st, server)
}

func (u *DesktopUI) renderServiceHealthCard(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	readiness serviceReadiness,
) {
	check, hasCheck := u.State.Setup.SourceChecks[server.ID]
	SailCard(c, func() {
		SailCardTitle(
			c,
			i18n.T("services.health.title"),
			"",
			func() {
				refreshLabel := i18n.T("common.refresh")
				if st.maintLoading {
					refreshLabel = i18n.T("common.loading")
				}
				if SailSmallButton(c.Key("maint-refresh-"+server.ID), refreshLabel).
					Disabled(st.maintLoading).
					Clicked() {
					u.loadMaintenanceOverview(st, server)
				}
				if SailSmallButton(c.Key("health-probe-"+server.ID), i18n.T("services.health.probe_now")).
					Disabled(st.connBusy).
					Clicked() {
					u.probeServerConnection(st, server)
				}
			},
		)

		ui.Row(c).FillWidth().Gap(14).Children(func() {
			renderServiceHealthStatusPanel(c, readiness, check, hasCheck)
			renderMaintenanceRuntimePanel(c, st, server)
		})
	})
}

func renderServiceHealthStatusPanel(
	c *ui.Context,
	readiness serviceReadiness,
	check apitypes.ConnectionCheck,
	hasCheck bool,
) {
	subtlePanel(c, 220, 1, func() {
		ui.Text(c, i18n.T("services.health.panel_title")).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if readiness.BadgeLabel != "" {
				SailBadgeDot(c, readiness.BadgeLabel, readiness.BadgeTone)
			} else {
				SailQuietStatus(c, readiness.StatusLabel, readiness.DotColor, SailInkStrong)
			}
			if hasCheck {
				ui.Text(c, i18n.T("services.health.last_check_at", FormatTime(check.CheckedAt))).FontSize(12).TextColor(SailInkMuted)
			} else {
				ui.Text(c, i18n.T("services.health.no_history")).FontSize(12).TextColor(SailInkMuted)
			}
		})
		if readiness.AlertTitle != "" {
			ui.Text(c, readiness.AlertTitle).FontSize(12.5).TextColor(SailInkBody)
		} else if hasCheck && check.Message != "" {
			ui.Text(c, check.Message).FontSize(12.5).TextColor(SailInkBody)
		}
		if hasCheck && check.Detail != "" {
			ui.Text(c, check.Detail).Font("monospace").FontSize(11.5).TextColor(SailDangerInk).Selectable()
		}
	})
}

func renderMaintenanceRuntimePanel(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	subtlePanel(c, 220, 1, func() {
		ui.Text(c, i18n.T("services.health.runtime")).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
		rt := parseMaintenanceRuntimeInfo(st.maintOverview)
		if !rt.ready {
			ui.Text(c, i18n.T("common.reading")).FontSize(12).TextColor(SailInkMuted)
			return
		}
		if rt.available {
			SailQuietStatus(c, i18n.T("services.health.pi_ready", rt.command), SailSuccessInk, SailInkStrong)
		} else {
			SailQuietStatus(c, i18n.T("services.health.deterministic"), SailInkFaint, SailInkBody)
		}
		ui.Text(c, fmt.Sprintf("%s · %s", rt.execMode, serviceProtocolTag(server))).
			FontSize(12).
			TextColor(SailInkMuted)
	})
}

func (u *DesktopUI) renderMaintenancePolicyCard(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	SailCard(c, func() {
		SailCardTitle(c, i18n.T("services.maint.policy_title"), "", nil)

		modeOpt0 := i18n.T("services.maint.mode_native")
		if server.Kind == "http" {
			modeOpt0 = i18n.T("services.maint.mode_openapi")
		}
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("services.maint.mode")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.Segmented(c.Key("maint-fmt-"+server.ID), &st.maintFormatIdx, modeOpt0, i18n.T("services.maint.mode_pi"))
		})

		ui.Column(c).FillWidth().Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.maint.filter")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("maint-ops-"+server.ID), &st.maintOpsText).
				Font("monospace").
				Placeholder(i18n.T("services.maint.filter_placeholder")).
				FillWidth()
		})

		if server.Kind == "http" || st.maintFormatIdx == 1 {
			ui.Row(c).FillWidth().Gap(14).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, i18n.T("services.maint.spec_path")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("maint-files-"+server.ID), &st.maintFilesText).
						Font("monospace").
						FontSize(12).
						Height(72).
						FillWidth()
				})
				ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
					ui.Text(c, i18n.T("services.maint.spec_url")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
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
				ui.Text(c, i18n.T("services.maint.prompt")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("maint-inst-"+server.ID), &st.maintInstructions).
					FontSize(12.5).
					Height(72).
					FillWidth()
			})
		}

		ui.Row(c).FillWidth().Gap(14).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, i18n.T("services.maint.period")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("maint-interval-"+server.ID), &st.maintIntervalStr).Font("monospace").FillWidth()
			})
			ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
				ui.Text(c, i18n.T("services.maint.timeout")).FontSize(12).FontWeight(600).TextColor(SailInkBody)
				ui.TextInput(c.Key("maint-timeout-"+server.ID), &st.maintTimeoutStr).Font("monospace").FillWidth()
			})
		})

		ui.Row(c).FillWidth().Gap(16).Wrap().Children(func() {
			ui.Checkbox(c.Key("maint-enabled-"+server.ID), &st.maintEnabled, i18n.T("services.maint.schedule"))
			ui.Checkbox(c.Key("maint-autopub-"+server.ID), &st.maintAutoPublish, i18n.T("services.maint.auto_publish"))
		})

		u.renderMaintenancePolicyActions(c, st, server)
	})
}

func (u *DesktopUI) renderMaintenancePolicyActions(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Row(c).FillWidth().Padding(6, 0, 0, 0).AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if SailButton(c.Key("maint-save-"+server.ID), i18n.T("services.maint.save_policy")).
			Disabled(st.maintBusy).
			Clicked() {
			u.saveMaintenanceConfig(st, server, false, false)
		}
		if SailButton(c.Key("maint-force-run-"+server.ID), i18n.T("services.maint.force_run")).
			Disabled(st.maintBusy).
			Clicked() {
			u.saveMaintenanceConfig(st, server, true, true)
		}
		if SailPrimaryButton(c.Key("maint-save-run-"+server.ID), i18n.T("services.maint.save_and_run")).
			Disabled(st.maintBusy).
			Clicked() {
			u.saveMaintenanceConfig(st, server, true, false)
		}
	})
}

func (u *DesktopUI) renderMaintenanceRunsCard(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	// Maintenance runs list (hairline-divided FlushCard)
	SailFlushCard(c, func() {
		runs := filterMaintenanceRunsForServer(st.maintOverview, server.ID)
		SailListCardHeader(c, i18n.T("services.maint.runs_title"), fmt.Sprintf(i18n.T("services.maint.runs_count"), len(runs)), nil)

		if len(runs) == 0 {
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, i18n.T("services.maint.runs_empty"), "", "", nil)
			})
			return
		}

		for idx, runItem := range runs {
			u.renderMaintenanceRunRow(c, st, server, runItem, idx == len(runs)-1)
		}
	})
}

func (u *DesktopUI) renderMaintenanceRunRow(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	r apitypes.MaintenanceRun,
	isLast bool,
) {
	row := ui.Row(c.Key("maint-run-"+r.ID)).
		FillWidth().
		Padding(13, 20).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Gap(14)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	row.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				renderMaintenanceStatusIndicator(c, r.Status)
				ui.Text(c, r.Stage).Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
			})
			if summary := maintenanceRunSummary(r); summary != "" {
				ui.Text(c, summary).FontSize(12.5).TextColor(SailInkMuted).SingleLine()
			}
		})

		u.renderMaintenanceRunRowActions(c, st, server, r)
	})
}

func renderMaintenanceStatusIndicator(c *ui.Context, status string) {
	lbl := maintenanceStatusLabel(status)
	switch status {
	case "failed", "interrupted":
		SailBadge(c, lbl, ToneDanger)
	case "ready":
		SailBadge(c, lbl, ToneWarning)
	case "published", "no_change":
		SailQuietStatus(c, lbl, SailSuccessInk, SailInkStrong)
	default:
		SailQuietStatus(c, lbl, SailInkFaint, SailInkMuted)
	}
}

func (u *DesktopUI) renderMaintenanceRunRowActions(
	c *ui.Context,
	st *servicesPageState,
	server apitypes.Server,
	r apitypes.MaintenanceRun,
) {
	ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Text(c, FormatTime(r.CreatedAt)).FontSize(11.5).TextColor(SailInkFaint).FontFeatures("tnum")
		if r.Status == "ready" && r.Acceptance.Passed {
			if SailSmallPrimaryButton(c.Key("run-pub-"+r.ID), i18n.T("services.maint.publish")).
				Disabled(st.maintBusy).
				Clicked() {
				u.runMaintenanceAction(st, server, i18n.T("services.toast.published"), func(ctx context.Context) error {
					return u.Client.PublishMaintenanceRun(ctx, r.ID)
				})
			}
		}
		if r.Status == "published" {
			if SailSmallButton(c.Key("run-rb-"+r.ID), i18n.T("services.maint.rollback")).
				Disabled(st.maintBusy).
				Clicked() {
				u.runMaintenanceAction(st, server, i18n.T("services.toast.rolled_back"), func(ctx context.Context) error {
					return u.Client.RollbackMaintenanceRun(ctx, r.ID)
				})
			}
		}
		if r.Status == "queued" || r.Status == "running" {
			if SailSmallButton(c.Key("run-cancel-"+r.ID), i18n.T("common.cancel")).
				Disabled(st.maintBusy).
				Clicked() {
				u.runMaintenanceAction(st, server, i18n.T("services.toast.cancelled"), func(ctx context.Context) error {
					return u.Client.CancelMaintenanceRun(ctx, r.ID)
				})
			}
		}
	})
}

func (u *DesktopUI) loadMaintenanceOverview(st *servicesPageState, server apitypes.Server) {
	if u.Client == nil || st.maintLoading {
		return
	}
	st.maintLoading = true
	st.maintError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		ov, err := u.Client.Maintenance(ctx)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.maintLoading = false })
				return err
			}
			u.updateUI(func() {
				st.maintLoading = false
				st.maintError = formatAPIError(err)
			})
			return nil
		}
		u.updateUI(func() {
			st.maintLoading = false
			st.maintOverview = &ov
			st.hydrateMaintSource(ov, server)
		})
		return nil
	})
}

func (u *DesktopUI) saveMaintenanceConfig(st *servicesPageState, server apitypes.Server, runNow, force bool) {
	if u.Client == nil || st.maintBusy {
		return
	}
	st.maintError = ""
	src := st.buildMaintenanceSource(server)

	st.maintBusy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := u.Client.SaveMaintenanceSource(ctx, src); err != nil {
			return u.handleMaintenanceError(st, err)
		}
		if runNow {
			if _, err := u.Client.RunMaintenance(ctx, server.ID, force); err != nil {
				return u.handleMaintenanceError(st, err)
			}
		}
		okMsg := i18n.T("services.toast.maint_saved")
		if runNow {
			okMsg = i18n.T("services.toast.maint_saved_run")
		}
		return u.refreshMaintenanceAfterAction(ctx, st, server, true, okMsg)
	})
}

func (u *DesktopUI) runMaintenanceAction(st *servicesPageState, server apitypes.Server, okMsg string, fn func(context.Context) error) {
	if u.Client == nil || st.maintBusy {
		return
	}
	st.maintBusy = true
	st.maintError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := fn(ctx); err != nil {
			return u.handleMaintenanceError(st, err)
		}
		return u.refreshMaintenanceAfterAction(ctx, st, server, false, okMsg)
	})
}

func (u *DesktopUI) handleMaintenanceError(st *servicesPageState, err error) error {
	if apiclient.IsUnauthorized(err) {
		u.updateUI(func() { st.maintBusy = false })
		return err
	}
	u.updateUI(func() {
		st.maintBusy = false
		st.maintError = formatAPIError(err)
	})
	return nil
}

func (u *DesktopUI) refreshMaintenanceAfterAction(
	ctx context.Context,
	st *servicesPageState,
	server apitypes.Server,
	hydrate bool,
	okMsg string,
) error {
	ov, ovErr := u.Client.Maintenance(ctx)
	if ovErr != nil && apiclient.IsUnauthorized(ovErr) {
		u.updateUI(func() { st.maintBusy = false })
		return ovErr
	}
	if syncErr := u.RefreshSync(); syncErr != nil && apiclient.IsUnauthorized(syncErr) {
		u.updateUI(func() { st.maintBusy = false })
		return syncErr
	}
	u.updateUI(func() {
		st.maintBusy = false
		if ovErr == nil {
			st.maintOverview = &ov
			if hydrate {
				st.hydrateMaintSource(ov, server)
			}
		}
	})
	u.Notify(okMsg)
	return nil
}
