package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

type playgroundDemoExample struct {
	Short  string
	Intent string
}

// playgroundDemoExamples is resolved on every render so the sample intents
// follow the active UI language instead of being frozen at package init.
func playgroundDemoExamples() []playgroundDemoExample {
	return []playgroundDemoExample{
		{Short: i18n.T("ops.sample-short-errors"), Intent: i18n.T("ops.sample-intent-errors")},
		{Short: i18n.T("ops.sample-short-ownership"), Intent: i18n.T("ops.sample-intent-ownership")},
		{Short: i18n.T("ops.sample-short-deploys"), Intent: i18n.T("ops.sample-intent-deploys")},
	}
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
	includeDemo       bool
	summaryOnly       bool

	routing    bool
	routeError string
	result     *apitypes.DiscoverResult

	selectedTool  *apitypes.Selection
	loadingSchema bool
	inspectorTab  int // 0: Input Schema, 1: 发起试调用
	argsText      string
	fieldsText    string
	invoking      bool
	invokeError   string
	invocation    *apitypes.InvokeResult
}

func (u *DesktopUI) playgroundState() *playgroundState {
	if u.playground == nil {
		u.playground = &playgroundState{
			actor:             "developer",
			serviceScopeLabel: i18n.T("ops.all-enabled-services"),
			simRepoLabel:      i18n.T("ops.admin-all"),
			argsText:          "{}",
		}
	}
	return u.playground
}

