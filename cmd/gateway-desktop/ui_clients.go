package main

import (
	"errors"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"

	"github.com/egoist/mygo/ui"
)

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
	if u.ClientConfigs == nil || u.ClientsBusy {
		return
	}
	endpoint := u.clientEndpoint()
	u.ClientsBusy = true
	u.RunAsync(func() error {
		rows := u.ClientConfigs.Statuses(endpoint)
		u.updateUI(func() { u.ClientStatuses = rows; u.ClientsBusy = false })
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
func (u *DesktopUI) runClientConfig(id, action string) {
	if u.ClientsBusy {
		return
	}
	endpoint := u.clientEndpoint()
	u.ClientsBusy = true
	u.RunAsync(func() error {
		err := u.changeClientConfig(id, action, endpoint)
		rows := u.ClientConfigs.Statuses(endpoint)
		u.updateUI(func() {
			u.ClientStatuses = rows
			u.ClientsBusy = false
			u.ClientConfigError = ""
			if err != nil {
				u.ClientConfigError = err.Error()
			}
		})
		if err == nil {
			if action == "write" {
				u.Notify("配置已写入，请重启客户端；Pi 可执行 /reload")
			} else if action == "update" {
				u.Notify("配置已更新，请重载客户端")
			} else {
				u.Notify("配置已撤回，请重载客户端")
			}
		}
		return nil
	})
}
func (u *DesktopUI) renderClients(c *ui.Context) {
	ui.Text(c, "连接客户端").FontSize(24).Bold().TextColor(SailInkStrong)
	ui.Text(c, "将 MCP Gateway 写入本机当前用户的全局配置。客户端自动发现 discover_tools、get_tool_schema 和 invoke_tool。").FontSize(13).TextColor(SailInkMuted)
	ui.Text(c, "客户端条目："+buildinfo.Current().Slug+" · "+u.clientEndpoint()).FontSize(12).TextColor(SailInkMuted)
	ui.Text(c, "已配置表示文件已写入；实际连接需要 Gateway 运行，并由客户端重载配置。项目配置可能覆盖全局配置。").FontSize(12).TextColor(SailInkMuted)
	if u.State != nil && u.State.Access.Shared {
		ui.Text(c, "当前网关需要身份凭据。写入后需在客户端补充 Bearer Token 才能连接。").FontSize(12).TextColor(SailInkMuted)
	}
	if u.ClientConfigError != "" {
		ui.Text(c, u.ClientConfigError).FontSize(13).TextColor(SailInkStrong)
	}
	if SailSmallButton(c.Key("clients-refresh"), "刷新状态").Width(110).Disabled(u.ClientsBusy).Clicked() {
		u.refreshClientConfigs()
	}

	ui.Column(c.Key("client-list")).FillWidth().Radius(8).Border(1, SailBorder).Background(SailBgCanvas).Clip().Children(func() {
		for i, row := range u.ClientStatuses {
			u.renderClientRow(c.Key("client-"+row.ID), row, i > 0)
		}
	})
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
				u.RunAsync(func() error { return revealClientConfig(row.Path) })
			}
		})
		ui.Row(c).AlignItems(ui.Center).Gap(8).Shrink(0).Children(func() {
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
					if menu.Item("撤回配置").Chosen() {
						u.runClientConfig(row.ID, "revoke")
					}
				})
			}
		})
	})
}
