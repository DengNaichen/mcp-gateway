package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// pgPolicyState backs the sim_policy panel: pick a tool, repository and
// simulated identity, then show the backend decision and its trace.
type pgPolicyState struct {
	toolID    string
	repoLabel string
	actor     string

	evaluating bool
	err        string
	result     *apitypes.PolicyEvaluateResult
}

func (st *playgroundState) policyState() *pgPolicyState {
	if st.policy == nil {
		st.policy = &pgPolicyState{}
	}
	return st.policy
}

// pgPolicyToolOptions lists enabled tools of real services; the selected
// tool falls back to the first option when it disappears.
func pgPolicyToolOptions(state *apitypes.Bootstrap, ps *pgPolicyState) []string {
	enabledServers := make(map[string]bool, len(state.Registry.Servers))
	for _, s := range state.Registry.Servers {
		if s.Enabled && !s.Demo {
			enabledServers[s.ID] = true
		}
	}
	var out []string
	for _, t := range state.Registry.Tools {
		if t.Enabled && enabledServers[t.ServerID] {
			out = append(out, t.ID)
		}
	}
	if len(out) > 0 && !slices.Contains(out, ps.toolID) {
		ps.toolID = out[0]
	}
	return out
}

// pgPolicyRequest builds the evaluate request. An empty actor in shared mode
// asks the backend to use the authenticated principal.
func pgPolicyRequest(ps *pgPolicyState, repoLabelToKey map[string]string) apitypes.PolicyEvaluateRequest {
	return apitypes.PolicyEvaluateRequest{
		ToolID: ps.toolID,
		RepoID: repoLabelToKey[ps.repoLabel],
		Actor:  strings.TrimSpace(ps.actor),
	}
}

func pgPolicyStageLabel(stage string) string {
	switch stage {
	case "internal", "identity", "deny", "baseline", "approve", "allow":
		return i18n.T("policy.stage-" + stage)
	default:
		return stage
	}
}

// pgPolicyVisibleTrace hides the unmatched internal-bypass probe so the
// trace reads as the four documented stages plus the terminal verdict.
func pgPolicyVisibleTrace(trace []apitypes.PolicyTraceStep) []apitypes.PolicyTraceStep {
	out := make([]apitypes.PolicyTraceStep, 0, len(trace))
	for _, step := range trace {
		if step.Stage == "internal" && !step.Matched {
			continue
		}
		out = append(out, step)
	}
	return out
}

func (u *DesktopUI) renderPlaygroundPolicyPanel(c *ui.Context, st *playgroundState, state *apitypes.Bootstrap, vm playgroundViewModel) {
	ps := st.policyState()
	tools := pgPolicyToolOptions(state, ps)
	if _, ok := vm.repoLabelToKey[ps.repoLabel]; !ok && len(vm.repoOptions) > 0 {
		ps.repoLabel = vm.repoOptions[0]
	}
	local := policyIsLocalGuardrail(state)

	SailCard(c.Key("pg-policy-card"), func() {
		SailCardTitle(c, i18n.T("policy.sim-title"), i18n.T("policy.sim-subtitle"), nil)

		ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Wrap().Children(func() {
			policyFormField(c, i18n.T("policy.sim-tool"), func() {
				if len(tools) == 0 {
					ui.Text(c, i18n.T("policy.sim-no-tools")).FontSize(12.5).TextColor(SailInkFaint).SingleLine()
					return
				}
				if SailSelect(c.Key("pg-policy-tool"), &ps.toolID, tools).MinWidth(200).Changed() {
					ps.result = nil
				}
			})
			policyFormField(c, i18n.T("ops.repo-label"), func() {
				if SailSelect(c.Key("pg-policy-repo"), &ps.repoLabel, vm.repoOptions).MinWidth(156).Changed() {
					ps.result = nil
				}
			})
			policyFormField(c, i18n.T("policy.sim-actor"), func() {
				placeholder := i18n.T("policy.sim-actor-shared-placeholder")
				if local {
					placeholder = i18n.T("policy.sim-actor-local-placeholder")
				}
				if SailTextInput(c.Key("pg-policy-actor"), &ps.actor).
					Font("monospace").
					Placeholder(placeholder).
					Label(i18n.T("policy.sim-actor")).
					Width(170).
					Changed() {
					ps.result = nil
				}
			})
		})

		ui.Row(c).FillWidth().Justify(ui.End).Children(func() {
			lbl := i18n.T("policy.sim-run")
			if ps.evaluating {
				lbl = i18n.T("policy.sim-running")
			}
			if SailPrimaryButton(c.Key("pg-policy-run"), lbl).
				Disabled(ps.evaluating || ps.toolID == "" || u.Client == nil).
				Clicked() {
				u.runPlaygroundPolicyEval(ps, pgPolicyRequest(ps, vm.repoLabelToKey))
			}
		})

		if ps.err != "" {
			SailAlertBox(c, ToneDanger, i18n.T("policy.sim-failed"), ps.err)
		}
		if ps.result != nil {
			renderPlaygroundPolicyResult(c, *ps.result)
		}
	})
}

