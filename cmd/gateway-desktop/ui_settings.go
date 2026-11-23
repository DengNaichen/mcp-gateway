package main

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type settingsState struct {
	initialized        bool
	lastSyncedSettings apitypes.Settings

	editingProfileID string
	config           apitypes.Settings
	maxToolsFloat    float64
	timeoutFloat     float64
	apiKey           string
	clearKey         bool
	testIntent       string
	advancedExpanded bool
	busyMode         string // "", "save", "test"
	pingBusy         bool
	pingResult       *apitypes.PingModelResult
	pingPassedKey    string
	pingErr          string
	pingDetail       string
	profileBusyID    string
	guardrailBusy    bool
	errTitle         string
	errDetail        string
	testResult       *apitypes.DiscoverResult
}

func (u *DesktopUI) settingsState() *settingsState {
	if u.settings == nil {
		u.settings = &settingsState{}
	}
	return u.settings
}

func settingsIsDirty(st *settingsState, saved apitypes.Settings) bool {
	return st.config.BaseURL != saved.BaseURL ||
		st.config.Model != saved.Model ||
		st.config.MaxTools != saved.MaxTools ||
		st.config.TimeoutSeconds != saved.TimeoutSeconds ||
		st.apiKey != "" ||
		st.clearKey
}

func settingsConnectionKey(cfg apitypes.Settings, profileID, apiKey string, clearKey bool) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s\x00%t",
		profileID,
		strings.TrimSpace(cfg.BaseURL),
		strings.TrimSpace(cfg.Model),
		cfg.TimeoutSeconds,
		strings.TrimSpace(apiKey),
		clearKey,
	)
}

func settingsPingPassed(st *settingsState) bool {
	return st.pingResult != nil &&
		st.pingPassedKey != "" &&
		st.pingPassedKey == settingsConnectionKey(st.config, st.editingProfileID, st.apiKey, st.clearKey)
}

func isVerifiedActiveConnectionUnchanged(st *settingsState, state *apitypes.Bootstrap) bool {
	if state == nil || !state.Setup.ModelReady || (state.Setup.ModelCheck != nil && state.Setup.ModelCheck.Stale) {
		return false
	}
	if !state.KeyConfigured || st.clearKey || strings.TrimSpace(st.apiKey) != "" {
		return false
	}
	activeID := ""
	for _, p := range state.Profiles {
		if p.Active {
			activeID = p.ID
			break
		}
	}
	if st.editingProfileID != activeID {
		return false
	}
	return strings.TrimSpace(st.config.BaseURL) == strings.TrimSpace(state.Settings.BaseURL) &&
		strings.TrimSpace(st.config.Model) == strings.TrimSpace(state.Settings.Model) &&
		st.config.TimeoutSeconds == state.Settings.TimeoutSeconds
}

func settingsCanSave(st *settingsState, state *apitypes.Bootstrap) bool {
	if st.busyMode != "" || st.pingBusy {
		return false
	}
	if strings.TrimSpace(st.config.BaseURL) == "" || strings.TrimSpace(st.config.Model) == "" {
		return false
	}
	if settingsPingPassed(st) {
		return true
	}
	return isVerifiedActiveConnectionUnchanged(st, state) && st.config.MaxTools != state.Settings.MaxTools
}

func resetSettingsTestOutput(st *settingsState) {
	st.testResult = nil
	st.errTitle = ""
	st.errDetail = ""
}

func clearSettingsFeedback(st *settingsState) {
	st.pingResult = nil
	st.pingPassedKey = ""
	st.pingErr = ""
	st.pingDetail = ""
	resetSettingsTestOutput(st)
}

func loadProfileIntoSettings(st *settingsState, id string, s apitypes.Settings) {
	st.editingProfileID = id
	st.config = s
	st.maxToolsFloat = float64(clampInt(s.MaxTools, 1, 12))
	st.timeoutFloat = float64(clampInt(s.TimeoutSeconds, 5, 180))
	st.apiKey = ""
	st.clearKey = false
	clearSettingsFeedback(st)
}

