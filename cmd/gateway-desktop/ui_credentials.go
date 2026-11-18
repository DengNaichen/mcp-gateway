package main

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
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
		subtitle := i18n.T("ops.credentials-ephemeral-note")
		if st.credentialState.Persistent {
			subtitle = i18n.T("ops.credentials-keychain-note")
		}
		SailCardTitle(c, i18n.T("ops.service-credentials"), subtitle, nil)
		if st.credentialError != "" {
			SailAlertBox(c, ToneDanger, i18n.T("ops.credential-action-failed"), st.credentialError)
		}
		if len(st.credentialState.Credentials) == 0 && !st.credentialBusy {
			ui.Text(c, i18n.T("ops.credentials-not-referenced")).FontSize(12).TextColor(SailInkMuted)
		}
		for _, entry := range st.credentialState.Credentials {
			status := i18n.T("client.state.not-configured")
			dotCol := SailDangerInk
			txtCol := SailDangerInk
			if entry.Saved {
				status = i18n.T("ops.credential-saved")
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			} else if entry.Configured {
				status = i18n.T("ops.credential-from-env")
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			}
			if entry.Environment {
				status = i18n.T("ops.credential-from-env")
				dotCol = SailSuccessInk
				txtCol = SailInkMuted
			}
			if entry.Saved && !st.credentialState.Persistent && !entry.Environment {
				status = i18n.T("ops.credential-session-only")
				dotCol = SailWarningInk
				txtCol = SailInkMuted
			}
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, entry.Name).Font("monospace").FontSize(12).FontWeight(600).TextColor(SailInkStrong).Width(220).SingleLine()
				value := st.credentialValues[entry.Name]
				if SailTextInput(c.Key("credential-"+server.ID+"-"+entry.Name), &value).Password().Disabled(st.credentialBusy).Placeholder(i18n.T("ops.credential-placeholder")).Grow(1).Changed() {
					st.credentialValues[entry.Name] = value
				}
				SailQuietStatus(c, status, dotCol, txtCol)
				if SailButton(c.Key("credential-clear-"+server.ID+"-"+entry.Name), i18n.T("ops.clear")).Disabled(st.credentialBusy || !entry.Saved).Clicked() {
					u.saveServerCredentialValues(st, server, map[string]string{entry.Name: ""}, false)
				}
			})
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailButton(c.Key("credential-refresh-"+server.ID), i18n.T("ops.refresh")).Disabled(st.credentialBusy).Clicked() {
				u.loadServerCredentials(st, server.ID)
			}
			values := map[string]string{}
			for name, value := range st.credentialValues {
				if value != "" {
					values[name] = value
				}
			}
			if len(st.credentialState.Credentials) > 0 {
				if SailButton(c.Key("credential-save-"+server.ID), i18n.T("ops.save-credentials")).Disabled(st.credentialBusy || len(values) == 0).Clicked() {
					u.saveServerCredentialValues(st, server, values, false)
				}
				testBtnLabel := i18n.T("ops.save-and-test")
				if st.credentialBusy && st.connBusy {
					testBtnLabel = i18n.T("ops.verifying-connection")
				}
				if SailPrimaryButton(c.Key("credential-save-test-"+server.ID), testBtnLabel).Disabled(st.credentialBusy || st.connBusy || len(values) == 0).Clicked() {
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
				st.credentialError = i18n.T("ops.credentials-saved-probe-failed", formatAPIError(probeErr))
			}
		})
		if err == nil && testAfter && probeErr == nil {
			msg := probeCheck.Message
			if msg == "" {
				msg = i18n.T("ops.credentials-saved-probe-passed")
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
				u.Notify(i18n.T("ops.credentials-cleared"))
			} else {
				u.Notify(i18n.T("ops.service-credentials-saved"))
			}
		}
		return nil
	})
}
