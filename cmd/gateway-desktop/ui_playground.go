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
	simRepoLabel      string
	simRepoKey        string // "": 管理员全量, "*": 未绑定新仓库, or repo.ID
	simAgentLabel     string
	simAgentKey       string // "": 不限 ("*" when simulating repo), or codex/claude/cursor/opencode/pi
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
			simRepoLabel:      "管理员全量",
			simAgentLabel:     "不限",
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
		if u.ReplaySeq > 0 {
			if u.ReplayIntent != "" {
				st.intent = u.ReplayIntent
			}
			applyPlaygroundReplayScope(st, u)
			st.lastReplaySeq = u.ReplaySeq
		}
	}

	// Sync handoff when navigating from Overview, Logs, or Repos preview.
	if u.ReplaySeq != st.lastReplaySeq && u.ReplaySeq > 0 {
		st.lastReplaySeq = u.ReplaySeq
		if u.ReplayIntent != "" {
			st.intent = u.ReplayIntent
		}
		if !state.Access.Shared && u.ReplayActor != "" {
			st.actor = u.ReplayActor
		}
		applyPlaygroundReplayScope(st, u)
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

	// Build repository & coding agent simulation options.
	repoOptions := []string{"管理员全量", "未绑定新仓库 (*)"}
	repoLabelToKey := map[string]string{
		"管理员全量":      "",
		"未绑定新仓库 (*)": "*",
	}
	repoKeyToLabel := map[string]string{
		"":  "管理员全量",
		"*": "未绑定新仓库 (*)",
	}
	for _, repo := range state.Scopes.Repositories {
		lbl := repo.Name
		if lbl == "" {
			lbl = repo.ID
		} else if _, dup := repoLabelToKey[lbl]; dup {
			lbl = fmt.Sprintf("%s (%s)", repo.Name, repo.ID)
		}
		repoOptions = append(repoOptions, lbl)
		repoLabelToKey[lbl] = repo.ID
		repoKeyToLabel[repo.ID] = lbl
	}
	if lbl, ok := repoKeyToLabel[st.simRepoKey]; ok {
		st.simRepoLabel = lbl
	} else {
		st.simRepoKey = ""
		st.simRepoLabel = "管理员全量"
	}

	agentOptions := []string{"不限"}
	agentLabelToKey := map[string]string{"不限": ""}
	agentKeyToLabel := map[string]string{"": "不限", "*": "不限"}
	for _, ag := range scopeAgents {
		agentOptions = append(agentOptions, ag.Name)
		agentLabelToKey[ag.Name] = ag.ID
		agentKeyToLabel[ag.ID] = ag.Name
	}
	if lbl, ok := agentKeyToLabel[st.simAgentKey]; ok {
		st.simAgentLabel = lbl
	} else {
		st.simAgentKey = ""
		st.simAgentLabel = "不限"
	}

	simScope := activePlaygroundSimScope(st)
	var simGrants []ScopeServiceGrant
	if simScope != nil {
		simGrants, _, _, _ = resolveEffectiveScopeRule(state.Scopes, simScope.RepoKey, simScope.AgentKey)
	}

	// Count eligible tools under current actor, demo toggle, service scope, and simulated repo/agent scope.
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
		if simScope != nil && !scopeGrantAllowsTool(simGrants, t) {
			continue
		}
		eligibleCount++
	}

	// Page Header.
	modelDisplay := state.Settings.Model
	if modelDisplay == "" {
		modelDisplay = "未配置模型"
	}
	SailSectionHeader(c, "路由沙盒", "", func() {
		ui.Textf(c, "%s · 候选 %d", modelDisplay, eligibleCount).
			FontSize(12.5).
			FontFeatures("tnum").
			TextColor(SailInkMuted).
			SingleLine()
		if SailSmallButton(c.Key("pg-settings-btn"), "路由参数").Clicked() {
			u.Navigate(ViewSettings)
		}
	})

	if strings.TrimSpace(state.Settings.Model) == "" {
		SailAlertBox(c, ToneWarning, "尚未配置路由模型", "请前往「访问与路由」设置 Model ID 与 API Key。")
	}

	// Two-column Workbench Split.
	ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Start).Children(func() {
		// Left Column: Intent Composer + Discovery Results.
		ui.Column(c).Grow(1).MinWidth(0).Gap(24).Children(func() {
			u.renderPlaygroundComposer(c, st, state, effectiveActor, scopeOptions, scopeLabelToID, repoOptions, repoLabelToKey, agentOptions, agentLabelToKey)
			u.renderPlaygroundResults(c, st, serversByID, toolsByID, effectiveActor)
		})

		// Right Column: Tool Schema & Direct Invoke Inspector.
		ui.Column(c).Grow(1).MinWidth(0).Gap(24).Children(func() {
			u.renderPlaygroundInspector(c, st, toolsByID, effectiveActor)
		})
	})
}

