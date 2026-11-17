package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/apitypes"

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

var (
	settingsMu     sync.Mutex
	settingsStates = map[*DesktopUI]*settingsState{}
)

func getSettingsState(u *DesktopUI) *settingsState {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	st, ok := settingsStates[u]
	if !ok {
		st = &settingsState{}
		settingsStates[u] = st
	}
	return st
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
	st := getSettingsState(u)

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

	SailSectionHeader(c, "访问与路由", "", nil)

	// Section 1: Actor & Read/Write Guardrail Matrix.
	u.renderSettingsGuardrailSection(c, st, state)

	// Section 2: Unified Decisions Model & Collapsible Advanced Routing Policy.
	u.renderSettingsModelSection(c, st, state, dirty)

	// Section 3: Saved Model Profiles List.
	u.renderSettingsProfilesSection(c, st, state)
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
		accessDesc := "本机直连 · developer"
		if state.Access.Shared {
			accessDesc = fmt.Sprintf("共享鉴权 · %s", state.Access.Actor)
		}

		SailListCardHeader(c, "调用身份与写操作护栏矩阵", accessDesc, func() {
			if len(enabledWriteTools) == 0 {
				SailQuietStatus(c, "全部只读", SailSuccessInk, SailInkStrong)
			} else {
				SailBadgeDot(c, fmt.Sprintf("写工具 %d", len(enabledWriteTools)), ToneWarning)
			}
			if SailSmallButton(c.Key("set-review-rw"), "审查写操作").Clicked() {
				u.OpenCatalogFilter("", "rw")
			}
			if len(enabledWriteTools) > 0 {
				lbl := "全局收敛为只读"
				if st.guardrailBusy {
					lbl = "收敛中…"
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
				ui.Text(c, "身份").Width(120).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, "服务").Width(72).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, "开放工具").Width(72).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, "只读").Width(64).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, "写操作").Width(68).Shrink(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				ui.Text(c, "服务分布").Grow(1).MinWidth(0).FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
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
				Background(SailBgCanvas).
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
								seg += fmt.Sprintf(" (%d 写)", srv.Write)
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
		SailCardTitle(c, "Decisions 语义路由模型", "", func() {
			if SailSmallButton(c.Key("set-preset-openai"), "OpenAI 预设").Clicked() {
				st.editingProfileID = ""
				st.config.BaseURL = "https://api.openai.com/v1"
				st.config.Model = "gpt-6-luna"
				st.clearKey = false
				clearSettingsFeedback(st)
			}
			if SailSmallButton(c.Key("set-preset-gemini"), "Gemini 预设").Clicked() {
				st.editingProfileID = ""
				st.config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
				st.config.Model = "gemini-decision-1"
				st.clearKey = false
				clearSettingsFeedback(st)
			}
		})

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
						SailQuietStatus(c, "已配置", SailSuccessInk, SailInkStrong)
						if SailSmallButton(c.Key("set-clear-key-inline"), "清除").Clicked() {
							st.clearKey = true
							st.apiKey = ""
							clearSettingsFeedback(st)
						}
					} else {
						SailQuietStatus(c, "未配置", SailInkMuted, SailInkMuted)
					}
				})
			})

			placeholder := "粘贴 API Key"
			if keyConfigured {
				placeholder = "留空保持当前 Key"
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

		// Collapsible advanced routing policy & end-to-end verification.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			arrow := "▸"
			if st.advancedExpanded {
				arrow = "▾"
			}
			if SailSmallButton(c.Key("set-toggle-advanced"), fmt.Sprintf("%s 高级设置 · 路由策略与校验", arrow)).Clicked() {
				st.advancedExpanded = !st.advancedExpanded
			}
			ui.Textf(c, "上限 %d · 超时 %ds", st.config.MaxTools, st.config.TimeoutSeconds).
				Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted)
		})

		if st.advancedExpanded {
			SailSubtleCard(c, func() {
				ui.Row(c).FillWidth().Gap(16).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
						ui.Text(c, "单次返回上限 (1–12)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						if ui.NumberInput(c.Key("set-max-tools"), &st.maxToolsFloat, 1, 12, 1).
							Label("单次返回工具上限").
							Width(200).
							Changed() {
							st.config.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
							resetSettingsTestOutput(st)
						}
					})

					ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
						ui.Text(c, "请求超时 (5–180 秒)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						if ui.NumberInput(c.Key("set-timeout"), &st.timeoutFloat, 5, 180, 5).
							Label("模型请求超时").
							Width(200).
							Changed() {
							st.config.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
							clearSettingsFeedback(st)
						}
					})
				})

				ui.Column(c).FillWidth().Gap(6).Children(func() {
					ui.Text(c, "校验意图（可选）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					ui.TextInput(c.Key("set-test-intent"), &st.testIntent).
						Placeholder("留空自动生成").
						FillWidth()
				})

				if !state.Setup.ToolsReady {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
						SailQuietStatus(c, "尚未启用真实工具，暂不可运行路由校验", SailWarningInk, SailInkMuted)
						if SailSmallButton(c.Key("set-goto-services"), "前往上游服务").Clicked() {
							u.Navigate(ViewServices)
						}
					})
				}

				modelCheck := state.Setup.ModelCheck
				if !dirty && modelCheck != nil && modelCheck.Status == "error" {
					SailAlertBox(c, ToneDanger, fmt.Sprintf("%s（%s）", modelCheck.Message, FormatTime(modelCheck.CheckedAt)), modelCheck.Detail)
				} else if !dirty && modelCheck != nil {
					dotCol := SailSuccessInk
					lbl := fmt.Sprintf("%s · %s", modelCheck.Message, FormatTime(modelCheck.CheckedAt))
					if modelCheck.Stale {
						dotCol = SailWarningInk
						lbl = fmt.Sprintf("配置已变更，建议重新校验 · 上次 %s", FormatTime(modelCheck.CheckedAt))
					}
					SailQuietStatus(c, lbl, dotCol, SailInkMuted)
				}

				if res := st.testResult; res != nil {
					SailCard(c, func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
							if res.Status == "selected" {
								SailQuietStatus(c, "路由校验通过 · "+res.Status, SailSuccessInk, SailInkStrong)
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

				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Children(func() {
					testLabel := "运行路由校验"
					if st.busyMode == "test" {
						testLabel = "校验中…"
					}
					if ui.Button(c.Key("set-save-test"), testLabel).
						Disabled(st.busyMode != "" || !state.Setup.ToolsReady).
						Clicked() {
						u.submitSettings(st, true)
					}
				})
			})
		}

		if st.errTitle != "" {
			SailAlertBox(c, ToneDanger, st.errTitle, st.errDetail)
		}
		if st.pingErr != "" && st.pingDetail != "" {
			SailAlertBox(c, ToneDanger, st.pingErr, st.pingDetail)
		}

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Children(func() {
				switch {
				case st.pingBusy:
					SailQuietStatus(c, "正在测试模型连接…", SailWarningInk, SailInkMuted)
				case settingsPingPassed(st):
					SailQuietStatus(c, fmt.Sprintf("模型测试通过 · %s 已响应 · %d ms", st.pingResult.Model, st.pingResult.DurationMS), SailSuccessInk, SailSuccessInk)
				case st.pingErr != "":
					SailQuietStatus(c, st.pingErr, SailDangerInk, SailDangerInk)
				case isVerifiedActiveConnectionUnchanged(st, state) && st.config.MaxTools != state.Settings.MaxTools:
					SailQuietStatus(c, "连接已验证，可直接保存上限调整", SailSuccessInk, SailInkMuted)
				default:
					SailQuietStatus(c, "通过「测试连接」后才可保存配置", SailInkMuted, SailInkMuted)
				}
			})

			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				pingLabel := "测试连接"
				if st.pingBusy {
					pingLabel = "测试中…"
				}
				if ui.Button(c.Key("set-ping-model"), pingLabel).Disabled(st.pingBusy || st.busyMode != "").Clicked() {
					u.pingSettingsModel(st)
				}

				saveLabel := "保存配置"
				if st.busyMode == "save" {
					saveLabel = "保存中…"
				}
				if ui.PrimaryButton(c.Key("set-save-only"), saveLabel).Disabled(!settingsCanSave(st, state)).Clicked() {
					u.submitSettings(st, false)
				}
			})
		})
	})
}

