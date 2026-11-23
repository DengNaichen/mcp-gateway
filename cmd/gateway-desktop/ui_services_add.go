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

type serviceAddFormState struct {
	show            bool
	tab             int // 0=MCP Stdio, 1=MCP HTTP/SSE, 2=OpenAPI 导入, 3=粘贴 JSON, 4=受控 CLI（gh 预设）
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
	remoteProto     string // "MCP Streamable HTTP" or i18n.T("services.add.proto_sse")
	remoteOAuth     bool
	headersText     string
	openAPIBaseURL  string
	openAPISpecURL  string
	openAPISpecText string
	openAPIOpsText  string
	openAPIHeaders  string
	pasteText       string
	cliCommand      string
	cliSHA          string
	cliToken        string
}

type openAPIImportRequest struct {
	server    apitypes.Server
	specBytes []byte
	specURL   string
	opIDs     []string
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
	s.cliToken = ""
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

func (s *serviceAddFormState) resolveIdentity(fallbackPrefix string) (id, name string) {
	name = strings.TrimSpace(s.name)
	id = strings.TrimSpace(s.id)
	if id == "" {
		id = slugServiceID(name, fallbackPrefix)
	}
	if name == "" {
		name = id
	}
	return id, name
}

func (s *serviceAddFormState) buildMCPServerPayload() (apitypes.Server, error) {
	id, name := s.resolveIdentity("mcp")
	payload := apitypes.Server{
		ID:          id,
		Name:        name,
		Description: strings.TrimSpace(s.description),
		Kind:        "mcp",
		Enabled:     true,
	}
	if s.tab == 0 {
		cmd := strings.TrimSpace(s.command)
		if cmd == "" {
			return payload, errors.New(i18n.T("services.err.command_required"))
		}
		args, err := parseStringSliceJSON(s.argsText)
		if err != nil {
			return payload, errors.New(i18n.T("services.err.args_format", err.Error()))
		}
		env, err := parseStringMapJSON(s.envText)
		if err != nil {
			return payload, errors.New(i18n.T("services.err.env_format", err.Error()))
		}
		payload.Transport = "stdio"
		payload.Command = cmd
		payload.Args = args
		payload.Env = env
		payload.WorkDir = strings.TrimSpace(s.workDir)
		return payload, nil
	}

	ep := strings.TrimSpace(s.endpoint)
	if ep == "" {
		return payload, errors.New(i18n.T("services.err.endpoint_required"))
	}
	headers, err := parseStringMapJSON(s.headersText)
	if err != nil {
		return payload, errors.New(i18n.T("services.err.headers_format", err.Error()))
	}
	payload.Transport = "http"
	payload.Endpoint = ep
	payload.Headers = headers
	payload.OAuth = s.remoteOAuth
	return payload, nil
}

func (s *serviceAddFormState) buildOpenAPIServerPayload() (openAPIImportRequest, error) {
	id, name := s.resolveIdentity("api")
	baseURL := strings.TrimSpace(s.openAPIBaseURL)
	if baseURL == "" {
		return openAPIImportRequest{}, errors.New(i18n.T("services.err.base_url_required"))
	}
	headers, err := parseStringMapJSON(s.openAPIHeaders)
	if err != nil {
		return openAPIImportRequest{}, errors.New(i18n.T("services.err.headers_format", err.Error()))
	}
	specBytes := []byte(strings.TrimSpace(s.openAPISpecText))
	specURL := strings.TrimSpace(s.openAPISpecURL)
	if len(specBytes) == 0 && specURL == "" {
		return openAPIImportRequest{}, errors.New(i18n.T("services.err.openapi_required"))
	}
	return openAPIImportRequest{
		server: apitypes.Server{
			ID:          id,
			Name:        name,
			Description: strings.TrimSpace(s.description),
			Kind:        "http",
			Transport:   "http",
			Endpoint:    baseURL,
			Headers:     headers,
			Enabled:     true,
		},
		specBytes: specBytes,
		specURL:   specURL,
		opIDs:     splitCommaList(s.openAPIOpsText),
	}, nil
}

func (s *serviceAddFormState) parsePasteJSON() {
	s.errMsg = ""
	var raw map[string]any
	if err := json.Unmarshal([]byte(s.pasteText), &raw); err != nil {
		s.errMsg = i18n.T("services.err.json_parse", err.Error())
		return
	}
	if kind, ok := raw["kind"].(string); ok && kind != "" {
		s.applyGatewayServerPaste(raw, kind)
		return
	}
	s.applyMCPServersPaste(raw)
}

func (s *serviceAddFormState) applyGatewayServerPaste(raw map[string]any, kind string) {
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
	if kind == "cli" {
		s.cliCommand, _ = raw["command"].(string)
		s.cliSHA, _ = raw["command_sha256"].(string)
		s.tab = serviceAddTabCLI
		return
	}
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
}

func (s *serviceAddFormState) applyMCPServersPaste(raw map[string]any) {
	srvName := "my-server"
	entry := raw
	if mcpServers, ok := raw["mcpServers"].(map[string]any); ok {
		if len(mcpServers) != 1 {
			s.errMsg = i18n.T("services.add.paste_single")
			return
		}
		entry = nil
		for k, v := range mcpServers {
			srvName = k
			entry, _ = v.(map[string]any)
		}
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

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("services.action.add")).FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("add-modal-close"), i18n.T("common.close")).Clicked() {
				st.add.show = false
			}
		})

		if st.add.tab == serviceAddTabCLI {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
					ui.Text(c, i18n.T("services.cli.preset_title")).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Text(c, "CLI").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
				})
				if SailSmallButton(c.Key("add-cli-back"), i18n.T("services.cli.other_methods")).Disabled(st.add.busy).Clicked() {
					st.add.tab = 0
					st.add.errMsg = ""
				}
			})
		} else {
			ui.Segmented(
				c.Key("add-svc-tabs"),
				&st.add.tab,
				i18n.T("services.add.tab_stdio"),
				i18n.T("services.add.tab_remote"),
				i18n.T("services.add.tab_openapi"),
				i18n.T("services.add.tab_paste"),
			).FillEqually()
		}

		if st.add.errMsg != "" {
			SailAlertBox(c, ToneDanger, i18n.T("services.add.error_title"), st.add.errMsg)
		}

		ui.Scroll(c.Key("add-svc-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			renderAddServiceBody(c, &st.add)
		})

		u.renderAddServiceFooter(c, st)
	})
}