func (u *DesktopUI) renderPlayground(c *ui.Context) {
	state := u.State
	st := u.playgroundState()

	// Initialize default actor once State is available.
	if !st.initialized {
		st.initialized = true
		if state.Access.Shared && state.Access.Actor != "" {
			st.actor = state.Access.Actor
		} else {
			st.actor = "developer"
		}
	}

	// Sync handoff when navigating from Overview, Logs, or Repos preview.
	if replay, ok := u.consumePendingReplay(); ok {
		if replay.intent != "" {
			st.intent = replay.intent
		}
		if !state.Access.Shared && replay.actor != "" {
			st.actor = replay.actor
		}
		applyPlaygroundReplayScope(st, replay)
		st.includeDemo = false
		st.selectedServiceID = ""
		st.serviceScopeLabel = i18n.T("ops.all-enabled-services")
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
	serversByID := make(map[string]apitypes.Server, len(state.Registry.Servers))
	scopeOptions := []string{i18n.T("ops.all-enabled-services")}
	scopeLabelToID := map[string]string{i18n.T("ops.all-enabled-services"): ""}
	for _, srv := range state.Registry.Servers {
		serversByID[srv.ID] = srv
		if !srv.Enabled || (srv.Demo && !st.includeDemo) {
			continue
		}
		label := fmt.Sprintf("%s (%s)", srv.Name, srv.ID)
		if srv.Demo {
			label += i18n.T("ops.demo-suffix")
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
	repoOptions := []string{i18n.T("ops.admin-all"), i18n.T("ops.unbound-new-repo")}
	repoLabelToKey := map[string]string{
		i18n.T("ops.admin-all"):        "",
		i18n.T("ops.unbound-new-repo"): "*",
	}
	repoKeyToLabel := map[string]string{
		"":  i18n.T("ops.admin-all"),
		"*": i18n.T("ops.unbound-new-repo"),
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
		st.simRepoLabel = i18n.T("ops.admin-all")
	}

	simScope := activePlaygroundSimScope(st)
	var simGrants []apitypes.ServiceGrant
	if simScope != nil {
		simGrants, _, _ = resolveEffectiveScopeRule(state.Scopes, simScope.RepoKey)
	}

	// Count eligible tools under current actor, demo toggle, service scope, and simulated repo scope.
	eligibleCount := 0
	toolsByID := make(map[string]apitypes.Tool, len(state.Registry.Tools))
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
		modelDisplay = i18n.T("ops.no-model-configured")
	}
	SailSectionHeader(c, i18n.T("ops.playground"), "", func() {
		ui.Text(c, i18n.T("ops.model-with-candidates", modelDisplay, eligibleCount)).
			FontSize(12.5).
			FontFeatures("tnum").
			TextColor(SailInkMuted).
			SingleLine()
		if SailSmallButton(c.Key("pg-settings-btn"), i18n.T("ops.routing-params")).Clicked() {
			u.Navigate(ViewSettings)
		}
	})

	if strings.TrimSpace(state.Settings.Model) == "" {
		SailAlertBox(c, ToneWarning, i18n.T("ops.routing-model-missing"), i18n.T("ops.routing-model-missing-detail"))
	}

	// Two-column Workbench Split.
	ui.Row(c).FillWidth().Gap(24).AlignItems(ui.Start).Children(func() {
		// Left Column: Intent Composer + Discovery Results.
		ui.Column(c).Grow(1).MinWidth(0).Gap(24).Children(func() {
			u.renderPlaygroundComposer(c, st, state, effectiveActor, scopeOptions, scopeLabelToID, repoOptions, repoLabelToKey)
			u.renderPlaygroundResults(c, st, serversByID, toolsByID, effectiveActor)
		})

		// Right Column: Tool Schema & Direct Invoke Inspector.
		ui.Column(c).Grow(1).MinWidth(0).Gap(24).Children(func() {
			u.renderPlaygroundInspector(c, st, toolsByID, effectiveActor)
		})
	})
}

func applyPlaygroundReplayScope(st *playgroundState, replay playgroundReplayIntent) {
	if replay.hasScope {
		st.simRepoKey = replay.repoKey
	} else {
		st.simRepoKey = ""
	}
}

func activePlaygroundSimScope(st *playgroundState) *apitypes.SimScope {
	if st.simRepoKey == "" {
		return nil
	}
	return &apitypes.SimScope{
		RepoKey: st.simRepoKey,
	}
}

func (u *DesktopUI) renderPlaygroundComposer(
	c *ui.Context,
	st *playgroundState,
	state *apitypes.Bootstrap,
	effectiveActor string,
	scopeOptions []string,
	scopeLabelToID map[string]string,
	repoOptions []string,
	repoLabelToKey map[string]string,
) {
	SailCard(c, func() {
		SailCardTitle(c, i18n.T("ops.task-intent"), "", func() {
			ui.Text(c, "⌘+Enter").FontSize(11.5).Font("monospace").TextColor(SailInkFaint).SingleLine()
		})

		ui.TextArea(c.Key("pg-intent"), &st.intent).
			Placeholder(i18n.T("ops.task-intent-placeholder")).
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
				ui.Text(c, i18n.T("ops.extra-context")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
				ui.TextArea(c.Key("pg-context"), &st.context).
					Placeholder(i18n.T("ops.extra-context-placeholder")).
					Lines(2, 4).
					FillWidth()
			})
		}

		// Context toggle + quick demo examples.
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Wrap().Children(func() {
			ctxLabel := i18n.T("ops.context-open")
			if st.showContext {
				ctxLabel = i18n.T("ops.context-close")
			}
			if SailSmallButton(c.Key("pg-toggle-context"), ctxLabel).Clicked() {
				st.showContext = !st.showContext
			}

			ui.Row(c).Gap(6).AlignItems(ui.Center).Wrap().Children(func() {
				for i, ex := range playgroundDemoExamples() {
					if SailSmallButton(c.Key(fmt.Sprintf("pg-demo-%d", i)), ex.Short).Clicked() {
						st.intent = ex.Intent
						st.includeDemo = true
						st.selectedServiceID = ""
						st.serviceScopeLabel = i18n.T("ops.all-enabled-services")
						st.simRepoKey = ""
						st.simRepoLabel = i18n.T("ops.admin-all")
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
				ui.Text(c, i18n.T("ops.actor")).FontSize(12).TextColor(SailInkMuted).SingleLine()
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
				ui.Text(c, i18n.T("ops.services")).FontSize(12).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-scope"), &st.serviceScopeLabel, scopeOptions).MinWidth(164).Changed() {
					st.selectedServiceID = scopeLabelToID[st.serviceScopeLabel]
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, i18n.T("ops.repo-label")).FontSize(12).TextColor(SailInkMuted).SingleLine()
				if ui.Select(c.Key("pg-sim-repo"), &st.simRepoLabel, repoOptions).MinWidth(156).Changed() {
					st.simRepoKey = repoLabelToKey[st.simRepoLabel]
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})
		})

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Wrap().Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
				if ui.Checkbox(c.Key("pg-include-demo"), &st.includeDemo, i18n.T("ops.demo-tools")).Changed() {
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}

				if ui.Checkbox(c.Key("pg-summary-only"), &st.summaryOnly, i18n.T("ops.summary-only")).Changed() {
					st.result = nil
					st.selectedTool = nil
					st.invocation = nil
				}
			})

			btnLabel := i18n.T("ops.start-routing")
			if st.routing {
				btnLabel = i18n.T("ops.routing-in-progress")
			}
			if SailPrimaryButton(c.Key("pg-discover-btn"), btnLabel).
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
	serversByID map[string]apitypes.Server,
	toolsByID map[string]apitypes.Tool,
	effectiveActor string,
) {
	SailFlushCard(c, func() {
		var headerNote string
		if res := st.result; res != nil {
			headerNote = i18n.T("ops.candidates-with-latency", res.Eligible, res.DurationMS)
			if res.PromptTokens+res.OutputTokens > 0 {
				headerNote += fmt.Sprintf(" · %d tok", res.PromptTokens+res.OutputTokens)
			}
		}

		SailListCardHeader(c, i18n.T("ops.routing-result"), headerNote, func() {
			if res := st.result; res != nil {
				switch res.Status {
				case "selected":
					SailQuietStatus(c, i18n.T("ops.routing-matched"), SailSuccessInk, SailInkStrong)
				case "needs_context":
					SailBadge(c, i18n.T("ops.routing-needs-more"), ToneWarning)
				case "error", "failed":
					SailBadge(c, discoveryStatusLabel(res.Status), ToneDanger)
				default:
					SailQuietStatus(c, discoveryStatusLabel(res.Status), SailInkFaint, SailInkMuted)
				}
			}
		})

		if st.routeError != "" {
			ui.Column(c).FillWidth().Padding(16, 20).Children(func() {
				SailAlertBox(c, ToneDanger, i18n.T("ops.routing-failed"), st.routeError)
			})
		}

		if st.routing {
			ui.Column(c).FillWidth().Padding(32, 20).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c).Label(i18n.T("ops.requesting-routing-model"))
				ui.Text(c, i18n.T("ops.evaluating-candidates")).FontSize(13).TextColor(SailInkMuted)
			})
			return
		}

		res := st.result
		if res == nil {
			if st.routeError == "" {
				ui.Column(c).FillWidth().Padding(20).Children(func() {
					SailEmptyState(c, i18n.T("ops.no-routing-yet"), "", "", nil)
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
						ui.Text(c, i18n.T("ops.suggested-followup", res.Question)).FontSize(12.5).TextColor(SailWarningInk)
					}
				})
		}

		if len(res.Tools) == 0 {
			ui.Column(c).FillWidth().Padding(20).Children(func() {
				SailEmptyState(c, i18n.T("ops.no-tool-matched"), "", "", nil)
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
					SailBadge(c, i18n.T("ops.write-access"), ToneWarning).Shrink(0)
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
	toolsByID map[string]apitypes.Tool,
	effectiveActor string,
) {
	SailCard(c, func() {
		SailCardTitle(c, i18n.T("ops.tool-inspector"), "", func() {
			if st.selectedTool != nil {
				if st.selectedTool.ReadOnly {
					ui.Text(c, i18n.T("ops.read-only")).FontSize(12).TextColor(SailInkMuted).SingleLine()
				} else {
					SailBadge(c, i18n.T("ops.write-access"), ToneWarning)
				}
			}
		})

		sel := st.selectedTool
		if sel == nil {
			SailEmptyState(c, i18n.T("ops.no-tool-selected"), "", "", nil)
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

		ui.Segmented(c.Key("pg-inspector-tab"), &st.inspectorTab, "Input Schema", i18n.T("ops.start-trial-call")).Label(i18n.T("ops.inspector-mode"))

		if st.inspectorTab == 0 {
			if sel.InputSchema == nil {
				SailSubtleCard(c, func() {
					ui.Text(c, i18n.T("ops.summary-mode-no-schema")).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong)
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						btnText := i18n.T("ops.fetch-schema")
						if st.loadingSchema {
							btnText = i18n.T("ops.loading")
						}
						if SailSmallPrimaryButton(c.Key("pg-fetch-schema"), btnText).Disabled(st.loadingSchema).Clicked() {
							u.fetchPlaygroundToolSchema(st, sel.ID, effectiveActor)
						}
						if full, ok := toolsByID[sel.ID]; ok && full.InputSchema != nil {
							if SailSmallButton(c.Key("pg-use-local-schema"), i18n.T("ops.use-local-cache")).Clicked() {
								sel.Tool = full
								st.argsText = initialArgsForTool(full)
							}
						}
					})
				})
			} else {
				SailCodeBlock(c, "INPUT SCHEMA", PrettyJSON(sel.InputSchema), func() {
					u.Notify(i18n.T("ops.copied-schema"))
				})
			}

			if SailPrimaryButton(c.Key("pg-goto-invoke"), i18n.T("ops.try-this-tool")).Clicked() {
				st.inspectorTab = 1
			}
			return
		}

		// Tab 1: Direct Invoke Workbench.
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				ui.Text(c, i18n.T("ops.arguments-json")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
				if SailSmallButton(c.Key("pg-reset-args"), i18n.T("ops.example-args")).Clicked() {
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
			ui.Text(c, i18n.T("ops.field-trimming")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
			ui.TextInput(c.Key("pg-fields"), &st.fieldsText).
				Font("monospace").
				Placeholder(i18n.T("ops.field-trimming-placeholder")).
				FillWidth()
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			invokeLabel := i18n.T("ops.invoke")
			if st.invoking {
				invokeLabel = i18n.T("ops.invoking")
			}
			if SailPrimaryButton(c.Key("pg-invoke-btn"), invokeLabel).Disabled(st.invoking).Clicked() {
				u.runPlaygroundInvoke(st, sel.ID, effectiveActor)
			}
			ui.Text(c, i18n.T("ops.actor-with-value", effectiveActor)).Font("monospace").FontSize(12).TextColor(SailInkMuted)
		})

		if st.invokeError != "" {
			SailAlertBox(c, ToneDanger, i18n.T("ops.invoke-failed"), st.invokeError)
		}

		if inv := st.invocation; inv != nil {
			title := fmt.Sprintf("RESPONSE · %s · %d ms", inv.Protocol, inv.DurationMS)
			SailCodeBlock(c, title, PrettyJSON(inv.Data), func() {
				u.Notify(i18n.T("ops.copied-invoke-result"))
			})
		}
	})
}

func (u *DesktopUI) selectPlaygroundTool(
	st *playgroundState,
	sel apitypes.Selection,
	toolsByID map[string]apitypes.Tool,
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

func initialArgsForTool(t apitypes.Tool) string {
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

	req := apitypes.DiscoverRequest{
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
			if apiclient.IsUnauthorized(err) {
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
				var toolsByID map[string]apitypes.Tool
				if u.State != nil {
					toolsByID = make(map[string]apitypes.Tool, len(u.State.Registry.Tools))
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
		st.invokeError = i18n.T("ops.invalid-json-arguments", err.Error())
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

	req := apitypes.InvokeRequest{
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
			if apiclient.IsUnauthorized(err) {
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
		return i18n.T("ops.routing-matched")
	case "needs_context":
		return i18n.T("ops.routing-needs-more")
	case "no_match":
		return i18n.T("ops.no-match")
	default:
		return status
	}
}
