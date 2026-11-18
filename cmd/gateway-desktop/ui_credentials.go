package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
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
		if err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() { st.credentialBusy = false })
			return err
		}
		u.updateUI(func() {
			if st.credentialServerID != id {
				return
			}
			st.credentialBusy = false
			if err != nil {
				st.credentialError = formatAPIError(err)
			} else {
				st.credentialState = state
				if u.State != nil {
					for _, s := range u.State.Registry.Servers {
						if s.ID == id {
							u.applyServerCredentialsSnapshot(u.State.Registry.Servers, map[string]apitypes.ServerCredentials{id: state})
							break
						}
					}
				}
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
		if cached := u.cachedServerCredentials(server); cached != nil {
			st.credentialServerID = server.ID
			st.credentialState = *cached
			st.credentialValues = map[string]string{}
			st.credentialError = ""
		} else {
			u.loadServerCredentials(st, server.ID)
		}
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
			dotCol := SailDangerInk
			txtCol := SailDangerInk
			if entry.Saved {
				status = "已保存"
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			} else if entry.Configured {
				status = "由运行环境提供"
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			}
			if entry.Environment {
				status = "由运行环境提供"
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			}
			if entry.Saved && !st.credentialState.Persistent && !entry.Environment {
				status = "仅本次运行"
				dotCol = SailWarningInk
				txtCol = SailInkMuted
			}
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, entry.Name).Font("monospace").FontSize(12).FontWeight(600).TextColor(SailInkStrong).Width(220).SingleLine()
				value := st.credentialValues[entry.Name]
				if ui.TextInput(c.Key("credential-"+server.ID+"-"+entry.Name), &value).Password().Disabled(st.credentialBusy).Placeholder("输入新凭据，留空保留").Grow(1).Changed() {
					st.credentialValues[entry.Name] = value
				}
				SailQuietStatus(c, status, dotCol, txtCol)
				if SailSmallButton(c.Key("credential-clear-"+server.ID+"-"+entry.Name), "清除").Disabled(st.credentialBusy || !entry.Saved).Clicked() {
					u.saveServerCredentialValues(st, server, map[string]string{entry.Name: ""}, false)
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
			if len(st.credentialState.Credentials) > 0 {
				if SailSmallButton(c.Key("credential-save-"+server.ID), "保存凭据").Disabled(st.credentialBusy || len(values) == 0).Clicked() {
					u.saveServerCredentialValues(st, server, values, false)
				}
				testBtnLabel := "保存凭据并测试连接"
				if st.credentialBusy && st.connBusy {
					testBtnLabel = "验证连接中…"
				}
				if ui.PrimaryButton(c.Key("credential-save-test-"+server.ID), testBtnLabel).Disabled(st.credentialBusy || st.connBusy || len(values) == 0).Clicked() {
					u.saveServerCredentialValues(st, server, values, true)
				}
			}
		})
	})
}

func (u *DesktopUI) saveServerCredentialValues(st *servicesPageState, server apitypes.Server, values map[string]string, testAfter bool) {
	id := server.ID
	st.credentialBusy = true
	if testAfter {
		st.connBusy = true
	}
	st.credentialError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		err := u.Client.SaveServerCredentials(ctx, id, values)
		var state apitypes.ServerCredentials
		if err == nil {
			state, err = u.Client.ServerCredentials(ctx, id)
		}
		var probeCheck apitypes.ConnectionCheck
		var probeErr error
		if err == nil && testAfter {
			target := server
			if u.State != nil {
				for _, latest := range u.State.Registry.Servers {
					if latest.ID == id {
						target = latest
						break
					}
				}
			}
			probeCheck, probeErr = u.Client.TestSource(ctx, target)
			if probeErr != nil && apiclient.IsUnauthorized(probeErr) {
				u.updateUI(func() {
					st.credentialBusy = false
					st.connBusy = false
				})
				return probeErr
			}
		}
		if err == nil {
			err = u.RefreshSync()
		}
		if err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() {
				st.credentialBusy = false
				st.connBusy = false
			})
			return err
		}
		u.updateUI(func() {
			st.connBusy = false
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
			if probeErr != nil {
				st.credentialError = "凭据已保存，但连接测试未通过：" + formatAPIError(probeErr)
			}
		})
		if err == nil && testAfter && probeErr == nil {
			msg := probeCheck.Message
			if msg == "" {
				msg = "凭据已保存且连接测试通过"
			}
			u.Notify(msg)
		} else if err == nil && !testAfter {
			isClear := len(values) == 1
			for _, v := range values {
				if v != "" {
					isClear = false
				}
			}
			if isClear {
				u.Notify("已清除保存的凭据")
			} else {
				u.Notify("服务凭据已保存")
			}
		}
		return nil
	})
}