func (u *DesktopUI) renderSettings(c *ui.Context) {
	state := u.State
	st := u.settingsState()

	// Synchronize form state from Bootstrap when first initialized or when saved settings change while clean.
	if !st.initialized {
		st.initialized = true
		st.lastSyncedSettings = state.Settings
		activeID := ""
		for _, p := range state.Profiles {
			if p.Active {
				activeID = p.ID
				break
			}
		}
		loadProfileIntoSettings(st, activeID, state.Settings)
	} else if st.lastSyncedSettings != state.Settings {
		wasDirty := settingsIsDirty(st, st.lastSyncedSettings)
		st.lastSyncedSettings = state.Settings
		if !wasDirty && st.config != state.Settings {
			activeID := ""
			for _, p := range state.Profiles {
				if p.Active {
					activeID = p.ID
					break
				}
			}
			loadProfileIntoSettings(st, activeID, state.Settings)
		}
	}

	dirty := settingsIsDirty(st, state.Settings)

	SailSectionHeader(c, i18n.T("ops.settings-title"), "", nil)

	// Section 0: Read-only runtime trust mode.
	renderSettingsTrustModeCard(c, state)

	// Section 1: Actor & Read/Write Guardrail Matrix.
	u.renderSettingsGuardrailSection(c, st, state)

	// Section 2: Unified Decisions Model & Collapsible Advanced Routing Policy.
	u.renderSettingsModelSection(c, st, state, dirty)

	// Section 3: Saved Model Profiles List.
	u.renderSettingsProfilesSection(c, st, state)
}

// renderSettingsTrustModeCard states the runtime trust boundary in one line;
// it is read-only because the mode is fixed by how the gateway was started.
func renderSettingsTrustModeCard(c *ui.Context, state *apitypes.Bootstrap) {
	mode := policySecurityMode(state)
	desc := i18n.T("policy.mode-local-desc")
	if mode == apitypes.SecurityModeSharedBoundary {
		desc = i18n.T("policy.mode-shared-desc")
	}
	SailCard(c.Key("settings-trust-mode"), func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Text(c, i18n.T("policy.trust-mode")).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
				SailQuietStatus(c, policySecurityModeLabel(mode), SailSuccessInk, SailInkStrong)
			})
			ui.Text(c, "trust = "+policyExpectedTrust(mode)).Font("monospace").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
		})
		ui.Text(c, desc).FontSize(12.5).LineHeight(1.5).TextColor(SailInkBody)
	})
}

