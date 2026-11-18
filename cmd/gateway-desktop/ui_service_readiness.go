package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
)

type serviceReadinessKind uint8

const (
	readinessReady serviceReadinessKind = iota
	readinessDisabled
	readinessDemo
	readinessMissingCreds
	readinessConnError
	readinessInvokeError
	readinessUnverified
	readinessNoOpenTools
)

type serviceCheckDisplay uint8

const (
	checkDisplayNone serviceCheckDisplay = iota
	checkDisplaySuccess
	checkDisplaySubtle
	checkDisplayWarn
	checkDisplayDanger
)

type readinessActionKind uint8

const (
	readinessActionNone readinessActionKind = iota
	readinessActionFillCreds
	readinessActionProbe
	readinessActionEnableReadOnly
)

type serverInvokeFailure struct {
	Time   time.Time
	ToolID string
	Detail string
}

type serviceReadiness struct {
	Kind             serviceReadinessKind
	DotColor         ui.Color
	BadgeLabel       string
	BadgeTone        BadgeTone
	StatusLabel      string
	RowSummary       string
	ReferencedVars   []string
	MissingVars      []string
	CredSummary      string
	CredDanger       bool
	CheckLabel       string
	CheckDisplay     serviceCheckDisplay
	AlertTone        BadgeTone
	AlertTitle       string
	AlertDetail      string
	AlertAction      readinessActionKind
	AlertActionLabel string
}

func (r serviceReadiness) IsHealthyReady() bool {
	return r.Kind == readinessReady
}

func (r serviceReadiness) HasError() bool {
	return r.Kind == readinessMissingCreds || r.Kind == readinessConnError || r.Kind == readinessInvokeError
}

func (r serviceReadiness) NeedsAttention() bool {
	return r.Kind == readinessUnverified || r.Kind == readinessNoOpenTools
}

