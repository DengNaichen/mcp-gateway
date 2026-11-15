package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/gateway"

	"github.com/egoist/mygo/ui"
)

type settingsState struct {
	initialized        bool
	lastSyncedSettings gateway.Settings

	config        gateway.Settings
	maxToolsFloat float64
	timeoutFloat  float64
	apiKey        string
	clearKey      bool
	testIntent    string
	busyMode      string // "", "save", "test"
	guardrailBusy bool
	errTitle      string
	errDetail     string
	testResult    *gateway.DiscoverResult
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

func settingsIsDirty(st *settingsState, saved gateway.Settings) bool {
	return st.config.BaseURL != saved.BaseURL ||
		st.config.Model != saved.Model ||
		st.config.MaxTools != saved.MaxTools ||
		st.config.TimeoutSeconds != saved.TimeoutSeconds ||
		st.apiKey != "" ||
		st.clearKey
}

func (u *DesktopUI) renderSettings(c *ui.Context) {
	state := u.State
	st := getSettingsState(u)

	// Synchronize form state from Bootstrap when first initialized or when saved settings change while clean.
	if !st.initialized {
		st.initialized = true
		st.lastSyncedSettings = state.Settings
		st.config = state.Settings
		st.maxToolsFloat = float64(clampInt(state.Settings.MaxTools, 1, 12))
		st.timeoutFloat = float64(clampInt(state.Settings.TimeoutSeconds, 5, 180))
	} else if st.lastSyncedSettings != state.Settings {
		wasDirty := settingsIsDirty(st, st.lastSyncedSettings)
		st.lastSyncedSettings = state.Settings
		if !wasDirty {
			st.config = state.Settings
			st.maxToolsFloat = float64(clampInt(state.Settings.MaxTools, 1, 12))
			st.timeoutFloat = float64(clampInt(state.Settings.TimeoutSeconds, 5, 180))
		}
	}

	dirty := settingsIsDirty(st, state.Settings)

	SailSectionHeader(c, "访问与路由", "", nil)

	// Section 1: Actor & Read/Write Guardrail Matrix.
	u.renderSettingsGuardrailSection(c, st, state)

	// Section 2: Decisions Model Credentials.
	u.renderSettingsModelSection(c, st, state)

	// Section 3: Routing Limits & Real-time Verification.
	u.renderSettingsStrategySection(c, st, state, dirty)
}

func (u *DesktopUI) renderSettingsGuardrailSection(c *ui.Context, st *settingsState, state *Bootstrap) {
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
	var enabledWriteTools []gateway.Tool
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

func (u *DesktopUI) renderSettingsModelSection(c *ui.Context, st *settingsState, state *Bootstrap) {
	SailCard(c, func() {
		SailCardTitle(c, "Decisions 语义路由模型", "", func() {
			if SailSmallButton(c.Key("set-preset-openai"), "OpenAI 预设").Clicked() {
				st.config.BaseURL = "https://api.openai.com/v1"
				st.config.Model = "gpt-6-luna"
				st.testResult = nil
			}
			if SailSmallButton(c.Key("set-preset-gemini"), "Gemini 预设").Clicked() {
				st.config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
				st.config.Model = "gemini-decision-1"
				st.testResult = nil
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
					st.testResult = nil
				}
			})

			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "Model ID").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if ui.TextInput(c.Key("set-model"), &st.config.Model).
					Font("monospace").
					Placeholder("gpt-6-luna / gemini-decision-1").
					FillWidth().
					Changed() {
					st.testResult = nil
				}
			})
		})

		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "API Key").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if state.KeyConfigured {
					SailQuietStatus(c, "已配置", SailSuccessInk, SailInkStrong)
				} else {
					SailBadgeDot(c, "未配置", ToneWarning)
				}
			})

			placeholder := "粘贴 API Key"
			if state.KeyConfigured {
				placeholder = "留空保持当前 Key"
			}
			if ui.TextInput(c.Key("set-api-key"), &st.apiKey).
				Password().
				Font("monospace").
				Placeholder(placeholder).
				FillWidth().
				Changed() {
				st.clearKey = false
				st.testResult = nil
			}
			// Counter-intuitive risk hint (<= 25 chars): API Key lives only in process memory.
			ui.Text(c, "仅驻留内存不落盘，或用环境变量注入").
				FontSize(11.5).
				TextColor(SailInkMuted)

			if state.KeyConfigured {
				if ui.Checkbox(c.Key("set-clear-key"), &st.clearKey, "清除内存中的 API Key").Changed() {
					if st.clearKey {
						st.apiKey = ""
					}
					st.testResult = nil
				}
			}
		})
	})
}

