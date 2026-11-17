package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/apitypes"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type scopeAgentMeta struct {
	ID   string
	Name string
}

var scopeAgents = []scopeAgentMeta{
	{ID: "codex", Name: "Codex CLI"},
	{ID: "claude", Name: "Claude Code"},
	{ID: "cursor", Name: "Cursor"},
	{ID: "opencode", Name: "OpenCode v2"},
	{ID: "pi", Name: "Pi"},
}

func agentDisplayName(id string) string {
	for _, a := range scopeAgents {
		if a.ID == id {
			return a.Name
		}
	}
	if id == "" || id == "*" {
		return "全部客户端"
	}
	return id
}

func agentIndex(id string) int {
	for i, a := range scopeAgents {
		if a.ID == id {
			return i
		}
	}
	return 0
}

type ruleDraft struct {
	grants           []apitypes.ServiceGrant
	dirty            bool
	localOnly        bool
	expanded         bool
	expandedServerID string
	saving           bool
}

type reposPageState struct {
	selectedRepoKey string // "*" or ScopeRepository.ID

	modeBusy bool
	repoBusy bool

	confirmDeleteRepoID   string
	confirmDeleteRepoName string
	showDeleteConfirm     bool

	drafts map[string]*ruleDraft // key: repoKey + "::" + agentKey

	// Preview cache from POST /api/scopes/preview
	previewKey     string
	previewLoading bool
	preview        *apitypes.ScopePreviewResponse

	// Hook snippet modal state (shared with ViewClients)
	showHookModal    bool
	hookAgentIdx     int
	hookLoadingAgent string
	hookError        string
	snippets         map[string]apitypes.ScopeSnippetResponse
}

var (
	reposStatesMu sync.Mutex
	reposStates   = map[*DesktopUI]*reposPageState{}
)

func getReposState(u *DesktopUI) *reposPageState {
	reposStatesMu.Lock()
	defer reposStatesMu.Unlock()
	st, ok := reposStates[u]
	if !ok {
		st = &reposPageState{
			selectedRepoKey: "*",
			drafts:          make(map[string]*ruleDraft),
			snippets:        make(map[string]apitypes.ScopeSnippetResponse),
		}
		reposStates[u] = st
	}
	return st
}

func cloneGrants(in []apitypes.ServiceGrant) []apitypes.ServiceGrant {
	if len(in) == 0 {
		return []apitypes.ServiceGrant{}
	}
	out := make([]apitypes.ServiceGrant, len(in))
	for i, g := range in {
		access := strings.ToLower(strings.TrimSpace(g.Access))
		if access != "rw" {
			access = "ro"
		}
		var toolIDs []string
		if len(g.ToolIDs) > 0 {
			toolIDs = slices.Clone(g.ToolIDs)
		}
		out[i] = apitypes.ServiceGrant{
			ServerID: g.ServerID,
			Access:   access,
			ToolIDs:  toolIDs,
		}
	}
	return out
}

func findScopeRule(rules []apitypes.ScopeRule, repoKey, agentKey string) (*apitypes.ScopeRule, bool) {
	for i := range rules {
		if rules[i].RepoKey == repoKey && rules[i].AgentKey == agentKey {
			return &rules[i], true
		}
	}
	return nil, false
}

func draftMapKey(repoKey, agentKey string) string {
	return repoKey + "::" + agentKey
}

func (st *reposPageState) ensureDraft(rules []apitypes.ScopeRule, repoKey, agentKey string) *ruleDraft {
	key := draftMapKey(repoKey, agentKey)
	d, ok := st.drafts[key]
	saved, hasSaved := findScopeRule(rules, repoKey, agentKey)
	if !ok {
		var initial []apitypes.ServiceGrant
		if hasSaved {
			initial = cloneGrants(saved.Grants)
		} else {
			initial = []apitypes.ServiceGrant{}
		}
		d = &ruleDraft{
			grants:   initial,
			expanded: agentKey == "*",
		}
		st.drafts[key] = d
		return d
	}
	if !d.dirty && !d.localOnly && !d.saving {
		if hasSaved {
			d.grants = cloneGrants(saved.Grants)
		} else {
			d.grants = []apitypes.ServiceGrant{}
		}
	}
	return d
}

// OpenHookSnippetModal opens the manual Hook configuration modal pre-selected to agentID.
func (u *DesktopUI) OpenHookSnippetModal(agentID string) {
	st := getReposState(u)
	if agentID == "" {
		agentID = "claude"
	}
	st.hookAgentIdx = agentIndex(agentID)
	st.hookError = ""
	st.showHookModal = true
	u.ensureHookSnippetLoaded(st, scopeAgents[st.hookAgentIdx].ID, false)
}

func (u *DesktopUI) ensureHookSnippetLoaded(st *reposPageState, agentID string, force bool) {
	if u.Client == nil || agentID == "" {
		return
	}
	if !force {
		if _, cached := st.snippets[agentID]; cached {
			return
		}
	}
	if st.hookLoadingAgent == agentID {
		return
	}
	st.hookLoadingAgent = agentID
	st.hookError = ""
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		snip, err := u.Client.GetHookSnippet(ctx, agentID)
		u.updateUI(func() {
			if st.hookLoadingAgent == agentID {
				st.hookLoadingAgent = ""
			}
			if err != nil {
				st.hookError = err.Error()
				return
			}
			if snip.Agent == "" {
				snip.Agent = agentID
			}
			st.snippets[agentID] = snip
		})
		return nil
	})
}

func shortRemoteHint(remote string) string {
	r := strings.TrimSpace(remote)
	if r == "" {
		return "本地仓库"
	}
	r = strings.TrimSuffix(r, ".git")
	for _, prefix := range []string{"https://", "http://", "ssh://", "git://"} {
		r = strings.TrimPrefix(r, prefix)
	}
	if at := strings.IndexByte(r, '@'); at >= 0 {
		r = r[at+1:]
	}
	// Convert git@host:owner/repo style to host/owner/repo
	if colon := strings.IndexByte(r, ':'); colon > 0 && !strings.Contains(r[:colon], "/") {
		tail := r[colon+1:]
		isPort := len(tail) > 0 && tail[0] >= '0' && tail[0] <= '9'
		if !isPort {
			r = r[:colon] + "/" + tail
		}
	}
	parts := strings.Split(strings.Trim(r, "/"), "/")
	if len(parts) >= 3 && (parts[0] == "github.com" || parts[0] == "gitlab.com" || parts[0] == "bitbucket.org") {
		return strings.Join(parts[1:], "/")
	}
	return r
}

func primaryRepoPath(repo apitypes.Repository) string {
	for _, p := range repo.RootPaths {
		if strings.TrimSpace(p) != "" {
			return strings.TrimSpace(p)
		}
	}
	for _, p := range repo.GitCommonDirs {
		if strings.TrimSpace(p) != "" {
			if filepath.Base(p) == ".git" {
				return filepath.Dir(p)
			}
			return strings.TrimSpace(p)
		}
	}
	return ""
}

