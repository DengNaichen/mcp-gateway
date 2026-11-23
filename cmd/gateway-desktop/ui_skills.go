package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// Skill admission allowlist, embedded at the bottom of the Services page.
// This is a declarative guardrail (skill_trust = declared): it is only checked
// when a call explicitly declares a skill_id. It is not a skill marketplace.

type skillDraft struct {
	id       string
	name     string
	repos    string
	services string
}

type skillsAdmissionState struct {
	adding  bool
	saving  bool
	draft   skillDraft
	formErr string
	busy    map[string]bool // skill ID -> mutation in flight

	confirmDeleteID   string
	showDeleteConfirm bool
}

func (st *servicesPageState) skillsState() *skillsAdmissionState {
	if st.skills == nil {
		st.skills = &skillsAdmissionState{busy: map[string]bool{}}
	}
	return st.skills
}

func newSkillDraft() skillDraft {
	return skillDraft{repos: "*", services: "*"}
}

// parseSkillAllowlist accepts a single "*" or a list of gateway IDs.
func parseSkillAllowlist(raw string) ([]string, bool) {
	ids := splitCommaOrLineList(raw)
	if len(ids) == 0 {
		return []string{}, true
	}
	if slices.Contains(ids, "*") {
		return []string{"*"}, len(ids) == 1
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if !validSkillID(id) {
			return nil, false
		}
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out, true
}

func validSkillID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	return strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r))
	}) < 0
}

func buildSkillFromDraft(d skillDraft) (apitypes.Skill, error) {
	id := strings.TrimSpace(d.id)
	if !validSkillID(id) {
		return apitypes.Skill{}, errors.New(i18n.T("skills.err-id"))
	}
	repos, ok := parseSkillAllowlist(d.repos)
	if !ok {
		return apitypes.Skill{}, errors.New(i18n.T("skills.err-allowlist"))
	}
	services, ok := parseSkillAllowlist(d.services)
	if !ok {
		return apitypes.Skill{}, errors.New(i18n.T("skills.err-allowlist"))
	}
	return apitypes.Skill{
		ID:              id,
		Name:            strings.TrimSpace(d.name),
		Enabled:         true,
		AllowedRepos:    repos,
		AllowedServices: services,
	}, nil
}

// skillScopeText renders "repos · services", resolving IDs to display names.
func skillScopeText(s apitypes.Skill, state *apitypes.Bootstrap) string {
	repoNames := map[string]string{}
	serviceNames := map[string]string{}
	if state != nil {
		for _, r := range state.Scopes.Repositories {
			if r.Name != "" {
				repoNames[r.ID] = r.Name
			}
		}
		for _, srv := range state.Registry.Servers {
			if srv.Name != "" {
				serviceNames[srv.ID] = srv.Name
			}
		}
	}
	list := func(ids []string, names map[string]string, allKey string) string {
		switch {
		case len(ids) == 0:
			return i18n.T("skills.scope-none")
		case len(ids) == 1 && ids[0] == "*":
			return i18n.T(allKey)
		}
		out := make([]string, 0, len(ids))
		for _, id := range ids {
			if n := names[id]; n != "" {
				out = append(out, n)
			} else {
				out = append(out, id)
			}
		}
		return strings.Join(out, ", ")
	}
	return list(s.AllowedRepos, repoNames, "skills.scope-all-repos") + " · " + list(s.AllowedServices, serviceNames, "skills.scope-all-services")
}

