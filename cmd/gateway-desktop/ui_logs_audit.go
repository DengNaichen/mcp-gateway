package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// Structured audit filters for the unified ledger. Index 0 always means "all".
var (
	logsProtocolValues = []string{"", "mcp", "http", "cli"}
	logsDecisionValues = []string{"", "allow", "approve", "deny"}
)

// logsRepoUnbound selects events without a bound repository (RepoID == "").
// The backend filter is an equality match, so this one is applied locally.
const logsRepoUnbound = "\x00unbound"

const logsQueryPageSize = 200

// logsRemotePage caches the backend QueryEvents result for one filter key.
type logsRemotePage struct {
	key        string
	loading    bool
	loaded     bool
	err        string
	items      []apitypes.Event
	nextCursor string
	hasMore    bool
}

type logsFilterOptions struct {
	repoOptions    []string
	repoLabelToKey map[string]string
	serviceOptions []string
	serviceToID    map[string]string
}

func buildLogsFilterOptions(state *apitypes.Bootstrap) logsFilterOptions {
	all := i18n.T("ops.filter-all")
	unbound := i18n.T("policy.repo-unbound")
	o := logsFilterOptions{
		repoOptions:    []string{all, unbound},
		repoLabelToKey: map[string]string{all: "", unbound: logsRepoUnbound},
		serviceOptions: []string{all},
		serviceToID:    map[string]string{all: ""},
	}
	if state == nil {
		return o
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
	}
	for _, srv := range state.Registry.Servers {
		lbl := srv.Name
		if lbl == "" {
			lbl = srv.ID
		}
		if _, dup := o.serviceToID[lbl]; dup {
			lbl = fmt.Sprintf("%s (%s)", lbl, srv.ID)
		}
		o.serviceOptions = append(o.serviceOptions, lbl)
		o.serviceToID[lbl] = srv.ID
	}
	return o
}

// syncLabels resolves the select labels into keys, resetting a selection whose
// repository or service no longer exists.
func (st *logsPageState) syncLabels(o logsFilterOptions) {
	if key, ok := o.repoLabelToKey[st.repoLabel]; ok {
		st.repoKey = key
	} else {
		st.repoLabel, st.repoKey = o.repoOptions[0], ""
	}
	if id, ok := o.serviceToID[st.serviceLabel]; ok {
		st.serviceID = id
	} else {
		st.serviceLabel, st.serviceID = o.serviceOptions[0], ""
	}
}

func logsIndexValue(values []string, idx int) string {
	if idx < 0 || idx >= len(values) {
		return ""
	}
	return values[idx]
}

// logsServerFilter maps the page filters onto the backend query. The bool is
// false when nothing is filtered so the page can render Bootstrap.Events.
func logsServerFilter(st *logsPageState) (apitypes.EventFilter, bool) {
	f := apitypes.EventFilter{
		Protocol:  logsIndexValue(logsProtocolValues, st.protocolIdx),
		Decision:  logsIndexValue(logsDecisionValues, st.decisionIdx),
		ServiceID: st.serviceID,
		Query:     strings.TrimSpace(st.query),
		Limit:     logsQueryPageSize,
	}
	if st.repoKey != logsRepoUnbound {
		f.RepoID = st.repoKey
	}
	active := f.Protocol != "" || f.Decision != "" || f.ServiceID != "" || f.Query != "" || st.repoKey != ""
	return f, active
}

func logsQueryKey(f apitypes.EventFilter, headID string) string {
	return strings.Join([]string{f.Protocol, f.Decision, f.RepoID, f.ServiceID, f.Query, headID}, "\x1f")
}

