package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

// approvalNow is the clock for TTL rendering; tests pin it.
var approvalNow = time.Now

const approvalStatusPending = "pending"

// logsDecisionApproveIdx is the index of "approve" in logsDecisionValues.
var logsDecisionApproveIdx = slices.Index(logsDecisionValues, "approve")

// approvalsState holds the Logs-page review queue selection and drafts.
type approvalsState struct {
	selectedID string
	notes      map[string]*string
	deciding   string // approval ID with an in-flight decision
}

func (s *approvalsState) note(id string) *string {
	if s.notes == nil {
		s.notes = make(map[string]*string)
	}
	p, ok := s.notes[id]
	if !ok {
		p = new(string)
		s.notes[id] = p
	}
	return p
}

func parseApprovalTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// pendingApprovals returns pending approvals, newest first.
func pendingApprovals(state *apitypes.Bootstrap) []apitypes.Approval {
	if state == nil {
		return nil
	}
	out := make([]apitypes.Approval, 0, len(state.Approvals))
	for _, a := range state.Approvals {
		if a.Status == approvalStatusPending {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b apitypes.Approval) int {
		ta, _ := parseApprovalTime(a.CreatedAt)
		tb, _ := parseApprovalTime(b.CreatedAt)
		return tb.Compare(ta)
	})
	return out
}

func findApproval(state *apitypes.Bootstrap, id string) (apitypes.Approval, bool) {
	if state == nil || id == "" {
		return apitypes.Approval{}, false
	}
	for _, a := range state.Approvals {
		if a.ID == id {
			return a, true
		}
	}
	return apitypes.Approval{}, false
}

// approvalDeadline is the moment the current approval stage lapses: the
// pending window for pending approvals, the use window once approved.
func approvalDeadline(a apitypes.Approval) (time.Time, bool) {
	switch a.Status {
	case approvalStatusPending:
		return parseApprovalTime(a.PendingExpiresAt)
	case "approved":
		return parseApprovalTime(a.ExpiresAt)
	}
	return time.Time{}, false
}

func approvalExpired(a apitypes.Approval, now time.Time) bool {
	if a.Status == "expired" {
		return true
	}
	deadline, ok := approvalDeadline(a)
	return ok && !now.Before(deadline)
}

// approvalTTLLabel renders the remaining time ("4m 12s left") or "expired".
func approvalTTLLabel(a apitypes.Approval, now time.Time) string {
	if approvalExpired(a, now) {
		return i18n.T("approval.expired")
	}
	deadline, ok := approvalDeadline(a)
	if !ok {
		return ""
	}
	left := deadline.Sub(now).Round(time.Second)
	var span string
	switch {
	case left >= time.Hour:
		span = fmt.Sprintf("%dh %dm", int(left.Hours()), int(left.Minutes())%60)
	case left >= time.Minute:
		span = fmt.Sprintf("%dm %ds", int(left.Minutes()), int(left.Seconds())%60)
	default:
		span = fmt.Sprintf("%ds", int(left.Seconds()))
	}
	return i18n.T("approval.ttl-left", span)
}

func approvalRepoLabel(a apitypes.Approval) string {
	if a.RepoName != "" {
		return a.RepoName
	}
	if a.RepoID != "" {
		return a.RepoID
	}
	return i18n.T("policy.repo-unbound")
}

// approvalSourceLine is the redacted one-line origin: actor / repo / service / action.
func approvalSourceLine(a apitypes.Approval) string {
	actor := a.Actor
	if actor == "" {
		actor = "anonymous"
	}
	parts := []string{actor, approvalRepoLabel(a)}
	for _, p := range []string{a.ServiceID, a.Action} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " / ")
}

// approvalBlock explains why the current operator cannot decide an approval;
// "" means the decision buttons are live.
func approvalBlock(a apitypes.Approval, access apitypes.AccessInfo, now time.Time) string {
	switch {
	case a.Status != approvalStatusPending:
		return i18n.T("approval.block-decided", a.Status)
	case approvalExpired(a, now):
		return i18n.T("approval.block-expired")
	case access.Shared && a.Actor != "" && a.Actor == access.Actor:
		return i18n.T("approval.block-self")
	}
	return ""
}

// OpenLogsApproval jumps to Logs filtered to decision=approve with the given
// approval selected in the review queue.
func (u *DesktopUI) OpenLogsApproval(approvalID string) {
	u.ViewMode = ViewLogs
	u.nav.openLogsApproval(approvalID)
}

// applyPendingApproval consumes an Overview handoff on the Logs page.
func (u *DesktopUI) applyPendingApproval(st *logsPageState) {
	id, ok := u.nav.consumeApprovalID()
	if !ok {
		return
	}
	st.resetFilters()
	st.decisionIdx = logsDecisionApproveIdx
	st.approvals.selectedID = id
	for _, ev := range u.State.Events {
		if ev.ApprovalID == id {
			st.selectedID = ev.ID
			break
		}
	}
}

