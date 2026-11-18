package main

import (
	"context"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

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
		backdrop.Background(SailModalBackdrop)
		panel.Width(700).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgSurface).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, SailModalShadow)

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("ops.manual-hook-config")).FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("hook-modal-close"), i18n.T("ops.close")).Clicked() {
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
			SailAlertBox(c, ToneDanger, i18n.T("ops.hook-config-failed"), st.errMsg)
		}

		u.renderHookModalBody(c, st, activeAgent, snip, hasSnip, hookBin)
		u.renderHookModalFooter(c, st, activeAgent, snip)
	})
}

func (u *DesktopUI) renderHookModalBody(
	c *ui.Context,
	st *hookSnippetModalState,
	activeAgent scopeAgentMeta,
	snip apitypes.ScopeSnippetResponse,
	hasSnip bool,
	hookBin string,
) {
	ui.Scroll(c.Key("hook-modal-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
		if st.loadingAgent == activeAgent.ID && !hasSnip {
			ui.Row(c).FillWidth().Padding(24).Gap(8).Center().Children(func() {
				ui.Spinner(c).Label(i18n.T("ops.loading-snippet"))
				ui.Text(c, i18n.T("ops.generating-hook-config")).FontSize(13).TextColor(SailInkMuted)
			})
			return
		}

		u.renderHookMetaCard(c, snip, hookBin)

		if instr := strings.TrimSpace(snip.Instructions); instr != "" {
			ui.Text(c, instr).FontSize(12.5).LineHeight(1.5).TextColor(SailInkBody).Selectable()
		}

		code := snip.Snippet
		if code == "" && !hasSnip {
			code = i18n.T("ops.no-hook-snippet")
		}
		SailCodeBlock(c, strings.ToUpper(activeAgent.ID)+" HOOK", code, func() {
			u.Notify(i18n.T("ops.copied-hook-config-for", activeAgent.Name))
		})
	})
}

func (u *DesktopUI) renderHookMetaCard(c *ui.Context, snip apitypes.ScopeSnippetResponse, hookBin string) {
	SailSubtleCard(c, func() {
		if snip.ConfigPath != "" {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					SailKeyValueRow(c, i18n.T("ops.config-file"), snip.ConfigPath, true)
				})
				if u.State == nil || !u.State.Access.Shared {
					if SailSmallButton(c.Key("hook-reveal-cfg"), i18n.T("ops.reveal")).Clicked() {
						cfgPath := snip.ConfigPath
						u.RunAsync(func() error { return revealClientConfig(cfgPath) })
					}
				}
			})
		}
		if hookBin != "" {
			SailKeyValueRow(c, i18n.T("ops.hook-program"), hookBin, true)
		}
		if snip.MCPURL != "" {
			SailKeyValueRow(c, i18n.T("ops.mcp-endpoint"), snip.MCPURL, true)
		}
	})
}

func (u *DesktopUI) renderHookModalFooter(
	c *ui.Context,
	st *hookSnippetModalState,
	activeAgent scopeAgentMeta,
	snip apitypes.ScopeSnippetResponse,
) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.End).Gap(8).Children(func() {
		if SailButton(c.Key("hook-refresh-btn"), i18n.T("ops.refetch")).Disabled(st.loadingAgent != "").Clicked() {
			u.ensureHookSnippetLoaded(st, activeAgent.ID, true)
		}
		if SailPrimaryButton(c.Key("hook-copy-primary"), i18n.T("ops.copy-snippet")).
			Disabled(strings.TrimSpace(snip.Snippet) == "").
			Clicked() {
			c.WriteClipboard(snip.Snippet)
			u.Notify(i18n.T("ops.copied-hook-config-for", activeAgent.Name))
		}
	})
}