func applyPlaygroundReplayScope(st *playgroundState, u *DesktopUI) {
	if u.ReplayHasScope {
		st.simRepoKey = u.ReplayRepoKey
		if u.ReplayAgentKey == "*" {
			st.simAgentKey = ""
		} else {
			st.simAgentKey = u.ReplayAgentKey
		}
		if st.simRepoKey == "" && st.simAgentKey != "" {
			st.simRepoKey = "*"
		}
	} else {
		st.simRepoKey = ""
		st.simAgentKey = ""
	}
}

func activePlaygroundSimScope(st *playgroundState) *SimScope {
	if st.simRepoKey == "" && st.simAgentKey == "" {
		return nil
	}
	repoKey := st.simRepoKey
	if repoKey == "" {
		repoKey = "*"
	}
	agentKey := st.simAgentKey
	if agentKey == "" {
		agentKey = "*"
	}
	return &SimScope{
		RepoKey:  repoKey,
		AgentKey: agentKey,
	}
}

func (u *DesktopUI) renderPlaygroundComposer(
	c *ui.Context,
	st *playgroundState,
	state *Bootstrap,
	effectiveActor string,
	scopeOptions []string,
	scopeLabelToID map[string]string,
	repoOptions []string,
	repoLabelToKey map[string]string,
	agentOptions []string,
	agentLabelToKey map[string]string,
) {
	SailCard(c, func() {
		SailCardTitle(c, "任务意图", "", func() {
			ui.Text(c, "⌘+Enter").FontSize(11.5).Font("monospace").TextColor(SailInkFaint).SingleLine()
		})

		ui.TextArea(c.Key("pg-intent"), &st.intent).
			Placeholder("输入任务意图，例如：查看待 review 的未关闭 PR…").
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

		if st.showContext {
			ui.Column(c).FillWidth().Gap(5).Children(func() {
				ui.Text(c, "补充上下文").FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				ui.TextArea(c.Key("pg-context"), &st.context).
					Placeholder("已知服务名、仓库名或排查线索…").
					Lines(2, 4).
					FillWidth()
			})
		}

		// Context toggle + quick demo examples.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Wrap().Children(func() {
			ctxLabel := "+ 上下文"
			if st.showContext {
				ctxLabel = "收起上下文"
			}
			if SailSmallButton(c.Key("pg-toggle-context"), ctxLabel).Clicked() {
				st.showContext = !st.showContext
			}

			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				for i, ex := range playgroundDemoExamples {
					if SailSmallButton(c.Key(fmt.Sprintf("pg-demo-%d", i)), ex.Short).Clicked() {
						st.intent = ex.Intent
						st.includeDemo = true
						st.selectedServiceID = ""
						st.serviceScopeLabel = "全部已启用服务"
						st.simRepoKey = ""
						st.simRepoLabel = "管理员全量"
						st.simAgentKey = ""
						st.simAgentLabel = "不限"
						st.result = nil
						st.selectedTool = nil
						st.invocation = nil
						st.routeError = ""
						st.invokeError = ""
					}
				}
			})
		})

		ui.Box(c).FillWidth().Height(1).Background(SailDivider)

		// Actor, Service Scope, Include Demo, Summary Only controls + Primary Discover action.
		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "身份").FontSize(12).TextColor(SailInkMuted).SingleLine()
				if state.Access.Shared {
					ui.Text(c, effectiveActor).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				} else {
					if ui.Select(c.Key("pg-actor"), &st.actor, []string{"developer", "observer"}).MinWidth(116).Changed() {
						st.result = nil
						st.selectedTool = nil
						st.invocation = nil
					}
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "服务").FontSize(12).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-scope"), &st.serviceScopeLabel, scopeOptions).MinWidth(164).Changed() {
					st.selectedServiceID = scopeLabelToID[st.serviceScopeLabel]
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "仓库").FontSize(12).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-sim-repo"), &st.simRepoLabel, repoOptions).MinWidth(156).Changed() {
					st.simRepoKey = repoLabelToKey[st.simRepoLabel]
					if st.simRepoKey == "" {
						st.simAgentKey = ""
						st.simAgentLabel = "不限"
					}
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "客户端").FontSize(12).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-sim-agent"), &st.simAgentLabel, agentOptions).MinWidth(128).Changed() {
					st.simAgentKey = agentLabelToKey[st.simAgentLabel]
					if st.simAgentKey != "" && st.simRepoKey == "" {
						st.simRepoKey = "*"
						st.simRepoLabel = "未绑定新仓库 (*)"
					}
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
				if ui.Checkbox(c.Key("pg-include-demo"), &st.includeDemo, "演示工具").Changed() {
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}

				if ui.Checkbox(c.Key("pg-summary-only"), &st.summaryOnly, "仅摘要").Changed() {
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})

			btnLabel := "发起路由发现"
			if st.routing {
				btnLabel = "路由中…"
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
	SailFlushCard(c, func() {
		var headerNote string
		if res := st.result; res != nil {
			headerNote = fmt.Sprintf("候选 %d · %d ms", res.Eligible, res.DurationMS)
			if res.PromptTokens+res.OutputTokens > 0 {
				headerNote += fmt.Sprintf(" · %d tok", res.PromptTokens+res.OutputTokens)
			}
		}

		SailListCardHeader(c, "路由结果", headerNote, func() {
			if res := st.result; res != nil {
				switch res.Status {
				case "selected":
					SailQuietStatus(c, "已命中", SailSuccessInk, SailInkStrong)
				case "needs_context":
					SailBadge(c, "需补充信息", ToneWarning)
				case "error", "failed":
					SailBadge(c, discoveryStatusLabel(res.Status), ToneDanger)
				default:
					SailQuietStatus(c, discoveryStatusLabel(res.Status), SailInkFaint, SailInkMuted)
				}
			}
		})

		if st.routeError != "" {
			ui.Column(c).FillWidth().Padding(16, 20).Children(func() {
				SailAlertBox(c, ToneDanger, "路由发现失败", st.routeError)
			})
		}

		if st.routing {
			ui.Column(c).FillWidth().Padding(32, 20).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label("正在请求路由模型")
				ui.Text(c, "正在评估候选工具…").FontSize(13).TextColor(SailInkMuted)
			})
			return
		}

		res := st.result
		if res == nil {
			if st.routeError == "" {
				ui.Column(c).FillWidth().Padding(20).Children(func() {
					SailEmptyState(c, "尚未发起路由", "", "", nil)
				})
			}
			return
		}

		if res.Summary != "" || res.Question != "" {
			ui.Column(c).
				FillWidth().
				Padding(14, 20).
				Gap(4).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailDivider).
				Children(func() {
					if res.Summary != "" {
						ui.Text(c, res.Summary).FontSize(13).LineHeight(1.48).FontWeight(500).TextColor(SailInkStrong)
					}
					if res.Question != "" {
						ui.Text(c, "建议补充："+res.Question).FontSize(12.5).TextColor(SailWarningInk)
					}
				})
		}

		if len(res.Tools) == 0 {
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, "未命中工具", "", "", nil)
			})
			return
		}

		for idx, item := range res.Tools {
			sel := item
			isSelected := st.selectedTool != nil && st.selectedTool.ID == sel.ID
			isLast := idx == len(res.Tools)-1
			srvName := sel.ServerID
			if srv, ok := serversByID[sel.ServerID]; ok && srv.Name != "" {
				srvName = srv.Name
			}

			rowBtn := ui.ButtonBase(c.Key("pg-hit-"+sel.ID)).
				FillWidth().
				Padding(13, 20).
				Gap(14).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Cursor(ui.CursorPointer)
			if !isLast {
				rowBtn.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			if isSelected {
				rowBtn.Background(SailAccentSoft)
			} else if rowBtn.Hovered() {
				rowBtn.Background(SailBgHover)
			}

			rowBtn.Children(func() {
				// Anchor 1 & 2: Rank + Name + Service, plus 1–2 line Reason/Summary
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Textf(c, "#%d", idx+1).Font("monospace").FontSize(11.5).FontWeight(600).TextColor(SailInkFaint).SingleLine()
						nameCol := SailInkStrong
						if isSelected {
							nameCol = SailAccent
						}
						ui.Text(c, sel.Name).Font("monospace").FontSize(13).FontWeight(600).TextColor(nameCol).SingleLine()
						ui.Text(c, srvName).FontSize(11.5).TextColor(SailInkFaint).SingleLine()
					})

					if sel.Reason != "" {
						ui.Text(c, sel.Reason).FontSize(12.5).LineHeight(1.42).TextColor(SailInkMuted).MaxLines(2)
					} else {
						SailDescSummary(c, sel.Description, "", 1)
					}
				})

				// Anchor 3: RW risk badge only when write-capable
				if !sel.ReadOnly {
					SailBadge(c, "写操作", ToneWarning).Shrink(0)
				}
			})

			if rowBtn.Clicked() {
				u.selectPlaygroundTool(st, sel, toolsByID, effectiveActor)
			}
		}
	})
}