func (u *DesktopUI) decideApproval(id string, approve bool, note string) {
	if u.Client == nil {
		return
	}
	st := u.logsState()
	st.approvals.deciding = id
	client := u.Client
	u.RunAsync(func() error {
		defer u.updateUI(func() { st.approvals.deciding = "" })
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req := apitypes.ApprovalDecisionRequest{Note: strings.TrimSpace(note)}
		var err error
		if approve {
			_, err = client.Approve(ctx, id, req)
		} else {
			_, err = client.Deny(ctx, id, req)
		}
		if err != nil {
			return err
		}
		msg := i18n.T("approval.toast-denied", id)
		if approve {
			msg = i18n.T("approval.toast-approved", id)
		}
		u.updateUI(func() {
			delete(st.approvals.notes, id)
			u.toasts.push(msg)
		})
		return u.RefreshSync()
	})
}

// --- Overview: pending approvals inbox strip ---

func (u *DesktopUI) renderOverviewApprovalStrip(c *ui.Context) {
	pending := pendingApprovals(u.State)
	if u.State.PendingApprovals <= 0 || len(pending) == 0 {
		return
	}
	latest := pending[0]
	now := approvalNow()
	applyCardElevation(
		ui.Row(c.Key("ov-approval-strip")).
			FillWidth().
			Padding(12, 16).
			Gap(12).
			AlignItems(ui.Center).
			Radius(10).
			Background(SailBgSurface),
	).Children(func() {
		SailStatusDot(c, SailWarningInk)
		ui.Text(c, i18n.T("approval.strip-title", u.State.PendingApprovals)).
			FontSize(13).
			FontWeight(600).
			TextColor(SailInkStrong).
			Shrink(0).
			SingleLine()
		ui.Text(c, approvalSourceLine(latest)).
			Grow(1).
			MinWidth(0).
			Font("monospace").
			FontSize(12).
			TextColor(SailInkBody).
			SingleLine()
		ui.Text(c, approvalTTLLabel(latest, now)).
			FontSize(12).
			FontFeatures("tnum").
			TextColor(SailWarningInk).
			Shrink(0).
			SingleLine()
		if SailSmallPrimaryButton(c.Key("ov-approval-go"), i18n.T("approval.strip-go")).Clicked() {
			u.OpenLogsApproval(latest.ID)
		}
	})
}

// --- Logs: review queue + inspector ---

func (u *DesktopUI) renderLogsApprovalQueue(c *ui.Context, st *logsPageState) {
	pending := pendingApprovals(u.State)
	if len(pending) == 0 {
		if st.decisionIdx == logsDecisionApproveIdx {
			ui.Text(c, i18n.T("approval.queue-empty")).FontSize(12.5).TextColor(SailInkMuted)
		}
		return
	}
	selected := pending[0]
	for _, a := range pending {
		if a.ID == st.approvals.selectedID {
			selected = a
			break
		}
	}
	st.approvals.selectedID = selected.ID
	now := approvalNow()

	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		applyCardElevation(
			ui.Column(c.Key("approval-queue-card")).
				Width(376).
				Shrink(0).
				Radius(10).
				Background(SailBgSurface),
		).Clip().Children(func() {
			SailListCardHeader(c, i18n.T("approval.queue-title"), i18n.T("approval.queue-count", len(pending)), nil)
			for i, a := range pending {
				renderApprovalQueueRow(c, st, a, a.ID == selected.ID, i == len(pending)-1, now)
			}
		})
		u.renderApprovalReviewPanel(c, st, selected, now)
	})
}

func renderApprovalQueueRow(c *ui.Context, st *logsPageState, a apitypes.Approval, selected, isLast bool, now time.Time) {
	rowBg := SailBgSurface
	if selected {
		rowBg = SailAccentSoft
	}
	row := ui.ButtonBase(c.Key("approval-row-"+a.ID)).
		Column().
		FillWidth().
		Padding(12, 18).
		Gap(5).
		AlignItems(ui.Stretch).
		Background(rowBg).
		Cursor(ui.CursorPointer)
	if !isLast {
		row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}
	if row.Hovered() && !selected {
		row.Background(SailBgHover)
	}
	row.Children(func() {
		// Anchor 1: decision dot + source; anchor 3: TTL.
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Gap(6).AlignItems(ui.Center).MinWidth(0).Children(func() {
				SailStatusDot(c, SailWarningInk)
				ui.Text(c, approvalSourceLine(a)).Font("monospace").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
			})
			ttlInk := SailWarningInk
			if approvalExpired(a, now) {
				ttlInk = SailDangerInk
			}
			ui.Text(c, approvalTTLLabel(a, now)).FontSize(11).FontFeatures("tnum").TextColor(ttlInk).Shrink(0).SingleLine()
		})
		// Anchor 2: what will run.
		title := a.Summary
		if title == "" {
			title = a.Action
		}
		titleInk := SailInkStrong
		if selected {
			titleInk = SailAccent
		}
		ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(titleInk).SingleLine()
	})
	if row.Clicked() {
		st.approvals.selectedID = a.ID
	}
}