func sortedSkills(skills []apitypes.Skill) []apitypes.Skill {
	out := slices.Clone(skills)
	slices.SortStableFunc(out, func(a, b apitypes.Skill) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (u *DesktopUI) renderSkillAllowlistSection(c *ui.Context, st *servicesPageState) {
	ks := st.skillsState()
	skills := sortedSkills(u.State.Skills)

	if ks.showDeleteConfirm && ks.confirmDeleteID != "" {
		targetID := ks.confirmDeleteID
		if ui.AlertDialog(
			c,
			&ks.showDeleteConfirm,
			i18n.T("skills.delete-confirm-title"),
			i18n.T("skills.delete-confirm-body", targetID),
			i18n.T("common.cancel"),
			i18n.T("skills.delete"),
		) == 1 {
			u.deleteSkillAsync(ks, targetID)
		}
	}

	SailFlushCard(c.Key("skills-card"), func() {
		SailListCardHeader(c, i18n.T("skills.title"), i18n.T("skills.note"), func() {
			if !ks.adding {
				if SailSmallButton(c.Key("skills-add-btn"), i18n.T("skills.add")).Disabled(u.Client == nil).Clicked() {
					ks.adding = true
					ks.formErr = ""
					ks.draft = newSkillDraft()
				}
			}
		})

		if ks.adding {
			u.renderSkillForm(c, ks)
		}

		if len(skills) == 0 && !ks.adding {
			ui.Row(c).FillWidth().Padding(16, 20).Children(func() {
				ui.Text(c, i18n.T("skills.empty")).FontSize(12.5).TextColor(SailInkMuted)
			})
			return
		}
		for i, s := range skills {
			u.renderSkillRow(c, ks, s, i == len(skills)-1)
		}
	})
}

func (u *DesktopUI) renderSkillForm(c *ui.Context, ks *skillsAdmissionState) {
	ui.Column(c.Key("skills-form")).FillWidth().Padding(14, 20).Gap(12).Background(SailBgSubtle).
		BorderWidth(0, 0, 1, 0).BorderColor(SailDivider).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
			SailTextInput(c.Key("skills-form-id"), &ks.draft.id).
				Font("monospace").
				Placeholder("skill_id").
				Label("skill_id").
				Width(170)
			SailTextInput(c.Key("skills-form-name"), &ks.draft.name).
				Placeholder(i18n.T("skills.name-placeholder")).
				Label(i18n.T("skills.name")).
				Width(170)
			policyFormField(c, i18n.T("ops.repo-label"), func() {
				SailTextInput(c.Key("skills-form-repos"), &ks.draft.repos).
					Font("monospace").
					Placeholder(i18n.T("skills.allowlist-placeholder")).
					Label(i18n.T("ops.repo-label")).
					Width(150)
			})
			policyFormField(c, i18n.T("ops.services"), func() {
				SailTextInput(c.Key("skills-form-services"), &ks.draft.services).
					Font("monospace").
					Placeholder(i18n.T("skills.allowlist-placeholder")).
					Label(i18n.T("ops.services")).
					Width(150)
			})
		})
		if ks.formErr != "" {
			SailAlertBox(c, ToneDanger, i18n.T("skills.save-failed"), ks.formErr)
		}
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("skills.allowlist-hint")).FontSize(11.5).TextColor(SailInkFaint).SingleLine().MinWidth(0)
			ui.Row(c).Gap(8).Shrink(0).Children(func() {
				if SailSmallButton(c.Key("skills-form-cancel"), i18n.T("common.cancel")).Disabled(ks.saving).Clicked() {
					ks.adding = false
					ks.formErr = ""
				}
				lbl := i18n.T("skills.save")
				if ks.saving {
					lbl = i18n.T("ops.saving")
				}
				if SailSmallPrimaryButton(c.Key("skills-form-save"), lbl).Disabled(ks.saving || u.Client == nil).Clicked() {
					skill, err := buildSkillFromDraft(ks.draft)
					if err != nil {
						ks.formErr = err.Error()
					} else {
						u.createSkillAsync(ks, skill)
					}
				}
			})
		})
	})
}

func (u *DesktopUI) renderSkillRow(c *ui.Context, ks *skillsAdmissionState, s apitypes.Skill, isLast bool) {
	row := ui.Row(c.Key("skill-row-"+s.ID)).FillWidth().Padding(12, 20).Gap(14).AlignItems(ui.Center)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	busy := ks.busy[s.ID]
	idInk, scopeInk := SailInkStrong, SailInkMuted
	if !s.Enabled {
		idInk, scopeInk = SailInkFaint, SailInkFaint
	}
	row.Children(func() {
		// Anchor 1: ID / Name.
		ui.Row(c).Width(240).Shrink(0).Gap(8).AlignItems(ui.Baseline).Children(func() {
			ui.Text(c, s.ID).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(idInk).SingleLine().Selectable()
			if s.Name != "" && s.Name != s.ID {
				ui.Text(c, s.Name).FontSize(12).TextColor(SailInkMuted).SingleLine().MinWidth(0)
			}
		})
		// Anchor 2: admitted repositories · services.
		scope := skillScopeText(s, u.State)
		ui.Text(c, scope).FontSize(12.5).TextColor(scopeInk).SingleLine().Grow(1).MinWidth(0).Tooltip(scope)
		// Anchor 3: enable switch + delete.
		enabled := s.Enabled
		if ui.Switch(c.Key("skill-sw-"+s.ID), &enabled).
			Disabled(busy || u.Client == nil).
			Label(i18n.T("skills.toggle", s.ID)).
			Changed() {
			next := s
			next.Enabled = enabled
			u.saveSkillAsync(ks, next)
		}
		if SailSmallButton(c.Key("skill-del-"+s.ID), i18n.T("skills.delete")).
			Label(i18n.T("skills.delete-label", s.ID)).
			Disabled(busy || u.Client == nil).
			Clicked() {
			ks.confirmDeleteID = s.ID
			ks.showDeleteConfirm = true
		}
	})
}

func (u *DesktopUI) createSkillAsync(ks *skillsAdmissionState, skill apitypes.Skill) {
	if ks.saving || u.Client == nil {
		return
	}
	ks.saving = true
	ks.formErr = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := u.Client.SaveSkill(ctx, skill); err != nil {
			u.updateUI(func() {
				ks.saving = false
				ks.formErr = formatAPIError(err)
			})
			return nil
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			ks.saving = false
			ks.adding = false
		})
		u.Notify(i18n.T("skills.saved", skill.ID))
		return nil
	})
}

func (u *DesktopUI) saveSkillAsync(ks *skillsAdmissionState, skill apitypes.Skill) {
	if ks.busy[skill.ID] || u.Client == nil {
		return
	}
	ks.busy[skill.ID] = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := u.Client.SaveSkill(ctx, skill)
		if err == nil {
			_ = u.RefreshSync()
		}
		u.updateUI(func() { delete(ks.busy, skill.ID) })
		return err
	})
}

func (u *DesktopUI) deleteSkillAsync(ks *skillsAdmissionState, id string) {
	if ks.busy[id] || u.Client == nil {
		return
	}
	ks.busy[id] = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := u.Client.DeleteSkill(ctx, id)
		if err == nil {
			_ = u.RefreshSync()
		}
		u.updateUI(func() { delete(ks.busy, id) })
		if err == nil {
			u.Notify(i18n.T("skills.deleted", id))
		}
		return err
	})
}
