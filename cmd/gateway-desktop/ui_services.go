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

type serviceAddFormState struct {
	show            bool
	tab             int // 0=MCP Stdio, 1=MCP HTTP/SSE, 2=OpenAPI 导入, 3=粘贴 JSON
	busy            bool
	errMsg          string
	id              string
	name            string
	description     string
	command         string
	argsText        string
	envText         string
	workDir         string
	endpoint        string
	remoteProto     string // "MCP Streamable HTTP" or "MCP SSE (Streamable 兼容)"
	remoteOAuth     bool
	headersText     string
	openAPIBaseURL  string
	openAPISpecURL  string
	openAPISpecText string
	openAPIOpsText  string
	openAPIHeaders  string
	pasteText       string
}

func defaultServiceAddFormState() serviceAddFormState {
	return serviceAddFormState{
		argsText:       "[]",
		envText:        "{}",
		headersText:    "{}",
		openAPIHeaders: "{}",
		remoteProto:    "MCP Streamable HTTP",
	}
}

func (s *serviceAddFormState) open(tab int) {
	s.show = true
	s.tab = tab
	s.errMsg = ""
	if s.argsText == "" {
		s.argsText = "[]"
	}
	if s.envText == "" {
		s.envText = "{}"
	}
	if s.headersText == "" {
		s.headersText = "{}"
	}
	if s.openAPIHeaders == "" {
		s.openAPIHeaders = "{}"
	}
	if s.remoteProto == "" {
		s.remoteProto = "MCP Streamable HTTP"
	}
}

func (s *serviceAddFormState) reset() {
	show, tab := s.show, s.tab
	*s = defaultServiceAddFormState()
	s.show = show
	s.tab = tab
}

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

// --- Add / Import Service Modal ---