func (u *DesktopUI) renderSettingsGuardrailSection(c *ui.Context, st *settingsState, state *apitypes.Bootstrap) {
	sharedActor := ""
	if state.Access.Shared {
		sharedActor = state.Access.Actor
	}
	actorRows := ComputeActorMatrix(state.Registry.Servers, state.Registry.Tools, sharedActor)

	realActiveServers := make(map[string]bool)
	for _, s := range state.Registry.Servers {
		if !s.Demo && s.Enabled {
			realActiveServers[s.ID] = true
		}
	}
	var enabledWriteTools []apitypes.Tool
	for _, t := range state.Registry.Tools {
		if t.Enabled && !t.ReadOnly && realActiveServers[t.ServerID] {
			enabledWriteTools = append(enabledWriteTools, t)
		}
	}

	SailFlushCard(c, func() {
		accessDesc := i18n.T("ops.local-direct-developer")
		if state.Access.Shared {
			accessDesc = i18n.T("ops.shared-auth", state.Access.Actor)
		}

		SailListCardHeader(c, i18n.T("ops.actor-write-matrix"), accessDesc, func() {
			if len(enabledWriteTools) == 0 {
				SailQuietStatus(c, i18n.T("ops.all-read-only"), SailSuccessInk, SailInkStrong)
			} else {
				SailBadgeDot(c, i18n.T("ops.write-tools-count", len(enabledWriteTools)), ToneWarning)
			}
			if SailSmallButton(c.Key("set-review-rw"), i18n.T("ops.review-writes")).Clicked() {
				u.OpenCatalogFilter("", "rw")
			}
			if len(enabledWriteTools) > 0 {
				lbl := i18n.T("ops.enforce-read-only")
				if st.guardrailBusy {
					lbl = i18n.T("ops.enforcing")
				}
				if SailDangerButton(c.Key("set-enforce-ro"), lbl).Disabled(st.guardrailBusy).Clicked() {
					u.enforceSettingsReadOnlyGuardrail(st, state, enabledWriteTools)
				}
			}
		})

		// Hairline Actor Matrix Table (no nested inner box border).
		ui.Row(c).
			FillWidth().
			Padding(10, 20).
			Gap(12).
			Background(SailBgSubtle).
			BorderWidth(0, 0, 1, 0).
			BorderColor(SailDivider).
			AlignItems(ui.Center).
			Children(func() {
				ui.Text(c, i18n.T("ops.actor")).Width(120).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, i18n.T("ops.services")).Width(72).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, i18n.T("ops.exposed-tools")).Width(72).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, i18n.T("ops.read-only")).Width(64).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, i18n.T("ops.write-access")).Width(68).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, i18n.T("ops.service-distribution")).Grow(1).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
			})

		for i, row := range actorRows {
			borderBottom := float32(1)
			if i == len(actorRows)-1 {
				borderBottom = 0
			}
			ui.Row(c.Key("set-actor-"+row.Actor)).
				FillWidth().
				Padding(13, 20).
				Gap(12).
				Background(SailBgSurface).
				BorderWidth(0, 0, borderBottom, 0).
				BorderColor(SailDivider).
				AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, row.Actor).Width(120).Shrink(0).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Textf(c, "%d", row.ReachableServers).Width(72).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkBody).SingleLine()
					ui.Textf(c, "%d", row.TotalEnabledTools).Width(72).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkStrong).SingleLine()
					ui.Textf(c, "%d", row.ReadOnlyTools).Width(64).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkBody).SingleLine()

					writeColor := SailInkMuted
					writeWeight := 400
					if row.WriteTools > 0 {
						writeColor = SailWarningInk
						writeWeight = 600
					}
					ui.Textf(c, "%d", row.WriteTools).Width(68).Shrink(0).FontSize(12.5).FontWeight(writeWeight).FontFeatures("tnum").TextColor(writeColor).SingleLine()

					if len(row.ByServer) == 0 {
						ui.Text(c, "—").Grow(1).MinWidth(0).FontSize(12.5).TextColor(SailInkFaint)
					} else {
						parts := make([]string, 0, len(row.ByServer))
						for _, srv := range row.ByServer {
							seg := fmt.Sprintf("%s %d", srv.ServerName, srv.ReadOnly+srv.Write)
							if srv.Write > 0 {
								seg += i18n.T("ops.write-count-suffix", srv.Write)
							}
							parts = append(parts, seg)
						}
						ui.Text(c, strings.Join(parts, " · ")).Grow(1).MinWidth(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkMuted)
					}
				})
		}
	})
}

func editingProfileKeyConfigured(st *settingsState, state *apitypes.Bootstrap) bool {
	if st.clearKey {
		return false
	}
	if st.editingProfileID != "" {
		for _, p := range state.Profiles {
			if p.ID == st.editingProfileID {
				return p.KeyConfigured
			}
		}
	}
	return state.KeyConfigured
}

func (u *DesktopUI) renderSettingsModelSection(c *ui.Context, st *settingsState, state *apitypes.Bootstrap, dirty bool) {
	SailCard(c, func() {
		renderSettingsModelPresetsHeader(c, st)
		renderSettingsModelInputs(c, st, state)
		u.renderSettingsAdvancedSection(c, st, state, dirty)

		if st.errTitle != "" {
			SailAlertBox(c, ToneDanger, st.errTitle, st.errDetail)
		}
		if st.pingErr != "" && st.pingDetail != "" {
			SailAlertBox(c, ToneDanger, st.pingErr, st.pingDetail)
		}

		u.renderSettingsModelFooter(c, st, state)
	})
}

func renderSettingsModelPresetsHeader(c *ui.Context, st *settingsState) {
	SailCardTitle(c, i18n.T("ops.decisions-model-title"), "", func() {
		if SailSmallButton(c.Key("set-preset-openai"), i18n.T("ops.preset-openai")).Clicked() {
			st.editingProfileID = ""
			st.config.BaseURL = "https://api.openai.com/v1"
			st.config.Model = "gpt-6-luna"
			st.clearKey = false
			clearSettingsFeedback(st)
		}
		if SailSmallButton(c.Key("set-preset-gemini"), i18n.T("ops.preset-gemini")).Clicked() {
			st.editingProfileID = ""
			st.config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
			st.config.Model = "gemini-decision-1"
			st.clearKey = false
			clearSettingsFeedback(st)
		}
	})
}

