package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// --- Trust mode helpers shared by Repos / Playground / Settings ---

// policySecurityMode resolves the effective trust mode, falling back to the
// access shape when an older backend omits security_mode.
func policySecurityMode(state *apitypes.Bootstrap) string {
	if state == nil {
		return apitypes.SecurityModeLocalGuardrail
	}
	switch state.Access.SecurityMode {
	case apitypes.SecurityModeLocalGuardrail, apitypes.SecurityModeSharedBoundary:
		return state.Access.SecurityMode
	}
	if state.Access.Shared {
		return apitypes.SecurityModeSharedBoundary
	}
	return apitypes.SecurityModeLocalGuardrail
}

func policyIsLocalGuardrail(state *apitypes.Bootstrap) bool {
	return policySecurityMode(state) == apitypes.SecurityModeLocalGuardrail
}

func policySecurityModeLabel(mode string) string {
	if mode == apitypes.SecurityModeSharedBoundary {
		return i18n.T("policy.mode-shared")
	}
	return i18n.T("policy.mode-local")
}

// policyExpectedTrust is the trust level identities carry in the given mode.
func policyExpectedTrust(mode string) string {
	if mode == apitypes.SecurityModeSharedBoundary {
		return "authenticated"
	}
	return "declared"
}

// policyEffectBadge renders a decision with the noise-reduced palette:
// allow stays a quiet dot, approve is warm, deny is red.
func policyEffectBadge(c *ui.Context, effect string) {
	switch effect {
	case "deny":
		SailBadge(c, "deny", ToneDanger)
	case "approve":
		SailBadge(c, "approve", ToneWarning)
	case "allow":
		SailQuietStatus(c, "allow", SailSuccessInk, SailInkStrong)
	case "":
	default:
		SailBadge(c, effect, ToneNeutral)
	}
}

// --- Repos: extra policy rules (Deny / Approve) ---

var (
	policyEffectValues = []string{"deny", "approve"}
	policyAccessValues = []string{"*", "ro", "rw"}
)

type policyRuleDraft struct {
	effectIdx    int // index into policyEffectValues
	accessIdx    int // index into policyAccessValues
	repoLabel    string
	serviceLabel string
	toolIDs      string
	actor        string
	reason       string
}

type policyRulesState struct {
	adding  bool
	saving  bool
	draft   policyRuleDraft
	formErr string
	busy    map[string]bool // rule ID -> mutation in flight

	confirmDeleteID   string
	showDeleteConfirm bool
}

func (st *reposPageState) policyState() *policyRulesState {
	if st.policy == nil {
		st.policy = &policyRulesState{busy: make(map[string]bool)}
	}
	return st.policy
}

// sortPolicyRulesForDisplay pins global repo_key="*" rules first while keeping
// the server order (which is also the evaluation order) inside each group.
func sortPolicyRulesForDisplay(rules []apitypes.PolicyRule) []apitypes.PolicyRule {
	out := slices.Clone(rules)
	slices.SortStableFunc(out, func(a, b apitypes.PolicyRule) int {
		ag, bg := a.RepoKey == "*", b.RepoKey == "*"
		switch {
		case ag && !bg:
			return -1
		case !ag && bg:
			return 1
		default:
			return 0
		}
	})
	return out
}

type policyRuleOptions struct {
	repoOptions    []string
	repoLabelToKey map[string]string
	repoKeyToName  map[string]string
	serviceOptions []string
	serviceToID    map[string]string
	serviceToName  map[string]string
}