func (u *DesktopUI) renderAddServiceModal(c *ui.Context, st *servicesPageState) {
	ui.DialogBase(c.Key("add-service-dialog"), &st.add.show, func(backdrop, panel ui.Element) {
		backdrop.Background(SailModalBackdrop)
		panel.Width(680).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		// Header
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("services.action.add")).FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("add-modal-close"), i18n.T("common.close")).Clicked() {
				st.add.show = false
			}
		})

		ui.Segmented(
			c.Key("add-svc-tabs"),
			&st.add.tab,
			i18n.T("services.add.tab_stdio"),
			i18n.T("services.add.tab_remote"),
			i18n.T("services.add.tab_openapi"),
			i18n.T("services.add.tab_paste"),
		).FillEqually()

		if st.add.errMsg != "" {
			SailAlertBox(c, ToneDanger, i18n.T("services.add.error_title"), st.add.errMsg)
		}

		// Scrollable form body
		ui.Scroll(c.Key("add-svc-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			switch st.add.tab {
			case 0: // MCP Stdio
				u.renderAddCommonIdentityFields(c, st, "stdio")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.field.command_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-cmd"), &st.add.command).
							Font("monospace").
							Placeholder(i18n.T("services.add.command_placeholder")).
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.field.workdir")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-cwd"), &st.add.workDir).
							Font("monospace").
							FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, i18n.T("services.field.args")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-args"), &st.add.argsText).
						Font("monospace").
						FontSize(12).
						Placeholder("[\"-y\", \"@modelcontextprotocol/server-filesystem\", \"/tmp\"]").
						Height(84).
						FillWidth()
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Baseline).Justify(ui.SpaceBetween).Children(func() {
						ui.Text(c, i18n.T("services.field.env")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.Text(c, i18n.T("services.add.env_only")).FontSize(11.5).TextColor(SailInkMuted)
					})
					ui.TextArea(c.Key("add-env"), &st.add.envText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\"GITHUB_TOKEN\": \"${GITHUB_TOKEN}\"}").
						Height(84).
						FillWidth()
				})

			case 1: // MCP Remote HTTP / SSE
				u.renderAddCommonIdentityFields(c, st, "remote")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(2).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.field.endpoint_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						SailTextInput(c.Key("add-url"), &st.add.endpoint).
							Font("monospace").
							Placeholder("https://api.example.com/mcp").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("common.protocol")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						SailSelect(
							c.Key("add-remote-proto"),
							&st.add.remoteProto,
							[]string{"MCP Streamable HTTP", i18n.T("services.add.proto_sse")},
						).FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Baseline).Justify(ui.SpaceBetween).Children(func() {
						ui.Text(c, i18n.T("services.field.headers")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.Text(c, i18n.T("services.add.env_only")).FontSize(11.5).TextColor(SailInkMuted)
					})
					ui.TextArea(c.Key("add-headers"), &st.add.headersText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\"Authorization\": \"Bearer ${SERVICE_TOKEN}\"}").
						Height(96).
						FillWidth()
				})
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
					ui.Checkbox(c.Key("add-remote-oauth"), &st.add.remoteOAuth, i18n.T("services.add.remote_oauth"))
					if st.add.remoteOAuth {
						ui.Text(c, i18n.T("services.add.remote_oauth_hint")).FontSize(11.5).TextColor(SailInkMuted)
					}
				})

			case 2: // OpenAPI / Swagger Import
				u.renderAddCommonIdentityFields(c, st, "openapi")
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, "API Base URL *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-base"), &st.add.openAPIBaseURL).
							Font("monospace").
							Placeholder("https://api.internal.example/v1").
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.add.doc_url")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-url"), &st.add.openAPISpecURL).
							Font("monospace").
							Placeholder("https://api.internal.example/openapi.json").
							FillWidth()
					})
				})
				ui.Column(c).FillWidth().Gap(5).Children(func() {
					ui.Text(c, "OpenAPI JSON / YAML").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-oapi-spec"), &st.add.openAPISpecText).
						Font("monospace").
						FontSize(12).
						Placeholder("openapi: 3.0.3 ...").
						Height(132).
						FillWidth()
				})
				ui.Row(c).FillWidth().Gap(12).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.add.filter_operations")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-ops"), &st.add.openAPIOpsText).
							Font("monospace").
							Placeholder(i18n.T("services.add.filter_placeholder")).
							FillWidth()
					})
					ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
						ui.Text(c, i18n.T("services.field.headers")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						ui.TextInput(c.Key("add-oapi-hdrs"), &st.add.openAPIHeaders).
							Font("monospace").
							Placeholder("{\"Authorization\": \"Bearer ${API_TOKEN}\"}").
							FillWidth()
					})
				})

			case 3: // Paste mcpServers JSON
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Text(c, i18n.T("services.add.paste_label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextArea(c.Key("add-paste-json"), &st.add.pasteText).
						Font("monospace").
						FontSize(12).
						Placeholder("{\n  \"mcpServers\": {\n    \"github\": {\n      \"type\": \"http\",\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  }\n}").
						Height(220).
						FillWidth()
				})
			}
		})

		// Footer buttons
		ui.Row(c).FillWidth().Padding(8, 0, 0, 0).BorderWidth(1, 0, 0, 0).BorderColor(SailBorder).
			AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			if SailButton(c.Key("add-reset"), i18n.T("services.add.clear_form")).Disabled(st.add.busy).Clicked() {
				st.add.reset()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if SailButton(c.Key("add-cancel"), i18n.T("common.cancel")).Disabled(st.add.busy).Clicked() {
					st.add.show = false
				}
				switch st.add.tab {
				case 0, 1:
					lbl := i18n.T("services.add.connect_and_read")
					if st.add.busy {
						lbl = i18n.T("common.connecting")
					}
					if SailPrimaryButton(c.Key("add-submit-mcp"), lbl).Disabled(st.add.busy).Clicked() {
						u.submitAddMCPService(st)
					}
				case 2:
					lbl := i18n.T("services.add.import_openapi")
					if st.add.busy {
						lbl = i18n.T("common.importing")
					}
					if SailPrimaryButton(c.Key("add-submit-oapi"), lbl).Disabled(st.add.busy).Clicked() {
						u.submitAddOpenAPIService(st)
					}
				case 3:
					if SailPrimaryButton(c.Key("add-parse-paste"), i18n.T("services.add.parse_into_form")).
						Disabled(strings.TrimSpace(st.add.pasteText) == "").
						Clicked() {
						st.add.parsePasteJSON()
					}
				}
			})
		})
	})
}