func renderPlaygroundPolicyResult(c *ui.Context, res apitypes.PolicyEvaluateResult) {
	SailSubtleCard(c, func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).MinWidth(0).Children(func() {
				ui.Text(c, i18n.T("policy.sim-verdict")).FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
				policyEffectBadge(c, res.Effect)
				if res.RuleID != "" {
					ui.Text(c, res.RuleID).Font("monospace").FontSize(12).TextColor(SailInkBody).Selectable().SingleLine()
				}
			})
			if res.Trust != "" {
				SailBadge(c, "trust: "+res.Trust, ToneNeutral)
			}
		})
		if res.Reason != "" {
			ui.Text(c, res.Reason).FontSize(12.5).LineHeight(1.45).TextColor(SailInkBody).Selectable()
		}
		if res.Trust == "declared" {
			ui.Text(c, i18n.T("policy.trust-declared-hint")).FontSize(11.5).TextColor(SailInkFaint)
		}

		trace := pgPolicyVisibleTrace(res.Trace)
		if len(trace) == 0 {
			return
		}
		ui.Column(c).FillWidth().Children(func() {
			for i, step := range trace {
				row := ui.Row(c.Key(fmt.Sprintf("pg-policy-step-%d", i))).FillWidth().Padding(8, 0).Gap(10).AlignItems(ui.Center)
				if i < len(trace)-1 {
					row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
				}
				row.Children(func() {
					dot := SailBorderStrong
					if step.Matched {
						dot = policyEffectInk(step.Effect)
					}
					SailStatusDot(c, dot)
					ui.Text(c, pgPolicyStageLabel(step.Stage)).Width(120).Shrink(0).FontSize(12.5).FontWeight(500).TextColor(SailInkStrong).SingleLine()
					detail := i18n.T("policy.step-pass")
					if step.Matched {
						detail = step.Effect
						if step.RuleID != "" {
							detail += " · " + step.RuleID
						}
					}
					if step.Reason != "" {
						detail += " · " + step.Reason
					}
					ui.Text(c, detail).Grow(1).MinWidth(0).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine().Tooltip(detail)
				})
			}
		})
	})
}

func policyEffectInk(effect string) ui.Color {
	switch effect {
	case "deny":
		return SailDangerInk
	case "approve":
		return SailWarningInk
	default:
		return SailSuccessInk
	}
}

func (u *DesktopUI) runPlaygroundPolicyEval(ps *pgPolicyState, req apitypes.PolicyEvaluateRequest) {
	if ps.evaluating || u.Client == nil || req.ToolID == "" {
		return
	}
	ps.evaluating = true
	ps.err = ""
	ps.result = nil
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		res, err := u.Client.EvaluatePolicy(ctx, req)
		if apiclient.IsUnauthorized(err) {
			u.updateUI(func() { ps.evaluating = false })
			return err
		}
		u.updateUI(func() {
			ps.evaluating = false
			if err != nil {
				ps.err = err.Error()
				return
			}
			ps.result = &res
		})
		return nil
	})
}
