package main

import (
	"errors"
	"strings"
	"sync/atomic"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
	"gatewaylab/internal/i18n"
)

type clientsPageState struct {
	showVerifyModal bool
	verifyClientID  string
}

var openConfigLocation = revealClientConfig

func (u *DesktopUI) clientsState() *clientsPageState {
	if u.clients == nil {
		u.clients = &clientsPageState{}
	}
	return u.clients
}

func (u *DesktopUI) clientEndpoint() string {
	if u.State != nil {
		return u.State.MCPURL
	}
	if u.Runtime != nil {
		return u.Runtime.MCPURL
	}
	return ""
}

// clientStatusLang records the UI language that the cached client status rows
// were built in. Client state labels are localized when the rows are produced,
// so a language switch must rebuild them before they are compared or rendered.
var clientStatusLang atomic.Value

// noteClientStatusLang remembers the language the cached rows were built in.
func noteClientStatusLang(lang i18n.Lang) {
	clientStatusLang.Store(lang)
}

// staleClientStatusLang reports whether the cached rows predate the active
// language and therefore need a refresh.
func staleClientStatusLang() bool {
	lang, ok := clientStatusLang.Load().(i18n.Lang)
	return ok && lang != i18n.Current()
}

func (u *DesktopUI) refreshClientConfigs() {
	if (u.ClientConfigs == nil && u.ClientPlugins == nil) || u.ClientsBusy || u.PluginsBusy {
		return
	}
	endpoint := u.clientEndpoint()
	lang := i18n.Current()
	u.ClientsBusy = true
	u.PluginsBusy = true
	u.RunAsync(func() error {
		var clientRows []clientconfig.Status
		if u.ClientConfigs != nil {
			clientRows = u.ClientConfigs.Statuses(endpoint)
		}
		var pluginRows []clientplugin.Status
		if u.ClientPlugins != nil {
			pluginRows = u.ClientPlugins.Statuses(endpoint)
		}
		u.updateUI(func() {
			u.ClientStatuses = clientRows
			u.PluginStatuses = pluginRows
			u.ClientConfigError = ""
			u.PluginError = ""
			u.ClientsBusy = false
			u.PluginsBusy = false
			noteClientStatusLang(lang)
		})
		return nil
	})
}

// changeClientConfig is shared by native actions and the local verification socket.
func (u *DesktopUI) changeClientConfig(id, action, endpoint string) error {
	if u.ClientConfigs == nil {
		return errors.New(i18n.T("ops.read-user-config-dir-failed"))
	}
	switch action {
	case "write":
		return u.ClientConfigs.Write(id, endpoint)
	case "update":
		return u.ClientConfigs.Update(id, endpoint)
	case "revoke":
		return u.ClientConfigs.Revoke(id)
	default:
		return errors.New(i18n.T("ops.unknown-config-action"))
	}
}

// changeClientPlugin is shared by native actions and the local verification socket.
func (u *DesktopUI) changeClientPlugin(id, action, endpoint string) error {
	if u.ClientPlugins == nil {
		return errors.New(i18n.T("ops.read-plugin-status-failed"))
	}
	switch action {
	case "install":
		return u.ClientPlugins.Install(id, endpoint)
	case "update":
		return u.ClientPlugins.Update(id, endpoint)
	case "remove":
		return u.ClientPlugins.Remove(id)
	default:
		return errors.New(i18n.T("ops.unknown-plugin-action"))
	}
}

func (u *DesktopUI) runClientConfig(id, action string) {
	if u.ClientsBusy || u.PluginsBusy {
		return
	}
	endpoint := u.clientEndpoint()
	lang := i18n.Current()
	u.ClientsBusy = true
	u.PluginsBusy = true
	u.RunAsync(func() error {
		err := u.changeClientConfig(id, action, endpoint)
		var rows []clientconfig.Status
		if u.ClientConfigs != nil {
			rows = u.ClientConfigs.Statuses(endpoint)
		}
		var pluginRows []clientplugin.Status
		if u.ClientPlugins != nil {
			pluginRows = u.ClientPlugins.Statuses(endpoint)
		}
		u.updateUI(func() {
			u.ClientStatuses = rows
			u.PluginStatuses = pluginRows
			u.ClientsBusy = false
			u.PluginsBusy = false
			u.ClientConfigError = ""
			if err != nil {
				u.ClientConfigError = err.Error()
			}
			noteClientStatusLang(lang)
		})
		if err == nil {
			switch action {
			case "write":
				u.Notify(i18n.T("ops.config-written"))
			case "update":
				u.Notify(i18n.T("ops.config-updated"))
			default:
				u.Notify(i18n.T("ops.config-revoked"))
			}
		}
		return nil
	})
}

