package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/gateway"

	"github.com/egoist/mygo/ui"
)

type playgroundDemoExample struct {
	Short  string
	Intent string
}

var playgroundDemoExamples = []playgroundDemoExample{
	{Short: "排查服务错误日志", Intent: "排查 billing 服务最近频繁报错的原因"},
	{Short: "查询服务归属团队", Intent: "查一下 checkout 服务由哪个团队负责"},
	{Short: "查看近期部署版本", Intent: "查看 billing 最近部署了什么版本"},
}

type playgroundState struct {
	initialized bool

	intent            string
	context           string
	showContext       bool
	actor             string
	serviceScopeLabel string
	selectedServiceID string
	includeDemo       bool
	summaryOnly       bool

	lastReplaySeq int

	routing    bool
	routeError string
	result     *gateway.DiscoverResult

	selectedTool  *gateway.Selection
	loadingSchema bool
	inspectorTab  int // 0: Input Schema, 1: 发起试调用
	argsText      string
	fieldsText    string
	invoking      bool
	invokeError   string
	invocation    *gateway.InvokeResult
}

var (
	playgroundMu     sync.Mutex
	playgroundStates = map[*DesktopUI]*playgroundState{}
)

func getPlaygroundState(u *DesktopUI) *playgroundState {
	playgroundMu.Lock()
	defer playgroundMu.Unlock()
	st, ok := playgroundStates[u]
	if !ok {
		st = &playgroundState{
			actor:             "developer",
			serviceScopeLabel: "全部已启用服务",
			argsText:          "{}",
		}
		playgroundStates[u] = st
	}
	return st
}

func (u *DesktopUI) renderPlayground(c *ui.Context) {
	state := u.State
	st := getPlaygroundState(u)

	// Initialize default actor once State is available.
	if !st.initialized {
		st.initialized = true
		if state.Access.Shared && state.Access.Actor != "" {
			st.actor = state.Access.Actor
		} else if u.ReplayActor != "" {
			st.actor = u.ReplayActor
		} else {
			st.actor = "developer"
		}
		if u.ReplayIntent != "" && u.ReplaySeq > 0 {
			st.intent = u.ReplayIntent
			st.lastReplaySeq = u.ReplaySeq
		}
	}

	// Sync handoff when navigating from Overview blind-spot replay.
	if u.ReplaySeq != st.lastReplaySeq && u.ReplaySeq > 0 {
		st.lastReplaySeq = u.ReplaySeq
		st.intent = u.ReplayIntent
		if !state.Access.Shared && u.ReplayActor != "" {
			st.actor = u.ReplayActor
		}
		st.includeDemo = false
		st.selectedServiceID = ""
		st.serviceScopeLabel = "全部已启用服务"
		st.result = nil
		st.selectedTool = nil
		st.invocation = nil
		st.routeError = ""
		st.invokeError = ""
	}

	effectiveActor := st.actor
	if state.Access.Shared && state.Access.Actor != "" {
		effectiveActor = state.Access.Actor
	}
	if effectiveActor == "" {
		effectiveActor = "developer"
	}

	// Build active server lookup & scope selector options.
	serversByID := make(map[string]gateway.Server, len(state.Registry.Servers))
	scopeOptions := []string{"全部已启用服务"}
	scopeLabelToID := map[string]string{"全部已启用服务": ""}
	for _, srv := range state.Registry.Servers {
		serversByID[srv.ID] = srv
		if !srv.Enabled || (srv.Demo && !st.includeDemo) {
			continue
		}
		label := fmt.Sprintf("%s (%s)", srv.Name, srv.ID)
		if srv.Demo {
			label += " [演示]"
		}
		scopeOptions = append(scopeOptions, label)
		scopeLabelToID[label] = srv.ID
	}
	if _, ok := scopeLabelToID[st.serviceScopeLabel]; !ok {
		st.serviceScopeLabel = scopeOptions[0]
		st.selectedServiceID = ""
	} else {
		st.selectedServiceID = scopeLabelToID[st.serviceScopeLabel]
	}

	// Count eligible tools under current actor, demo toggle, and service scope.
	eligibleCount := 0
	toolsByID := make(map[string]gateway.Tool, len(state.Registry.Tools))
	for _, t := range state.Registry.Tools {
		toolsByID[t.ID] = t
		if !t.Enabled {
			continue
		}
		srv, ok := serversByID[t.ServerID]
		if !ok || !srv.Enabled {
			continue
		}
		if srv.Demo && !st.includeDemo {
			continue
		}
		if st.selectedServiceID != "" && t.ServerID != st.selectedServiceID {
			continue
		}
		if !slices.Contains(t.Actors, "*") && !slices.Contains(t.Actors, effectiveActor) {
			continue
		}
		eligibleCount++
	}

	// Page Header.
	modelDisplay := state.Settings.Model
	if modelDisplay == "" {
		modelDisplay = "未配置"
	}
	SailSectionHeader(c, "路由沙盒", "模拟下游 Agent 发起意图发现（discover_tools），检验语义召回结果并直接试调候选工具（invoke_tool）。", func() {
		ui.Row(c).Gap(6).Padding(4, 10).Radius(999).Background(SailBgSubtle).Border(1, SailBorder).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "模型").FontSize(12).TextColor(SailInkMuted).SingleLine()
			ui.Text(c, modelDisplay).Font("monospace").FontSize(12).FontWeight(600).TextColor(SailInkStrong).SingleLine()
			ui.Text(c, "·").FontSize(12).TextColor(SailInkFaint)
			ui.Textf(c, "候选池 %d 个工具", eligibleCount).FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()
		})
		if SailSmallButton(c.Key("pg-settings-btn"), "路由参数").Clicked() {
			u.Navigate(ViewSettings)
		}
	})

	if strings.TrimSpace(state.Settings.Model) == "" {
		SailAlertBox(c, ToneWarning, "尚未配置 Decisions 路由模型", "请先前往「访问与路由」填写模型名称、Base URL 与 API Key。")
	}

	// Two-column Workbench Split.
	ui.Row(c).FillWidth().Gap(18).AlignItems(ui.Start).Children(func() {
		// Left Column: Intent Composer + Discovery Results.
		ui.Column(c).Grow(1).MinWidth(0).Gap(16).Children(func() {
			u.renderPlaygroundComposer(c, st, state, effectiveActor, scopeOptions, scopeLabelToID)
			u.renderPlaygroundResults(c, st, serversByID, toolsByID, effectiveActor)
		})

		// Right Column: Tool Schema & Direct Invoke Inspector.
		ui.Column(c).Grow(1).MinWidth(0).Gap(16).Children(func() {
			u.renderPlaygroundInspector(c, st, toolsByID, effectiveActor)
		})
	})
}