func (u *DesktopUI) renderApprovalReviewPanel(c *ui.Context, st *logsPageState, a apitypes.Approval, now time.Time) {
	access := u.State.Access
	block := approvalBlock(a, access, now)
	busy := st.approvals.deciding == a.ID

	applyCardElevation(
		ui.Column(c.Key("approval-review-"+a.ID)).
			Grow(1).
			MinWidth(0).
			Padding(20, 22).
			Gap(16).
			Radius(10).
			Background(SailBgSurface),
	).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).MinWidth(0).Children(func() {
				policyEffectBadge(c, "approve")
				ui.Text(c, i18n.T("approval.review-title")).FontSize(15).Bold().TextColor(SailInkStrong).SingleLine()
				ui.Text(c, a.ID).Font("monospace").FontSize(11.5).TextColor(SailInkFaint).Selectable().SingleLine()
			})
			ttlInk := SailWarningInk
			if approvalExpired(a, now) {
				ttlInk = SailDangerInk
			}
			ui.Text(c, approvalTTLLabel(a, now)).FontSize(12.5).FontWeight(600).FontFeatures("tnum").TextColor(ttlInk).Shrink(0).SingleLine()
		})

		if a.Summary != "" {
			ui.Text(c, a.Summary).FontSize(13.5).LineHeight(1.5).TextColor(SailInkStrong).Selectable()
		}
		if args := strings.TrimSpace(a.ArgsRedacted); args != "" {
			SailCodeBlock(c.Key("approval-args-"+a.ID), i18n.T("approval.args"), args, func() {
				u.Notify(i18n.T("approval.args-copied"))
			})
		}

		SailSubtleCard(c, func() {
			ui.Text(c, i18n.T("approval.source")).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
			repo := a.RepoName
			if a.RepoID != "" && a.RepoID != repo {
				repo = strings.TrimSpace(repo + " (" + a.RepoID + ")")
			}
			rows := []struct{ key, value string }{
				{"actor", a.Actor},
				{"agent", a.Agent},
				{"repo", repo},
				{"service_id", a.ServiceID},
				{"action", a.Action},
				{"rule_id", a.RuleID},
				{"reason", a.Reason},
				{"created_at", a.CreatedAt},
				{"pending_expires_at", a.PendingExpiresAt},
			}
			for _, row := range rows {
				if row.value != "" {
					SailKeyValueRow(c, row.key, row.value, true)
				}
			}
		})

		if access.Shared {
			if block != "" {
				ui.Text(c, block).FontSize(12.5).TextColor(SailInkMuted)
			}
		} else {
			ui.Text(c, i18n.T("approval.local-mode-hint")).FontSize(12.5).TextColor(SailInkMuted)
			if block != "" {
				ui.Text(c, block).FontSize(12.5).TextColor(SailInkMuted)
			}
		}

		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Text(c, i18n.T("approval.note-label")).FontSize(12).TextColor(SailInkMuted).SingleLine()
			SailTextInput(c.Key("approval-note-"+a.ID), st.approvals.note(a.ID)).
				Placeholder(i18n.T("approval.note-placeholder")).
				FillWidth().
				Disabled(block != "" || busy)
		})

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			disabled := block != "" || busy
			if SailPrimaryButton(c.Key("approval-approve-"+a.ID), i18n.T("approval.approve")).Disabled(disabled).Clicked() && !disabled {
				u.decideApproval(a.ID, true, *st.approvals.note(a.ID))
			}
			if SailDangerButton(c.Key("approval-deny-"+a.ID), i18n.T("approval.deny")).Disabled(disabled).Clicked() && !disabled {
				u.decideApproval(a.ID, false, *st.approvals.note(a.ID))
			}
			if busy {
				ui.Spinner(c).Label(i18n.T("ops.loading"))
			}
		})
	})
}

// renderLogsLinkedApproval shows the approval an audit event is tied to.
func (u *DesktopUI) renderLogsLinkedApproval(c *ui.Context, st *logsPageState, ev apitypes.Event) {
	if ev.ApprovalID == "" {
		return
	}
	a, ok := findApproval(u.State, ev.ApprovalID)
	SailSubtleCard(c, func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, i18n.T("approval.linked-title")).FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
			if ok && a.Status == approvalStatusPending {
				if SailSmallButton(c.Key("logs-linked-approval-"+ev.ID), i18n.T("approval.linked-review")).Clicked() {
					st.approvals.selectedID = a.ID
				}
			}
		})
		if !ok {
			ui.Text(c, i18n.T("approval.linked-missing", ev.ApprovalID)).FontSize(12.5).TextColor(SailInkMuted)
			return
		}
		rows := []struct{ key, value string }{
			{"status", a.Status},
			{"decided_by", a.DecidedBy},
			{"decided_at", a.DecidedAt},
			{"decision_note", a.DecisionNote},
			{"expires_at", a.ExpiresAt},
			{"consumed_at", a.ConsumedAt},
		}
		for _, row := range rows {
			if row.value != "" {
				SailKeyValueRow(c, row.key, row.value, true)
			}
		}
	})
}