func (u *DesktopUI) runClientPlugin(id, action string) {
	if u.ClientsBusy || u.PluginsBusy {
		return
	}
	endpoint := u.clientEndpoint()
	lang := i18n.Current()
	u.ClientsBusy = true
	u.PluginsBusy = true
	u.RunAsync(func() error {
		err := u.changeClientPlugin(id, action, endpoint)
		var pluginRows []clientplugin.Status
		if u.ClientPlugins != nil {
			pluginRows = u.ClientPlugins.Statuses(endpoint)
		}
		var clientRows []clientconfig.Status
		if u.ClientConfigs != nil {
			clientRows = u.ClientConfigs.Statuses(endpoint)
		}
		u.updateUI(func() {
			u.PluginStatuses = pluginRows
			u.ClientStatuses = clientRows
			u.PluginsBusy = false
			u.ClientsBusy = false
			noteClientStatusLang(lang)
			u.PluginError = ""
			if err != nil {
				u.PluginError = err.Error()
			}
		})
		if err == nil {
			switch action {
			case "install":
				u.Notify(i18n.T("ops.plugin-installed"))
			case "update":
				u.Notify(i18n.T("ops.plugin-updated"))
			default:
				u.Notify(i18n.T("ops.plugin-removed"))
			}
		}
		return nil
	})
}

func (u *DesktopUI) renderClients(c *ui.Context) {
	// Client state labels are localized when the rows are built, so rebuild the
	// cached rows when the active language changed under them.
	if staleClientStatusLang() && !u.ClientsBusy && !u.PluginsBusy {
		u.refreshClientConfigs()
	}
	st := u.clientsState()
	u.renderHookSnippetModal(c)
	u.renderClientVerifyModal(c, st)

	note := ""
	if u.State != nil && u.State.Access.Shared {
		note = i18n.T("ops.shared-gateway-note")
	}
	installedCount := 0
	for _, row := range u.PluginStatuses {
		if row.State == i18n.T("client.state.installed") {
			installedCount++
		}
	}
	busy := u.ClientsBusy || u.PluginsBusy
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, i18n.T("ops.clients-title")).FontSize(21).Bold().LetterSpacing(-0.2).TextColor(SailInkStrong)
				if u.ClientPlugins != nil && len(u.PluginStatuses) > 0 {
					ui.Text(c, i18n.T("ops.plugins-installed-count", installedCount, len(u.PluginStatuses))).
						FontSize(12).
						TextColor(SailInkMuted).
						FontFeatures("tnum").
						SingleLine()
				}
			})
			if note != "" {
				ui.Text(c, note).FontSize(12.5).TextColor(SailInkMuted)
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, buildinfo.Current().Slug+" · "+u.clientEndpoint()).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
			if SailSmallButton(c.Key("clients-refresh"), i18n.T("ops.refresh-status")).Disabled(busy).Clicked() {
				u.refreshClientConfigs()
			}
		})
	})
	if u.PluginError != "" {
		SailAlertBox(c, ToneDanger, i18n.T("ops.plugin-action-failed"), u.PluginError)
	}
	if u.ClientConfigError != "" {
		SailAlertBox(c, ToneDanger, i18n.T("ops.config-action-failed"), u.ClientConfigError)
	}

	SailFlushCard(c.Key("client-list"), func() {
		if u.ClientPlugins != nil {
			legacyMap := make(map[string]clientconfig.Status, len(u.ClientStatuses))
			for _, st := range u.ClientStatuses {
				legacyMap[st.ID] = st
			}
			for i, row := range u.PluginStatuses {
				legacy, hasLegacy := legacyMap[row.ID]
				u.renderPluginRow(c.Key("client-"+row.ID), st, row, legacy, hasLegacy, i > 0)
			}
			return
		}
		for i, row := range u.ClientStatuses {
			u.renderClientRow(c.Key("client-"+row.ID), row, i > 0)
		}
	})
}