// serverReferencedEnvVars extracts sorted unique ${VAR} names referenced by a server's Headers and Env.
func serverReferencedEnvVars(s apitypes.Server) []string {
	found := map[string]bool{}
	for _, values := range []map[string]string{s.Headers, s.Env} {
		for _, value := range values {
			os.Expand(value, func(name string) string {
				if strings.TrimSpace(name) != "" {
					found[name] = true
				}
				return ""
			})
		}
	}
	if len(found) == 0 {
		return nil
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func serverRefsSignature(s apitypes.Server) string {
	refs := serverReferencedEnvVars(s)
	if len(refs) == 0 {
		return ""
	}
	return strings.Join(refs, ",")
}

const missingEnvErrorMarker = "gateway process environment is missing:"

// parseMissingEnvVarsFromError extracts missing variable names from gateway environment errors.
func parseMissingEnvVarsFromError(errText string) []string {
	idx := strings.Index(errText, missingEnvErrorMarker)
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(errText[idx+len(missingEnvErrorMarker):])
	if semi := strings.IndexByte(rest, ';'); semi >= 0 {
		rest = rest[:semi]
	}
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	parts := strings.Split(rest, ",")
	var out []string
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func isCallerSideInvokeRejection(errText string) bool {
	lower := strings.ToLower(strings.TrimSpace(errText))
	if lower == "" {
		return false
	}
	callerMarkers := []string{
		"参数不符合工具 schema",
		"工具不存在",
		"工具已禁用",
		"没有调用权限",
		"scope_required",
		"scope_denied",
	}
	for _, m := range callerMarkers {
		if strings.Contains(lower, strings.ToLower(m)) {
			return true
		}
	}
	return false
}

func extractInvokeEventInfo(ev apitypes.Event) (toolID string, errDetail string) {
	toolID = strings.TrimSpace(ev.Title)
	if len(ev.Detail) == 0 {
		return toolID, ""
	}
	var payload struct {
		ToolID string `json:"tool_id"`
		Tool   string `json:"tool"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(ev.Detail, &payload); err == nil {
		if toolID == "" {
			if payload.ToolID != "" {
				toolID = payload.ToolID
			} else if payload.Tool != "" {
				toolID = payload.Tool
			}
		}
		errDetail = strings.TrimSpace(payload.Error)
	}
	return toolID, errDetail
}

func eventBelongsToServer(serverID, toolID string, serverToolIDs map[string]bool) bool {
	if toolID == "" {
		return false
	}
	if serverToolIDs[toolID] {
		return true
	}
	return strings.HasPrefix(toolID, serverID+".")
}

// latestServerInvokeFailure returns the most recent invoke error for serverID if and only if
// no newer invoke succeeded and no newer successful connection check superseded it.
func latestServerInvokeFailure(
	serverID string,
	serverToolIDs map[string]bool,
	events []apitypes.Event,
	supersededBefore time.Time,
) *serverInvokeFailure {
	var latest *apitypes.Event
	var latestToolID, latestErr string
	for i := range events {
		ev := &events[i]
		if ev.Kind != "invoke" {
			continue
		}
		toolID, errDetail := extractInvokeEventInfo(*ev)
		if !eventBelongsToServer(serverID, toolID, serverToolIDs) {
			continue
		}
		if latest == nil || ev.Time.After(latest.Time) {
			latest = ev
			latestToolID = toolID
			latestErr = errDetail
		}
	}
	if latest == nil || latest.Status != "error" {
		return nil
	}
	if !supersededBefore.IsZero() && !latest.Time.After(supersededBefore) {
		return nil
	}
	if isCallerSideInvokeRejection(latestErr) {
		return nil
	}
	if latestErr == "" {
		latestErr = i18n.T("readiness.invoke.failed_default")
	}
	return &serverInvokeFailure{
		Time:   latest.Time,
		ToolID: latestToolID,
		Detail: latestErr,
	}
}

func checkIsPassing(check apitypes.ConnectionCheck, hasCheck bool) bool {
	return hasCheck && check.Status != "" && check.Status != "error"
}

func evaluateServiceReadiness(
	s apitypes.Server,
	srvTools []apitypes.Tool,
	check apitypes.ConnectionCheck,
	hasCheck bool,
	creds *apitypes.ServerCredentials,
	events []apitypes.Event,
) serviceReadiness {
	if s.Demo {
		return serviceReadiness{
			Kind:        readinessDemo,
			DotColor:    SailInkFaint,
			BadgeLabel:  i18n.T("common.demo"),
			BadgeTone:   ToneNeutral,
			StatusLabel: i18n.T("readiness.demo.status"),
		}
	}

	refs := serverReferencedEnvVars(s)
	var missingVars []string
	credLoaded := creds != nil && len(creds.Credentials) > 0
	if credLoaded {
		for _, c := range creds.Credentials {
			if !c.Configured && slices.Contains(refs, c.Name) && !slices.Contains(missingVars, c.Name) {
				missingVars = append(missingVars, c.Name)
			}
		}
	}

	toolIDs := make(map[string]bool, len(srvTools))
	enabledTools := 0
	for _, t := range srvTools {
		toolIDs[t.ID] = true
		if t.Enabled {
			enabledTools++
		}
	}

	var supersededBefore time.Time
	if checkIsPassing(check, hasCheck) && !check.Stale {
		supersededBefore = check.CheckedAt
	}
	invFail := latestServerInvokeFailure(s.ID, toolIDs, events, supersededBefore)

	// If ServerCredentials hasn't loaded yet, still catch missing env vars reported by check or invoke errors
	// when they belong to this server's current referenced variables.
	if !credLoaded && len(refs) > 0 {
		var errCandidates []string
		if hasCheck && check.Status == "error" {
			errCandidates = append(errCandidates, check.Detail, check.Message)
		}
		if invFail != nil {
			errCandidates = append(errCandidates, invFail.Detail)
		}
		for _, text := range errCandidates {
			for _, v := range parseMissingEnvVarsFromError(text) {
				if slices.Contains(refs, v) && !slices.Contains(missingVars, v) {
					missingVars = append(missingVars, v)
				}
			}
		}
	}
	sort.Strings(missingVars)

	credSummary := ""
	credDanger := false
	if len(refs) > 0 {
		switch {
		case len(missingVars) > 0:
			credSummary = i18n.T("readiness.cred.missing_list", strings.Join(missingVars, ", "))
			credDanger = true
		case credLoaded:
			credSummary = fmt.Sprintf(i18n.T("readiness.cred.ready"), len(refs), len(refs))
		default:
			credSummary = fmt.Sprintf(i18n.T("readiness.cred.referenced"), len(refs))
		}
	}

	if !s.Enabled {
		chkLabel := ""
		chkDisp := checkDisplayNone
		if hasCheck {
			chkLabel = i18n.T("readiness.check.history", FormatTime(check.CheckedAt))
			chkDisp = checkDisplaySubtle
		}
		rowSum := ""
		if len(missingVars) > 0 {
			rowSum = i18n.T("readiness.cred.disabled_missing", strings.Join(missingVars, ", "))
		}
		return serviceReadiness{
			Kind:           readinessDisabled,
			DotColor:       SailInkFaint,
			StatusLabel:    i18n.T("common.disabled"),
			RowSummary:     rowSum,
			ReferencedVars: refs,
			MissingVars:    missingVars,
			CredSummary:    credSummary,
			CredDanger:     credDanger,
			CheckLabel:     chkLabel,
			CheckDisplay:   chkDisp,
		}
	}

	// 1. Missing referenced credentials.
	if len(missingVars) > 0 {
		joined := strings.Join(missingVars, ", ")
		chkLabel := i18n.T("readiness.check.unchecked")
		if hasCheck {
			if check.Status == "error" {
				chkLabel = i18n.T("readiness.check.error_at", FormatTime(check.CheckedAt))
			} else {
				chkLabel = i18n.T("readiness.check.passed_at", FormatTime(check.CheckedAt))
			}
		}
		rowSummary := i18n.T("readiness.alert.missing_creds", joined)
		detailMsg := fmt.Sprintf(
			i18n.T("readiness.cred.detail_missing"),
			joined,
		)
		if invFail != nil {
			rowSummary = fmt.Sprintf(i18n.T("readiness.cred.missing_recent_fail"), joined, invFail.ToolID)
			detailMsg = fmt.Sprintf(
				i18n.T("readiness.cred.detail_missing_recent"),
				joined,
				invFail.ToolID,
				FormatTime(invFail.Time),
			)
		}
		return serviceReadiness{
			Kind:             readinessMissingCreds,
			DotColor:         SailDangerInk,
			BadgeLabel:       i18n.T("common.missing_credentials"),
			BadgeTone:        ToneDanger,
			StatusLabel:      i18n.T("common.missing_credentials"),
			RowSummary:       rowSummary,
			ReferencedVars:   refs,
			MissingVars:      missingVars,
			CredSummary:      credSummary,
			CredDanger:       true,
			CheckLabel:       chkLabel,
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneDanger,
			AlertTitle:       i18n.T("readiness.alert.missing_creds_prefix", joined),
			AlertDetail:      detailMsg,
			AlertAction:      readinessActionFillCreds,
			AlertActionLabel: i18n.T("readiness.action.fill_creds"),
		}
	}

	// 2. Active connection check failed.
	if hasCheck && check.Status == "error" {
		summary := strings.TrimSpace(check.Detail)
		if summary == "" {
			summary = strings.TrimSpace(check.Message)
		}
		title := strings.TrimSpace(check.Message)
		if title == "" {
			title = i18n.T("readiness.conn.failed_title")
		}
		return serviceReadiness{
			Kind:             readinessConnError,
			DotColor:         SailDangerInk,
			BadgeLabel:       i18n.T("readiness.conn.error"),
			BadgeTone:        ToneDanger,
			StatusLabel:      i18n.T("readiness.conn.error"),
			RowSummary:       summary,
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       i18n.T("readiness.check.error_at", FormatTime(check.CheckedAt)),
			CheckDisplay:     checkDisplayDanger,
			AlertTone:        ToneDanger,
			AlertTitle:       title,
			AlertDetail:      strings.TrimSpace(check.Detail),
			AlertAction:      readinessActionProbe,
			AlertActionLabel: i18n.T("readiness.action.retest"),
		}
	}

	// 3. Runtime invoke failed after last passing check (or with no check).
	if invFail != nil {
		chkLabel := i18n.T("readiness.check.unchecked")
		if hasCheck {
			chkLabel = i18n.T("readiness.check.passed_at", FormatTime(check.CheckedAt))
		}
		return serviceReadiness{
			Kind:             readinessInvokeError,
			DotColor:         SailDangerInk,
			BadgeLabel:       i18n.T("readiness.invoke.failed_badge"),
			BadgeTone:        ToneDanger,
			StatusLabel:      i18n.T("readiness.invoke.failed_status"),
			RowSummary:       fmt.Sprintf(i18n.T("readiness.invoke.row_summary"), invFail.ToolID, invFail.Detail),
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       chkLabel,
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneDanger,
			AlertTitle:       fmt.Sprintf(i18n.T("readiness.invoke.alert_title"), invFail.ToolID, FormatTime(invFail.Time)),
			AlertDetail:      invFail.Detail,
			AlertAction:      readinessActionProbe,
			AlertActionLabel: i18n.T("readiness.action.probe_now"),
		}
	}

	// 4. Never checked or connection/credential fingerprint changed (Stale).
	if !hasCheck || check.Stale {
		if hasCheck && check.Stale {
			return serviceReadiness{
				Kind:             readinessUnverified,
				DotColor:         SailWarningInk,
				BadgeLabel:       i18n.T("readiness.unverified.badge"),
				BadgeTone:        ToneWarning,
				StatusLabel:      i18n.T("readiness.stale.status"),
				RowSummary:       fmt.Sprintf(i18n.T("readiness.stale.row_summary"), FormatTime(check.CheckedAt)),
				ReferencedVars:   refs,
				CredSummary:      credSummary,
				CheckLabel:       i18n.T("readiness.check.stale_at", FormatTime(check.CheckedAt)),
				CheckDisplay:     checkDisplayWarn,
				AlertTone:        ToneWarning,
				AlertTitle:       i18n.T("readiness.stale.alert_title"),
				AlertDetail:      fmt.Sprintf(i18n.T("readiness.stale.alert_detail"), FormatTime(check.CheckedAt)),
				AlertAction:      readinessActionProbe,
				AlertActionLabel: i18n.T("common.test_connection"),
			}
		}
		return serviceReadiness{
			Kind:             readinessUnverified,
			DotColor:         SailWarningInk,
			BadgeLabel:       i18n.T("readiness.unverified.badge"),
			BadgeTone:        ToneWarning,
			StatusLabel:      i18n.T("readiness.unverified.status"),
			RowSummary:       i18n.T("readiness.unverified.row_summary"),
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       i18n.T("readiness.check.unchecked"),
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneWarning,
			AlertTitle:       i18n.T("readiness.unverified.row_prefix"),
			AlertDetail:      i18n.T("readiness.unverified.alert_detail"),
			AlertAction:      readinessActionProbe,
			AlertActionLabel: i18n.T("common.test_connection"),
		}
	}

	// 5. Verified connection, but 0 open tools in whitelist.
	if enabledTools == 0 {
		badge := i18n.T("readiness.no_tools.badge")
		statusLbl := i18n.T("readiness.no_tools.badge")
		rowSum := fmt.Sprintf(i18n.T("readiness.no_tools.row_summary"), len(srvTools))
		alertTitle := i18n.T("readiness.no_tools.alert_title")
		alertDetail := i18n.T("readiness.no_tools.alert_detail")
		action := readinessActionEnableReadOnly
		actionLbl := i18n.T("readiness.action.enable_read_only")
		if len(srvTools) == 0 {
			badge = i18n.T("readiness.empty_tools.badge")
			statusLbl = i18n.T("readiness.empty_tools.status")
			rowSum = i18n.T("readiness.empty_tools.row_summary")
			alertTitle = i18n.T("readiness.empty_tools.alert_title")
			alertDetail = i18n.T("readiness.empty_tools.alert_detail")
			action = readinessActionNone
			actionLbl = ""
		}
		return serviceReadiness{
			Kind:             readinessNoOpenTools,
			DotColor:         SailWarningInk,
			BadgeLabel:       badge,
			BadgeTone:        ToneWarning,
			StatusLabel:      statusLbl,
			RowSummary:       rowSum,
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       FormatTime(check.CheckedAt),
			CheckDisplay:     checkDisplaySuccess,
			AlertTone:        ToneWarning,
			AlertTitle:       alertTitle,
			AlertDetail:      alertDetail,
			AlertAction:      action,
			AlertActionLabel: actionLbl,
		}
	}

	// 6. Truly Ready!
	return serviceReadiness{
		Kind:           readinessReady,
		DotColor:       SailSuccessInk,
		StatusLabel:    i18n.T("readiness.ready.status"),
		ReferencedVars: refs,
		CredSummary:    credSummary,
		CheckLabel:     FormatTime(check.CheckedAt),
		CheckDisplay:   checkDisplaySuccess,
	}
}

// fetchReferencedServerCredentials loads credential availability metadata (without secret values)
// for every non-demo server that references ${ENV_VAR} placeholders.
func (u *DesktopUI) fetchReferencedServerCredentials(ctx context.Context, servers []apitypes.Server) map[string]apitypes.ServerCredentials {
	if u.Client == nil {
		return nil
	}
	out := map[string]apitypes.ServerCredentials{}
	for _, s := range servers {
		if s.Demo || len(serverReferencedEnvVars(s)) == 0 {
			continue
		}
		cred, err := u.Client.ServerCredentials(ctx, s.ID)
		if err != nil {
			continue
		}
		out[s.ID] = cred
	}
	return out
}

func (u *DesktopUI) applyServerCredentialsSnapshot(servers []apitypes.Server, fetched map[string]apitypes.ServerCredentials) {
	if u.serverCreds == nil {
		u.serverCreds = map[string]apitypes.ServerCredentials{}
	}
	if u.serverCredsSig == nil {
		u.serverCredsSig = map[string]string{}
	}
	active := map[string]bool{}
	for _, s := range servers {
		sig := serverRefsSignature(s)
		if s.Demo || sig == "" {
			continue
		}
		active[s.ID] = true
		if cred, ok := fetched[s.ID]; ok {
			u.serverCreds[s.ID] = cred
			u.serverCredsSig[s.ID] = sig
		}
	}
	for id := range u.serverCreds {
		if !active[id] {
			delete(u.serverCreds, id)
			delete(u.serverCredsSig, id)
		}
	}
}

func (u *DesktopUI) cachedServerCredentials(s apitypes.Server) *apitypes.ServerCredentials {
	sig := serverRefsSignature(s)
	if s.Demo || sig == "" {
		return nil
	}
	if u.serverCreds != nil && u.serverCredsSig[s.ID] == sig {
		if cred, ok := u.serverCreds[s.ID]; ok {
			copyCred := cred
			return &copyCred
		}
	}
	if u.services != nil && u.services.credentialServerID == s.ID && len(u.services.credentialState.Credentials) > 0 {
		copyCred := u.services.credentialState
		return &copyCred
	}
	return nil
}

// ensureServerCredentialsLoaded lazily fetches credential metadata when u.State was hydrated
// without going through Refresh/RefreshSync (or after server config edits).
func (u *DesktopUI) ensureServerCredentialsLoaded(s apitypes.Server) {
	sig := serverRefsSignature(s)
	if u.Client == nil || s.Demo || sig == "" {
		return
	}
	if u.serverCredsSig != nil && u.serverCredsSig[s.ID] == sig {
		return
	}
	if u.serverCredsBusy == nil {
		u.serverCredsBusy = map[string]bool{}
	}
	if u.serverCredsBusy[s.ID] {
		return
	}
	u.serverCredsBusy[s.ID] = true
	serverID := s.ID
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cred, err := u.Client.ServerCredentials(ctx, serverID)
		if err != nil {
			u.updateUI(func() {
				delete(u.serverCredsBusy, serverID)
			})
			if apiclient.IsUnauthorized(err) {
				return err
			}
			return nil
		}
		u.updateUI(func() {
			delete(u.serverCredsBusy, serverID)
			if u.serverCreds == nil {
				u.serverCreds = map[string]apitypes.ServerCredentials{}
			}
			if u.serverCredsSig == nil {
				u.serverCredsSig = map[string]string{}
			}
			u.serverCreds[serverID] = cred
			u.serverCredsSig[serverID] = sig
			if u.services != nil && u.services.credentialServerID == serverID {
				u.services.credentialState = cred
			}
		})
		return nil
	})
}

func (u *DesktopUI) readinessForServer(s apitypes.Server, srvTools []apitypes.Tool) serviceReadiness {
	var check apitypes.ConnectionCheck
	hasCheck := false
	var events []apitypes.Event
	if u.State != nil {
		check, hasCheck = u.State.Setup.SourceChecks[s.ID]
		events = u.State.Events
	}
	u.ensureServerCredentialsLoaded(s)
	return evaluateServiceReadiness(s, srvTools, check, hasCheck, u.cachedServerCredentials(s), events)
}

func renderReadinessActionBanner(c *ui.Context, keyPrefix string, r serviceReadiness, busy bool, onAction func()) {
	if r.AlertTitle == "" && r.AlertDetail == "" {
		return
	}
	tc := colorsForTone(r.AlertTone)
	ui.Row(c).
		FillWidth().
		Padding(12, 16).
		Gap(14).
		Radius(8).
		Background(tc.Bg).
		Border(1, tc.Border).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				if r.AlertTitle != "" {
					ui.Text(c, r.AlertTitle).FontSize(12.5).FontWeight(600).TextColor(tc.Ink).Selectable()
				}
				if r.AlertDetail != "" {
					ui.Text(c, r.AlertDetail).FontSize(12).LineHeight(1.45).TextColor(tc.Ink).Selectable()
				}
			})
			if r.AlertAction != readinessActionNone && r.AlertActionLabel != "" && onAction != nil {
				if r.AlertTone == ToneDanger {
					if SailPrimaryButton(c.Key(keyPrefix+"-act"), r.AlertActionLabel).
						Padding(5, 11).
						FontSize(12).
						Disabled(busy).
						Shrink(0).
						Clicked() {
						onAction()
					}
				} else {
					if SailSmallButton(c.Key(keyPrefix+"-act"), r.AlertActionLabel).
						Disabled(busy).
						Shrink(0).
						Clicked() {
						onAction()
					}
				}
			}
		})
}

// serviceTriageBucket groups the readiness kinds into the four buckets the
// services list sorts and filters by, ordered by how urgently they need action.
type serviceTriageBucket uint8

const (
	triageAttention serviceTriageBucket = iota
	triagePending
	triageReady
	triageDisabled
	triageBucketCount
)

// triageBucketLabelKeys lists the label keys per triage bucket in bucket order.
var triageBucketLabelKeys = [triageBucketCount]string{
	"readiness.bucket.attention",
	"readiness.unverified.badge",
	"readiness.ready.status",
	"common.disabled",
}

func (b serviceTriageBucket) Label() string { return i18n.T(triageBucketLabelKeys[b]) }

func (r serviceReadiness) TriageBucket() serviceTriageBucket {
	switch {
	case r.Kind == readinessDisabled || r.Kind == readinessDemo:
		return triageDisabled
	case r.HasError():
		return triageAttention
	case r.NeedsAttention():
		return triagePending
	default:
		return triageReady
	}
}

// RowAction is the single in-row fix offered by the services list, or
// readinessActionNone when the service needs nothing.
func (r serviceReadiness) RowAction() (readinessActionKind, string) {
	switch r.Kind {
	case readinessMissingCreds:
		return readinessActionFillCreds, i18n.T("readiness.row_action.fill_creds")
	case readinessConnError, readinessInvokeError:
		return readinessActionProbe, i18n.T("readiness.row_action.retest")
	case readinessUnverified:
		return readinessActionProbe, i18n.T("common.test_connection")
	case readinessNoOpenTools:
		if r.AlertAction == readinessActionEnableReadOnly {
			return readinessActionEnableReadOnly, i18n.T("readiness.row_action.enable_read_only")
		}
	}
	return readinessActionNone, ""
}

// Probeable reports whether a bulk "test all" should include this service.
func (r serviceReadiness) Probeable() bool {
	return r.Kind == readinessUnverified || r.Kind == readinessConnError || r.Kind == readinessInvokeError
}

// servicesNeedingAttention counts real services in the 需处理 bucket.
func (u *DesktopUI) servicesNeedingAttention() int {
	if u.State == nil {
		return 0
	}
	toolsByServer := map[string][]apitypes.Tool{}
	for _, t := range u.State.Registry.Tools {
		toolsByServer[t.ServerID] = append(toolsByServer[t.ServerID], t)
	}
	n := 0
	for _, s := range u.State.Registry.Servers {
		if s.Demo {
			continue
		}
		if u.readinessForServer(s, toolsByServer[s.ID]).TriageBucket() == triageAttention {
			n++
		}
	}
	return n
}
