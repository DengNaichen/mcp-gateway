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
	for _, row := range u.ClientStatuses {
		u.renderClientCard(c.Key("client-"+row.ID), row)
	}
}
func (u *DesktopUI) renderClientCard(c *ui.Context, row clientconfig.Status) {
	ui.Column(c).FillWidth().Padding(18).Gap(8).Radius(8).Border(1, SailBorder).Background(SailBgCanvas).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Image(c, clientIcons[row.ID]).Size(28, 28).TextColor(SailInkStrong)
				ui.Text(c, row.Name).FontSize(16).Bold().TextColor(SailInkStrong)
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
			ui.Text(c, row.State).FontSize(12).TextColor(SailInkMuted)
		})
		if row.Detail != "" {
			ui.Text(c, row.Detail).FontSize(12).TextColor(SailInkMuted)
		}
		ui.Row(c).Gap(8).Children(func() {
			if SailSmallButton(c.Key("write"), "写入配置").Disabled(u.ClientsBusy || !row.CanWrite).Clicked() {
				u.runClientConfig(row.ID, "write")
			}
			if SailSmallButton(c.Key("revoke"), "撤回配置").Disabled(u.ClientsBusy || !row.CanRevoke).Clicked() {
				u.runClientConfig(row.ID, "revoke")
			}
		})
	})
}