func (u *DesktopUI) renderPlaygroundComposer(
	c *ui.Context,
	st *playgroundState,
	state *Bootstrap,
	effectiveActor string,
	scopeOptions []string,
	scopeLabelToID map[string]string,
) {
	SailCard(c, func() {
		SailCardTitle(c, "1. 输入任务意图（discover_tools）", "", func() {
			ui.Text(c, "快捷键 ⌘+Enter").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
		})

		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, "自然语言任务意图（intent）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextArea(c.Key("pg-intent"), &st.intent).
				Placeholder("描述当前要完成的开发或运维任务，例如：查看仓库最近有哪些未关闭的 PR 需要 review…").
				Lines(3, 6).
				FillWidth().
				OnTextKey(func(k ui.TextKey) bool {
					if k.Key == ui.KeyEnter && (k.Mods == ui.Cmd || k.Mods == ui.Ctrl) {
						intent := k.Text()
						st.intent = intent
						u.runPlaygroundDiscover(st, intent, effectiveActor)
						return true
					}
					return false
				})
		})

		if st.showContext {
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, "补充上下文（context）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				ui.TextArea(c.Key("pg-context"), &st.context).
					Placeholder("补充已知服务名、仓库名或前序排查线索…").
					Lines(2, 4).
					FillWidth()
			})
		}

		// Context toggle + quick demo examples.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Wrap().Children(func() {
			ctxLabel := "+ 添加上下文"
			if st.showContext {
				ctxLabel = "收起上下文"
			}
			if SailSmallButton(c.Key("pg-toggle-context"), ctxLabel).Clicked() {
				st.showContext = !st.showContext
			}

			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, "快捷示例：").FontSize(12).TextColor(SailInkMuted).SingleLine()
				for i, ex := range playgroundDemoExamples {
					if SailSmallButton(c.Key(fmt.Sprintf("pg-demo-%d", i)), ex.Short).Clicked() {
						st.intent = ex.Intent
						st.includeDemo = true
						st.selectedServiceID = ""
						st.serviceScopeLabel = "全部已启用服务"
						st.result = nil
						st.selectedTool = nil
						st.invocation = nil
						st.routeError = ""
						st.invokeError = ""
					}
				}
			})
		})

		ui.Divider(c)

		// Actor, Service Scope, Include Demo, Summary Only controls.
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				actorLabel := "模拟身份"
				if state.Access.Shared {
					actorLabel = "当前身份"
				}
				ui.Text(c, actorLabel).FontSize(12.5).TextColor(SailInkMuted).SingleLine()
				if state.Access.Shared {
					SailBadge(c, effectiveActor, ToneInfo)
				} else {
					if ui.Select(c.Key("pg-actor"), &st.actor, []string{"developer", "observer"}).MinWidth(120).Changed() {
						st.result = nil
						st.selectedTool = nil
						st.invocation = nil
					}
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "限定服务").FontSize(12.5).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-scope"), &st.serviceScopeLabel, scopeOptions).MinWidth(170).Changed() {
					st.selectedServiceID = scopeLabelToID[st.serviceScopeLabel]
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
				if ui.Checkbox(c.Key("pg-include-demo"), &st.includeDemo, "包含内置演示工具").Changed() {
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}

				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.Switch(c.Key("pg-summary-only"), &st.summaryOnly).Label("仅返回摘要").Changed() {
						st.result = nil
						st.selectedTool = nil
						st.invocation = nil
					}
					ui.Text(c, "仅返回摘要（summary_only）").FontSize(12.5).TextColor(SailInkBody).SingleLine()
				})
			})

			btnLabel := "发起路由发现"
			if st.routing {
				btnLabel = "模型选择中…"
			}
			if ui.PrimaryButton(c.Key("pg-discover-btn"), btnLabel).
				Disabled(strings.TrimSpace(st.intent) == "" || st.routing).
				Clicked() {
				u.runPlaygroundDiscover(st, st.intent, effectiveActor)
			}
		})
	})
}

