package main

import (
	"context"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo/ui"
)

type hookSnippetModalState struct {
	show         bool
	agentIdx     int
	loadingAgent string
	errMsg       string
	snippets     map[string]apitypes.ScopeSnippetResponse
}

func (u *DesktopUI) hookModalState() *hookSnippetModalState {
	if u.hookModal == nil {
		u.hookModal = &hookSnippetModalState{
			snippets: make(map[string]apitypes.ScopeSnippetResponse),
		}
	}
	return u.hookModal
}

// OpenHookSnippetModal opens the manual Hook configuration modal pre-selected to agentID.
func (u *DesktopUI) OpenHookSnippetModal(agentID string) {
	st := u.hookModalState()
	if agentID == "" {
		agentID = "claude"
	}
	st.agentIdx = agentIndex(agentID)
	st.errMsg = ""
	st.show = true
	u.ensureHookSnippetLoaded(st, scopeAgents[st.agentIdx].ID, false)
}

func (u *DesktopUI) ensureHookSnippetLoaded(st *hookSnippetModalState, agentID string, force bool) {
	if u.Client == nil || agentID == "" {
		return
	}
	if !force {
		if _, cached := st.snippets[agentID]; cached {
			return
		}
	}
	if st.loadingAgent == agentID {
		return
	}
	st.loadingAgent = agentID
	st.errMsg = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		snip, err := u.Client.GetHookSnippet(ctx, agentID)
		if err != nil {
			u.updateUI(func() {
				if st.loadingAgent == agentID {
					st.loadingAgent = ""
				}
				if !apiclient.IsUnauthorized(err) {
					st.errMsg = err.Error()
				}
			})
			if apiclient.IsUnauthorized(err) {
				return err
			}
			return nil
		}
		u.updateUI(func() {
			if st.loadingAgent == agentID {
				st.loadingAgent = ""
			}
			if snip.Agent == "" {
				snip.Agent = agentID
			}
			st.snippets[agentID] = snip
		})
		return nil
	})
}

func (u *DesktopUI) renderHookSnippetModal(c *ui.Context) {
	st := u.hookModalState()
	if !st.show {
		return
	}

	ui.DialogBase(c.Key("hook-snippet-dialog"), &st.show, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42))
		panel.Width(700).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "手动配置 Hook").FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("hook-modal-close"), "关闭").Clicked() {
				st.show = false
			}
		})

		if st.agentIdx < 0 || st.agentIdx >= len(scopeAgents) {
			st.agentIdx = 0
		}
		if ui.Segmented(
			c.Key("hook-agent-tabs"),
			&st.agentIdx,
			scopeAgentNames...,
		).FillEqually().Changed() {
			u.ensureHookSnippetLoaded(st, scopeAgents[st.agentIdx].ID, false)
		}

		activeAgent := scopeAgents[st.agentIdx]
		snip, hasSnip := st.snippets[activeAgent.ID]
		hookBin := snip.HookBinPath
		if hookBin == "" && u.State != nil {
			hookBin = u.State.Scopes.HookBinPath
		}

		if st.errMsg != "" {
			SailAlertBox(c, ToneDanger, "获取 Hook 配置失败", st.errMsg)
		}

		ui.Scroll(c.Key("hook-modal-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			if st.loadingAgent == activeAgent.ID && !hasSnip {
				ui.Row(c).FillWidth().Padding(24).Gap(8).Center().Children(func() {
					ui.Spinner(c).Label("加载配置片段")
					ui.Text(c, "正在生成 Hook 配置…").FontSize(13).TextColor(SailInkMuted)
				})
				return
			}

			SailSubtleCard(c, func() {
				if snip.ConfigPath != "" {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Children(func() {
							SailKeyValueRow(c, "配置文件", snip.ConfigPath, true)
						})
						if u.State == nil || !u.State.Access.Shared {
							if SailSmallButton(c.Key("hook-reveal-cfg"), "定位 ↗").Clicked() {
								cfgPath := snip.ConfigPath
								u.RunAsync(func() error { return revealClientConfig(cfgPath) })
							}
						}
					})
				}
				if hookBin != "" {
					SailKeyValueRow(c, "Hook 程序", hookBin, true)
				}
				if snip.MCPURL != "" {
					SailKeyValueRow(c, "MCP 端点", snip.MCPURL, true)
				}
			})

			if strings.TrimSpace(snip.Instructions) != "" {
				ui.Text(c, strings.TrimSpace(snip.Instructions)).
					FontSize(12.5).
					LineHeight(1.5).
					TextColor(SailInkBody).
					Selectable()
			}

			code := snip.Snippet
			if code == "" && !hasSnip {
				code = "// 暂无可展示的 Hook 配置片段"
			}
			SailCodeBlock(c, strings.ToUpper(activeAgent.ID)+" HOOK", code, func() {
				u.Notify("已复制 " + activeAgent.Name + " Hook 配置")
			})
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
			if SailSmallButton(c.Key("hook-refresh-btn"), "重新获取").Disabled(st.loadingAgent != "").Clicked() {
				u.ensureHookSnippetLoaded(st, activeAgent.ID, true)
			}
			if SailPrimaryButton(c.Key("hook-copy-primary"), "复制配置片段").
				Disabled(strings.TrimSpace(snip.Snippet) == "").
				Clicked() {
				c.WriteClipboard(snip.Snippet)
				u.Notify("已复制 " + activeAgent.Name + " Hook 配置")
			}
		})
	})
}