// logsSourceEvents returns the ledger rows to filter and render. Filtered views
// query the backend; until that answers (or when it fails, e.g. offline) the
// local Bootstrap.Events snapshot is used so the page never goes blank.
func (u *DesktopUI) logsSourceEvents(st *logsPageState) (events []apitypes.Event, remote bool) {
	local := u.State.Events
	filter, active := logsServerFilter(st)
	if !active || u.Client == nil {
		return local, false
	}
	headID := ""
	if len(local) > 0 {
		headID = local[0].ID
	}
	key := logsQueryKey(filter, headID)
	r := &st.remote
	if r.key == key && r.loaded {
		return r.items, true
	}
	if r.key != key && !r.loading {
		u.fetchLogsPage(st, filter, key, false)
	}
	return local, false
}

func (u *DesktopUI) fetchLogsPage(st *logsPageState, filter apitypes.EventFilter, key string, appendPage bool) {
	r := &st.remote
	r.loading = true
	if !appendPage {
		*r = logsRemotePage{key: key, loading: true}
	} else {
		filter.Before = r.nextCursor
	}
	client := u.Client
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		page, err := client.QueryEvents(ctx, filter)
		u.updateUI(func() {
			if r.key != key {
				return // superseded by a newer filter
			}
			r.loading = false
			if err != nil {
				r.err = err.Error()
				return
			}
			r.loaded = true
			r.err = ""
			if appendPage {
				r.items = append(r.items, page.Items...)
			} else {
				r.items = page.Items
			}
			r.nextCursor = page.NextCursor
			r.hasMore = page.HasMore && page.NextCursor != ""
		})
	}()
}

func logsMatchesFilters(ev apitypes.Event, st *logsPageState) bool {
	if p := logsIndexValue(logsProtocolValues, st.protocolIdx); p != "" && ev.Protocol != p {
		return false
	}
	if d := logsIndexValue(logsDecisionValues, st.decisionIdx); d != "" && ev.Decision != d {
		return false
	}
	switch st.repoKey {
	case "":
	case logsRepoUnbound:
		if ev.RepoID != "" {
			return false
		}
	default:
		if ev.RepoID != st.repoKey {
			return false
		}
	}
	if st.serviceID != "" && ev.ServiceID != st.serviceID {
		return false
	}

	q := strings.ToLower(strings.TrimSpace(st.query))
	if q == "" {
		return true
	}
	haystack := strings.ToLower(strings.Join([]string{
		ev.ID,
		ev.Title,
		ev.Actor,
		ev.Kind,
		ev.Status,
		ev.Protocol,
		ev.Decision,
		ev.Action,
		ev.RuleID,
		ev.ApprovalID,
		ev.ServiceID,
		ev.RepoID,
		ev.SkillID,
		string(ev.Detail),
	}, " "))
	return strings.Contains(haystack, q)
}

// logsRowDotColor lets policy decisions override the execution status dot.
func logsRowDotColor(ev apitypes.Event) ui.Color {
	switch ev.Decision {
	case "deny":
		return SailDangerInk
	case "approve":
		return SailWarningInk
	}
	return logsStatusDotColor(ev.Status)
}

// logsRowKindLabel prefers the ledger protocol and falls back to the legacy kind.
func logsRowKindLabel(ev apitypes.Event) string {
	if ev.Protocol != "" {
		return ev.Protocol
	}
	return logsKindBadgeLabel(ev.Kind)
}

func logsHasAuditMeta(ev apitypes.Event) bool {
	return ev.Protocol != "" || ev.Decision != "" || ev.RuleID != "" || ev.ServiceID != "" || ev.Action != "" || ev.ApprovalID != "" || ev.SkillID != ""
}

// logsSkillTrust reads the trust level recorded with a declared skill_id
// ("declared" today; "hook-verified" once a hook attests the skill).
func logsSkillTrust(ev apitypes.Event) string {
	if ev.SkillID == "" || len(ev.Detail) == 0 {
		return ""
	}
	var d struct {
		SkillTrust string `json:"skill_trust"`
	}
	if json.Unmarshal(ev.Detail, &d) != nil {
		return ""
	}
	return strings.TrimSpace(d.SkillTrust)
}