func (u *DesktopUI) renderPlaygroundResults(
	c *ui.Context,
	st *playgroundState,
	serversByID map[string]gateway.Server,
	toolsByID map[string]gateway.Tool,
	effectiveActor string,
) {
	SailCard(c, func() {
		SailCardTitle(c, "2. 路由决策与命中工具", "", func() {
			if res := st.result; res != nil {
				SailBadgeDot(c, discoveryStatusLabel(res.Status), discoveryStatusTone(res.Status))
				ui.Textf(c, "候选 %d", res.Eligible).FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()
				ui.Textf(c, "%d ms", res.DurationMS).FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()
				if res.PromptTokens+res.OutputTokens > 0 {
					ui.Textf(c, "Prompt %d / Out %d tok", res.PromptTokens, res.OutputTokens).
						FontSize(12).
						FontFeatures("tnum").
						TextColor(SailInkMuted).
						SingleLine()
				}
			}
		})

		if st.routeError != "" {
			SailAlertBox(c, ToneDanger, "路由发现失败", st.routeError)
		}

		if st.routing {
			ui.Column(c).FillWidth().Padding(24, 16).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label("正在请求 Decisions 路由模型")
				ui.Text(c, "正在请求 Decisions 路由模型…").FontSize(13.5).FontWeight(600).TextColor(SailInkStrong)
				ui.Text(c, "按身份筛选候选工具 → 评估语义相关性 → 返回匹配工具与理由").FontSize(12).TextColor(SailInkMuted)
			})
			return
		}

		res := st.result
		if res == nil {
			if st.routeError == "" {
				SailEmptyState(c, "等待运行工具发现", "输入任务意图后点击「发起路由发现」，命中的候选工具、精选理由与 Token 耗时将在此展示。", "", nil)
			}
			return
		}

		SailSubtleCard(c, func() {
			summaryText := res.Summary
			if summaryText == "" {
				summaryText = "（模型未返回摘要）"
			}
			ui.Text(c, summaryText).FontSize(13).FontWeight(600).TextColor(SailInkStrong)

			if res.Question != "" {
				ui.Text(c, "补充信息建议："+res.Question).FontSize(12.5).TextColor(SailWarningInk)
			}

			ui.Row(c).Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
				if res.Model != "" {
					ui.Textf(c, "模型: %s", res.Model).Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
				}
				if len(res.Servers) > 0 {
					ui.Textf(c, "分层命中服务: %s", strings.Join(res.Servers, ", ")).Font("monospace").FontSize(11.5).TextColor(SailInkMuted)
				}
				if res.PromptTokens+res.OutputTokens > 0 {
					ui.Textf(c, "总 Token: %d（Prompt %d + Output %d）", res.PromptTokens+res.OutputTokens, res.PromptTokens, res.OutputTokens).
						FontSize(11.5).
						FontFeatures("tnum").
						TextColor(SailInkMuted)
				}
			})
		})

		if len(res.Tools) == 0 {
			SailEmptyState(c, "未命中工具", "请尝试补充更具体的服务名称、操作目标，或检查当前身份下的已启用工具。", "", nil)
			return
		}

		ui.Column(c).FillWidth().Gap(8).Children(func() {
			for idx, sel := range res.Tools {
				isSelected := st.selectedTool != nil && st.selectedTool.ID == sel.ID
				srvName := sel.ServerID
				if srv, ok := serversByID[sel.ServerID]; ok && srv.Name != "" {
					srvName = srv.Name
				}

				rowBtn := ui.ButtonBase(c.Key("pg-hit-"+sel.ID)).
					FillWidth().
					Padding(12, 14).
					Radius(7).
					Gap(10).
					AlignItems(ui.Start).
					Cursor(ui.CursorPointer)
				if isSelected {
					rowBtn.Background(SailAccentSoft).Border(1, SailAccentBorder)
				} else if rowBtn.Hovered() {
					rowBtn.Background(SailBgHover).Border(1, SailBorderStrong)
				} else {
					rowBtn.Background(SailBgCanvas).Border(1, SailBorder)
				}

				rowBtn.Children(func() {
					ui.Box(c).
						Padding(2, 7).
						Radius(999).
						Background(SailBgMuted).
						Children(func() {
							ui.Textf(c, "#%d", idx+1).Font("monospace").FontSize(11.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
						})

					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
							ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, sel.Name).Font("monospace").FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
								ui.Text(c, srvName).FontSize(12).TextColor(SailInkMuted).SingleLine()
							})
							if sel.ReadOnly {
								SailBadge(c, "只读", ToneSuccess)
							} else {
								SailBadge(c, "写操作", ToneWarning)
							}
						})
						if sel.Reason != "" {
							ui.Text(c, sel.Reason).FontSize(12.5).TextColor(SailInkBody)
						} else if sel.Description != "" {
							ui.Text(c, sel.Description).FontSize(12.5).TextColor(SailInkMuted)
						}
					})
				})

				if rowBtn.Clicked() {
					u.selectPlaygroundTool(st, sel, toolsByID, effectiveActor)
				}
			}
		})
	})
}