func renderSettingsModelInputs(c *ui.Context, st *settingsState, state *apitypes.Bootstrap) {
	ui.Row(c).FillWidth().Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, "Base URL").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			if ui.TextInput(c.Key("set-base-url"), &st.config.BaseURL).
				Font("monospace").
				Placeholder("https://api.openai.com/v1").
				FillWidth().
				Changed() {
				clearSettingsFeedback(st)
			}
		})

		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, "Model ID").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			if ui.TextInput(c.Key("set-model"), &st.config.Model).
				Font("monospace").
				Placeholder("gpt-6-luna / gemini-decision-1").
				FillWidth().
				Changed() {
				clearSettingsFeedback(st)
			}
		})
	})

	keyConfigured := editingProfileKeyConfigured(st, state)
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "API Key").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				if keyConfigured {
					SailQuietStatus(c, i18n.T("client.state.configured"), SailSuccessInk, SailInkStrong)
					if SailSmallButton(c.Key("set-clear-key-inline"), i18n.T("ops.clear")).Clicked() {
						st.clearKey = true
						st.apiKey = ""
						clearSettingsFeedback(st)
					}
				} else {
					SailQuietStatus(c, i18n.T("client.state.not-configured"), SailInkMuted, SailInkMuted)
				}
			})
		})

		placeholder := i18n.T("ops.paste-api-key")
		if keyConfigured {
			placeholder = i18n.T("ops.keep-current-key")
		}
		if ui.TextInput(c.Key("set-api-key"), &st.apiKey).
			Password().
			Font("monospace").
			Placeholder(placeholder).
			FillWidth().
			Changed() {
			st.clearKey = false
			clearSettingsFeedback(st)
		}
	})

	if state != nil && state.CredentialsPersistent {
		ui.Text(c, i18n.T("ops.key-stored-keychain")).FontSize(12).TextColor(SailInkMuted)
	} else {
		ui.Text(c, i18n.T("ops.key-memory-only")).FontSize(12).TextColor(SailInkMuted)
	}
}

func (u *DesktopUI) renderSettingsAdvancedSection(c *ui.Context, st *settingsState, state *apitypes.Bootstrap, dirty bool) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		arrow := "▸"
		if st.advancedExpanded {
			arrow = "▾"
		}
		if SailSmallButton(c.Key("set-toggle-advanced"), i18n.T("ops.advanced-settings", arrow)).Clicked() {
			st.advancedExpanded = !st.advancedExpanded
		}
		ui.Text(c, i18n.T("ops.max-tools-timeout", st.config.MaxTools, st.config.TimeoutSeconds)).
			Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted)
	})

	if !st.advancedExpanded {
		return
	}

	SailSubtleCard(c, func() {
		renderSettingsAdvancedInputs(c, st)

		if !state.Setup.ToolsReady {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
				SailQuietStatus(c, i18n.T("ops.validation-unavailable"), SailWarningInk, SailInkMuted)
				if SailSmallButton(c.Key("set-goto-services"), i18n.T("ops.goto-services")).Clicked() {
					u.Navigate(ViewServices)
				}
			})
		}

		renderSettingsModelCheckStatus(c, state.Setup.ModelCheck, dirty)
		if res := st.testResult; res != nil {
			renderSettingsTestResultCard(c, res)
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Children(func() {
			testLabel := i18n.T("ops.run-validation")
			if st.busyMode == "test" {
				testLabel = i18n.T("ops.validating")
			}
			if SailButton(c.Key("set-save-test"), testLabel).
				Disabled(st.busyMode != "" || !state.Setup.ToolsReady).
				Clicked() {
				u.submitSettings(st, true)
			}
		})
	})
}

func renderSettingsAdvancedInputs(c *ui.Context, st *settingsState) {
	ui.Row(c).FillWidth().Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, i18n.T("ops.max-tools-label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			if ui.NumberInput(c.Key("set-max-tools"), &st.maxToolsFloat, 1, 12, 1).
				Label(i18n.T("ops.max-tools-tooltip")).
				Width(200).
				Changed() {
				st.config.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
				resetSettingsTestOutput(st)
			}
		})

		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, i18n.T("ops.timeout-label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			if ui.NumberInput(c.Key("set-timeout"), &st.timeoutFloat, 5, 180, 5).
				Label(i18n.T("ops.timeout-tooltip")).
				Width(200).
				Changed() {
				st.config.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
				clearSettingsFeedback(st)
			}
		})
	})

	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Text(c, i18n.T("ops.validation-intent")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
		ui.TextInput(c.Key("set-test-intent"), &st.testIntent).
			Placeholder(i18n.T("ops.validation-intent-placeholder")).
			FillWidth()
	})
}

