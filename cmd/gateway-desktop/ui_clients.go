package main

import (
	"errors"

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
	note := ""
	if u.State != nil && u.State.Access.Shared {
		note = "共享网关需在客户端补充 Bearer Token"
	}
	SailSectionHeader(c, "连接客户端", note, func() {
		if ep := u.clientEndpoint(); ep != "" {
			ui.Text(c, ep).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
		}
		if SailSmallButton(c.Key("clients-refresh"), "刷新状态").Disabled(u.ClientsBusy).Clicked() {
			u.refreshClientConfigs()
		}
	})

	if u.ClientConfigError != "" {
		SailAlertBox(c, ToneDanger, "配置操作失败", u.ClientConfigError)
	}

	SailFlushCard(c, func() {
		for idx, row := range u.ClientStatuses {
			u.renderClientRow(c.Key("client-"+row.ID), row, idx == len(u.ClientStatuses)-1)
		}
	})
}

func (u *DesktopUI) renderClientRow(c *ui.Context, row clientconfig.Status, isLast bool) {
	dotColor := SailInkFaint
	stateColor := SailInkMuted
	if row.State == "已配置" {
		dotColor = SailSuccessInk
		stateColor = SailInkBody
	} else if row.State != "未配置" {
		dotColor = SailWarningInk
		stateColor = SailWarningInk
	}

	line := ui.Row(c).
		FillWidth().
		Padding(16, 20).
		Gap(16).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween)
	if !isLast {
		line.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	if line.Hovered() {
		line.Background(SailBgHover)
	}
	line.Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				SailStatusDot(c, dotColor)
				ui.Text(c, row.Name).FontSize(14).FontWeight(600).TextColor(SailInkStrong).SingleLine()
			})
			ui.Text(c, row.Path).Font("monospace").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
			if row.Detail != "" {
				ui.Text(c, row.Detail).FontSize(12).TextColor(SailInkMuted).MaxLines(1)
			}
		})

		ui.Row(c).Shrink(0).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, row.State).FontSize(12).FontWeight(500).TextColor(stateColor).SingleLine()
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if SailSmallButton(c.Key("write"), "写入配置").Disabled(u.ClientsBusy || !row.CanWrite).Clicked() {
					u.runClientConfig(row.ID, "write")
				}
				if SailSmallButton(c.Key("revoke"), "撤回配置").Disabled(u.ClientsBusy || !row.CanRevoke).Clicked() {
					u.runClientConfig(row.ID, "revoke")
				}
			})
		})
	})
}