func repoBoundServiceSummary(repoID string, rules []apitypes.ScopeRule) string {
	if r, ok := findScopeRule(rules, repoID, "*"); ok {
		if len(r.Grants) == 0 {
			return "无服务"
		}
		return fmt.Sprintf("%d 个服务", len(r.Grants))
	}
	if g, ok := findScopeRule(rules, "*", "*"); ok && len(g.Grants) > 0 {
		return fmt.Sprintf("继承 %d", len(g.Grants))
	}
	return "无服务"
}

func globalDefaultServiceSummary(rules []apitypes.ScopeRule) string {
	if r, ok := findScopeRule(rules, "*", "*"); ok && len(r.Grants) > 0 {
		return fmt.Sprintf("%d 个服务", len(r.Grants))
	}
	return "默认无服务"
}

// --- Deterministic Local Scope Policy Evaluator (mirrors backend precedence) ---

func resolveEffectiveScopeRule(scopes apitypes.ScopeOverview, repoKey, agentKey string) (grants []apitypes.ServiceGrant, matchedRepo, matchedAgent, sourceLabel string) {
	if repoKey != "" && repoKey != "*" {
		if agentKey != "" && agentKey != "*" {
			if r, ok := findScopeRule(scopes.Rules, repoKey, agentKey); ok {
				return r.Grants, repoKey, agentKey, "客户端例外"
			}
		}
		if r, ok := findScopeRule(scopes.Rules, repoKey, "*"); ok {
			return r.Grants, repoKey, "*", "仓库默认"
		}
	}
	if agentKey != "" && agentKey != "*" {
		if r, ok := findScopeRule(scopes.Rules, "*", agentKey); ok {
			return r.Grants, "*", agentKey, "客户端例外"
		}
	}
	if r, ok := findScopeRule(scopes.Rules, "*", "*"); ok {
		if len(r.Grants) == 0 {
			return r.Grants, "*", "*", "无权限"
		}
		return r.Grants, "*", "*", "全局默认"
	}
	return nil, "*", "*", "无权限"
}

func scopeGrantAllowsTool(grants []apitypes.ServiceGrant, t apitypes.Tool) bool {
	for _, g := range grants {
		if g.ServerID != t.ServerID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(g.Access)) != "rw" && !t.ReadOnly {
			return false
		}
		if len(g.ToolIDs) > 0 && !slices.Contains(g.ToolIDs, t.ID) {
			return false
		}
		return true
	}
	return false
}

func computeLocalScopePreview(state *apitypes.Bootstrap, repoKey, actor string) []apitypes.ScopeAgentPreview {
	if state == nil {
		return nil
	}
	if actor == "" {
		if state.Access.Shared && state.Access.Actor != "" {
			actor = state.Access.Actor
		} else {
			actor = "developer"
		}
	}

	activeServers := make(map[string]apitypes.Server)
	for _, s := range state.Registry.Servers {
		if s.Enabled && !s.Demo {
			activeServers[s.ID] = s
		}
	}

	out := make([]apitypes.ScopeAgentPreview, 0, len(scopeAgents))
	for _, ag := range scopeAgents {
		grants, matchedRepo, matchedAgent, srcLabel := resolveEffectiveScopeRule(state.Scopes, repoKey, ag.ID)
		var (
			toolCount    int
			hasWrite     bool
			srvSummaries []apitypes.ScopeServerSummary
		)
		for _, g := range grants {
			if _, ok := activeServers[g.ServerID]; !ok {
				continue
			}
			cnt := 0
			srvWrite := false
			for _, t := range state.Registry.Tools {
				if t.ServerID != g.ServerID || !t.Enabled {
					continue
				}
				if !slices.Contains(t.Actors, "*") && !slices.Contains(t.Actors, actor) {
					continue
				}
				if !scopeGrantAllowsTool(grants, t) {
					continue
				}
				cnt++
				if !t.ReadOnly {
					srvWrite = true
				}
			}
			if cnt > 0 {
				acc := "ro"
				if srvWrite {
					acc = "rw"
					hasWrite = true
				}
				toolCount += cnt
				srvSummaries = append(srvSummaries, apitypes.ScopeServerSummary{
					ServerID:  g.ServerID,
					Access:    acc,
					ToolCount: cnt,
				})
			}
		}
		if len(srvSummaries) == 0 && toolCount == 0 && srcLabel == "全局默认" {
			srcLabel = "无权限"
		}
		out = append(out, apitypes.ScopeAgentPreview{
			Agent:           ag.ID,
			MatchedRepoKey:  matchedRepo,
			MatchedAgentKey: matchedAgent,
			SourceLabel:     srcLabel,
			ServerCount:     len(srvSummaries),
			ToolCount:       toolCount,
			HasWrite:        hasWrite,
			Servers:         srvSummaries,
		})
	}
	return out
}

func formatPreviewSourceLabel(p apitypes.ScopeAgentPreview) string {
	s := strings.TrimSpace(p.SourceLabel)
	switch s {
	case "客户端例外", "仓库默认", "无权限":
		return s
	case "全局默认":
		if p.ServerCount == 0 && p.ToolCount == 0 {
			return "无权限"
		}
		return "全局默认"
	case "repo_agent", "agent_override", "global_agent", "client_override":
		return "客户端例外"
	case "repo_default", "repo":
		return "仓库默认"
	case "global_default", "global", "default":
		if p.ServerCount == 0 && p.ToolCount == 0 {
			return "无权限"
		}
		return "全局默认"
	case "none", "denied", "empty", "unconfigured":
		return "无权限"
	}
	if p.ServerCount == 0 && p.ToolCount == 0 {
		if p.MatchedAgentKey != "" && p.MatchedAgentKey != "*" {
			return "客户端例外"
		}
		if p.MatchedRepoKey != "" && p.MatchedRepoKey != "*" {
			return "仓库默认"
		}
		return "无权限"
	}
	if s != "" {
		return s
	}
	if p.MatchedAgentKey != "" && p.MatchedAgentKey != "*" {
		return "客户端例外"
	}
	if p.MatchedRepoKey != "" && p.MatchedRepoKey != "*" {
		return "仓库默认"
	}
	return "全局默认"
}

func previewRefreshSignature(state *apitypes.Bootstrap, repoKey, actor string) string {
	var b strings.Builder
	b.WriteString(repoKey)
	b.WriteByte('|')
	b.WriteString(actor)
	b.WriteByte('|')
	for _, r := range state.Scopes.Rules {
		b.WriteString(r.RepoKey)
		b.WriteByte(':')
		b.WriteString(r.AgentKey)
		b.WriteByte('=')
		for _, g := range r.Grants {
			b.WriteString(g.ServerID)
			b.WriteByte('/')
			b.WriteString(g.Access)
			b.WriteByte('/')
			b.WriteString(strings.Join(g.ToolIDs, ","))
			b.WriteByte(';')
		}
		b.WriteByte('|')
	}
	return b.String()
}

// --- Main Repos View (`仓库绑定`) ---