func renderSettingsModelCheckStatus(c *ui.Context, modelCheck *apitypes.ConnectionCheck, dirty bool) {
	if dirty || modelCheck == nil {
		return
	}
	if modelCheck.Status == "error" {
		SailAlertBox(c, ToneDanger, i18n.T("ops.message-with-time", modelCheck.Message, FormatTime(modelCheck.CheckedAt)), modelCheck.Detail)
		return
	}
	dotCol := SailSuccessInk
	lbl := fmt.Sprintf("%s · %s", modelCheck.Message, FormatTime(modelCheck.CheckedAt))
	if modelCheck.Stale {
		dotCol = SailWarningInk
		lbl = i18n.T("ops.config-changed-revalidate", FormatTime(modelCheck.CheckedAt))
	}
	SailQuietStatus(c, lbl, dotCol, SailInkMuted)
}

func renderSettingsTestResultCard(c *ui.Context, res *apitypes.DiscoverResult) {
	SailCard(c, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			if res.Status == "selected" {
				SailQuietStatus(c, i18n.T("ops.route-validation-passed", res.Status), SailSuccessInk, SailInkStrong)
			} else {
				SailBadge(c, res.Status, ToneWarning)
			}
			ui.Textf(c, "%d ms", res.DurationMS).Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted)
		})
		if res.Summary != "" {
			ui.Text(c, res.Summary).FontSize(12.5).TextColor(SailInkMuted)
		}
		if len(res.Tools) > 0 {
			ui.Column(c).FillWidth().Gap(4).Margin(4, 0, 0, 0).Children(func() {
				for _, t := range res.Tools {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Start).Children(func() {
						ui.Text(c, t.ID).Font("monospace").FontSize(12).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						ui.Text(c, "— "+t.Reason).Grow(1).MinWidth(0).FontSize(12).TextColor(SailInkMuted)
					})
				}
			})
		}
	})
}

func (u *DesktopUI) renderSettingsModelFooter(c *ui.Context, st *settingsState, state *apitypes.Bootstrap) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
			switch {
			case st.pingBusy:
				SailQuietStatus(c, i18n.T("ops.testing-model"), SailWarningInk, SailInkMuted)
			case settingsPingPassed(st):
				SailQuietStatus(c, i18n.T("ops.model-test-passed", st.pingResult.Model, st.pingResult.DurationMS), SailSuccessInk, SailSuccessInk)
			case st.pingErr != "":
				SailQuietStatus(c, st.pingErr, SailDangerInk, SailDangerInk)
			case isVerifiedActiveConnectionUnchanged(st, state) && st.config.MaxTools != state.Settings.MaxTools:
				SailQuietStatus(c, i18n.T("ops.connection-verified"), SailSuccessInk, SailInkMuted)
			default:
				SailQuietStatus(c, i18n.T("ops.test-before-save"), SailInkMuted, SailInkMuted)
			}
		})

		ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
			pingLabel := i18n.T("ops.test-connection")
			if st.pingBusy {
				pingLabel = i18n.T("ops.testing")
			}
			if SailButton(c.Key("set-ping-model"), pingLabel).Disabled(st.pingBusy || st.busyMode != "").Clicked() {
				u.pingSettingsModel(st)
			}

			saveLabel := i18n.T("ops.save-config")
			if st.busyMode == "save" {
				saveLabel = i18n.T("ops.saving")
			}
			if SailPrimaryButton(c.Key("set-save-only"), saveLabel).Disabled(!settingsCanSave(st, state)).Clicked() {
				u.submitSettings(st, false)
			}
		})
	})
}

