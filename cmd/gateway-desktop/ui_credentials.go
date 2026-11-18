package main

import (
	"context"
	"strings"
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

func credentialEntryStatus(entry apitypes.CredentialStatus, persistent bool) (label string, dotCol, txtCol ui.Color) {
	switch {
	case entry.Environment:
		return i18n.T("ops.credential-from-env"), SailSuccessInk, SailInkMuted
	case entry.Saved && !persistent:
		return i18n.T("ops.credential-session-only"), SailWarningInk, SailInkMuted
	case entry.Saved:
		return i18n.T("ops.credential-saved"), SailSuccessInk, SailInkMuted
	case entry.Configured:
		return i18n.T("ops.credential-from-env"), SailSuccessInk, SailInkMuted
	default:
		return i18n.T("client.state.not-configured"), SailDangerInk, SailDangerInk
	}
}

func collectPendingCredentialValues(values map[string]string) map[string]string {
	out := map[string]string{}
	for name, value := range values {
		if value != "" {
			out[name] = value
		}
	}
	return out
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
		isOAuthServer := server.Kind == "mcp" && server.Transport != "stdio" && server.OAuth
		if upg := st.credentialState.OAuthUpgrade; upg != nil {
			ui.Row(c).
				FillWidth().
				Padding(10, 12).
				Gap(12).
				Radius(7).
				Background(SailBgSubtle).
				Border(1, SailBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						ui.Text(c, i18n.T("ops.oauth-switch-title", upg.Title)).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
						ui.Text(c, i18n.T("ops.oauth-switch-desc", upg.Endpoint)).FontSize(11.5).TextColor(SailInkMuted)
					})
					btnLabel := i18n.T("ops.oauth-switch-btn")
					if st.connBusy {
						btnLabel = i18n.T("ops.oauth-switch-busy")
					}
					if SailPrimaryButton(c.Key("credential-oauth-upgrade-"+server.ID), btnLabel).
						Padding(5, 11).
						FontSize(12).
						Disabled(st.credentialBusy || st.connBusy).
						Shrink(0).
						Clicked() {
						u.switchServerToOAuth(st, server, *upg)
					}
				})
		}
		if isOAuthServer {
			hasAuthHeader := strings.TrimSpace(server.Headers["Authorization"]) != ""
			oauthAuthorized := st.credentialState.OAuth != nil && st.credentialState.OAuth.Authorized
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "OAuth 2.0").Font("monospace").FontSize(12).FontWeight(600).TextColor(SailInkStrong).Width(220).SingleLine()
				ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
					switch {
					case hasAuthHeader:
						SailQuietStatus(c, i18n.T("ops.oauth-header-override"), SailWarningInk, SailInkMuted)
					case oauthAuthorized:
						SailQuietStatus(c, i18n.T("ops.oauth-authorized"), SailSuccessInk, SailInkMuted)
					default:
						SailQuietStatus(c, i18n.T("ops.oauth-unauthorized"), SailDangerInk, SailDangerInk)
					}
				})
				if !hasAuthHeader {
					if oauthAuthorized {
						if SailButton(c.Key("oauth-reauth-"+server.ID), i18n.T("ops.oauth-reauth")).Disabled(st.credentialBusy || st.connBusy).Clicked() {
							u.clearServerOAuth(st, server, true)
						}
						if SailButton(c.Key("oauth-clear-"+server.ID), i18n.T("ops.oauth-clear")).Disabled(st.credentialBusy || st.connBusy).Clicked() {
							u.clearServerOAuth(st, server, false)
						}
					} else {
						loginLabel := i18n.T("ops.oauth-login")
						if st.connBusy {
							loginLabel = i18n.T("ops.oauth-waiting")
						}
						if SailPrimaryButton(c.Key("oauth-login-"+server.ID), loginLabel).Disabled(st.credentialBusy || st.connBusy).Clicked() {
							u.probeServerConnection(st, server)
						}
					}
				}
			})
		}
		if len(st.credentialState.Credentials) == 0 && !st.credentialBusy && !isOAuthServer && st.credentialState.OAuthUpgrade == nil {
			ui.Text(c, i18n.T("ops.credentials-not-referenced")).FontSize(12).TextColor(SailInkMuted)
		}
		for _, entry := range st.credentialState.Credentials {
			u.renderCredentialRow(c, st, server, entry)
		}
		u.renderCredentialActions(c, st, server)
	})
}

func (u *DesktopUI) renderCredentialRow(c *ui.Context, st *servicesPageState, server apitypes.Server, entry apitypes.CredentialStatus) {
	status, dotCol, txtCol := credentialEntryStatus(entry, st.credentialState.Persistent)
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

func (u *DesktopUI) renderCredentialActions(c *ui.Context, st *servicesPageState, server apitypes.Server) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if SailButton(c.Key("credential-refresh-"+server.ID), i18n.T("ops.refresh")).Disabled(st.credentialBusy).Clicked() {
			u.loadServerCredentials(st, server.ID)
		}
		if len(st.credentialState.Credentials) == 0 {
			return
		}
		values := collectPendingCredentialValues(st.credentialValues)
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
	})
}