func (u *DesktopUI) renderAddCommonIdentityFields(c *ui.Context, st *servicesPageState, keyPrefix string) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.name_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-name-"+keyPrefix), &st.add.name).FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.service_id")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-id-"+keyPrefix), &st.add.id).
				Font("monospace").
				Placeholder(i18n.T("services.add.id_placeholder")).
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.field.description")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("add-desc-"+keyPrefix), &st.add.description).FillWidth()
	})
}

func (s *serviceAddFormState) parsePasteJSON() {
	s.errMsg = ""
	var raw map[string]any
	if err := json.Unmarshal([]byte(s.pasteText), &raw); err != nil {
		s.errMsg = i18n.T("services.err.json_parse", err.Error())
		return
	}

	// Gateway Server format
	if kind, ok := raw["kind"].(string); ok && kind != "" {
		if id, _ := raw["id"].(string); id != "" {
			s.id = id
		}
		if name, _ := raw["name"].(string); name != "" {
			s.name = name
		} else {
			s.name = s.id
		}
		s.description, _ = raw["description"].(string)
		transport, _ := raw["transport"].(string)
		if kind == "http" {
			s.openAPIBaseURL, _ = raw["endpoint"].(string)
			s.openAPIHeaders = marshalMapOrDefault(raw["headers"])
			s.tab = 2
			return
		}
		if transport == "stdio" {
			s.command, _ = raw["command"].(string)
			s.workDir, _ = raw["work_dir"].(string)
			s.argsText = marshalSliceOrDefault(raw["args"])
			s.envText = marshalMapOrDefault(raw["env"])
			s.tab = 0
			return
		}
		s.endpoint, _ = raw["endpoint"].(string)
		s.headersText = marshalMapOrDefault(raw["headers"])
		s.remoteOAuth, _ = raw["oauth"].(bool)
		s.tab = 1
		return
	}

	// Standard mcpServers format
	var srvName string
	var entry map[string]any
	if mcpServers, ok := raw["mcpServers"].(map[string]any); ok {
		if len(mcpServers) != 1 {
			s.errMsg = i18n.T("services.add.paste_single")
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
		s.errMsg = i18n.T("services.add.paste_unrecognized")
		return
	}

	s.name = srvName
	s.id = slugServiceID(srvName, "mcp")
	if cmd, _ := entry["command"].(string); cmd != "" {
		s.command = cmd
		s.workDir, _ = entry["cwd"].(string)
		s.argsText = marshalSliceOrDefault(entry["args"])
		s.envText = marshalMapOrDefault(entry["env"])
		s.tab = 0
		return
	}
	s.endpoint, _ = entry["url"].(string)
	s.headersText = marshalMapOrDefault(entry["headers"])
	s.remoteOAuth, _ = entry["oauth"].(bool)
	if typ, _ := entry["type"].(string); strings.EqualFold(typ, "sse") {
		s.remoteProto = i18n.T("services.add.proto_sse")
	} else {
		s.remoteProto = "MCP Streamable HTTP"
	}
	s.tab = 1
}

// --- Async API Operations ---

func (u *DesktopUI) submitAddMCPService(st *servicesPageState) {
	if u.Client == nil || st.add.busy {
		return
	}
	st.add.errMsg = ""

	name := strings.TrimSpace(st.add.name)
	id := strings.TrimSpace(st.add.id)
	if id == "" {
		id = slugServiceID(name, "mcp")
	}
	if name == "" {
		name = id
	}

	payload := apitypes.Server{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(st.add.description),
		Kind:        "mcp",
		Enabled:     true,
	}

	if st.add.tab == 0 {
		cmd := strings.TrimSpace(st.add.command)
		if cmd == "" {
			st.add.errMsg = i18n.T("services.err.command_required")
			return
		}
		args, err := parseStringSliceJSON(st.add.argsText)
		if err != nil {
			st.add.errMsg = i18n.T("services.err.args_format", err.Error())
			return
		}
		env, err := parseStringMapJSON(st.add.envText)
		if err != nil {
			st.add.errMsg = i18n.T("services.err.env_format", err.Error())
			return
		}
		payload.Transport = "stdio"
		payload.Command = cmd
		payload.Args = args
		payload.Env = env
		payload.WorkDir = strings.TrimSpace(st.add.workDir)
	} else {
		ep := strings.TrimSpace(st.add.endpoint)
		if ep == "" {
			st.add.errMsg = i18n.T("services.err.endpoint_required")
			return
		}
		headers, err := parseStringMapJSON(st.add.headersText)
		if err != nil {
			st.add.errMsg = i18n.T("services.err.headers_format", err.Error())
			return
		}
		payload.Transport = "http"
		payload.Endpoint = ep
		payload.Headers = headers
		payload.OAuth = st.add.remoteOAuth
	}

	st.add.busy = true
	u.RunAsync(func() error {
		timeout := 40 * time.Second
		if payload.OAuth {
			timeout = 305 * time.Second
			u.Notify(i18n.T("services.toast.oauth_opened", payload.Name))
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		check, err := u.Client.TestSource(ctx, payload)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.add.busy = false })
				return err
			}
			if payload.OAuth {
				_ = u.RefreshSync()
			}
			u.updateUI(func() {
				st.add.busy = false
				st.add.errMsg = formatAPIError(err)
			})
			return nil
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.add.busy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.add.busy = false
			st.add.show = false
			st.add.reset()
			st.enterDetail(id)
			u.OpenServiceDetail(id)
		})
		msg := check.Message
		if msg == "" {
			msg = i18n.T("services.toast.connected")
		}
		u.Notify(msg)
		return nil
	})
}