func (u *DesktopUI) renderPlaygroundInspector(
	c *ui.Context,
	st *playgroundState,
	toolsByID map[string]gateway.Tool,
	effectiveActor string,
) {
	SailCard(c, func() {
		SailCardTitle(c, "3. 工具检查与试调用（invoke_tool）", "", func() {
			if st.selectedTool != nil {
				if st.selectedTool.ReadOnly {
					SailBadge(c, "只读", ToneSuccess)
				} else {
					SailBadge(c, "写操作", ToneWarning)
				}
			}
		})

		sel := st.selectedTool
		if sel == nil {
			SailEmptyState(c, "未选中工具", "从左侧命中结果中选择任一工具，检查真实 Input Schema 或直接发起试调用。", "", nil)
			return
		}

		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, sel.ID).Font("monospace").FontSize(14).Bold().TextColor(SailInkStrong).Selectable()
			if sel.Description != "" {
				ui.Text(c, sel.Description).FontSize(12.5).TextColor(SailInkMuted)
			}
			if sel.Reason != "" {
				ui.Text(c, "路由理由: "+sel.Reason).FontSize(12).TextColor(SailAccent)
			}
		})

		ui.Segmented(c.Key("pg-inspector-tab"), &st.inspectorTab, "Input Schema", "发起试调用").Label("工具检查模式")

		if st.inspectorTab == 0 {
			if sel.InputSchema == nil {
				SailAlertBox(c, ToneInfo, "当前命中结果来自摘要模式（summary_only）", "摘要模式仅返回能力描述与理由，可按需调用 get_tool_schema 拉取完整 Input Schema。")
				ui.Row(c).Gap(8).Children(func() {
					btnText := "拉取完整 Input Schema"
					if st.loadingSchema {
						btnText = "加载中…"
					}
					if ui.PrimaryButton(c.Key("pg-fetch-schema"), btnText).Disabled(st.loadingSchema).Clicked() {
						u.fetchPlaygroundToolSchema(st, sel.ID, effectiveActor)
					}
					if full, ok := toolsByID[sel.ID]; ok && full.InputSchema != nil {
						if SailSmallButton(c.Key("pg-use-local-schema"), "从本地注册表补全").Clicked() {
							sel.Tool = full
							st.argsText = initialArgsForTool(full)
						}
					}
				})
			} else {
				SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(sel.InputSchema), func() {
					u.Notify("已复制 Schema")
				})
			}

			if ui.PrimaryButton(c.Key("pg-goto-invoke"), "使用此工具试运行").Clicked() {
				st.inspectorTab = 1
			}
			return
		}

		// Tab 1: Direct Invoke Workbench.
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "调用参数（arguments） JSON").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if SailSmallButton(c.Key("pg-reset-args"), "填入示例参数").Clicked() {
					toolForArgs := sel.Tool
					if toolForArgs.InputSchema == nil {
						if full, ok := toolsByID[sel.ID]; ok {
							toolForArgs = full
						}
					}
					st.argsText = initialArgsForTool(toolForArgs)
					st.invokeError = ""
				}
			})
			ui.TextArea(c.Key("pg-args-"+sel.ID), &st.argsText).
				Font("monospace").
				Lines(6, 12).
				FillWidth()
		})

		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, "返回字段裁剪（fields，可选，逗号分隔路径）").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("pg-fields"), &st.fieldsText).
				Font("monospace").
				Placeholder("例如：logs.timestamp, logs.message").
				FillWidth()
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			invokeLabel := "执行调用（Invoke）"
			if st.invoking {
				invokeLabel = "执行中…"
			}
			if ui.PrimaryButton(c.Key("pg-invoke-btn"), invokeLabel).Disabled(st.invoking).Clicked() {
				u.runPlaygroundInvoke(st, sel.ID, effectiveActor)
			}
			ui.Textf(c, "调用身份: %s", effectiveActor).Font("monospace").FontSize(12).TextColor(SailInkMuted)
		})

		if st.invokeError != "" {
			SailAlertBox(c, ToneDanger, "工具调用失败", st.invokeError)
		}

		if inv := st.invocation; inv != nil {
			title := fmt.Sprintf("RESPONSE · %s · %d ms", inv.Protocol, inv.DurationMS)
			SailCodeBlock(c, title, PrettyJSON(inv.Data), func() {
				u.Notify("已复制调用结果")
			})
		}
	})
}