func (u *DesktopUI) renderSettingsProfilesSection(c *ui.Context, st *settingsState, state *apitypes.Bootstrap) {
	profiles := state.Profiles
	SailFlushCard(c, func() {
		SailListCardHeader(c, i18n.T("ops.saved-models"), i18n.T("ops.profiles-count", len(profiles)), nil)
		if len(profiles) == 0 {
			SailEmptyState(c, i18n.T("ops.no-saved-models"), i18n.T("ops.no-saved-models-detail"), "", nil)
			return
		}
		for i, p := range profiles {
			if i > 0 {
				ui.Box(c).FillWidth().Height(1).Background(SailDivider)
			}
			prof := p
			isEditing := prof.ID == st.editingProfileID
			bg := SailBgSurface
			if isEditing {
				bg = SailBgSubtle
			}
			ui.Row(c).FillWidth().Padding(14, 20).Background(bg).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, prof.Model).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						if prof.Active {
							SailQuietStatus(c, i18n.T("ops.profile-active"), SailSuccessInk, SailSuccessInk)
						} else {
							SailQuietStatus(c, i18n.T("ops.profile-standby"), SailInkMuted, SailInkMuted)
						}
					})
					ui.Text(c, prof.BaseURL).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
				})

				keyTxt := i18n.T("ops.key-not-configured")
				if prof.KeyConfigured {
					keyTxt = i18n.T("ops.key-configured")
				}
				ui.Text(c, i18n.T("ops.profile-summary", keyTxt, prof.MaxTools, prof.TimeoutSeconds)).
					Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()

				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					busy := st.profileBusyID == prof.ID
					if !prof.Active {
						if SailSmallButton(c.Key("set-prof-activate-"+prof.ID), i18n.T("ops.set-active")).Disabled(busy).Clicked() {
							u.activateProfile(st, prof.ID)
						}
					}
					editLabel := i18n.T("ops.edit")
					if isEditing {
						editLabel = i18n.T("ops.editing")
					}
					if SailSmallButton(c.Key("set-prof-edit-"+prof.ID), editLabel).Disabled(busy).Clicked() {
						loadProfileIntoSettings(st, prof.ID, apitypes.Settings{
							BaseURL:        prof.BaseURL,
							Model:          prof.Model,
							MaxTools:       prof.MaxTools,
							TimeoutSeconds: prof.TimeoutSeconds,
						})
					}
					if len(profiles) > 1 {
						if SailDangerButton(c.Key("set-prof-del-"+prof.ID), i18n.T("ops.delete")).Disabled(busy).Clicked() {
							u.deleteProfile(st, prof.ID)
						}
					}
				})
			})
		}
	})
}

func (u *DesktopUI) pingSettingsModel(st *settingsState) {
	if st.pingBusy || u.Client == nil {
		return
	}
	cfg := st.config
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
	st.config.TimeoutSeconds = cfg.TimeoutSeconds
	if cfg.BaseURL == "" || cfg.Model == "" {
		clearSettingsFeedback(st)
		st.pingErr = i18n.T("ops.base-url-model-required")
		return
	}
	st.pingBusy = true
	clearSettingsFeedback(st)

	profileID := st.editingProfileID
	apiKey := st.apiKey
	clearKey := st.clearKey
	pingKey := settingsConnectionKey(cfg, profileID, apiKey, clearKey)

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds+5)*time.Second)
		defer cancel()
		res, err := u.Client.PingModel(ctx, cfg, profileID, apiKey, clearKey)
		if err != nil {
			if apiclient.IsUnauthorized(err) {
				u.updateUI(func() { st.pingBusy = false })
				return err
			}
			title, detail := splitAPIError(err)
			u.updateUI(func() {
				st.pingBusy = false
				if settingsConnectionKey(st.config, st.editingProfileID, st.apiKey, st.clearKey) == pingKey {
					st.pingErr = title
					st.pingDetail = detail
				}
			})
			return nil
		}
		u.updateUI(func() {
			st.pingBusy = false
			if settingsConnectionKey(st.config, st.editingProfileID, st.apiKey, st.clearKey) == pingKey {
				st.pingResult = &res
				st.pingPassedKey = pingKey
			}
		})
		return nil
	})
}

func (u *DesktopUI) activateProfile(st *settingsState, id string) {
	if st.profileBusyID != "" || u.Client == nil {
		return
	}
	st.profileBusyID = id
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		prof, err := u.Client.ActivateModelProfile(ctx, id)
		if err != nil {
			u.updateUI(func() { st.profileBusyID = "" })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.profileBusyID = ""
			loadProfileIntoSettings(st, prof.ID, apitypes.Settings{
				BaseURL:        prof.BaseURL,
				Model:          prof.Model,
				MaxTools:       prof.MaxTools,
				TimeoutSeconds: prof.TimeoutSeconds,
			})
		})
		u.Notify(i18n.T("ops.model-switched-toast", prof.Model))
		return nil
	})
}