func renderAddServiceBody(c *ui.Context, add *serviceAddFormState) {
	switch add.tab {
	case 0:
		renderAddMCPStdioFields(c, add)
	case 1:
		renderAddMCPRemoteFields(c, add)
	case 2:
		renderAddOpenAPIFields(c, add)
	case serviceAddTabCLI:
		renderAddCLIFields(c, add)
	case 3:
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, i18n.T("services.add.paste_label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("add-paste-json"), &add.pasteText).
				Font("monospace").
				FontSize(12).
				Placeholder("{\n  \"mcpServers\": {\n    \"github\": {\n      \"type\": \"http\",\n      \"url\": \"https://api.githubcopilot.com/mcp/\"\n    }\n  }\n}").
				Height(220).
				FillWidth()
		})
	}
}

func renderAddMCPStdioFields(c *ui.Context, add *serviceAddFormState) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Padding(8, 12).Radius(7).Background(SailBgSubtle).Children(func() {
		ui.Text(c, i18n.T("services.cli.preset_hint")).FontSize(12).TextColor(SailInkMuted).SingleLine().MinWidth(0)
		if SailSmallButton(c.Key("add-gh-preset"), i18n.T("services.cli.preset_button")).Shrink(0).Clicked() {
			add.applyGHPreset()
		}
	})
	renderAddCommonIdentityFields(c, add, "stdio")
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.command_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-cmd"), &add.command).
				Font("monospace").
				Placeholder(i18n.T("services.add.command_placeholder")).
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.workdir")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-cwd"), &add.workDir).
				Font("monospace").
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.field.args")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextArea(c.Key("add-args"), &add.argsText).
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
		ui.TextArea(c.Key("add-env"), &add.envText).
			Font("monospace").
			FontSize(12).
			Placeholder("{\"GITHUB_TOKEN\": \"${GITHUB_TOKEN}\"}").
			Height(84).
			FillWidth()
	})
}

func renderAddMCPRemoteFields(c *ui.Context, add *serviceAddFormState) {
	renderAddCommonIdentityFields(c, add, "remote")
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(2).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.endpoint_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			SailTextInput(c.Key("add-url"), &add.endpoint).
				Font("monospace").
				Placeholder("https://api.example.com/mcp").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("common.protocol")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			SailSelect(
				c.Key("add-remote-proto"),
				&add.remoteProto,
				[]string{"MCP Streamable HTTP", i18n.T("services.add.proto_sse")},
			).FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Baseline).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("services.field.headers")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.Text(c, i18n.T("services.add.env_only")).FontSize(11.5).TextColor(SailInkMuted)
		})
		ui.TextArea(c.Key("add-headers"), &add.headersText).
			Font("monospace").
			FontSize(12).
			Placeholder("{\"Authorization\": \"Bearer ${SERVICE_TOKEN}\"}").
			Height(96).
			FillWidth()
	})
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		ui.Checkbox(c.Key("add-remote-oauth"), &add.remoteOAuth, i18n.T("services.add.remote_oauth"))
		if add.remoteOAuth {
			ui.Text(c, i18n.T("services.add.remote_oauth_hint")).FontSize(11.5).TextColor(SailInkMuted)
		}
	})
}