func buildPolicyRuleOptions(state *apitypes.Bootstrap) policyRuleOptions {
	allRepos := i18n.T("policy.any-repo")
	allServices := i18n.T("policy.any-service")
	o := policyRuleOptions{
		repoOptions:    []string{allRepos},
		repoLabelToKey: map[string]string{allRepos: "*"},
		repoKeyToName:  map[string]string{"*": "*"},
		serviceOptions: []string{allServices},
		serviceToID:    map[string]string{allServices: "*"},
		serviceToName:  map[string]string{"*": "*"},
	}
	for _, repo := range state.Scopes.Repositories {
		lbl := repo.Name
		if lbl == "" {
			lbl = repo.ID
		}
		if _, dup := o.repoLabelToKey[lbl]; dup {
			lbl = fmt.Sprintf("%s (%s)", lbl, repo.ID)
		}
		o.repoOptions = append(o.repoOptions, lbl)
		o.repoLabelToKey[lbl] = repo.ID
		o.repoKeyToName[repo.ID] = lbl
	}
	for _, srv := range state.Registry.Servers {
		if srv.Demo {
			continue
		}
		lbl := srv.Name
		if lbl == "" {
			lbl = srv.ID
		}
		if _, dup := o.serviceToID[lbl]; dup {
			lbl = fmt.Sprintf("%s (%s)", lbl, srv.ID)
		}
		o.serviceOptions = append(o.serviceOptions, lbl)
		o.serviceToID[lbl] = srv.ID
		o.serviceToName[srv.ID] = lbl
	}
	return o
}

func splitPolicyToolIDs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == ' ' || r == '\n' || r == '\t'
	})
	var out []string
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" && !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// buildPolicyRuleFromDraft converts the inline form into a new enabled rule.
// Actor is dropped in local guardrail mode where it would never be matched.
func buildPolicyRuleFromDraft(d policyRuleDraft, o policyRuleOptions, localGuardrail bool) (apitypes.PolicyRule, error) {
	if d.effectIdx < 0 || d.effectIdx >= len(policyEffectValues) || d.accessIdx < 0 || d.accessIdx >= len(policyAccessValues) {
		return apitypes.PolicyRule{}, errors.New(i18n.T("policy.err-invalid-form"))
	}
	repoKey, ok := o.repoLabelToKey[d.repoLabel]
	if !ok {
		repoKey = "*"
	}
	serviceID, ok := o.serviceToID[d.serviceLabel]
	if !ok {
		serviceID = "*"
	}
	rule := apitypes.PolicyRule{
		Effect:    policyEffectValues[d.effectIdx],
		Enabled:   true,
		RepoKey:   repoKey,
		ServiceID: serviceID,
		ToolIDs:   splitPolicyToolIDs(d.toolIDs),
		Access:    policyAccessValues[d.accessIdx],
		Reason:    strings.TrimSpace(d.reason),
	}
	if !localGuardrail {
		rule.Actor = strings.TrimSpace(d.actor)
	}
	return rule, nil
}

func (u *DesktopUI) renderPolicyRulesSection(c *ui.Context, st *reposPageState, state *apitypes.Bootstrap) {
	ps := st.policyState()
	opts := buildPolicyRuleOptions(state)
	local := policyIsLocalGuardrail(state)
	rules := sortPolicyRulesForDisplay(state.Policies.Rules)

	if ps.showDeleteConfirm && ps.confirmDeleteID != "" {
		targetID := ps.confirmDeleteID
		if ui.AlertDialog(
			c,
			&ps.showDeleteConfirm,
			i18n.T("policy.delete-confirm-title"),
			i18n.T("policy.delete-confirm-body", targetID),
			i18n.T("ops.cancel"),
			i18n.T("policy.delete"),
		) == 1 {
			u.deletePolicyRuleAsync(ps, targetID)
		}
	}

	SailFlushCard(c.Key("policy-rules-card"), func() {
		SailListCardHeader(c, i18n.T("policy.rules-title"), i18n.T("policy.rules-note"), func() {
			if !ps.adding {
				if SailSmallButton(c.Key("policy-add-btn"), i18n.T("policy.add-rule")).Disabled(u.Client == nil).Clicked() {
					ps.adding = true
					ps.formErr = ""
					ps.draft = policyRuleDraft{repoLabel: opts.repoOptions[0], serviceLabel: opts.serviceOptions[0]}
				}
			}
		})

		if ps.adding {
			u.renderPolicyRuleForm(c, ps, opts, local)
		}

		if len(rules) == 0 && !ps.adding {
			ui.Row(c).FillWidth().Padding(16, 20).Children(func() {
				ui.Text(c, i18n.T("policy.no-rules")).FontSize(12.5).TextColor(SailInkMuted)
			})
			return
		}
		for i, rule := range rules {
			u.renderPolicyRuleRow(c, ps, rule, opts, local, i == len(rules)-1)
		}
	})
}