func (u *DesktopUI) renderRepos(c *ui.Context) {
	if u.State == nil {
		return
	}
	state := u.State
	st := getReposState(u)

	// Consume cross-page selection handoff.
	if u.SelectedRepoKey != "" {
		st.selectedRepoKey = u.SelectedRepoKey
		u.SelectedRepoKey = ""
	}
	if st.selectedRepoKey == "" {
		st.selectedRepoKey = "*"
	}

	// Ensure selected repository still exists if not "*".
	var selectedRepo *apitypes.Repository
	if st.selectedRepoKey != "*" {
		for i := range state.Scopes.Repositories {
			if state.Scopes.Repositories[i].ID == st.selectedRepoKey {
				selectedRepo = &state.Scopes.Repositories[i]
				break
			}
		}
		if selectedRepo == nil {
			st.selectedRepoKey = "*"
		}
	}

	previewActor := "developer"
	if state.Access.Shared && state.Access.Actor != "" {
		previewActor = state.Access.Actor
	}

	// Trigger backend POST /api/scopes/preview when selection or rules change.
	sig := previewRefreshSignature(state, st.selectedRepoKey, previewActor)
	if u.Client != nil && st.previewKey != sig && !st.previewLoading {
		targetRepoKey := st.selectedRepoKey
		st.previewKey = sig
		st.previewLoading = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			resp, err := u.Client.PreviewScope(ctx, targetRepoKey, previewActor)
			u.updateUI(func() {
				st.previewLoading = false
				if err == nil && len(resp.Agents) > 0 {
					if resp.RepoKey == "" {
						resp.RepoKey = targetRepoKey
					}
					st.preview = &resp
				}
			})
		}()
	}

	// Render modals.
	if st.showHookModal {
		u.renderHookSnippetModal(c)
	}
	if st.showDeleteConfirm && st.confirmDeleteRepoID != "" {
		targetID := st.confirmDeleteRepoID
		targetName := st.confirmDeleteRepoName
		switch ui.AlertDialog(
			c,
			&st.showDeleteConfirm,
			fmt.Sprintf("移除仓库「%s」？", targetName),
			"移除后将删除该仓库专属规则，恢复按全局默认求值。",
			"取消",
			"移除绑定",
		) {
		case 1:
			u.deleteBoundRepository(st, targetID, targetName)
		}
	}

	// 1. Top Header: Title, Enforce Switch, Manual Hook Button, Add Local Repo Button.
	enforceVal := state.Scopes.Enforce
	SailSectionHeader(c, "仓库绑定", "", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(4, 10).Radius(6).Background(SailBgSubtle).Children(func() {
			sw := ui.Switch(c.Key("repos-enforce-sw"), &enforceVal).
				Disabled(st.modeBusy).
				Label("启用仓库隔离拦截")
			ui.Text(c, "启用仓库隔离拦截").FontSize(12.5).FontWeight(500).TextColor(SailInkStrong).SingleLine()
			if sw.Changed() {
				u.toggleScopeEnforceMode(st, enforceVal)
			}
		})

		if SailSmallButton(c.Key("repos-hook-btn"), "手动配置 Hook").Clicked() {
			u.OpenHookSnippetModal("claude")
		}

		if !state.Access.Shared {
			addLbl := "添加本地仓库…"
			if st.repoBusy {
				addLbl = "添加中…"
			}
			if ui.PrimaryButton(c.Key("repos-add-repo-btn"), addLbl).Disabled(st.repoBusy).Clicked() {
				u.pickAndBindLocalRepo(st)
			}
		}
	})

	// 2. Master-Detail Split Layout (Left ~280px Flush List + Right Detail Column).
	ui.Row(c).FillWidth().AlignItems(ui.Start).Gap(22).Children(func() {
		u.renderReposLeftColumn(c, st, state)
		u.renderReposRightColumn(c, st, state, selectedRepo, previewActor)
	})
}

