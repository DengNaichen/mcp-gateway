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

	config         gateway.Settings
	maxToolsFloat  float64
	timeoutFloat   float64
	apiKey         string
	clearKey       bool
	testIntent     string
	busyMode       string // "", "save", "test"
	guardrailBusy  bool
	errTitle       string
	errDetail      string
	testResult     *gateway.DiscoverResult
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

	SailSectionHeader(c, "访问与路由", "审计各调用方身份（Actor）的工具可见范围与写操作护栏，并配置 Decisions 语义路由模型与分流阈值。", nil)

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

	SailCard(c, func() {
		SailCardTitle(c, "调用身份与写操作护栏矩阵", "基于当前在线上游服务与工具开放状态实时汇总。不在候选池内的工具既不会暴露给模型，也无法通过 invoke_tool 越权调用。", func() {
			if len(enabledWriteTools) == 0 {
				SailBadgeDot(c, "写操作护栏：全部只读", ToneSuccess)
			} else {
				SailBadgeDot(c, fmt.Sprintf("已开放 %d 个写工具", len(enabledWriteTools)), ToneWarning)
			}
		})

		accessDesc := "本机直连（默认 developer）"
		if state.Access.Shared {
			accessDesc = fmt.Sprintf("共享网关鉴权（%s）", state.Access.Actor)
		}
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "接入鉴权模式：").FontSize(12.5).TextColor(SailInkMuted).SingleLine()
				ui.Text(c, accessDesc).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
			})

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if SailSmallButton(c.Key("set-review-rw"), fmt.Sprintf("审查写操作工具（%d）", len(enabledWriteTools))).Clicked() {
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
		})

		if len(enabledWriteTools) > 0 {
			SailAlertBox(
				c,
				ToneWarning,
				fmt.Sprintf("写操作护栏提醒：当前有 %d 个写操作工具处于启用状态", len(enabledWriteTools)),
				"若希望收紧 Agent 变更权限，可点击「全局收敛为只读」一键关闭所有非演示服务下的写操作工具。",
			)
		}

		// Actor Matrix Table.
		ui.Column(c).FillWidth().Radius(6).Border(1, SailBorder).Clip().Children(func() {
			// Header row.
			ui.Row(c).
				FillWidth().
				Padding(9, 12).
				Gap(12).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailBorder).
				AlignItems(ui.Center).
				Children(func() {
					ui.Text(c, "身份（Actor）").Width(120).Shrink(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "可达服务").Width(80).Shrink(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "开放工具").Width(72).Shrink(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "只读数").Width(68).Shrink(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "写操作数").Width(72).Shrink(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					ui.Text(c, "按上游服务分布").Grow(1).MinWidth(0).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				})

			for i, row := range actorRows {
				borderBottom := float32(1)
				if i == len(actorRows)-1 {
					borderBottom = 0
				}
				ui.Row(c.Key("set-actor-"+row.Actor)).
					FillWidth().
					Padding(10, 12).
					Gap(12).
					Background(SailBgCanvas).
					BorderWidth(0, 0, borderBottom, 0).
					BorderColor(SailBorder).
					AlignItems(ui.Center).
					Children(func() {
						ui.Text(c, row.Actor).Width(120).Shrink(0).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						ui.Textf(c, "%d 个服务", row.ReachableServers).Width(80).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkBody).SingleLine()
						ui.Textf(c, "%d", row.TotalEnabledTools).Width(72).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkStrong).SingleLine()
						ui.Textf(c, "%d", row.ReadOnlyTools).Width(68).Shrink(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailSuccessInk).SingleLine()

						writeColor := SailInkMuted
						writeWeight := 400
						if row.WriteTools > 0 {
							writeColor = SailWarningInk
							writeWeight = 600
						}
						ui.Textf(c, "%d", row.WriteTools).Width(72).Shrink(0).FontSize(12.5).FontWeight(writeWeight).FontFeatures("tnum").TextColor(writeColor).SingleLine()

						if len(row.ByServer) == 0 {
							ui.Text(c, "无可见工具").Grow(1).MinWidth(0).FontSize(12.5).TextColor(SailInkFaint)
						} else {
							parts := make([]string, 0, len(row.ByServer))
							for _, srv := range row.ByServer {
								seg := fmt.Sprintf("%s %d", srv.ServerName, srv.ReadOnly+srv.Write)
								if srv.Write > 0 {
									seg += fmt.Sprintf("（%d 写）", srv.Write)
								}
								parts = append(parts, seg)
							}
							ui.Text(c, strings.Join(parts, " · ")).Grow(1).MinWidth(0).FontSize(12.5).FontFeatures("tnum").TextColor(SailInkMuted)
						}
					})
			}
		})
	})
}