// logsCLICommand summarizes a controlled CLI call as its gh subcommand.
func logsCLICommand(ev apitypes.Event) string {
	if ev.Protocol != "cli" {
		return ""
	}
	action := ev.Action
	if action == "" {
		action = ev.Title
	}
	return cliCommandSummary(ev.ServiceID, action)
}

// renderLogsSkillTrust shows hook-verified skills as a quiet status and
// self-declared ones as a neutral badge (declarative guardrail, not proof).
func renderLogsSkillTrust(c *ui.Context, trust string) {
	switch trust {
	case "":
	case "hook-verified":
		SailQuietStatus(c, trust, SailSuccessInk, SailInkMuted)
	default:
		SailBadge(c, trust, ToneNeutral)
	}
}

func renderLogsInspectorAuditCard(c *ui.Context, ev apitypes.Event) {
	if !logsHasAuditMeta(ev) {
		return
	}
	SailSubtleCard(c, func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, i18n.T("policy.audit-meta")).FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
			policyEffectBadge(c, ev.Decision)
		})
		rows := []struct{ key, value string }{
			{"protocol", ev.Protocol},
			{"decision", ev.Decision},
			{"rule_id", ev.RuleID},
			{"service_id", ev.ServiceID},
			{"action", ev.Action},
			{i18n.T("policy.audit-cli-command"), logsCLICommand(ev)},
			{"approval_id", ev.ApprovalID},
			{"skill_id", ev.SkillID},
		}
		for _, row := range rows {
			if row.value != "" {
				SailKeyValueRow(c, row.key, row.value, true)
			}
		}
		if trust := logsSkillTrust(ev); trust != "" {
			ui.Row(c).FillWidth().Padding(6, 0).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "skill_trust").Width(116).Shrink(0).FontSize(12).FontWeight(500).TextColor(SailInkMuted)
				renderLogsSkillTrust(c, trust)
				if trust == "declared" {
					ui.Text(c, i18n.T("skills.trust-declared-hint")).FontSize(11.5).TextColor(SailInkFaint).SingleLine().MinWidth(0)
				}
			})
		}
	})
}

func renderLogsFilterBar(c *ui.Context, st *logsPageState, o logsFilterOptions, filteredCount, totalCount int, loading bool) {
	SailCard(c.Key("logs-filter-card"), func() {
		ui.Row(c).FillWidth().Wrap().Gap(10).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Wrap().Gap(10).AlignItems(ui.Center).Children(func() {
				SailSearchField(c.Key("logs-search"), &st.query).
					Placeholder(i18n.T("ops.search-logs-placeholder")).
					Label(i18n.T("ops.search-logs")).
					Width(220)

				SailSegmented(
					c.Key("logs-protocol-seg"),
					&st.protocolIdx,
					i18n.T("ops.filter-all"),
					"MCP",
					"HTTP",
					"CLI",
				).Label(i18n.T("policy.filter-protocol"))

				SailSegmented(
					c.Key("logs-decision-seg"),
					&st.decisionIdx,
					i18n.T("ops.filter-all"),
					"allow",
					"approve",
					"deny",
				).Label(i18n.T("policy.filter-decision"))

				policyFormField(c, i18n.T("ops.repo-label"), func() {
					SailSelect(c.Key("logs-repo-select"), &st.repoLabel, o.repoOptions).MinWidth(132)
				})
				policyFormField(c, i18n.T("ops.services"), func() {
					SailSelect(c.Key("logs-service-select"), &st.serviceLabel, o.serviceOptions).MinWidth(132)
				})
			})

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if loading {
					ui.Spinner(c).Label(i18n.T("ops.loading"))
				}
				if st.hasActiveFilters() {
					if SailButton(c.Key("logs-reset-btn"), i18n.T("ops.reset")).Clicked() {
						st.resetFilters()
					}
				}
				ui.Textf(c, "%d / %d", filteredCount, totalCount).
					FontSize(12).
					TextColor(SailInkMuted).
					FontFeatures("tnum").
					SingleLine()
			})
		})
	})
}