func (u *DesktopUI) renderReposLeftColumn(c *ui.Context, st *reposPageState, state *apitypes.Bootstrap) {
	ui.Column(c.Key("repos-left-col")).
		Width(284).
		Shrink(0).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Clip().
		Children(func() {
			// 1. Pinned First Row: 全局默认 (repo_key = "*")
			globalSelected := st.selectedRepoKey == "*"
			globalBtn := ui.ButtonBase(c.Key("repos-sel-global")).
				FillWidth().
				Padding(13, 16).
				Gap(10).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailDivider).
				Cursor(ui.CursorPointer)
			if globalSelected {
				globalBtn.Background(SailAccentSoft)
			} else if globalBtn.Hovered() {
				globalBtn.Background(SailBgHover)
			}
			globalBtn.Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					titleCol := SailInkStrong
					if globalSelected {
						titleCol = SailAccent
					}
					ui.Text(c, "全局默认").FontSize(13.5).FontWeight(600).TextColor(titleCol).SingleLine()
					ui.Text(c, "未绑定仓库兜底").FontSize(11.5).TextColor(SailInkMuted).SingleLine()
				})
				sum := globalDefaultServiceSummary(state.Scopes.Rules)
				sumCol := SailInkMuted
				if sum == "默认无服务" {
					sumCol = SailInkFaint
				}
				ui.Text(c, sum).FontSize(11.5).FontFeatures("tnum").TextColor(sumCol).Shrink(0).SingleLine()
			})
			if globalBtn.Clicked() {
				st.selectedRepoKey = "*"
			}

			// 2. Section Header: 已绑定仓库
			ui.Row(c).
				FillWidth().
				Padding(8, 16).
				Background(SailBgSubtle).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailDivider).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					ui.Text(c, "已绑定仓库").FontSize(11.5).FontWeight(600).TextColor(SailInkMuted).SingleLine()
					ui.Textf(c, "%d", len(state.Scopes.Repositories)).FontSize(11.5).FontFeatures("tnum").TextColor(SailInkFaint).SingleLine()
				})

			if len(state.Scopes.Repositories) == 0 {
				hasMoreBelow := len(state.Scopes.Observed) > 0
				emptyRow := ui.Column(c).FillWidth().Padding(16)
				if hasMoreBelow {
					emptyRow.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
				}
				emptyRow.Children(func() {
					ui.Text(c, "尚未绑定仓库").FontSize(12.5).TextColor(SailInkFaint)
				})
			} else {
				for idx, repoItem := range state.Scopes.Repositories {
					repo := repoItem
					selected := st.selectedRepoKey == repo.ID
					hasMoreBelow := idx < len(state.Scopes.Repositories)-1 || len(state.Scopes.Observed) > 0

					rowWrap := ui.Row(c.Key("repos-row-"+repo.ID)).
						FillWidth().
						Padding(10, 12, 10, 16).
						Gap(6).
						AlignItems(ui.Center)
					if hasMoreBelow {
						rowWrap.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
					}
					if selected {
						rowWrap.Background(SailAccentSoft)
					} else if rowWrap.Hovered() {
						rowWrap.Background(SailBgHover)
					}

					rowWrap.Children(func() {
						selBtn := ui.ButtonBase(c.Key("repos-sel-" + repo.ID)).
							Grow(1).
							MinWidth(0).
							Gap(8).
							AlignItems(ui.Center).
							Justify(ui.SpaceBetween).
							Cursor(ui.CursorPointer)
						selBtn.Children(func() {
							ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
								nameCol := SailInkStrong
								if selected {
									nameCol = SailAccent
								}
								ui.Text(c, repo.Name).FontSize(13).FontWeight(600).TextColor(nameCol).SingleLine()
								ui.Text(c, shortRemoteHint(repo.RemoteHint)).FontSize(11.5).TextColor(SailInkMuted).SingleLine()
							})
							ui.Text(c, repoBoundServiceSummary(repo.ID, state.Scopes.Rules)).
								FontSize(11.5).
								FontFeatures("tnum").
								TextColor(SailInkMuted).
								Shrink(0).
								SingleLine()
						})
						if selBtn.Clicked() {
							st.selectedRepoKey = repo.ID
						}

						repoPath := primaryRepoPath(repo)
						if repoPath != "" && !state.Access.Shared {
							revBtn := ui.ButtonBase(c.Key("repos-reveal-"+repo.ID)).
								Size(24, 24).
								Radius(5).
								Center().
								Cursor(ui.CursorPointer).
								Label("在 Finder 中定位 " + repo.Name).
								Tooltip(repoPath)
							if revBtn.Hovered() {
								revBtn.Background(SailBgMuted)
							}
							revBtn.Children(func() {
								ui.Icon(c, clientPathArrow).Size(14, 14).TextColor(SailInkMuted)
							})
							if revBtn.Clicked() {
								u.RunAsync(func() error { return revealClientConfig(repoPath) })
							}
						}

						moreBtn := ui.ButtonBase(c.Key("repos-more-"+repo.ID)).
							Size(24, 24).
							Radius(5).
							Center().
							Cursor(ui.CursorPointer).
							Label(repo.Name + " 更多操作")
						if moreBtn.Hovered() {
							moreBtn.Background(SailBgMuted)
						}
						moreBtn.Children(func() {
							ui.Text(c, "⋯").FontSize(15).TextColor(SailInkMuted)
						})
						moreBtn.PopoverMenuButton(func(menu *ui.Menu) {
							if repoPath != "" && !state.Access.Shared {
								if menu.Item("在 Finder 中显示").Chosen() {
									u.RunAsync(func() error { return revealClientConfig(repoPath) })
								}
							}
							if menu.Item("移除仓库绑定").Chosen() {
								st.confirmDeleteRepoID = repo.ID
								st.confirmDeleteRepoName = repo.Name
								st.showDeleteConfirm = true
							}
						})
					})
				}
			}

			// 3. Observed Unbound Section: 最近请求 · 未绑定
			if len(state.Scopes.Observed) > 0 {
				ui.Row(c).
					FillWidth().
					Padding(8, 16).
					Background(SailBgSubtle).
					BorderWidth(0, 0, 1, 0).
					BorderColor(SailDivider).
					AlignItems(ui.Center).
					Justify(ui.SpaceBetween).
					Children(func() {
						ui.Text(c, "最近请求 · 未绑定").FontSize(11.5).FontWeight(600).TextColor(SailInkMuted).SingleLine()
						ui.Textf(c, "%d", len(state.Scopes.Observed)).FontSize(11.5).FontFeatures("tnum").TextColor(SailInkFaint).SingleLine()
					})

				for idx, obsItem := range state.Scopes.Observed {
					obs := obsItem
					obsKey := obs.Fingerprint
					if obsKey == "" {
						obsKey = fmt.Sprintf("obs-%d", idx)
					}
					isLast := idx == len(state.Scopes.Observed)-1
					obsRow := ui.Row(c.Key("repos-obs-"+obsKey)).
						FillWidth().
						Padding(10, 16).
						Gap(8).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween)
					if !isLast {
						obsRow.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
					}
					obsRow.Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
							obsName := strings.TrimSpace(obs.Name)
							if obsName == "" && obs.Root != "" {
								obsName = filepath.Base(obs.Root)
							}
							if obsName == "" {
								obsName = "未命名目录"
							}
							ui.Text(c, obsName).FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()

							sub := shortRemoteHint(obs.RemoteHint)
							if obs.RemoteHint == "" && obs.Root != "" {
								sub = obs.Root
							}
							if obs.LastAgent != "" {
								sub = obs.LastAgent + " · " + sub
							}
							ui.Text(c, sub).TruncateMiddle().Tooltip(obs.Root).FontSize(11).TextColor(SailInkMuted).SingleLine()
						})

						if SailSmallButton(c.Key("repos-bind-btn-"+obsKey), "绑定").Disabled(st.repoBusy).Clicked() {
							u.bindObservedRepo(st, obs)
						}
					})
				}
			}
		})
}

func (u *DesktopUI) renderReposRightColumn(
	c *ui.Context,
	st *reposPageState,
	state *apitypes.Bootstrap,
	selectedRepo *apitypes.Repository,
	previewActor string,
) {
	repoKey := st.selectedRepoKey

	ui.Column(c.Key("repos-right-" + repoKey)).Grow(1).MinWidth(0).Gap(20).Children(func() {
		// 1. Detail Header Card
		u.renderRepoDetailHeaderCard(c, st, state, selectedRepo)

		// 2. Default Services Card: (repoKey, "*")
		u.renderRepoDefaultServicesCard(c, st, state, repoKey)

		// 3. Per-Agent Overrides Card: (repoKey, agent)
		u.renderRepoAgentOverridesCard(c, st, state, repoKey)

		// 4. Five-Client Effective Preview Card (read-only)
		u.renderRepoFiveClientPreviewCard(c, st, state, repoKey, previewActor)
	})
}

func (u *DesktopUI) renderRepoDetailHeaderCard(
	c *ui.Context,
	st *reposPageState,
	state *apitypes.Bootstrap,
	selectedRepo *apitypes.Repository,
) {
	SailCard(c, func() {
		if selectedRepo == nil {
			SailCardTitle(c, "全局默认", "适用于尚未单独绑定的仓库与客户端兜底规则", func() {
				if state.Scopes.Enforce {
					SailQuietStatus(c, "强制隔离中", SailSuccessInk, SailInkStrong)
				} else {
					SailQuietStatus(c, "兼容模式", SailInkFaint, SailInkMuted)
				}
			})
			return
		}

		repo := *selectedRepo
		repoPath := primaryRepoPath(repo)

		SailCardTitle(c, repo.Name, repo.RemoteHint, func() {
			if repoPath != "" && !state.Access.Shared {
				if SailSmallButton(c.Key("detail-reveal-"+repo.ID), "在 Finder 中显示 ↗").Clicked() {
					u.RunAsync(func() error { return revealClientConfig(repoPath) })
				}
			}
			if SailDangerButton(c.Key("detail-del-"+repo.ID), "移除绑定").Clicked() {
				st.confirmDeleteRepoID = repo.ID
				st.confirmDeleteRepoName = repo.Name
				st.showDeleteConfirm = true
			}
		})

		if len(repo.GitCommonDirs) > 0 || len(repo.RootPaths) > 0 {
			SailSubtleCard(c, func() {
				if len(repo.GitCommonDirs) > 0 {
					SailKeyValueRow(c, "CommonDir", strings.Join(repo.GitCommonDirs, "\n"), true)
				}
				if len(repo.RootPaths) > 0 {
					SailKeyValueRow(c, "工作目录", strings.Join(repo.RootPaths, "\n"), true)
				}
			})
		}
	})
}