func renderAddOpenAPIFields(c *ui.Context, add *serviceAddFormState) {
	renderAddCommonIdentityFields(c, add, "openapi")
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, "API Base URL *").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-oapi-base"), &add.openAPIBaseURL).
				Font("monospace").
				Placeholder("https://api.internal.example/v1").
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.add.doc_url")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-oapi-url"), &add.openAPISpecURL).
				Font("monospace").
				Placeholder("https://api.internal.example/openapi.json").
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, "OpenAPI JSON / YAML").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextArea(c.Key("add-oapi-spec"), &add.openAPISpecText).
			Font("monospace").
			FontSize(12).
			Placeholder("openapi: 3.0.3 ...").
			Height(132).
			FillWidth()
	})
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.add.filter_operations")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-oapi-ops"), &add.openAPIOpsText).
				Font("monospace").
				Placeholder(i18n.T("services.add.filter_placeholder")).
				FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.headers")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-oapi-hdrs"), &add.openAPIHeaders).
				Font("monospace").
				Placeholder("{\"Authorization\": \"Bearer ${API_TOKEN}\"}").
				FillWidth()
		})
	})
}

func renderAddCommonIdentityFields(c *ui.Context, add *serviceAddFormState, keyPrefix string) {
	ui.Row(c).FillWidth().Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.name_required")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-name-"+keyPrefix), &add.name).FillWidth()
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, i18n.T("services.field.service_id")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("add-id-"+keyPrefix), &add.id).
				Font("monospace").
				Placeholder(i18n.T("services.add.id_placeholder")).
				FillWidth()
		})
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		ui.Text(c, i18n.T("services.field.description")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("add-desc-"+keyPrefix), &add.description).FillWidth()
	})
}

func (u *DesktopUI) renderAddServiceFooter(c *ui.Context, st *servicesPageState) {
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
			case serviceAddTabCLI:
				lbl := i18n.T("services.cli.register")
				if st.add.busy {
					lbl = i18n.T("services.cli.registering")
				}
				if SailPrimaryButton(c.Key("add-submit-cli"), lbl).Disabled(st.add.busy).Clicked() {
					u.submitAddCLIService(st)
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
}

func (u *DesktopUI) submitAddMCPService(st *servicesPageState) {
	if u.Client == nil || st.add.busy {
		return
	}
	st.add.errMsg = ""
	payload, err := st.add.buildMCPServerPayload()
	if err != nil {
		st.add.errMsg = err.Error()
		return
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
			if payload.OAuth && !apiclient.IsUnauthorized(err) {
				_ = u.RefreshSync()
			}
			return u.handleAddServiceError(st, err)
		}
		if err := u.RefreshSync(); err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() { st.add.busy = false })
			return err
		}
		msg := check.Message
		if msg == "" {
			msg = i18n.T("services.toast.connected")
		}
		u.finishAddService(st, payload.ID, msg)
		return nil
	})
}

func (u *DesktopUI) submitAddOpenAPIService(st *servicesPageState) {
	if u.Client == nil || st.add.busy {
		return
	}
	st.add.errMsg = ""
	req, err := st.add.buildOpenAPIServerPayload()
	if err != nil {
		st.add.errMsg = err.Error()
		return
	}

	st.add.busy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		count, err := u.Client.ImportOpenAPI(ctx, req.server, req.specBytes, req.specURL, req.opIDs)
		if err != nil {
			return u.handleAddServiceError(st, err)
		}
		if err := u.RefreshSync(); err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() { st.add.busy = false })
			return err
		}
		u.finishAddService(st, req.server.ID, fmt.Sprintf(i18n.T("services.toast.openapi_imported"), count, req.server.Name))
		return nil
	})
}

func (u *DesktopUI) handleAddServiceError(st *servicesPageState, err error) error {
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

func (u *DesktopUI) finishAddService(st *servicesPageState, serverID, msg string) {
	u.updateUI(func() {
		st.add.busy = false
		st.add.show = false
		st.add.reset()
		st.enterDetail(serverID)
		u.OpenServiceDetail(serverID)
	})
	u.Notify(msg)
}
