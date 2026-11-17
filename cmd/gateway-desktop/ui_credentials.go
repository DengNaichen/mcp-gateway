package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
)

func (u *DesktopUI) loadServerCredentials(st *servicesPageState, id string) {
	st.credentialServerID = id
	st.credentialState = apitypes.ServerCredentials{}
	st.credentialValues = map[string]string{}
	st.credentialError = ""
	st.credentialBusy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		state, err := u.Client.ServerCredentials(ctx, id)
		u.updateUI(func() {
			if st.credentialServerID != id {
				return
			}
			st.credentialBusy = false
			if err != nil {
				st.credentialError = formatAPIError(err)
			} else {
				st.credentialState = state
			}
		})
		return nil
	})
}

func (u *DesktopUI) renderServerCredentials(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	if u.Client == nil {
		return
	}
	if st.credentialServerID != server.ID {
		u.loadServerCredentials(st, server.ID)
	}
	SailCard(c, func() {
		subtitle := "凭据仅用于本次运行；当前实例未启用安全持久化"
		if st.credentialState.Persistent {
			subtitle = "凭据保存在系统钥匙串，重启后自动恢复；环境变量优先"
		}
		SailCardTitle(c, "服务凭据", subtitle, nil)
		if st.credentialError != "" {
			SailAlertBox(c, ToneDanger, "凭据操作失败", st.credentialError)
		}
		if len(st.credentialState.Credentials) == 0 && !st.credentialBusy {
			ui.Text(c, "此服务未引用环境变量；修改连接配置后刷新凭据").FontSize(12).TextColor(SailInkMuted)
		}
		for _, entry := range st.credentialState.Credentials {
			status := "未配置"
			if entry.Saved {
				status = "已保存"
			} else if entry.Configured {
				status = "由运行环境提供"
			}
			if entry.Environment {
				status = "由运行环境提供"
			}
			if entry.Saved && !st.credentialState.Persistent && !entry.Environment {
				status = "仅本次运行"
			}
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, entry.Name).Font("monospace").FontSize(12).Width(220).SingleLine()
				value := st.credentialValues[entry.Name]
				if ui.TextInput(c.Key("credential-"+server.ID+"-"+entry.Name), &value).Password().Disabled(st.credentialBusy).Placeholder("输入新凭据，留空保留").Grow(1).Changed() {
					st.credentialValues[entry.Name] = value
				}
				ui.Text(c, status).FontSize(12).TextColor(SailInkMuted)
				if SailSmallButton(c.Key("credential-clear-"+server.ID+"-"+entry.Name), "清除").Disabled(st.credentialBusy || !entry.Saved).Clicked() {
					u.saveServerCredentialValues(st, server.ID, map[string]string{entry.Name: ""})
				}
			})
		}
		ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("credential-refresh-"+server.ID), "刷新").Disabled(st.credentialBusy).Clicked() {
				u.loadServerCredentials(st, server.ID)
			}
			values := map[string]string{}
			for name, value := range st.credentialValues {
				if value != "" {
					values[name] = value
				}
			}
			if ui.PrimaryButton(c.Key("credential-save-"+server.ID), "保存凭据").Disabled(st.credentialBusy || len(values) == 0).Clicked() {
				u.saveServerCredentialValues(st, server.ID, values)
			}
		})
	})
}

func (u *DesktopUI) saveServerCredentialValues(st *servicesPageState, id string, values map[string]string) {
	st.credentialBusy = true
	st.credentialError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		err := u.Client.SaveServerCredentials(ctx, id, values)
		var state apitypes.ServerCredentials
		if err == nil {
			state, err = u.Client.ServerCredentials(ctx, id)
		}
		if err == nil {
			err = u.RefreshSync()
		}
		u.updateUI(func() {
			if st.credentialServerID != id {
				return
			}
			st.credentialBusy = false
			if err != nil {
				st.credentialError = formatAPIError(err)
				return
			}
			st.credentialState = state
			st.credentialValues = map[string]string{}
		})
		return nil
	})
}