func enabledRealServers(state *apitypes.Bootstrap) []apitypes.Server {
	var out []apitypes.Server
	for _, s := range state.Registry.Servers {
		if s.Enabled && !s.Demo {
			out = append(out, s)
		}
	}
	return out
}

func enabledToolsByServer(state *apitypes.Bootstrap) map[string][]apitypes.Tool {
	m := make(map[string][]apitypes.Tool)
	for _, t := range state.Registry.Tools {
		if t.Enabled {
			m[t.ServerID] = append(m[t.ServerID], t)
		}
	}
	return m
}

func (u *DesktopUI) renderRepoDefaultServicesCard(
	c *ui.Context,
	st *reposPageState,
	state *apitypes.Bootstrap,
	repoKey string,
) {
	draft := st.ensureDraft(state.Scopes.Rules, repoKey, "*")
	_, hasSavedRule := findScopeRule(state.Scopes.Rules, repoKey, "*")
	servers := enabledRealServers(state)
	toolsBySrv := enabledToolsByServer(state)

	title := "仓库默认服务"
	if repoKey == "*" {
		title = "全局默认服务"
	}
	note := "默认无服务"
	if len(draft.grants) > 0 {
		note = fmt.Sprintf("%d 个服务", len(draft.grants))
	} else if repoKey != "*" && !hasSavedRule && !draft.dirty {
		note = "未单独设置 · 继承全局默认"
	}

	SailFlushCard(c, func() {
		SailListCardHeader(c, title, note, func() {
			u.renderAddServiceToDraftMenu(c.Key("def-add-svc-"+repoKey), draft, servers)

			if repoKey != "*" && hasSavedRule && !draft.dirty {
				if SailSmallButton(c.Key("def-inherit-"+repoKey), "恢复继承全局").Disabled(draft.saving).Clicked() {
					u.deleteScopeRuleAsync(st, repoKey, "*", "已恢复继承全局默认规则")
				}
			}
			if draft.dirty {
				if SailSmallButton(c.Key("def-reset-"+repoKey), "重置").Disabled(draft.saving).Clicked() {
					u.resetDraft(state.Scopes.Rules, draft, repoKey, "*")
				}
			}
			saveLbl := "保存"
			if draft.saving {
				saveLbl = "保存中…"
			}
			canSave := draft.dirty || (repoKey != "*" && !hasSavedRule)
			if ui.PrimaryButton(c.Key("def-save-"+repoKey), saveLbl).Disabled(!canSave || draft.saving).Clicked() {
				u.saveDraftRuleAsync(st, draft, repoKey, "*")
			}
		})

		u.renderServiceGrantList(c.Key("def-grants-"+repoKey), draft, state, toolsBySrv)
	})
}

func (u *DesktopUI) renderAddServiceToDraftMenu(
	c *ui.Context,
	draft *ruleDraft,
	servers []apitypes.Server,
) {
	added := make(map[string]bool, len(draft.grants))
	for _, g := range draft.grants {
		added[g.ServerID] = true
	}
	var available []apitypes.Server
	for _, s := range servers {
		if !added[s.ID] {
			available = append(available, s)
		}
	}

	addBtn := SailSmallButton(c.Key("add-svc-menu"), "+ 添加服务").Disabled(len(available) == 0 || draft.saving)
	if len(available) > 0 {
		addBtn.PopoverMenuButton(func(menu *ui.Menu) {
			for _, srvItem := range available {
				srv := srvItem
				if menu.Item(fmt.Sprintf("%s (%s)", srv.Name, srv.ID)).Chosen() {
					draft.grants = append(draft.grants, apitypes.ServiceGrant{
						ServerID: srv.ID,
						Access:   "ro",
					})
					draft.dirty = true
				}
			}
		})
	}
}

func (u *DesktopUI) renderServiceGrantList(
	c *ui.Context,
	draft *ruleDraft,
	state *apitypes.Bootstrap,
	toolsBySrv map[string][]apitypes.Tool,
) {
	if len(draft.grants) == 0 {
		ui.Row(c).FillWidth().Padding(18, 20).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "未授权任何上游服务（0 工具）").FontSize(12.5).TextColor(SailInkMuted)
		})
		return
	}

	serverByID := make(map[string]apitypes.Server, len(state.Registry.Servers))
	for _, s := range state.Registry.Servers {
		serverByID[s.ID] = s
	}

	for idx := range draft.grants {
		i := idx
		if i >= len(draft.grants) {
			break
		}
		g := draft.grants[i]
		srv, hasSrv := serverByID[g.ServerID]
		srvName := g.ServerID
		if hasSrv && srv.Name != "" {
			srvName = srv.Name
		}
		srvTools := toolsBySrv[g.ServerID]
		isExpanded := draft.expandedServerID == g.ServerID
		isLast := i == len(draft.grants)-1 && !isExpanded

		row := ui.Row(c.Key("grant-row-"+g.ServerID)).
			FillWidth().
			Padding(12, 20).
			Gap(14).
			AlignItems(ui.Center).
			Justify(ui.SpaceBetween)
		if !isLast {
			row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
		}

		var removeClicked bool
		row.Children(func() {
			// Anchor 1: Service Name + ID + Cleaned Description Summary
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, srvName).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					ui.Text(c, g.ServerID).Font("monospace").FontSize(11.5).TextColor(SailInkFaint).SingleLine()
					if strings.EqualFold(g.Access, "rw") {
						SailBadge(c, "RW", ToneWarning)
					}
				})
				if hasSrv && strings.TrimSpace(srv.Description) != "" {
					SailDescSummary(c, srv.Description, "", 1)
				}
			})

			// Anchor 2 & 3: Tool Subset Expander + RO/RW Toggle + Remove
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				subsetLabel := fmt.Sprintf("全部工具 (%d)", len(srvTools))
				if len(g.ToolIDs) > 0 {
					subsetLabel = fmt.Sprintf("工具子集 (%d/%d)", len(g.ToolIDs), len(srvTools))
				}
				if SailSmallButton(c.Key("subset-btn-"+g.ServerID), subsetLabel).Clicked() {
					if isExpanded {
						draft.expandedServerID = ""
					} else {
						draft.expandedServerID = g.ServerID
					}
				}

				accIdx := 0
				if strings.EqualFold(g.Access, "rw") {
					accIdx = 1
				}
				if ui.Segmented(c.Key("acc-seg-"+g.ServerID), &accIdx, "只读", "读写").
					Label(srvName + " 读写权限").
					Changed() {
					if accIdx == 1 {
						draft.grants[i].Access = "rw"
					} else {
						draft.grants[i].Access = "ro"
					}
					draft.dirty = true
				}

				if SailSmallButton(c.Key("rm-srv-"+g.ServerID), "移除").Disabled(draft.saving).Clicked() {
					removeClicked = true
				}
			})
		})

		if removeClicked {
			if draft.expandedServerID == g.ServerID {
				draft.expandedServerID = ""
			}
			draft.grants = append(draft.grants[:i], draft.grants[i+1:]...)
			draft.dirty = true
			return
		}

		if isExpanded {
			u.renderGrantToolSubsetDrawer(c.Key("subset-drawer-"+g.ServerID), draft, i, srvTools, i == len(draft.grants)-1)
		}
	}
}

