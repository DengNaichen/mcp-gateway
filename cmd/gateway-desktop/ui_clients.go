package main

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
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

func (u *DesktopUI) refreshClientConfigs() {
	if (u.ClientConfigs == nil && u.ClientPlugins == nil) || u.ClientsBusy || u.PluginsBusy {
		return
	}
	endpoint := u.clientEndpoint()
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
		})
		return nil
	})
}

// changeClientConfig is shared by native actions and the local verification socket.
func (u *DesktopUI) changeClientConfig(id, action, endpoint string) error {
	if u.ClientConfigs == nil {
		return errors.New("无法读取用户配置目录")
	}
	switch action {
	case "write":
		return u.ClientConfigs.Write(id, endpoint)
	case "update":
		return u.ClientConfigs.Update(id, endpoint)
	case "revoke":
		return u.ClientConfigs.Revoke(id)
	default:
		return errors.New("未知配置操作")
	}
}

// changeClientPlugin is shared by native actions and the local verification socket.
func (u *DesktopUI) changeClientPlugin(id, action, endpoint string) error {
	if u.ClientPlugins == nil {
		return errors.New("无法读取官方插件状态")
	}
	switch action {
	case "install":
		return u.ClientPlugins.Install(id, endpoint)
	case "update":
		return u.ClientPlugins.Update(id, endpoint)
	case "remove":
		return u.ClientPlugins.Remove(id)
	default:
		return errors.New("未知插件操作")
	}
}

func (u *DesktopUI) runClientConfig(id, action string) {
	if u.ClientsBusy || u.PluginsBusy {
		return
	}
	endpoint := u.clientEndpoint()
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
		})
		if err == nil {
			switch action {
			case "write":
				u.Notify("配置已写入，请重启客户端；Pi 可执行 /reload")
			case "update":
				u.Notify("配置已更新，请重载客户端")
			default:
				u.Notify("配置已撤回，请重载客户端")
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
			u.PluginError = ""
			if err != nil {
				u.PluginError = err.Error()
			}
		})
		if err == nil {
			switch action {
			case "install":
				u.Notify("官方插件已安装，重启客户端后生效；Pi 可执行 /reload")
			case "update":
				u.Notify("官方插件已更新，请重载客户端")
			default:
				u.Notify("官方插件已卸载，请重载客户端")
			}
		}
		return nil
	})
}

func (u *DesktopUI) renderClients(c *ui.Context) {
	st := u.clientsState()
	u.renderHookSnippetModal(c)
	u.renderClientVerifyModal(c, st)

	note := ""
	if u.State != nil && u.State.Access.Shared {
		note = "共享网关需在客户端补充 Bearer Token"
	}
	installedCount := 0
	for _, row := range u.PluginStatuses {
		if row.State == "已安装" {
			installedCount++
		}
	}
	busy := u.ClientsBusy || u.PluginsBusy
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, "连接客户端").FontSize(21).Bold().LetterSpacing(-0.2).TextColor(SailInkStrong)
				if u.ClientPlugins != nil && len(u.PluginStatuses) > 0 {
					ui.Textf(c, "官方插件已安装 %d / %d", installedCount, len(u.PluginStatuses)).
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
			if SailSmallButton(c.Key("clients-refresh"), "刷新状态").Disabled(busy).Clicked() {
				u.refreshClientConfigs()
			}
		})
	})
	if u.PluginError != "" {
		SailAlertBox(c, ToneDanger, "插件操作失败", u.PluginError)
	}
	if u.ClientConfigError != "" {
		SailAlertBox(c, ToneDanger, "配置操作失败", u.ClientConfigError)
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
				Label("打开 " + row.Name + " 配置位置").Tooltip(row.Path)
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
			more := ui.ButtonBase(c.Key("more")).Size(28, 28).Radius(6).Center().Label(row.Name + " 更多操作").Disabled(busy)
			if more.Hovered() {
				more.Background(SailBgMuted)
			}
			more.Children(func() { ui.Text(c, "⋯").FontSize(18).TextColor(SailInkMuted) })
			more.PopoverMenuButton(func(menu *ui.Menu) {
				if menu.Item("复制验证 Prompt").Chosen() {
					c.WriteClipboard(u.ClientPlugins.VerificationPrompt(row.ID, u.clientEndpoint()))
					u.Notify("已复制 " + row.Name + " 验证 Prompt")
				}
				if menu.Item("查看 Hook 配置").Chosen() {
					u.OpenHookSnippetModal(row.ID)
				}
				if hasLegacy {
					switch {
					case legacy.CanUpdate:
						if menu.Item("更新 MCP 配置").Chosen() {
							u.runClientConfig(row.ID, "update")
						}
					case legacy.CanWrite:
						if menu.Item("仅写入 MCP 配置").Chosen() {
							u.runClientConfig(row.ID, "write")
						}
					case legacy.CanRevoke:
						if menu.Item("撤回 MCP 配置").Chosen() {
							u.runClientConfig(row.ID, "revoke")
						}
					}
				}
				if row.CanRemove {
					if menu.Item("卸载插件").Chosen() {
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
		btn := ui.PrimaryButton(c.Key("update-plugin"), "更新插件").
			Padding(5, 11).
			FontSize(12.5).
			Label(row.Name + " 更新插件").
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "update")
		}
	case row.CanInstall && row.HasLegacyMCP:
		btn := ui.PrimaryButton(c.Key("upgrade-plugin"), "升级为插件").
			Padding(5, 11).
			FontSize(12.5).
			Label(row.Name + " 升级为插件").
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "install")
		}
	case row.CanInstall:
		btn := ui.PrimaryButton(c.Key("install-plugin"), "安装插件").
			Padding(5, 11).
			FontSize(12.5).
			Label(row.Name + " 安装插件").
			Disabled(busy)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
		if btn.Clicked() {
			u.runClientPlugin(row.ID, "install")
		}
	case row.State == "已安装":
		tip := "点击查看端到端验证 Prompt；客户端重启后生效，Pi 可执行 /reload"
		if fullDetail != "" {
			tip = fullDetail + "\n" + tip
		}
		btn := SailSmallButton(c.Key("verified-btn"), "✓ 已安装").
			Label(row.Name + " ✓ 已安装").
			Tooltip(tip)
		if btn.Clicked() && u.ClientPlugins != nil {
			st.verifyClientID = row.ID
			st.showVerifyModal = true
		}
	case !row.CLIAvailable && (row.State == "未安装" || row.State == "待升级"):
		label := "安装插件"
		if row.HasLegacyMCP {
			label = "升级为插件"
		}
		btn := SailSmallButton(c.Key("install-disabled"), label).Label(row.Name + " " + label).Disabled(true)
		if fullDetail != "" {
			btn.Tooltip(fullDetail)
		}
	default:
		if SailSmallButton(c.Key("inspect"), "检查配置").Label(row.Name + " 检查配置").Tooltip(fullDetail).Disabled(busy).Clicked() {
			u.PluginError = row.Name + "：" + row.Detail
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
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25)).
			Clip()

		ui.Row(c).
			FillWidth().
			Padding(16, 20).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailDivider).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween).
			Children(func() {
				ui.Textf(c, "客户端验证 Prompt · %s", clientName).FontSize(15).Bold().TextColor(SailInkStrong)
				if SailSmallButton(c.Key("verify-modal-close"), "关闭").Clicked() {
					st.showVerifyModal = false
				}
			})

		ui.Column(c).FillWidth().Padding(18, 20).Gap(12).Children(func() {
			ui.Text(c, "粘贴到对应 Coding Agent 会话中，确认 MCP 工具发现与 Hook 仓库签名链路已连通：").
				FontSize(12.5).
				LineHeight(1.5).
				TextColor(SailInkMuted)

			SailCodeBlock(c, "VERIFICATION PROMPT", promptText, func() {
				u.Notify("已复制 " + clientName + " 验证 Prompt")
			})

			ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
				if SailSmallButton(c.Key("verify-modal-cancel"), "关闭").Clicked() {
					st.showVerifyModal = false
				}
				if ui.PrimaryButton(c.Key("verify-modal-copy-close"), "复制并关闭").Padding(5, 12).FontSize(12.5).Clicked() {
					c.WriteClipboard(promptText)
					u.Notify("已复制 " + clientName + " 验证 Prompt")
					st.showVerifyModal = false
				}
			})
		})
	})
}