func (u *DesktopUI) renderSettingsProfilesSection(c *ui.Context, st *settingsState, state *apitypes.Bootstrap) {
	profiles := state.Profiles
	SailFlushCard(c, func() {
		SailListCardHeader(c, "已保存的模型", fmt.Sprintf("%d 个配置", len(profiles)), nil)
		if len(profiles) == 0 {
			SailEmptyState(c, "暂无已保存模型", "在上方填写模型信息并点击「保存配置」即可添加到列表。", "", nil)
			return
		}
		for i, p := range profiles {
			if i > 0 {
				ui.Box(c).FillWidth().Height(1).Background(SailDivider)
			}
			prof := p
			isEditing := prof.ID == st.editingProfileID
			bg := SailBgCanvas
			if isEditing {
				bg = SailBgSubtle
			}
			ui.Row(c).FillWidth().Padding(14, 20).Background(bg).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, prof.Model).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						if prof.Active {
							SailQuietStatus(c, "使用中", SailSuccessInk, SailSuccessInk)
						} else {
							SailQuietStatus(c, "备用", SailInkMuted, SailInkMuted)
						}
					})
					ui.Text(c, prof.BaseURL).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
				})

				keyTxt := "未配置 Key"
				if prof.KeyConfigured {
					keyTxt = "Key 已配置"
				}
				ui.Textf(c, "%s · 上限 %d · %ds", keyTxt, prof.MaxTools, prof.TimeoutSeconds).
					Font("monospace").FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()

				ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
					busy := st.profileBusyID == prof.ID
					if !prof.Active {
						if SailSmallButton(c.Key("set-prof-activate-"+prof.ID), "设为当前").Disabled(busy).Clicked() {
							u.activateProfile(st, prof.ID)
						}
					}
					editLabel := "编辑"
					if isEditing {
						editLabel = "编辑中"
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
						if SailDangerButton(c.Key("set-prof-del-"+prof.ID), "删除").Disabled(busy).Clicked() {
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
		st.pingErr = "请填写 Base URL 与 Model ID 后再测试连接"
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
			if IsUnauthorized(err) {
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
		u.Notify("已切换当前模型：" + prof.Model)
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
		u.Notify("已删除模型配置")
		return nil
	})
}