func (u *DesktopUI) renderGrantToolSubsetDrawer(
	c *ui.Context,
	draft *ruleDraft,
	grantIdx int,
	srvTools []apitypes.Tool,
	isLastGrant bool,
) {
	g := &draft.grants[grantIdx]
	drawer := ui.Column(c).
		FillWidth().
		Padding(12, 20).
		Gap(10).
		Background(SailBgSubtle)
	if !isLastGrant {
		drawer.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
	}

	drawer.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(8).Children(func() {
			modeNote := "当前开放该服务下全部已启用工具"
			if len(g.ToolIDs) > 0 {
				modeNote = fmt.Sprintf("仅开放已勾选的 %d 个工具", len(g.ToolIDs))
			}
			ui.Text(c, modeNote).FontSize(12).FontWeight(500).TextColor(SailInkMuted)

			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				if len(g.ToolIDs) > 0 {
					if SailSmallButton(c.Key("subset-all"), "恢复全部工具").Clicked() {
						g.ToolIDs = nil
						draft.dirty = true
					}
				}
				if len(srvTools) > 0 {
					if SailSmallButton(c.Key("subset-ro"), "仅选只读工具").Clicked() {
						var roIDs []string
						for _, t := range srvTools {
							if t.ReadOnly {
								roIDs = append(roIDs, t.ID)
							}
						}
						g.ToolIDs = roIDs
						draft.dirty = true
					}
				}
			})
		})

		if len(srvTools) == 0 {
			ui.Text(c, "该服务暂无已启用工具").FontSize(12).TextColor(SailInkFaint)
			return
		}

		for tIdx, toolItem := range srvTools {
			t := toolItem
			checked := len(g.ToolIDs) == 0 || slices.Contains(g.ToolIDs, t.ID)
			tRow := ui.Row(c.Key("subset-tool-"+t.ID)).
				FillWidth().
				Padding(6, 0).
				Gap(10).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween)
			if tIdx < len(srvTools)-1 {
				tRow.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}
			tRow.Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.Checkbox(c.Key("chk-"+t.ID), &checked, "").Label(t.Name).Changed() {
						if len(g.ToolIDs) == 0 {
							// Transition from implicit all -> explicit all minus unchecked tool.
							next := make([]string, 0, len(srvTools))
							for _, item := range srvTools {
								if item.ID != t.ID {
									next = append(next, item.ID)
								}
							}
							g.ToolIDs = next
						} else if checked {
							if !slices.Contains(g.ToolIDs, t.ID) {
								g.ToolIDs = append(g.ToolIDs, t.ID)
							}
						} else {
							g.ToolIDs = slices.DeleteFunc(g.ToolIDs, func(id string) bool { return id == t.ID })
						}
						draft.dirty = true
					}
					ui.Text(c, t.Name).Font("monospace").FontSize(12.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					SailDescSummary(c, t.Description, "", 1)
				})

				if !t.ReadOnly {
					ui.Row(c).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
						if !strings.EqualFold(g.Access, "rw") {
							ui.Text(c, "只读已拦截").FontSize(11).TextColor(SailInkFaint).SingleLine()
						}
						SailBadge(c, "RW", ToneWarning)
					})
				}
			})
		}
	})
}

func (u *DesktopUI) renderRepoAgentOverridesCard(
	c *ui.Context,
	st *reposPageState,
	state *apitypes.Bootstrap,
	repoKey string,
) {
	servers := enabledRealServers(state)
	toolsBySrv := enabledToolsByServer(state)

	var activeAgents []scopeAgentMeta
	var availableAgents []scopeAgentMeta
	for _, ag := range scopeAgents {
		_, hasSaved := findScopeRule(state.Scopes.Rules, repoKey, ag.ID)
		d, hasDraft := st.drafts[draftMapKey(repoKey, ag.ID)]
		if hasSaved || (hasDraft && d.localOnly) {
			activeAgents = append(activeAgents, ag)
		} else {
			availableAgents = append(availableAgents, ag)
		}
	}

	note := "全部继承默认规则"
	if len(activeAgents) > 0 {
		note = fmt.Sprintf("%d 个例外", len(activeAgents))
	}

	SailFlushCard(c, func() {
		SailListCardHeader(c, "客户端例外", note, func() {
			addOverrideBtn := SailSmallButton(c.Key("add-agent-override-"+repoKey), "+ 添加客户端例外").
				Disabled(len(availableAgents) == 0)
			if len(availableAgents) > 0 {
				addOverrideBtn.PopoverMenuButton(func(menu *ui.Menu) {
					for _, agItem := range availableAgents {
						ag := agItem
						if menu.Item(ag.Name).Chosen() {
							baseRule, hasBase := findScopeRule(state.Scopes.Rules, repoKey, "*")
							var seed []apitypes.ServiceGrant
							if hasBase {
								seed = cloneGrants(baseRule.Grants)
							} else if globalRule, ok := findScopeRule(state.Scopes.Rules, "*", "*"); ok {
								seed = cloneGrants(globalRule.Grants)
							} else {
								seed = []apitypes.ServiceGrant{}
							}
							st.drafts[draftMapKey(repoKey, ag.ID)] = &ruleDraft{
								grants:    seed,
								dirty:     true,
								localOnly: true,
								expanded:  true,
							}
						}
					}
				})
			}
		})

		if len(activeAgents) == 0 {
			ui.Row(c).FillWidth().Padding(16, 20).Children(func() {
				ui.Text(c, "暂无客户端例外，5 个客户端均按默认服务规则生效").FontSize(12.5).TextColor(SailInkMuted)
			})
			return
		}

		for idx, agItem := range activeAgents {
			ag := agItem
			draft := st.ensureDraft(state.Scopes.Rules, repoKey, ag.ID)
			_, hasSaved := findScopeRule(state.Scopes.Rules, repoKey, ag.ID)
			isLast := idx == len(activeAgents)-1 && !draft.expanded

			hasRW := false
			for _, g := range draft.grants {
				if strings.EqualFold(g.Access, "rw") {
					hasRW = true
					break
				}
			}

			row := ui.Row(c.Key("ov-row-"+repoKey+"-"+ag.ID)).
				FillWidth().
				Padding(12, 20).
				Gap(12).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}

			row.Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Center).Children(func() {
					if icon, ok := clientIcons[ag.ID]; ok {
						ui.Image(c, icon).Size(20, 20).TextColor(SailInkStrong)
					}
					ui.Text(c, ag.Name).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()

					sumTxt := "无服务"
					if len(draft.grants) > 0 {
						sumTxt = fmt.Sprintf("%d 个服务", len(draft.grants))
					}
					ui.Text(c, sumTxt).FontSize(12).FontFeatures("tnum").TextColor(SailInkMuted).SingleLine()
					if hasRW {
						SailBadge(c, "RW", ToneWarning)
					}
					if draft.localOnly && !hasSaved {
						ui.Text(c, "未保存").FontSize(11.5).TextColor(SailAccent).SingleLine()
					}
				})

				ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
					expLbl := "编辑"
					if draft.expanded {
						expLbl = "收起"
					}
					if SailSmallButton(c.Key("ov-toggle-"+ag.ID), expLbl).Clicked() {
						draft.expanded = !draft.expanded
					}

					if SailSmallButton(c.Key("ov-revert-"+ag.ID), "恢复继承").Disabled(draft.saving).Clicked() {
						if !hasSaved {
							delete(st.drafts, draftMapKey(repoKey, ag.ID))
						} else {
							u.deleteScopeRuleAsync(st, repoKey, ag.ID, "已恢复 "+ag.Name+" 继承默认规则")
						}
					}
				})
			})

			if draft.expanded {
				editorWrap := ui.Column(c.Key("ov-editor-" + repoKey + "-" + ag.ID)).
					FillWidth().
					Background(SailBgSubtle)
				if idx < len(activeAgents)-1 {
					editorWrap.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
				}
				editorWrap.Children(func() {
					ui.Row(c).
						FillWidth().
						Padding(10, 20).
						BorderWidth(0, 0, 1, 0).
						BorderColor(SailDivider).
						AlignItems(ui.Center).
						Justify(ui.SpaceBetween).
						Children(func() {
							u.renderAddServiceToDraftMenu(c.Key("ov-add-svc-"+ag.ID), draft, servers)
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								if draft.dirty && hasSaved {
									if SailSmallButton(c.Key("ov-reset-"+ag.ID), "重置").Disabled(draft.saving).Clicked() {
										u.resetDraft(state.Scopes.Rules, draft, repoKey, ag.ID)
									}
								}
								saveLbl := "保存例外"
								if draft.saving {
									saveLbl = "保存中…"
								}
								if ui.PrimaryButton(c.Key("ov-save-"+ag.ID), saveLbl).
									Disabled((!draft.dirty && hasSaved) || draft.saving).
									Clicked() {
									u.saveDraftRuleAsync(st, draft, repoKey, ag.ID)
								}
							})
						})

					u.renderServiceGrantList(c.Key("ov-grants-"+ag.ID), draft, state, toolsBySrv)
				})
			}
		}
	})
}