func (u *DesktopUI) latestServerSnapshot(server apitypes.Server) apitypes.Server {
	if u.State == nil {
		return server
	}
	for _, latest := range u.State.Registry.Servers {
		if latest.ID == server.ID {
			return latest
		}
	}
	return server
}

func (u *DesktopUI) notifyCredentialSaveResult(values map[string]string, testAfter bool, probeCheck apitypes.ConnectionCheck, probeErr error) {
	if testAfter {
		if probeErr != nil {
			return
		}
		msg := probeCheck.Message
		if msg == "" {
			msg = i18n.T("ops.credentials-saved-probe-passed")
		}
		u.Notify(msg)
		return
	}
	isClear := len(values) == 1
	for _, v := range values {
		if v != "" {
			isClear = false
			break
		}
	}
	if isClear {
		u.Notify(i18n.T("ops.credentials-cleared"))
	} else {
		u.Notify(i18n.T("ops.service-credentials-saved"))
	}
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
			probeCheck, probeErr = u.Client.TestSource(ctx, u.latestServerSnapshot(server))
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
		if err == nil {
			u.notifyCredentialSaveResult(values, testAfter, probeCheck, probeErr)
		}
		return nil
	})
}

func (u *DesktopUI) clearServerOAuth(st *servicesPageState, server apitypes.Server, reauthorizeAfter bool) {
	if u.Client == nil || st.credentialBusy || st.connBusy {
		return
	}
	id := server.ID
	st.credentialBusy = true
	if reauthorizeAfter {
		st.connBusy = true
	}
	st.credentialError = ""
	u.RunAsync(func() error {
		timeout := 20 * time.Second
		if reauthorizeAfter {
			timeout = 305 * time.Second
			u.Notify(i18n.T("services.toast.oauth_opened", server.Name))
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		err := u.Client.ClearServerOAuth(ctx, id)
		var probeCheck apitypes.ConnectionCheck
		var probeErr error
		if err == nil && reauthorizeAfter {
			probeCheck, probeErr = u.Client.TestSource(ctx, server)
			if probeErr != nil && apiclient.IsUnauthorized(probeErr) {
				u.updateUI(func() {
					st.credentialBusy = false
					st.connBusy = false
				})
				return probeErr
			}
		}
		var state apitypes.ServerCredentials
		if err == nil {
			state, _ = u.Client.ServerCredentials(ctx, id)
			if syncErr := u.RefreshSync(); syncErr != nil && apiclient.IsUnauthorized(syncErr) {
				u.updateUI(func() {
					st.credentialBusy = false
					st.connBusy = false
				})
				return syncErr
			}
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
			if probeErr != nil {
				st.credentialError = i18n.T("ops.oauth-reauth-incomplete", formatAPIError(probeErr))
			}
		})
		if err == nil {
			if reauthorizeAfter {
				if probeErr == nil {
					msg := probeCheck.Message
					if msg == "" {
						msg = i18n.T("ops.oauth-reauth-ok")
					}
					u.Notify(msg)
				} else {
					u.Notify(i18n.T("ops.oauth-incomplete", formatAPIError(probeErr)))
				}
			} else {
				u.Notify(i18n.T("ops.oauth-cleared"))
			}
		}
		return nil
	})
}

func (u *DesktopUI) switchServerToOAuth(st *servicesPageState, server apitypes.Server, upg apitypes.OAuthUpgradeInfo) {
	if u.Client == nil || st.connBusy || st.credentialBusy {
		return
	}
	updated := server
	updated.Kind = "mcp"
	updated.Transport = "http"
	updated.Endpoint = upg.Endpoint
	updated.OAuth = true
	updated.Enabled = true
	updated.Command = ""
	updated.Args = nil
	updated.Env = nil
	updated.WorkDir = ""
	updated.Headers = nil

	st.connBusy = true
	st.credentialBusy = true
	st.connError = ""
	st.credentialError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 305*time.Second)
		defer cancel()
		u.Notify(i18n.T("ops.oauth-switch-started", upg.Title))
		check, err := u.Client.TestSource(ctx, updated)
		if err != nil && apiclient.IsUnauthorized(err) {
			u.updateUI(func() {
				st.connBusy = false
				st.credentialBusy = false
			})
			return err
		}
		if syncErr := u.RefreshSync(); syncErr != nil && apiclient.IsUnauthorized(syncErr) {
			u.updateUI(func() {
				st.connBusy = false
				st.credentialBusy = false
			})
			return syncErr
		}
		u.updateUI(func() {
			st.connBusy = false
			st.credentialBusy = false
			st.connHydratedID = ""
			st.credentialServerID = ""
			if err != nil {
				st.connError = formatAPIError(err)
			}
		})
		if err != nil {
			u.Notify(i18n.T("ops.oauth-switch-failed", formatAPIError(err)))
			return nil
		}
		msg := check.Message
		if msg == "" {
			msg = i18n.T("ops.oauth-switch-ok", upg.Title)
		}
		u.Notify(msg)
		return nil
	})
}