func (u *DesktopUI) renderSettingsStrategySection(c *ui.Context, st *settingsState, state *Bootstrap, dirty bool) {
	SailCard(c, func() {
		SailCardTitle(c, "路由策略与校验", "", nil)

		ui.Row(c).FillWidth().Gap(16).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "单次返回上限 (1–12)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if ui.NumberInput(c.Key("set-max-tools"), &st.maxToolsFloat, 1, 12, 1).
					Label("单次返回工具上限").
					Width(200).
					Changed() {
					st.config.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
					st.testResult = nil
				}
			})

			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "请求超时 (5–180 秒)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if ui.NumberInput(c.Key("set-timeout"), &st.timeoutFloat, 5, 180, 5).
					Label("模型请求超时").
					Width(200).
					Changed() {
					st.config.TimeoutSeconds = clampInt(int(math.Round(st.timeoutFloat)), 5, 180)
					st.testResult = nil
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

		if st.errTitle != "" {
			SailAlertBox(c, ToneDanger, st.errTitle, st.errDetail)
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
			SailSubtleCard(c, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					if res.Status == "selected" {
						SailQuietStatus(c, "校验通过 · "+res.Status, SailSuccessInk, SailInkStrong)
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

		ui.Box(c).FillWidth().Height(1).Background(SailDivider)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			saveLabel := "仅保存配置"
			if st.busyMode == "save" {
				saveLabel = "保存中…"
			}
			if ui.Button(c.Key("set-save-only"), saveLabel).Disabled(st.busyMode != "").Clicked() {
				u.submitSettings(st, false)
			}

			testLabel := "保存并运行路由校验"
			if st.busyMode == "test" {
				testLabel = "校验中…"
			}
			if ui.PrimaryButton(c.Key("set-save-test"), testLabel).
				Disabled(st.busyMode != "" || !state.Setup.ToolsReady).
				Clicked() {
				u.submitSettings(st, true)
			}
		})
	})
}

func (u *DesktopUI) submitSettings(st *settingsState, runTest bool) {
	if st.busyMode != "" || u.Client == nil {
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

	mode := "save"
	if runTest {
		mode = "test"
	}
	st.busyMode = mode
	st.errTitle = ""
	st.errDetail = ""
	st.testResult = nil

	apiKey := st.apiKey
	clearKey := st.clearKey
	testIntent := st.testIntent

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.TimeoutSeconds+20)*time.Second)
		defer cancel()

		if runTest {
			res, err := u.Client.TestModel(ctx, cfg, apiKey, clearKey, testIntent)
			if err != nil {
				if IsUnauthorized(err) {
					u.updateUI(func() { st.busyMode = "" })
					return err
				}
				_ = u.RefreshSync()
				title, detail := splitAPIError(err)
				u.updateUI(func() {
					st.busyMode = ""
					st.errTitle = title
					st.errDetail = detail
				})
				return nil
			}
			_ = u.RefreshSync()
			u.updateUI(func() {
				st.busyMode = ""
				st.apiKey = ""
				st.clearKey = false
				st.testResult = &res
			})
			u.Notify("路由配置已保存并通过实时校验")
			return nil
		}

		if err := u.Client.SaveSettings(ctx, cfg, apiKey, clearKey); err != nil {
			if IsUnauthorized(err) {
				u.updateUI(func() { st.busyMode = "" })
				return err
			}
			title, detail := splitAPIError(err)
			u.updateUI(func() {
				st.busyMode = ""
				st.errTitle = title
				st.errDetail = detail
			})
			return nil
		}

		_ = u.RefreshSync()
		u.updateUI(func() {
			st.busyMode = ""
			st.apiKey = ""
			st.clearKey = false
		})
		u.Notify("路由配置已保存")
		return nil
	})
}

func (u *DesktopUI) enforceSettingsReadOnlyGuardrail(st *settingsState, state *Bootstrap, enabledWriteTools []gateway.Tool) {
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