func (u *DesktopUI) submitSettings(st *settingsState, runTest bool) {
	if st.busyMode != "" || st.pingBusy || u.Client == nil {
		return
	}
	cfg := st.config
	cfg.BaseURL = strings.TrimSpace(cfg.BaseURL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
	cfg.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
	st.config = cfg

	if cfg.Model == "" {
		st.errTitle = "请填写路由模型名称（如 gpt-6-luna 或 gemini-decision-1）。"
		st.errDetail = ""
		return
	}
	if !runTest && !settingsCanSave(st, u.State) {
		return
	}

	mode := "save"
	if runTest {
		mode = "test"
	}
	st.busyMode = mode
	resetSettingsTestOutput(st)

	profile := apitypes.ModelProfile{
		ID:             st.editingProfileID,
		BaseURL:        cfg.BaseURL,
		Model:          cfg.Model,
		MaxTools:       cfg.MaxTools,
		TimeoutSeconds: cfg.TimeoutSeconds,
	}
	apiKey := st.apiKey
	clearKey := st.clearKey
	testIntent := st.testIntent
	pingKey := settingsConnectionKey(cfg, profile.ID, apiKey, clearKey)
	pingPassed := settingsPingPassed(st)
	needsPingBeforeSave := !pingPassed && !isVerifiedActiveConnectionUnchanged(st, u.State)

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds*2+20)*time.Second)
		defer cancel()

		var pingRes *apitypes.PingModelResult
		if needsPingBeforeSave {
			res, err := u.Client.PingModel(ctx, cfg, profile.ID, apiKey, clearKey)
			if err != nil {
				if IsUnauthorized(err) {
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
			pingRes = &res
			pingPassed = true
		}

		saved, err := u.Client.SaveModelProfile(ctx, profile, apiKey, clearKey)
		if err != nil {
			if IsUnauthorized(err) {
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

		finishSaved := func(testRes *apitypes.DiscoverResult, errTitle, errDetail string) {
			st.busyMode = ""
			if pingRes != nil {
				st.pingResult = pingRes
				st.pingErr = ""
				st.pingDetail = ""
			}
			if pingPassed && settingsConnectionKey(st.config, st.editingProfileID, st.apiKey, st.clearKey) == pingKey {
				st.pingPassedKey = settingsConnectionKey(cfg, saved.ID, "", false)
			}
			st.editingProfileID = saved.ID
			st.apiKey = ""
			st.clearKey = false
			st.testResult = testRes
			st.errTitle = errTitle
			st.errDetail = errDetail
		}

		if runTest {
			res, err := u.Client.TestModel(ctx, cfg, "", false, testIntent)
			if err != nil {
				if IsUnauthorized(err) {
					u.updateUI(func() { st.busyMode = "" })
					return err
				}
				_ = u.RefreshSync()
				title, detail := splitAPIError(err)
				u.updateUI(func() {
					finishSaved(nil, title, detail)
				})
				return nil
			}
			_ = u.RefreshSync()
			u.updateUI(func() {
				finishSaved(&res, "", "")
			})
			u.Notify("路由配置已保存并通过实时校验")
			return nil
		}

		_ = u.RefreshSync()
		u.updateUI(func() {
			finishSaved(nil, "", "")
		})
		u.Notify("模型配置已保存并设为当前模型")
		return nil
	})
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
		u.Notify(fmt.Sprintf("已关闭 %d 个写操作工具，全局候选池已收敛为只读", closedCount))
		return nil
	})
}

func splitAPIError(err error) (string, string) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Error(), apiErr.Detail
	}
	return err.Error(), ""
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