func (u *DesktopUI) renderPluginRow(c *ui.Context, st *clientsPageState, row clientplugin.Status, legacy clientconfig.Status, hasLegacy, divider bool) {
	line := ui.Row(c).FillWidth().Padding(14, 18).Gap(12).AlignItems(ui.Center).Justify(ui.SpaceBetween)
	if divider {
		line.BorderWidth(1, 0, 0, 0).BorderColor(SailDivider)
	}
	busy := u.ClientsBusy || u.PluginsBusy
	fullDetail := buildPluginTooltip(row)

	line.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Image(c, clientIcons[row.ID]).Size(26, 26).TextColor(SailInkStrong)
			ui.Text(c, row.Name).FontSize(14).Bold().TextColor(SailInkStrong).SingleLine()
			reveal := ui.ButtonBase(c.Key("reveal-path")).Size(28, 28).Radius(6).Center().Cursor(ui.CursorPointer).
				Label(i18n.T("ops.open-config-location-for", row.Name)).Tooltip(row.Path)
			if reveal.Hovered() {
				reveal.Background(SailBgMuted)
			}
			reveal.Children(func() { ui.Icon(c, clientPathArrow).Size(17, 17).TextColor(SailInkMuted) })
			if reveal.Clicked() {
				u.RunAsync(func() error { return openConfigLocation(row.Path) })
			}
			if sub, warnTone := pluginRowSubtitle(row); sub != "" {
				col := SailInkMuted
				if warnTone {
					col = SailWarningInk
				}
				txt := ui.Text(c, sub).FontSize(12).TextColor(col).SingleLine()
				if fullDetail != "" {
					txt.Tooltip(fullDetail)
				}
			}
		})

		ui.Row(c).AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
			u.renderPluginPrimaryAction(c, st, row, busy, fullDetail)
			more := ui.ButtonBase(c.Key("more")).Size(28, 28).Radius(6).Center().Label(i18n.T("ops.more-actions-for", row.Name)).Disabled(busy)
			if more.Hovered() {
				more.Background(SailBgMuted)
			}
			more.Children(func() { ui.Text(c, "⋯").FontSize(18).TextColor(SailInkMuted) })
			more.PopoverMenuButton(func(menu *ui.Menu) {
				if menu.Item(i18n.T("ops.copy-verify-prompt")).Chosen() {
					c.WriteClipboard(u.ClientPlugins.VerificationPrompt(row.ID, u.clientEndpoint()))
					u.Notify(i18n.T("ops.copied-verify-prompt-for", row.Name))
				}
				if menu.Item(i18n.T("ops.view-hook-config")).Chosen() {
					u.OpenHookSnippetModal(row.ID)
				}
				if hasLegacy {
					switch {
					case legacy.CanUpdate:
						if menu.Item(i18n.T("ops.update-mcp-config")).Chosen() {
							u.runClientConfig(row.ID, "update")
						}
					case legacy.CanWrite:
						if menu.Item(i18n.T("ops.write-mcp-config")).Chosen() {
							u.runClientConfig(row.ID, "write")
						}
					case legacy.CanRevoke:
						if menu.Item(i18n.T("ops.revoke-mcp-config")).Chosen() {
							u.runClientConfig(row.ID, "revoke")
						}
					}
				}
				if row.CanRemove {
					if menu.Item(i18n.T("ops.remove-plugin")).Chosen() {
						u.runClientPlugin(row.ID, "remove")
					}
				}
			})
		})
	})
}