func (u *DesktopUI) renderPlaygroundInspector(
	c *ui.Context,
	st *playgroundState,
	toolsByID map[string]gateway.Tool,
	effectiveActor string,
) {
	SailCard(c, func() {
		SailCardTitle(c, "工具检查与试调用", "", func() {
			if st.selectedTool != nil {
				if st.selectedTool.ReadOnly {
					ui.Text(c, "只读").FontSize(12).TextColor(SailInkMuted).SingleLine()
				} else {
					SailBadge(c, "写操作", ToneWarning)
				}
			}
		})

		sel := st.selectedTool
		if sel == nil {
			SailEmptyState(c, "未选择工具", "", "", nil)
			return
		}

		ui.Column(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, sel.ID).Font("monospace").FontSize(14).Bold().TextColor(SailInkStrong).Selectable()
			if sel.Reason != "" {
				ui.Text(c, sel.Reason).FontSize(12.5).LineHeight(1.45).TextColor(SailAccent)
			}
			if strings.TrimSpace(sel.Description) != "" {
				SailDescriptionDetail(c, "pg-desc-"+sel.ID, sel.Description)
			}
		})

		ui.Segmented(c.Key("pg-inspector-tab"), &st.inspectorTab, "Input Schema", "发起试调用").Label("工具检查模式")

		if st.inspectorTab == 0 {
			if sel.InputSchema == nil {
				SailSubtleCard(c, func() {
					ui.Text(c, "摘要模式未附带 Input Schema").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
					ui.Row(c).Gap(8).Children(func() {
						btnText := "拉取 Schema"
						if st.loadingSchema {
							btnText = "加载中…"
						}
						if ui.PrimaryButton(c.Key("pg-fetch-schema"), btnText).Disabled(st.loadingSchema).Clicked() {
							u.fetchPlaygroundToolSchema(st, sel.ID, effectiveActor)
						}
						if full, ok := toolsByID[sel.ID]; ok && full.InputSchema != nil {
							if SailSmallButton(c.Key("pg-use-local-schema"), "使用本地缓存").Clicked() {
								sel.Tool = full
								st.argsText = initialArgsForTool(full)
							}
						}
					})
				})
			} else {
				SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(sel.InputSchema), func() {
					u.Notify("已复制 Schema")
				})
			}

			if ui.PrimaryButton(c.Key("pg-goto-invoke"), "试运行此工具").Clicked() {
				st.inspectorTab = 1
			}
			return
		}

		// Tab 1: Direct Invoke Workbench.
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, "参数 (JSON)").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if SailSmallButton(c.Key("pg-reset-args"), "示例参数").Clicked() {
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
			ui.Text(c, "字段裁剪").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("pg-fields"), &st.fieldsText).
				Font("monospace").
				Placeholder("可选，如 logs.timestamp, logs.message").
				FillWidth()
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			invokeLabel := "执行调用"
			if st.invoking {
				invokeLabel = "执行中…"
			}
			if ui.PrimaryButton(c.Key("pg-invoke-btn"), invokeLabel).Disabled(st.invoking).Clicked() {
				u.runPlaygroundInvoke(st, sel.ID, effectiveActor)
			}
			ui.Textf(c, "身份 · %s", effectiveActor).Font("monospace").FontSize(12).TextColor(SailInkMuted)
		})

		if st.invokeError != "" {
			SailAlertBox(c, ToneDanger, "调用失败", st.invokeError)
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
	simScope := activePlaygroundSimScope(st)

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

		res, err := u.Client.DiscoverScoped(ctx, req, simScope)
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
	simScope := activePlaygroundSimScope(st)

	st.invoking = true
	st.invokeError = ""
	st.invocation = nil

	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		res, err := u.Client.InvokeScoped(ctx, req, simScope)
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
		return "已命中"
	case "needs_context":
		return "需补充信息"
	case "no_match":
		return "无匹配"
	default:
		return status
	}
}