func pluginRowSubtitle(row clientplugin.Status) (string, bool) {
	switch row.State {
	case "已有插件", "配置已变更", "配置异常", "记录异常":
		return row.State, true
	case "待升级":
		if !row.CLIAvailable {
			return "仅全局 MCP · 未检测到 CLI", true
		}
		return "旧版全局 MCP", true
	case "未安装":
		if !row.CLIAvailable {
			return "未检测到 CLI", false
		}
		return "", false
	case "需要更新", "已禁用":
		return row.State, true
	case "已安装":
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
				Label("打开 " + row.Name + " 配置位置").Tooltip(row.Path)
			if reveal.Hovered() {
				reveal.Background(SailBgMuted)
			}
			reveal.Children(func() { ui.Icon(c, clientPathArrow).Size(17, 17).TextColor(SailInkMuted) })
			if reveal.Clicked() {
				u.RunAsync(func() error { return openConfigLocation(row.Path) })
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
			if SailSmallButton(c.Key("hook-snippet"), "Hook 配置").
				Label(row.Name + " Hook 配置").
				Tooltip("查看并复制 " + row.Name + " 手动 Hook 配置片段").
				Clicked() {
				u.OpenHookSnippetModal(row.ID)
			}
			switch {
			case row.CanUpdate:
				if SailSmallButton(c.Key("update"), "更新配置").Disabled(u.ClientsBusy).Clicked() {
					u.runClientConfig(row.ID, "update")
				}
			case row.CanWrite:
				if SailSmallButton(c.Key("write"), "写入配置").Disabled(u.ClientsBusy).Clicked() {
					u.runClientConfig(row.ID, "write")
				}
			case row.CanRevoke:
				ui.Text(c, "✓ 已配置").FontSize(12).TextColor(SailInkMuted).Tooltip("配置已是最新；客户端重载后生效")
			default:
				if SailSmallButton(c.Key("inspect"), "检查配置").Tooltip(row.Detail).Disabled(u.ClientsBusy).Clicked() {
					u.ClientConfigError = row.Name + "：" + row.Detail
					u.RunAsync(func() error { return revealClientConfig(row.Path) })
				}
			}
			if row.CanRevoke {
				more := ui.ButtonBase(c.Key("more")).Size(28, 28).Radius(6).Center().Label(row.Name + " 更多操作").Disabled(u.ClientsBusy)
				if more.Hovered() {
					more.Background(SailBgMuted)
				}
				more.Children(func() { ui.Text(c, "⋯").FontSize(18).TextColor(SailInkMuted) })
				more.PopoverMenuButton(func(menu *ui.Menu) {
					if menu.Item("查看 Hook 配置").Chosen() {
						u.OpenHookSnippetModal(row.ID)
					}
					if menu.Item("撤回配置").Chosen() {
						u.runClientConfig(row.ID, "revoke")
					}
				})
			}
		})
	})
}