func (u *DesktopUI) renderSettingsModelSection(c *ui.Context, st *settingsState, state *Bootstrap) {
	SailCard(c, func() {
		SailCardTitle(c, "Decisions 语义路由模型", "模型名称以 gemini- 开头时自动使用 Google Gemini Decisions 协议，其余使用 OpenAI Decisions 协议（自动补全 /decisions）。", func() {
			if SailSmallButton(c.Key("set-preset-openai"), "填入 OpenAI 预设 (gpt-6-luna)").Clicked() {
				st.config.BaseURL = "https://api.openai.com/v1"
				st.config.Model = "gpt-6-luna"
				st.testResult = nil
			}
			if SailSmallButton(c.Key("set-preset-gemini"), "填入 Gemini 预设 (gemini-decision-1)").Clicked() {
				st.config.BaseURL = "https://generativelanguage.googleapis.com/v1beta"
				st.config.Model = "gemini-decision-1"
				st.testResult = nil
			}
		})

		ui.Row(c).FillWidth().Gap(14).Children(func() {
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
				ui.Text(c, "模型名称（Model ID）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if ui.TextInput(c.Key("set-model"), &st.config.Model).
					Font("monospace").
					Placeholder("gpt-6-luna 或 gemini-decision-1").
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
					SailBadgeDot(c, "当前内存已配置 Key", ToneSuccess)
				} else {
					SailBadgeDot(c, "未检测到 Key", ToneWarning)
				}
			})

			placeholder := "粘贴 API Key（仅保留在后端进程内存中）"
			if state.KeyConfigured {
				placeholder = "留空则继续使用已加载的 Key"
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
			ui.Text(c, "密钥不会写入 SQLite 或本地文件；长期运行可通过环境变量 GATEWAY_MODEL_API_KEY 注入。").
				FontSize(11.5).
				TextColor(SailInkMuted)

			if state.KeyConfigured {
				if ui.Checkbox(c.Key("set-clear-key"), &st.clearKey, "保存时清除当前内存中的 API Key").Changed() {
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
		SailCardTitle(c, "路由策略与实时校验", "控制每次向本地 Agent 返回的最大候选工具数（相关性概率 ≥ 0.5 按降序截断），并可立即用真实已启用工具验证连通性。", nil)

		ui.Row(c).FillWidth().Gap(16).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "单次返回工具上限 max_tools（1–12）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if ui.NumberInput(c.Key("set-max-tools"), &st.maxToolsFloat, 1, 12, 1).
					Label("单次返回工具上限").
					Width(200).
					Changed() {
					st.config.MaxTools = clampInt(int(math.Round(st.maxToolsFloat)), 1, 12)
					st.testResult = nil
				}
			})

			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				ui.Text(c, "模型请求超时 timeout_seconds（5–180 秒）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
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
			ui.Text(c, "自定义校验意图（可选，留空自动根据已启用工具生成）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("set-test-intent"), &st.testIntent).
				Placeholder("例如：查看最近需要 review 的 pull request").
				FillWidth()
		})

		if !state.Setup.ToolsReady {
			SailAlertBox(c, ToneWarning, "当前尚未启用任何非演示工具", "可先「仅保存配置」，启用工具后再运行路由校验。")
			ui.Row(c).Children(func() {
				if SailSmallButton(c.Key("set-goto-services"), "前往启用工具 →").Clicked() {
					u.Navigate(ViewServices)
				}
			})
		}

		if st.errTitle != "" {
			SailAlertBox(c, ToneDanger, st.errTitle, st.errDetail)
		}

		modelCheck := state.Setup.ModelCheck
		if dirty && modelCheck != nil {
			SailAlertBox(c, ToneInfo, "表单参数已修改", "点击「保存并运行路由校验」更新验证状态。")
		} else if !dirty && modelCheck != nil {
			tone := ToneSuccess
			if modelCheck.Status == "error" {
				tone = ToneDanger
			} else if modelCheck.Stale {
				tone = ToneWarning
			}
			title := fmt.Sprintf("%s（%s）", modelCheck.Message, FormatTime(modelCheck.CheckedAt))
			if modelCheck.Stale {
				title = fmt.Sprintf("工具池或参数发生变化，建议重新校验（上次：%s）", FormatTime(modelCheck.CheckedAt))
			}
			SailAlertBox(c, tone, title, modelCheck.Detail)
		}

		if res := st.testResult; res != nil {
			SailSubtleCard(c, func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "实时校验响应：").FontSize(13).FontWeight(600).TextColor(SailInkStrong)
						SailBadgeDot(c, res.Status, discoveryStatusTone(res.Status))
					})
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

		ui.Divider(c)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
			ui.Text(c, "路由校验仅验证模型响应与工具映射，不执行下游工具。").FontSize(12).TextColor(SailInkMuted)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				saveLabel := "仅保存配置"
				if st.busyMode == "save" {
					saveLabel = "保存中…"
				}
				if ui.Button(c.Key("set-save-only"), saveLabel).Disabled(st.busyMode != "").Clicked() {
					u.submitSettings(st, false)
				}

				testLabel := "保存并运行路由校验"
				if st.busyMode == "test" {
					testLabel = "正在校验…"
				}
				if ui.PrimaryButton(c.Key("set-save-test"), testLabel).
					Disabled(st.busyMode != "" || !state.Setup.ToolsReady).
					Clicked() {
					u.submitSettings(st, true)
				}
			})
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