func (u *DesktopUI) renderPluginPrimaryAction(c *ui.Context, st *clientsPageState, row clientplugin.Status, busy bool, fullDetail string) {
	switch {
	case row.CanUpdate:
		btn := SailSmallPrimaryButton(c.Key("update-plugin"), i18n.T("ops.update-plugin")).
			Label(i18n.T("ops.update-plugin-for", row.Name)).
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "update")
		}
	case row.CanInstall && row.HasLegacyMCP:
		btn := SailSmallPrimaryButton(c.Key("upgrade-plugin"), i18n.T("ops.upgrade-to-plugin")).
			Label(i18n.T("ops.upgrade-to-plugin-for", row.Name)).
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "install")
		}
	case row.CanInstall:
		btn := SailSmallPrimaryButton(c.Key("install-plugin"), i18n.T("ops.install-plugin")).
			Label(i18n.T("ops.install-plugin-for", row.Name)).
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "install")
		}
	case row.State == i18n.T("client.state.installed"):
		tip := i18n.T("ops.verify-tip")
		if fullDetail != "" {
			tip = fullDetail + "\n" + tip
		}
		btn := SailSmallButton(c.Key("verified-btn"), i18n.T("ops.installed-check")).
			Label(i18n.T("ops.installed-check-for", row.Name)).
			Tooltip(tip)
		if btn.Clicked() && u.ClientPlugins != nil {
			st.verifyClientID = row.ID
			st.showVerifyModal = true
		}
	case !row.CLIAvailable && (row.State == i18n.T("client.state.not-installed") || row.State == i18n.T("client.state.upgradable")):
		label := i18n.T("ops.install-plugin")
		if row.HasLegacyMCP {
			label = i18n.T("ops.upgrade-to-plugin")
		}
		btn := SailSmallButton(c.Key("install-disabled"), label).Label(row.Name + " " + label).Disabled(true)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
	default:
		if SailSmallButton(c.Key("inspect"), i18n.T("ops.inspect-config")).Label(i18n.T("ops.inspect-config-for", row.Name)).Tooltip(fullDetail).Disabled(busy).Clicked() {
			u.PluginError = i18n.T("ops.client-error-detail", row.Name, row.Detail)
			u.RunAsync(func() error { return openConfigLocation(row.Path) })
		}
	}
}

func (u *DesktopUI) renderClientVerifyModal(c *ui.Context, st *clientsPageState) {
	if !st.showVerifyModal || u.ClientPlugins == nil || st.verifyClientID == "" {
		return
	}
	clientName := agentDisplayName(st.verifyClientID)
	promptText := u.ClientPlugins.VerificationPrompt(st.verifyClientID, u.clientEndpoint())

	ui.DialogBase(c.Key("client-verify-dialog"), &st.showVerifyModal, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42)).Padding(24)
		panel.Width(560).
			MaxWidthPercent(94).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow).
			Clip()

		ui.Row(c).
			FillWidth().
			Padding(16, 20).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailDivider).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Text(c, i18n.T("ops.verify-prompt-title", clientName)).FontSize(15).Bold().TextColor(SailInkStrong)
				if SailSmallButton(c.Key("verify-modal-close"), i18n.T("ops.close")).Clicked() {
					st.showVerifyModal = false
				}
			})

		ui.Column(c).FillWidth().Padding(18, 20).Gap(12).Children(func() {
			ui.Text(c, i18n.T("ops.verify-prompt-body")).
				FontSize(12.5).
				LineHeight(1.5).
				TextColor(SailInkMuted)

			SailCodeBlock(c, "VERIFICATION PROMPT", promptText, func() {
				u.Notify(i18n.T("ops.copied-verify-prompt-for", clientName))
			})

			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
				if SailButton(c.Key("verify-modal-cancel"), i18n.T("ops.close")).Clicked() {
					st.showVerifyModal = false
				}
				if SailPrimaryButton(c.Key("verify-modal-copy-close"), i18n.T("ops.copy-and-close")).Clicked() {
					c.WriteClipboard(promptText)
					u.Notify(i18n.T("ops.copied-verify-prompt-for", clientName))
					st.showVerifyModal = false
				}
			})
		})
	})
}