func (u *DesktopUI) selectPlaygroundTool(
	st *playgroundState,
	sel gateway.Selection,
	toolsByID map[string]gateway.Tool,
	effectiveActor string,
) {
	chosen := sel
	// When summary_only is off, Discover returns full schema/example;
	// if summary_only was on, try local registry first for example args,
	// while preserving sel.InputSchema == nil until explicitly loaded or invoked,
	// or auto-fill argsText from local registry/example.
	argsSource := chosen.Tool
	if argsSource.InputSchema == nil && argsSource.Example == nil {
		if full, ok := toolsByID[chosen.ID]; ok {
			argsSource = full
		}
	}
	st.selectedTool = &chosen
	st.inspectorTab = 0
	st.argsText = initialArgsForTool(argsSource)
	st.fieldsText = ""
	st.invokeError = ""
	st.invocation = nil
	_ = effectiveActor
}

func initialArgsForTool(t gateway.Tool) string {
	if len(t.Example) > 0 {
		return PrettyJSON(t.Example)
	}
	if len(t.InputSchema) > 0 {
		return PrettyJSON(ExampleFromSchema(t.InputSchema))
	}
	return "{}"
}

func (u *DesktopUI) runPlaygroundDiscover(st *playgroundState, intent, effectiveActor string) {
	trimmed := strings.TrimSpace(intent)
	if trimmed == "" || st.routing || u.Client == nil {
		return
	}

	req := gateway.DiscoverRequest{
		Intent:      intent,
		Context:     st.context,
		Actor:       effectiveActor,
		IncludeDemo: st.includeDemo,
		SummaryOnly: st.summaryOnly,
	}
	if st.selectedServiceID != "" {
		req.Services = []string{st.selectedServiceID}
	}

	timeoutSec := 45
	if u.State != nil && u.State.Settings.TimeoutSeconds > 0 {
		timeoutSec = u.State.Settings.TimeoutSeconds
	}

	st.routing = true
	st.routeError = ""
	st.result = nil
	st.selectedTool = nil
	st.invocation = nil
	st.invokeError = ""

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec+15)*time.Second)
		defer cancel()

		res, err := u.Client.Discover(ctx, req)
		if err != nil {
			if IsUnauthorized(err) {
				u.updateUI(func() { st.routing = false })
				return err
			}
			_ = u.RefreshSync()
			u.updateUI(func() {
				st.routing = false
				st.routeError = err.Error()
			})
			return nil
		}

		u.updateUI(func() {
			st.routing = false
			st.result = &res
			if len(res.Tools) > 0 {
				var toolsByID map[string]gateway.Tool
				if u.State != nil {
					toolsByID = make(map[string]gateway.Tool, len(u.State.Registry.Tools))
					for _, t := range u.State.Registry.Tools {
						toolsByID[t.ID] = t
					}
				}
				u.selectPlaygroundTool(st, res.Tools[0], toolsByID, effectiveActor)
			}
		})
		_ = u.RefreshSync()
		return nil
	})
}