func (u *DesktopUI) submitAddOpenAPIService(st *servicesPageState) {
	if u.Client == nil || st.add.busy {
		return
	}
	st.add.errMsg = ""

	name := strings.TrimSpace(st.add.name)
	id := strings.TrimSpace(st.add.id)
	if id == "" {
		id = slugServiceID(name, "api")
	}
	if name == "" {
		name = id
	}
	baseURL := strings.TrimSpace(st.add.openAPIBaseURL)
	if baseURL == "" {
		st.add.errMsg = i18n.T("services.err.base_url_required")
		return
	}
	headers, err := parseStringMapJSON(st.add.openAPIHeaders)
	if err != nil {
		st.add.errMsg = i18n.T("services.err.headers_format", err.Error())
		return
	}
	specBytes := []byte(strings.TrimSpace(st.add.openAPISpecText))
	specURL := strings.TrimSpace(st.add.openAPISpecURL)
	if len(specBytes) == 0 && specURL == "" {
		st.add.errMsg = i18n.T("services.err.openapi_required")
		return
	}

	srv := apitypes.Server{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(st.add.description),
		Kind:        "http",
		Transport:   "http",
		Endpoint:    baseURL,
		Headers:     headers,
		Enabled:     true,
	}
	opIDs := splitCommaList(st.add.openAPIOpsText)

	st.add.busy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		count, err := u.Client.ImportOpenAPI(ctx, srv, specBytes, specURL, opIDs)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.add.busy = false })
				return err
			}
			u.updateUI(func() {
				st.add.busy = false
				st.add.errMsg = formatAPIError(err)
			})
			return nil
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.add.busy = false })
			if apiclient.IsUnauthorized(err) {
				return err
			}
		}
		u.updateUI(func() {
			st.add.busy = false
			st.add.show = false
			st.add.reset()
			st.enterDetail(id)
			u.OpenServiceDetail(id)
		})
		u.Notify(fmt.Sprintf(i18n.T("services.toast.openapi_imported"), count, name))
		return nil
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