func (u *DesktopUI) deleteProfile(st *settingsState, id string) {
	if st.profileBusyID != "" || u.Client == nil {
		return
	}
	st.profileBusyID = id
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.DeleteModelProfile(ctx, id); err != nil {
			u.updateUI(func() { st.profileBusyID = "" })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.profileBusyID = ""
			if st.editingProfileID == id {
				st.editingProfileID = ""
				if u.State != nil {
					for _, p := range u.State.Profiles {
						if p.Active {
							loadProfileIntoSettings(st, p.ID, apitypes.Settings{
								BaseURL:        p.BaseURL,
								Model:          p.Model,
								MaxTools:       p.MaxTools,
								TimeoutSeconds: p.TimeoutSeconds,
							})
							break
						}
					}
				}
			}
		})
		u.Notify(i18n.T("ops.profile-deleted"))
		return nil
	})
}

type settingsSubmitPlan struct {
	runTest             bool
	mode                string
	cfg                 apitypes.Settings
	profile             apitypes.ModelProfile
	apiKey              string
	clearKey            bool
	testIntent          string
	pingKey             string
	pingPassed          bool
	needsPingBeforeSave bool
}

func prepareSettingsSubmit(st *settingsState, state *apitypes.Bootstrap, runTest bool) (settingsSubmitPlan, string) {
	cfg := st.config
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
	cfg.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
	st.config = cfg

	if cfg.Model == "" {
		return settingsSubmitPlan{}, i18n.T("ops.model-name-required")
	}
	if !runTest && !settingsCanSave(st, state) {
		return settingsSubmitPlan{}, "skip"
	}

	mode := "save"
	if runTest {
		mode = "test"
	}
	profile := apitypes.ModelProfile{
		ID:             st.editingProfileID,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		MaxTools:       cfg.MaxTools,
		TimeoutSeconds: cfg.TimeoutSeconds,
	}
	pingPassed := settingsPingPassed(st)
	return settingsSubmitPlan{
		runTest:             runTest,
		mode:                mode,
		cfg:                 cfg,
		profile:             profile,
		apiKey:              st.apiKey,
		clearKey:            st.clearKey,
		testIntent:          st.testIntent,
		pingKey:             settingsConnectionKey(cfg, profile.ID, st.apiKey, st.clearKey),
		pingPassed:          pingPassed,
		needsPingBeforeSave: !pingPassed && !isVerifiedActiveConnectionUnchanged(st, state),
	}, ""
}

func (u *DesktopUI) submitSettings(st *settingsState, runTest bool) {
	if st.busyMode != "" || st.pingBusy || u.Client == nil {
		return
	}
	plan, errMsg := prepareSettingsSubmit(st, u.State, runTest)
	if errMsg != "" {
		if errMsg != "skip" {
			st.errTitle = errMsg
			st.errDetail = ""
		}
		return
	}

	st.busyMode = plan.mode
	resetSettingsTestOutput(st)

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(plan.cfg.TimeoutSeconds*2+20)*time.Second)
		defer cancel()
		return u.executeSettingsSubmit(ctx, st, plan)
	})
}