func (u *DesktopUI) renderPolicyRuleForm(c *ui.Context, ps *policyRulesState, opts policyRuleOptions, local bool) {
	ui.Column(c.Key("policy-form")).FillWidth().Padding(14, 20).Gap(12).Background(SailBgSubtle).
		BorderWidth(0, 0, 1, 0).BorderColor(SailDivider).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
			SailSmallSegmented(c.Key("policy-form-effect"), &ps.draft.effectIdx, "Deny", "Approve").Label(i18n.T("policy.effect"))
			policyFormField(c, i18n.T("ops.repo-label"), func() {
				SailSelect(c.Key("policy-form-repo"), &ps.draft.repoLabel, opts.repoOptions).MinWidth(150)
			})
			policyFormField(c, i18n.T("ops.services"), func() {
				SailSelect(c.Key("policy-form-service"), &ps.draft.serviceLabel, opts.serviceOptions).MinWidth(150)
			})
			policyFormField(c, i18n.T("ops.access-mode"), func() {
				SailSmallSegmented(c.Key("policy-form-access"), &ps.draft.accessIdx, "*", "ro", "rw").Label(i18n.T("ops.access-mode"))
			})
		})
		ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
			SailTextInput(c.Key("policy-form-tools"), &ps.draft.toolIDs).
				Font("monospace").
				Placeholder(i18n.T("policy.tool-ids-placeholder")).
				Label(i18n.T("policy.tool-ids")).
				Width(260)
			actorIn := SailTextInput(c.Key("policy-form-actor"), &ps.draft.actor).
				Font("monospace").
				Label(i18n.T("ops.actor")).
				Width(170)
			if local {
				actorIn.Disabled(true).Placeholder(i18n.T("policy.actor-local-placeholder"))
			} else {
				actorIn.Placeholder(i18n.T("policy.actor-placeholder"))
			}
			SailTextInput(c.Key("policy-form-reason"), &ps.draft.reason).
				Placeholder(i18n.T("policy.reason-placeholder")).
				Label(i18n.T("policy.reason")).
				Grow(1).
				MinWidth(180)
		})
		if local {
			ui.Text(c, i18n.T("policy.actor-local-hint")).FontSize(11.5).TextColor(SailInkFaint)
		}
		if ps.formErr != "" {
			SailAlertBox(c, ToneDanger, i18n.T("policy.save-failed"), ps.formErr)
		}
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			if SailSmallButton(c.Key("policy-form-cancel"), i18n.T("ops.cancel")).Disabled(ps.saving).Clicked() {
				ps.adding = false
				ps.formErr = ""
			}
			lbl := i18n.T("policy.save-rule")
			if ps.saving {
				lbl = i18n.T("ops.saving")
			}
			if SailSmallPrimaryButton(c.Key("policy-form-save"), lbl).Disabled(ps.saving || u.Client == nil).Clicked() {
				rule, err := buildPolicyRuleFromDraft(ps.draft, opts, local)
				if err != nil {
					ps.formErr = err.Error()
				} else {
					u.createPolicyRuleAsync(ps, rule)
				}
			}
		})
	})
}

func policyFormField(c *ui.Context, label string, fn func()) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12).TextColor(SailInkMuted).SingleLine()
		fn()
	})
}

func policyRuleTargetText(rule apitypes.PolicyRule, o policyRuleOptions) string {
	repo := o.repoKeyToName[rule.RepoKey]
	if repo == "" {
		repo = rule.RepoKey
	}
	svc := o.serviceToName[rule.ServiceID]
	if svc == "" {
		svc = rule.ServiceID
	}
	return fmt.Sprintf("%s · %s · %s", repo, svc, rule.Access)
}