func (u *DesktopUI) renderRepoFiveClientPreviewCard(
	c *ui.Context,
	st *reposPageState,
	state *apitypes.Bootstrap,
	repoKey string,
	previewActor string,
) {
	var rows []apitypes.ScopeAgentPreview
	if st.preview != nil && st.preview.RepoKey == repoKey && len(st.preview.Agents) > 0 {
		rows = st.preview.Agents
	} else {
		rows = computeLocalScopePreview(state, repoKey, previewActor)
	}

	SailFlushCard(c, func() {
		SailListCardHeader(c, "五端生效预览", "身份 · "+previewActor, nil)

		for idx, item := range rows {
			p := item
			isLast := idx == len(rows)-1
			row := ui.Row(c.Key("preview-row-"+p.Agent)).
				FillWidth().
				Padding(12, 20).
				Gap(12).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween)
			if !isLast {
				row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
			}

			row.Children(func() {
				// Anchor 1: Client Icon + Name
				ui.Row(c).Width(164).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
					if icon, ok := clientIcons[p.Agent]; ok {
						ui.Image(c, icon).Size(20, 20).TextColor(SailInkStrong)
					}
					ui.Text(c, agentDisplayName(p.Agent)).FontSize(13.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				})

				// Anchor 2: Rule Source + Effective Service/Tool Counts
				srcLabel := formatPreviewSourceLabel(p)
				ui.Row(c).Grow(1).MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
					srcCol := SailInkMuted
					if srcLabel == "客户端例外" {
						srcCol = SailAccent
					} else if srcLabel == "无权限" {
						srcCol = SailInkFaint
					}
					ui.Text(c, srcLabel).Width(84).Shrink(0).FontSize(12).FontWeight(500).TextColor(srcCol).SingleLine()

					countCol := SailInkBody
					if p.ToolCount == 0 {
						countCol = SailInkFaint
					}
					ui.Textf(c, "%d 服务 · %d 工具", p.ServerCount, p.ToolCount).
						FontSize(12.5).
						FontFeatures("tnum").
						TextColor(countCol).
						SingleLine()
				})

				// Anchor 3: RO/RW Risk Status + ↗ Jump to Playground
				ui.Row(c).Shrink(0).Gap(10).AlignItems(ui.Center).Children(func() {
					if p.HasWrite {
						SailBadge(c, "RW", ToneWarning)
					} else if p.ToolCount > 0 {
						ui.Text(c, "只读").FontSize(12).TextColor(SailInkMuted).SingleLine()
					} else {
						ui.Text(c, "无权限").FontSize(12).TextColor(SailInkFaint).SingleLine()
					}

					jumpBtn := ui.ButtonBase(c.Key("preview-jump-"+p.Agent)).
						Size(28, 28).
						Radius(6).
						Center().
						Cursor(ui.CursorPointer).
						Label("在路由沙盒模拟 " + agentDisplayName(p.Agent)).
						Tooltip("在路由沙盒模拟此作用域")
					if jumpBtn.Hovered() {
						jumpBtn.Background(SailBgMuted)
					}
					jumpBtn.Children(func() {
						ui.Icon(c, clientPathArrow).Size(15, 15).TextColor(SailInkMuted)
					})
					if jumpBtn.Clicked() {
						u.OpenPlaygroundScope(repoKey, p.Agent)
					}
				})
			})
		}
	})
}

// --- Manual Hook Configuration Modal (shared between ViewRepos and ViewClients) ---