func (u *DesktopUI) fetchPlaygroundToolSchema(st *playgroundState, toolID, effectiveActor string) {
	if st.loadingSchema || u.Client == nil {
		return
	}
	st.loadingSchema = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		tool, err := u.Client.ToolSchema(ctx, toolID, effectiveActor)
		if err != nil {
			u.updateUI(func() { st.loadingSchema = false })
			return err
		}
		u.updateUI(func() {
			st.loadingSchema = false
			if st.selectedTool != nil && st.selectedTool.ID == toolID {
				reason := st.selectedTool.Reason
				st.selectedTool.Tool = tool
				st.selectedTool.Reason = reason
				if strings.TrimSpace(st.argsText) == "" || strings.TrimSpace(st.argsText) == "{}" {
					st.argsText = initialArgsForTool(tool)
				}
			}
		})
		return nil
	})
}

func (u *DesktopUI) runPlaygroundInvoke(st *playgroundState, toolID, effectiveActor string) {
	if st.invoking || u.Client == nil {
		return
	}

	rawArgs := strings.TrimSpace(st.argsText)
	if rawArgs == "" {
		rawArgs = "{}"
	}
	var parsedArgs map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &parsedArgs); err != nil {
		st.invokeError = "arguments 必须是合法的 JSON 对象: " + err.Error()
		return
	}
	if parsedArgs == nil {
		parsedArgs = map[string]any{}
	}

	var fields []string
	for _, part := range strings.Split(st.fieldsText, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}

	req := gateway.InvokeRequest{
		ToolID:    toolID,
		Arguments: parsedArgs,
		Fields:    fields,
		Actor:     effectiveActor,
	}

	st.invoking = true
	st.invokeError = ""
	st.invocation = nil

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := u.Client.Invoke(ctx, req)
		if err != nil {
			if IsUnauthorized(err) {
				u.updateUI(func() { st.invoking = false })
				return err
			}
			_ = u.RefreshSync()
			u.updateUI(func() {
				st.invoking = false
				st.invokeError = err.Error()
			})
			return nil
		}

		u.updateUI(func() {
			st.invoking = false
			st.invocation = &res
		})
		_ = u.RefreshSync()
		return nil
	})
}

func discoveryStatusLabel(status string) string {
	switch status {
	case "selected":
		return "selected · 已命中"
	case "needs_context":
		return "needs_context · 需补充信息"
	case "no_match":
		return "no_match · 无匹配"
	default:
		return status
	}
}

func discoveryStatusTone(status string) BadgeTone {
	switch status {
	case "selected":
		return ToneSuccess
	case "needs_context":
		return ToneWarning
	case "error", "failed":
		return ToneDanger
	default:
		return ToneNeutral
	}
}