func pluginRowSubtitle(row clientplugin.Status) (string, bool) {
	switch row.State {
	case i18n.T("client.state.existing-plugin"), i18n.T("client.state.config-changed"), i18n.T("client.state.config-error"), i18n.T("client.state.record-error"):
		return row.State, true
	case i18n.T("client.state.upgradable"):
		if !row.CLIAvailable {
			return i18n.T("ops.legacy-mcp-no-cli"), true
		}
		return i18n.T("ops.legacy-global-mcp"), true
	case i18n.T("client.state.not-installed"):
		if !row.CLIAvailable {
			return i18n.T("ops.cli-not-detected"), false
		}
		return "", false
	case i18n.T("client.state.update-required"), i18n.T("client.state.disabled"):
		return row.State, true
	case i18n.T("client.state.installed"):
		ver := ""
		if row.InstalledVersion != "" {
			ver = "v" + row.InstalledVersion
		}
		if len(row.Warnings) > 0 {
			if ver != "" {
				return ver + " · " + row.Warnings[0], true
			}
			return row.Warnings[0], true
		}
		return ver, false
	default:
		return "", false
	}
}

func buildPluginTooltip(row clientplugin.Status) string {
	var parts []string
	if row.Detail != "" {
		parts = append(parts, row.Detail)
	}
	parts = append(parts, row.Warnings...)
	return strings.Join(parts, "\n")
}

func (u *DesktopUI) renderClientRow(c *ui.Context, row clientconfig.Status, divider bool) {
	line := ui.Row(c).FillWidth().Padding(14, 18).Gap(12).AlignItems(ui.Center).Justify(ui.SpaceBetween)
	if divider {
		line.BorderWidth(1, 0, 0, 0).BorderColor(SailBorder)
	}
	line.Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Image(c, clientIcons[row.ID]).Size(26, 26).TextColor(SailInkStrong)
			ui.Text(c, row.Name).FontSize(14).Bold().TextColor(SailInkStrong).SingleLine()
			reveal := ui.ButtonBase(c.Key("reveal-path")).Size(28, 28).Radius(6).Center().Cursor(ui.CursorPointer).
				Label(i18n.T("ops.open-config-location-for", row.Name)).Tooltip(row.Path)
			if reveal.Hovered() {
				reveal.Background(SailBgMuted)
			}
			reveal.Children(func() { ui.Icon(c, clientPathArrow).Size(17, 17).TextColor(SailInkMuted) })
			if reveal.Clicked() {
				u.RunAsync(func() error { return openConfigLocation(row.Path) })
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
			if SailSmallButton(c.Key("hook-snippet"), i18n.T("ops.hook-config")).
				Label(i18n.T("ops.hook-config-for", row.Name)).
				Tooltip(i18n.T("ops.view-copy-hook-snippet-for", row.Name)).
				Clicked() {
				u.OpenHookSnippetModal(row.ID)
			}
			switch {
			case row.CanUpdate:
				if SailSmallButton(c.Key("update"), i18n.T("ops.update-config")).Disabled(u.ClientsBusy).Clicked() {
					u.runClientConfig(row.ID, "update")
				}
			case row.CanWrite:
				if SailSmallButton(c.Key("write"), i18n.T("ops.write-config")).Disabled(u.ClientsBusy).Clicked() {
					u.runClientConfig(row.ID, "write")
				}
			case row.CanRevoke:
				ui.Text(c, i18n.T("ops.configured-check")).FontSize(12).TextColor(SailInkMuted).Tooltip(i18n.T("ops.config-up-to-date"))
			default:
				if SailSmallButton(c.Key("inspect"), i18n.T("ops.inspect-config")).Tooltip(row.Detail).Disabled(u.ClientsBusy).Clicked() {
					u.ClientConfigError = i18n.T("ops.client-error-detail", row.Name, row.Detail)
					u.RunAsync(func() error { return revealClientConfig(row.Path) })
				}
			}
			if row.CanRevoke {
				more := ui.ButtonBase(c.Key("more")).Size(28, 28).Radius(6).Center().Label(i18n.T("ops.more-actions-for", row.Name)).Disabled(u.ClientsBusy)
				if more.Hovered() {
					more.Background(SailBgMuted)
				}
				more.Children(func() { ui.Text(c, "⋯").FontSize(18).TextColor(SailInkMuted) })
				more.PopoverMenuButton(func(menu *ui.Menu) {
					if menu.Item(i18n.T("ops.view-hook-config")).Chosen() {
						u.OpenHookSnippetModal(row.ID)
					}
					if menu.Item(i18n.T("ops.revoke-config")).Chosen() {
						u.runClientConfig(row.ID, "revoke")
					}
				})
			}
		})
	})
}