func (u *DesktopUI) renderHookSnippetModal(c *ui.Context) {
	st := getReposState(u)
	if !st.showHookModal {
		return
	}

	ui.DialogBase(c.Key("hook-snippet-dialog"), &st.showHookModal, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(10, 37, 64, 0.42))
		panel.Width(660).
			MaxWidthPercent(92).
			MaxHeightPercent(88).
			Padding(22).
			Gap(14).
			Radius(10).
			Background(SailBgCanvas).
			Border(1, SailBorder).
			Shadow(0, 12, 36, 0, ui.RGBA(10, 37, 64, 0.25))

		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, "手动配置 Hook").FontSize(17).Bold().TextColor(SailInkStrong)
			if SailSmallButton(c.Key("hook-modal-close"), "关闭").Clicked() {
				st.showHookModal = false
			}
		})

		if st.hookAgentIdx < 0 || st.hookAgentIdx >= len(scopeAgents) {
			st.hookAgentIdx = 0
		}
		if ui.Segmented(
			c.Key("hook-agent-tabs"),
			&st.hookAgentIdx,
			"Codex CLI",
			"Claude Code",
			"Cursor",
			"OpenCode v2",
			"Pi",
		).FillEqually().Changed() {
			u.ensureHookSnippetLoaded(st, scopeAgents[st.hookAgentIdx].ID, false)
		}

		activeAgent := scopeAgents[st.hookAgentIdx]
		snip, hasSnip := st.snippets[activeAgent.ID]
		hookBin := snip.HookBinPath
		if hookBin == "" && u.State != nil {
			hookBin = u.State.Scopes.HookBinPath
		}

		if st.hookError != "" {
			SailAlertBox(c, ToneDanger, "获取 Hook 配置失败", st.hookError)
		}

		ui.Scroll(c.Key("hook-modal-scroll")).Grow(1).FillWidth().MaxHeight(460).Gap(12).Children(func() {
			if st.hookLoadingAgent == activeAgent.ID && !hasSnip {
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
			if SailSmallButton(c.Key("hook-refresh-btn"), "重新获取").Disabled(st.hookLoadingAgent != "").Clicked() {
				u.ensureHookSnippetLoaded(st, activeAgent.ID, true)
			}
			if ui.PrimaryButton(c.Key("hook-copy-primary"), "复制配置片段").
				Disabled(strings.TrimSpace(snip.Snippet) == "").
				Clicked() {
				c.WriteClipboard(snip.Snippet)
				u.Notify("已复制 " + activeAgent.Name + " Hook 配置")
			}
		})
	})
}

// --- Async Mutations ---

func (u *DesktopUI) toggleScopeEnforceMode(st *reposPageState, enforce bool) {
	if st.modeBusy || u.Client == nil {
		return
	}
	st.modeBusy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.SetScopeMode(ctx, enforce); err != nil {
			u.updateUI(func() { st.modeBusy = false })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.modeBusy = false
			st.previewKey = ""
		})
		if enforce {
			u.Notify("已启用仓库隔离拦截")
		} else {
			u.Notify("已切换为兼容模式")
		}
		return nil
	})
}

func (u *DesktopUI) pickAndBindLocalRepo(st *reposPageState) {
	if st.repoBusy || u.Client == nil {
		return
	}
	st.repoBusy = true
	u.RunAsync(func() error {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:    u.currentWindow(),
			Title:     "选择本地仓库目录",
			Directory: true,
		})
		if err != nil {
			u.updateUI(func() { st.repoBusy = false })
			return err
		}
		if len(paths) == 0 || strings.TrimSpace(paths[0]) == "" {
			u.updateUI(func() { st.repoBusy = false })
			return nil
		}
		chosen := strings.TrimSpace(paths[0])
		return u.saveRepoInputAsync(st, apitypes.SaveRepositoryRequest{
			Name: filepath.Base(chosen),
			Path: chosen,
		})
	})
}

func (u *DesktopUI) bindObservedRepo(st *reposPageState, obs apitypes.ObservedRepo) {
	if st.repoBusy || u.Client == nil {
		return
	}
	st.repoBusy = true
	name := strings.TrimSpace(obs.Name)
	if name == "" && obs.Root != "" {
		name = filepath.Base(obs.Root)
	}
	in := apitypes.SaveRepositoryRequest{
		Name:       name,
		RemoteHint: obs.RemoteHint,
	}
	shared := u.State != nil && u.State.Access.Shared
	if obs.GitCommonDir != "" {
		in.GitCommonDirs = []string{obs.GitCommonDir}
	} else if obs.Root != "" {
		if !shared {
			in.Path = obs.Root
		} else {
			in.RootPaths = []string{obs.Root}
		}
	}

	u.RunAsync(func() error {
		return u.saveRepoInputAsync(st, in)
	})
}

func (u *DesktopUI) saveRepoInputAsync(st *reposPageState, in apitypes.SaveRepositoryRequest) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	saved, err := u.Client.SaveRepository(ctx, in)
	if err != nil {
		u.updateUI(func() { st.repoBusy = false })
		return err
	}
	_ = u.RefreshSync()
	u.updateUI(func() {
		st.repoBusy = false
		if saved.ID != "" {
			st.selectedRepoKey = saved.ID
		}
		st.previewKey = ""
	})
	u.Notify("已绑定仓库 " + saved.Name)
	return nil
}

func (u *DesktopUI) deleteBoundRepository(st *reposPageState, repoID, repoName string) {
	if st.repoBusy || u.Client == nil || repoID == "" {
		return
	}
	st.repoBusy = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.DeleteRepository(ctx, repoID); err != nil {
			u.updateUI(func() { st.repoBusy = false })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			st.repoBusy = false
			if st.selectedRepoKey == repoID {
				st.selectedRepoKey = "*"
			}
			for k := range st.drafts {
				if strings.HasPrefix(k, repoID+"::") {
					delete(st.drafts, k)
				}
			}
			st.previewKey = ""
		})
		u.Notify("已移除仓库绑定 " + repoName)
		return nil
	})
}

func (u *DesktopUI) resetDraft(rules []apitypes.ScopeRule, draft *ruleDraft, repoKey, agentKey string) {
	if saved, ok := findScopeRule(rules, repoKey, agentKey); ok {
		draft.grants = cloneGrants(saved.Grants)
	} else {
		draft.grants = []apitypes.ServiceGrant{}
	}
	draft.dirty = false
	draft.expandedServerID = ""
}

func (u *DesktopUI) saveDraftRuleAsync(st *reposPageState, draft *ruleDraft, repoKey, agentKey string) {
	if draft.saving || u.Client == nil {
		return
	}
	draft.saving = true
	rule := apitypes.ScopeRule{
		RepoKey:  repoKey,
		AgentKey: agentKey,
		Grants:   cloneGrants(draft.grants),
	}
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		saved, err := u.Client.SaveScopeRule(ctx, rule)
		if err != nil {
			u.updateUI(func() { draft.saving = false })
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			draft.saving = false
			draft.dirty = false
			draft.localOnly = false
			draft.grants = cloneGrants(saved.Grants)
			st.previewKey = ""
		})
		u.Notify("作用域规则已保存")
		return nil
	})
}

func (u *DesktopUI) deleteScopeRuleAsync(st *reposPageState, repoKey, agentKey, toastMsg string) {
	if u.Client == nil {
		return
	}
	key := draftMapKey(repoKey, agentKey)
	if d, ok := st.drafts[key]; ok {
		d.saving = true
	}
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := u.Client.DeleteScopeRule(ctx, repoKey, agentKey); err != nil {
			u.updateUI(func() {
				if d, ok := st.drafts[key]; ok {
					d.saving = false
				}
			})
			return err
		}
		_ = u.RefreshSync()
		u.updateUI(func() {
			delete(st.drafts, key)
			st.previewKey = ""
		})
		u.Notify(toastMsg)
		return nil
	})
}