func (u *DesktopUI) executeSettingsSubmit(ctx context.Context, st *settingsState, plan settingsSubmitPlan) error {
	var pingRes *apitypes.PingModelResult
	pingPassed := plan.pingPassed
	if plan.needsPingBeforeSave {
		res, err := u.Client.PingModel(ctx, plan.cfg, plan.profile.ID, plan.apiKey, plan.clearKey)
		if err != nil {
			return u.handleSettingsPingFailure(st, err)
		}
		pingRes = &res
		pingPassed = true
	}

	saved, err := u.Client.SaveModelProfile(ctx, plan.profile, plan.apiKey, plan.clearKey)
	if err != nil {
		return u.handleSettingsSaveFailure(st, err)
	}

	if plan.runTest {
		res, testErr := u.Client.TestModel(ctx, plan.cfg, "", false, plan.testIntent)
		if testErr != nil {
			if apiclient.IsUnauthorized(testErr) {
				u.updateUI(func() { st.busyMode = "" })
				return testErr
			}
			_ = u.RefreshSync()
			title, detail := splitAPIError(testErr)
			u.updateUI(func() {
				finishSettingsSaved(st, plan, settingsSavedResult{pingRes: pingRes, pingPassed: pingPassed, savedID: saved.ID, errTitle: title, errDetail: detail})
			})
			return nil
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			finishSettingsSaved(st, plan, settingsSavedResult{pingRes: pingRes, pingPassed: pingPassed, savedID: saved.ID, testRes: &res})
		})
		u.Notify(i18n.T("ops.routing-config-saved"))
		return nil
	}

	_ = u.RefreshSync()
	u.updateUI(func() {
		finishSettingsSaved(st, plan, settingsSavedResult{pingRes: pingRes, pingPassed: pingPassed, savedID: saved.ID})
	})
	u.Notify(i18n.T("ops.model-profile-saved"))
	return nil
}

func (u *DesktopUI) handleSettingsPingFailure(st *settingsState, err error) error {
	if apiclient.IsUnauthorized(err) {
		u.updateUI(func() { st.busyMode = "" })
		return err
	}
	title, detail := splitAPIError(err)
	u.updateUI(func() {
		st.busyMode = ""
		st.pingResult = nil
		st.pingPassedKey = ""
		st.pingErr = title
		st.pingDetail = detail
	})
	return nil
}

func (u *DesktopUI) handleSettingsSaveFailure(st *settingsState, err error) error {
	if apiclient.IsUnauthorized(err) {
		u.updateUI(func() { st.busyMode = "" })
		return err
	}
	title, detail := splitAPIError(err)
	u.updateUI(func() {
		st.busyMode = ""
		st.pingResult = nil
		st.pingPassedKey = ""
		st.errTitle = title
		st.errDetail = detail
	})
	return nil
}

type settingsSavedResult struct {
	pingRes    *apitypes.PingModelResult
	pingPassed bool
	savedID    string
	testRes    *apitypes.DiscoverResult
	errTitle   string
	errDetail  string
}

func finishSettingsSaved(st *settingsState, plan settingsSubmitPlan, r settingsSavedResult) {
	st.busyMode = ""
	if r.pingRes != nil {
		st.pingResult = r.pingRes
		st.pingErr = ""
		st.pingDetail = ""
	}
	if r.pingPassed && settingsConnectionKey(st.config, st.editingProfileID, st.apiKey, st.clearKey) == plan.pingKey {
		st.pingPassedKey = settingsConnectionKey(plan.cfg, r.savedID, "", false)
	}
	st.editingProfileID = r.savedID
	st.apiKey = ""
	st.clearKey = false
	st.testResult = r.testRes
	st.errTitle = r.errTitle
	st.errDetail = r.errDetail
}

func (u *DesktopUI) enforceSettingsReadOnlyGuardrail(st *settingsState, state *apitypes.Bootstrap, enabledWriteTools []apitypes.Tool) {
	if len(enabledWriteTools) == 0 || st.guardrailBusy || u.Client == nil {
		return
	}
	st.guardrailBusy = true

	affected := make([]string, 0)
	seen := make(map[string]bool)
	for _, t := range enabledWriteTools {
		if !seen[t.ServerID] {
			seen[t.ServerID] = true
			affected = append(affected, t.ServerID)
		}
	}

	keepByServer := make(map[string][]string, len(affected))
	for _, srvID := range affected {
		var keep []string
		for _, t := range state.Registry.Tools {
			if t.ServerID == srvID && t.Enabled && t.ReadOnly {
				keep = append(keep, t.ID)
			}
		}
		keepByServer[srvID] = keep
	}
	closedCount := len(enabledWriteTools)

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for _, srvID := range affected {
			if err := u.Client.EnableServerTools(ctx, srvID, keepByServer[srvID]); err != nil {
				u.updateUI(func() { st.guardrailBusy = false })
				return err
			}
		}
		if err := u.RefreshSync(); err != nil {
			u.updateUI(func() { st.guardrailBusy = false })
			return err
		}
		u.updateUI(func() { st.guardrailBusy = false })
		u.Notify(i18n.T("ops.guardrail-applied", closedCount))
		return nil
	})
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
