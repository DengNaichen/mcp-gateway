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
		latestErr = "下游工具调用失败"
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
			BadgeLabel:  "演示",
			BadgeTone:   ToneNeutral,
			StatusLabel: "演示服务",
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
			credSummary = "缺少 " + strings.Join(missingVars, ", ")
			credDanger = true
		case credLoaded:
			credSummary = fmt.Sprintf("%d/%d 已就绪", len(refs), len(refs))
		default:
			credSummary = fmt.Sprintf("引用 %d 项变量", len(refs))
		}
	}

	if !s.Enabled {
		chkLabel := ""
		chkDisp := checkDisplayNone
		if hasCheck {
			chkLabel = "历史记录 · " + FormatTime(check.CheckedAt)
			chkDisp = checkDisplaySubtle
		}
		rowSum := ""
		if len(missingVars) > 0 {
			rowSum = "已停用 · 缺少凭据 " + strings.Join(missingVars, ", ")
		}
		return serviceReadiness{
			Kind:           readinessDisabled,
			DotColor:       SailInkFaint,
			StatusLabel:    "已停用",
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
		chkLabel := "未检查"
		if hasCheck {
			if check.Status == "error" {
				chkLabel = "异常 · " + FormatTime(check.CheckedAt)
			} else {
				chkLabel = "历史通过 · " + FormatTime(check.CheckedAt)
			}
		}
		rowSummary := "缺少凭据：" + joined
		detailMsg := fmt.Sprintf(
			"当前网关进程与系统钥匙串均未提供 %s。请在「连接与认证配置 → 服务凭据」中直接填入保存（免重启立即生效），或在启动网关前导出该环境变量。",
			joined,
		)
		if invFail != nil {
			rowSummary = fmt.Sprintf("缺少凭据 %s · 最近调用 %s 失败", joined, invFail.ToolID)
			detailMsg = fmt.Sprintf(
				"当前网关进程与系统钥匙串均未提供 %s，最近调用 %s（%s）已因此失败。请在「连接与认证配置 → 服务凭据」中直接填入保存（免重启立即生效），或导出该环境变量后重启网关。",
				joined,
				invFail.ToolID,
				FormatTime(invFail.Time),
			)
		}
		return serviceReadiness{
			Kind:             readinessMissingCreds,
			DotColor:         SailDangerInk,
			BadgeLabel:       "缺少凭据",
			BadgeTone:        ToneDanger,
			StatusLabel:      "缺少凭据",
			RowSummary:       rowSummary,
			ReferencedVars:   refs,
			MissingVars:      missingVars,
			CredSummary:      credSummary,
			CredDanger:       true,
			CheckLabel:       chkLabel,
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneDanger,
			AlertTitle:       "当前缺少凭据：" + joined,
			AlertDetail:      detailMsg,
			AlertAction:      readinessActionFillCreds,
			AlertActionLabel: "填写服务凭据",
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
			title = "连接检查失败"
		}
		return serviceReadiness{
			Kind:             readinessConnError,
			DotColor:         SailDangerInk,
			BadgeLabel:       "连接异常",
			BadgeTone:        ToneDanger,
			StatusLabel:      "连接异常",
			RowSummary:       summary,
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       "异常 · " + FormatTime(check.CheckedAt),
			CheckDisplay:     checkDisplayDanger,
			AlertTone:        ToneDanger,
			AlertTitle:       title,
			AlertDetail:      strings.TrimSpace(check.Detail),
			AlertAction:      readinessActionProbe,
			AlertActionLabel: "重新测试连接",
		}
	}

	// 3. Runtime invoke failed after last passing check (or with no check).
	if invFail != nil {
		chkLabel := "未检查"
		if hasCheck {
			chkLabel = "历史通过 · " + FormatTime(check.CheckedAt)
		}
		return serviceReadiness{
			Kind:             readinessInvokeError,
			DotColor:         SailDangerInk,
			BadgeLabel:       "调用失败",
			BadgeTone:        ToneDanger,
			StatusLabel:      "最近调用失败",
			RowSummary:       fmt.Sprintf("最近调用 %s 失败：%s", invFail.ToolID, invFail.Detail),
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       chkLabel,
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneDanger,
			AlertTitle:       fmt.Sprintf("最近实际调用失败 · %s（%s）", invFail.ToolID, FormatTime(invFail.Time)),
			AlertDetail:      invFail.Detail,
			AlertAction:      readinessActionProbe,
			AlertActionLabel: "立即测试连接",
		}
	}

	// 4. Never checked or connection/credential fingerprint changed (Stale).
	if !hasCheck || check.Stale {
		if hasCheck && check.Stale {
			return serviceReadiness{
				Kind:             readinessUnverified,
				DotColor:         SailWarningInk,
				BadgeLabel:       "待验证",
				BadgeTone:        ToneWarning,
				StatusLabel:      "待复测",
				RowSummary:       fmt.Sprintf("连接配置或凭据环境已变更，上次检查通过于 %s", FormatTime(check.CheckedAt)),
				ReferencedVars:   refs,
				CredSummary:      credSummary,
				CheckLabel:       "待复测 · " + FormatTime(check.CheckedAt),
				CheckDisplay:     checkDisplayWarn,
				AlertTone:        ToneWarning,
				AlertTitle:       "连接配置或凭据已变更，待重新验证",
				AlertDetail:      fmt.Sprintf("上次检查通过时间为 %s，当前进程环境或配置指纹已变化，历史结果不代表当前可用。", FormatTime(check.CheckedAt)),
				AlertAction:      readinessActionProbe,
				AlertActionLabel: "测试连接",
			}
		}
		return serviceReadiness{
			Kind:             readinessUnverified,
			DotColor:         SailWarningInk,
			BadgeLabel:       "待验证",
			BadgeTone:        ToneWarning,
			StatusLabel:      "尚未检查",
			RowSummary:       "尚未执行连接检查，点击测试连接确认服务可用",
			ReferencedVars:   refs,
			CredSummary:      credSummary,
			CheckLabel:       "未检查",
			CheckDisplay:     checkDisplaySubtle,
			AlertTone:        ToneWarning,
			AlertTitle:       "尚未执行连接检查",
			AlertDetail:      "服务已启用，但尚未验证当前连接与认证是否可用。",
			AlertAction:      readinessActionProbe,
			AlertActionLabel: "测试连接",
		}
	}

	// 5. Verified connection, but 0 open tools in whitelist.
	if enabledTools == 0 {
		badge := "未开放工具"
		statusLbl := "未开放工具"
		rowSum := fmt.Sprintf("连接正常，但 %d 个工具均未开放白名单", len(srvTools))
		alertTitle := "尚未向 Agent 开放任何工具"
		alertDetail := "连接与认证检查已通过，但当前白名单开放工具数为 0，Agent 无法发现或调用此服务。"
		action := readinessActionEnableReadOnly
		actionLbl := "仅开放只读工具"
		if len(srvTools) == 0 {
			badge = "无工具"
			statusLbl = "无可用工具"
			rowSum = "连接正常，但当前服务下尚无工具定义"
			alertTitle = "当前服务尚无工具定义"
			alertDetail = "连接检查已通过，请同步下游工具或添加 HTTP 工具。"
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
		StatusLabel:    "就绪",
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
