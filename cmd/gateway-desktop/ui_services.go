package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

const servicesToolPageSize = 30

var nonServiceIDRe = regexp.MustCompile(`[^a-z0-9._-]+`)

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

func defaultServiceMaintState() serviceMaintState {
	return serviceMaintState{
		maintIntervalStr: "60",
		maintTimeoutStr:  "900",
		maintAutoPublish: true,
	}
}

type servicesPageState struct {
	serviceListState
	serviceDetailState
	serviceConnFormState
	serviceMaintState

	add      serviceAddFormState
	toolEdit serviceToolEditState
	httpTool serviceHTTPToolState

	// Kept directly on servicesPageState because sibling credentials files
	// construct &servicesPageState{credentialServerID: ...} and access these directly.
	credentialServerID string
	credentialState    apitypes.ServerCredentials
	credentialValues   map[string]string
	credentialBusy     bool
	credentialError    string
}

func (u *DesktopUI) servicesState() *servicesPageState {
	if u.services == nil {
		u.services = &servicesPageState{
			serviceMaintState: defaultServiceMaintState(),
			add:               defaultServiceAddFormState(),
			httpTool:          defaultServiceHTTPToolState(),
		}
	}
	return u.services
}

func serviceProtocolTag(s apitypes.Server) string {
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
	st := u.servicesState()

	// Consume cross-page trigger to open Add Service modal.
	if u.consumePendingAddService() {
		st.add.open(0)
	}

	var selectedServer *apitypes.Server
	if activeID := u.activeServiceID(); activeID != "" {
		for i := range u.State.Registry.Servers {
			if u.State.Registry.Servers[i].ID == activeID {
				selectedServer = &u.State.Registry.Servers[i]
				break
			}
		}
		if selectedServer == nil {
			u.clearActiveService()
		}
	}

	if selectedServer != nil {
		u.renderServiceDetail(c, st, *selectedServer)
	} else {
		u.renderServiceList(c, st)
	}

	if st.add.show {
		u.renderAddServiceModal(c, st)
	}
	if st.toolEdit.show && st.toolEdit.tool != nil {
		u.renderToolEditModal(c, st)
	}
	if st.httpTool.show && selectedServer != nil {
		u.renderAddHTTPToolModal(c, st, *selectedServer)
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
		for _, t := range u.State.Registry.Tools {
			if t.ServerID == server.ID {
				srvTools = append(srvTools, t)
			}
		}
	}
	readiness := u.readinessForServer(server, srvTools)
	check, hasCheck := u.State.Setup.SourceChecks[server.ID]

	// Health & process summary card
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

			subtlePanel(c, 220, 1, func() {
				ui.Text(c, i18n.T("services.health.runtime")).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
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
						SailQuietStatus(c, i18n.T("services.health.pi_ready", cmdStr), SailSuccessInk, SailInkStrong)
					} else {
						SailQuietStatus(c, i18n.T("services.health.deterministic"), SailInkFaint, SailInkBody)
					}
					ui.Text(c, fmt.Sprintf("%s · %s", execMode, serviceProtocolTag(server))).
						FontSize(12).
						TextColor(SailInkMuted)
				} else {
					ui.Text(c, i18n.T("common.reading")).FontSize(12).TextColor(SailInkMuted)
				}
			})
		})
	})

	if st.maintError != "" {
		SailAlertBox(c, ToneDanger, i18n.T("services.health.error_title"), st.maintError)
	}

	// Maintenance Source Policy
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
	})

	// Maintenance runs list (hairline-divided FlushCard)
	SailFlushCard(c, func() {
		var runs []apitypes.MaintenanceRun
		if st.maintOverview != nil {
			for _, r := range st.maintOverview.Runs {
				if r.ServerID == server.ID {
					runs = append(runs, r)
				}
			}
		}

		SailListCardHeader(c, i18n.T("services.maint.runs_title"), fmt.Sprintf(i18n.T("services.maint.runs_count"), len(runs)), nil)

		if len(runs) == 0 {
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, i18n.T("services.maint.runs_empty"), "", "", nil)
			})
			return
		}

		for idx, runItem := range runs {
			r := runItem
			isLast := idx == len(runs)-1
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
						lbl := maintenanceStatusLabel(r.Status)
						switch r.Status {
						case "failed", "interrupted":
							SailBadge(c, lbl, ToneDanger)
						case "ready":
							SailBadge(c, lbl, ToneWarning)
						case "published", "no_change":
							SailQuietStatus(c, lbl, SailSuccessInk, SailInkStrong)
						default:
							SailQuietStatus(c, lbl, SailInkFaint, SailInkMuted)
						}
						ui.Text(c, r.Stage).Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
					})
					summary := r.Error
					if summary == "" {
						summary = r.Summary
					}
					if summary != "" {
						ui.Text(c, summary).FontSize(12.5).TextColor(SailInkMuted).SingleLine()
					}
				})

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
			})
		}
	})
}

func maintenanceStatusLabel(status string) string {
	switch status {
	case "queued":
		return i18n.T("services.run_status.queued")
	case "running":
		return i18n.T("services.run_status.running")
	case "ready":
		return i18n.T("services.run_status.ready")
	case "published":
		return i18n.T("services.run_status.published")
	case "no_change":
		return i18n.T("services.run_status.no_change")
	case "failed":
		return i18n.T("common.failed")
	case "cancelled":
		return i18n.T("services.run_status.cancelled")
	case "interrupted":
		return i18n.T("services.run_status.interrupted")
	case "rolled_back":
		return i18n.T("services.run_status.rolled_back")
	default:
		return status
	}
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
		if runNow {
			if _, err := u.Client.RunMaintenance(ctx, server.ID, force); err != nil {
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
		}
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
				st.hydrateMaintSource(ov, server)
			}
		})
		if runNow {
			u.Notify(i18n.T("services.toast.maint_saved_run"))
		} else {
			u.Notify(i18n.T("services.toast.maint_saved"))
		}
		return nil
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
			}
		})
		u.Notify(okMsg)
		return nil
	})
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
		Padding(14, 16).
		Gap(8).
		Radius(8).
		Background(SailBgSubtle).
		Children(fn)
}