func (u *DesktopUI) renderPolicyRuleRow(c *ui.Context, ps *policyRulesState, rule apitypes.PolicyRule, o policyRuleOptions, local, isLast bool) {
	row := ui.Row(c.Key("policy-rule-"+rule.ID)).FillWidth().Padding(12, 20).Gap(14).AlignItems(ui.Center)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	busy := ps.busy[rule.ID]
	row.Children(func() {
		ui.Row(c).Width(72).Shrink(0).Children(func() {
			policyEffectBadge(c, rule.Effect)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				target := ui.Text(c, policyRuleTargetText(rule, o)).Font("monospace").FontSize(12.5).FontWeight(600).SingleLine()
				if rule.Enabled {
					target.TextColor(SailInkStrong)
				} else {
					target.TextColor(SailInkFaint)
				}
				if len(rule.ToolIDs) > 0 {
					ui.Text(c, strings.Join(rule.ToolIDs, ", ")).Font("monospace").FontSize(11.5).TextColor(SailInkMuted).SingleLine().MinWidth(0)
				}
			})
			var meta []string
			if rule.RepoKey == "*" {
				meta = append(meta, i18n.T("policy.global-hint"))
			}
			if rule.Actor != "" {
				if local {
					meta = append(meta, i18n.T("policy.actor-ignored-local", rule.Actor))
				} else {
					meta = append(meta, i18n.T("ops.actor-with-value", rule.Actor))
				}
			}
			if rule.Reason != "" {
				meta = append(meta, rule.Reason)
			}
			if len(meta) > 0 {
				ui.Text(c, strings.Join(meta, " · ")).FontSize(11.5).TextColor(SailInkMuted).SingleLine().Tooltip(strings.Join(meta, "\n"))
			}
		})
		enabled := rule.Enabled
		if ui.Switch(c.Key("policy-sw-"+rule.ID), &enabled).
			Disabled(busy || u.Client == nil).
			Label(i18n.T("policy.toggle-rule", rule.ID)).
			Changed() {
			next := rule
			next.Enabled = enabled
			u.togglePolicyRuleAsync(ps, next)
		}
		if SailSmallButton(c.Key("policy-del-"+rule.ID), i18n.T("policy.delete")).
			Label(i18n.T("policy.delete-rule", rule.ID)).
			Disabled(busy || u.Client == nil).
			Clicked() {
			ps.confirmDeleteID = rule.ID
			ps.showDeleteConfirm = true
		}
	})
}

func (u *DesktopUI) createPolicyRuleAsync(ps *policyRulesState, rule apitypes.PolicyRule) {
	if ps.saving || u.Client == nil {
		return
	}
	ps.saving = true
	ps.formErr = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SavePolicyRule(ctx, rule); err != nil {
			u.updateUI(func() {
				ps.saving = false
				ps.formErr = err.Error()
			})
			return nil
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			ps.saving = false
			ps.adding = false
		})
		u.Notify(i18n.T("policy.rule-saved"))
		return nil
	})
}

func (u *DesktopUI) togglePolicyRuleAsync(ps *policyRulesState, rule apitypes.PolicyRule) {
	if ps.busy[rule.ID] || u.Client == nil {
		return
	}
	ps.busy[rule.ID] = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := u.Client.SavePolicyRule(ctx, rule)
		if err == nil {
			_ = u.RefreshSync()
		}
		u.updateUI(func() { delete(ps.busy, rule.ID) })
		return err
	})
}

func (u *DesktopUI) deletePolicyRuleAsync(ps *policyRulesState, id string) {
	if ps.busy[id] || u.Client == nil {
		return
	}
	ps.busy[id] = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := u.Client.DeletePolicyRule(ctx, id)
		if err == nil {
			_ = u.RefreshSync()
		}
		u.updateUI(func() { delete(ps.busy, id) })
		if err == nil {
			u.Notify(i18n.T("policy.rule-deleted"))
		}
		return err
	})
}
