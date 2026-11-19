package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"

	"github.com/egoist/mygo/ui"
)

const (
	repoMatrixRepoWidth       float32 = 240
	repoMatrixMinServiceWidth float32 = 116
	repoMatrixActionWidth     float32 = 80
)

// Matrix cells resolve the effective repository rule (explicit repo rule or global default).
func repoMatrixGrants(scopes apitypes.ScopeOverview, repoKey string) []apitypes.ServiceGrant {
	grants, _, _ := resolveEffectiveScopeRule(scopes, repoKey)
	return grants
}

func (u *DesktopUI) renderReposMatrix(c *ui.Context, st *reposPageState, state *apitypes.Bootstrap) {
	var servers []apitypes.Server
	for _, s := range state.Registry.Servers {
		if !s.Demo {
			servers = append(servers, s)
		}
	}
	market := u.marketState()
	if len(servers) > 0 && !market.loaded && !market.loading && u.Client != nil {
		u.fetchMarket(market, "")
	}
	for i, obs := range state.Scopes.Observed {
		ui.Row(c.Key(fmt.Sprintf("observed-%d", i))).FillWidth().Padding(10, 16).Gap(12).
			AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(SailDivider).Children(func() {
			ui.Text(c, i18n.T("ops.recent-request-unbound")).FontSize(12).TextColor(SailInkMuted).Shrink(0)
			ui.Text(c, obs.Name).FontSize(13).TextColor(SailInkStrong).Grow(1).MinWidth(0).SingleLine().Tooltip(obs.Root)
			if SailSmallButton(c, i18n.T("ops.bind-and-add")).Disabled(st.repoBusy).Clicked() {
				u.bindObservedRepo(st, obs)
			}
		})
	}
	if len(servers) == 0 {
		ui.Text(c, i18n.T("ops.no-mcp-services")).FontSize(13).TextColor(SailInkMuted)
	}
	// Keep header and cells in one horizontal scroller so every column stays aligned.
	SailFlushCard(c, func() {
		scroll := ui.ScrollHorizontal(c.Key("scroll")).FillWidth()
		viewportWidth := scroll.Bounds().W
		if viewportWidth == 0 {
			c.Invalidate()
		}
		serviceWidth := repoMatrixMinServiceWidth
		if len(servers) > 0 && viewportWidth > 0 {
			avail := viewportWidth - repoMatrixRepoWidth - repoMatrixActionWidth
			if perCol := avail / float32(len(servers)); perCol > serviceWidth {
				serviceWidth = perCol
			}
		}
		width := repoMatrixRepoWidth + float32(len(servers))*serviceWidth + repoMatrixActionWidth
		width = max(width, viewportWidth)
		scroll.Children(func() {
			ui.Column(c).Width(width).Shrink(0).Children(func() {
				ui.Row(c).FillWidth().Height(46).AlignItems(ui.Center).Background(SailBgSubtle).
					BorderWidth(0, 0, 1, 0).BorderColor(SailDivider).Children(func() {
					ui.Text(c, i18n.T("ops.repo-label")).Width(repoMatrixRepoWidth).Shrink(0).Padding(0, 16).FontSize(12).FontWeight(600).TextColor(SailInkMuted)
					for _, s := range servers {
						ui.Row(c.Key("header-"+s.ID)).Width(serviceWidth).Shrink(0).Padding(0, 10).Gap(7).
							AlignItems(ui.Center).Justify(ui.Center).Children(func() {
							u.renderRepoMatrixServiceIcon(c, market, s)
							ui.Text(c, s.Name).Shrink(1).MinWidth(0).SingleLine().Tooltip(s.Name).FontSize(12).FontWeight(600).TextColor(SailInkStrong)
						})
					}
					ui.Row(c).Grow(1).MinWidth(repoMatrixActionWidth).Shrink(0)
				})
				totalRows := 1 + len(state.Scopes.Repositories)
				renderRow := func(rowIndex int, repoKey, name, hint string) {
					draft := st.ensureDraft(state.Scopes.Rules, repoKey)
					grants := repoMatrixGrants(state.Scopes, repoKey)
					row := ui.Row(c.Key("row-" + repoKey)).FillWidth().Height(64).AlignItems(ui.Center)
					if rowIndex < totalRows-1 {
						row.BorderWidth(0, 0, 1, 0).BorderColor(SailDivider)
					}
					row.Children(func() {
						ui.Column(c).Width(repoMatrixRepoWidth).Shrink(0).Padding(0, 16).Gap(3).Children(func() {
							ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine().Tooltip(name)
							ui.Text(c, hint).FontSize(11.5).TextColor(SailInkMuted).SingleLine().Tooltip(hint)
						})
						for _, s := range servers {
							checked := slices.ContainsFunc(grants, func(g apitypes.ServiceGrant) bool { return g.ServerID == s.ID })
							ui.Row(c.Key("cell-" + s.ID)).Width(serviceWidth).Shrink(0).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
								checkbox := ui.Checkbox(c, &checked, "").Label(name + " · " + s.Name).
									Disabled(u.Client == nil || draft.saving || draft.dirty)
								if draft.dirty {
									checkbox.Tooltip(i18n.T("ops.save-or-reset-pending"))
								}
								if checkbox.Changed() {
									u.toggleRepoMatrixGrant(st, repoKey, s.ID, checked)
								}
							})
						}
						ui.Row(c).Grow(1).MinWidth(repoMatrixActionWidth).Shrink(0).Padding(0, 16).AlignItems(ui.Center).Justify(ui.End).Children(func() {
							if SailSmallButton(c, i18n.T("ops.details")).Label(i18n.T("ops.details-for", name)).Clicked() {
								st.selectedRepoKey = repoKey
								st.viewMode = 1
							}
						})
					})
				}
				renderRow(0, "*", i18n.T("ops.global-default-star"), i18n.T("ops.unbound-fallback"))
				for i, repo := range state.Scopes.Repositories {
					hint := shortRemoteHint(repo.RemoteHint)
					if _, ok := findScopeRule(state.Scopes.Rules, repo.ID); !ok {
						hint += i18n.T("ops.inherits-global-default")
					}
					renderRow(i+1, repo.ID, repo.Name, hint)
				}
			})
		})
	})
}

func (u *DesktopUI) renderRepoMatrixServiceIcon(c *ui.Context, st *marketPageState, server apitypes.Server) {
	if icon, ok := serviceIcons[server.ID]; ok {
		ui.Image(c, icon).Size(16, 16).Shrink(0)
		return
	}
	for _, item := range st.items {
		if item.ServerID != server.ID {
			continue
		}
		entry := u.marketIcon(st, item)
		if entry != nil && entry.img != nil {
			ui.Image(c, entry.img).Size(16, 16).Shrink(0)
			return
		}
		if entry != nil && entry.svg != nil {
			ui.Icon(c, entry.svg).Size(16, 16).Shrink(0)
			return
		}
		break
	}
	ui.Icon(c, IconServer).Size(16, 16).Shrink(0).TextColor(SailInkMuted)
}

func (u *DesktopUI) toggleRepoMatrixGrant(st *reposPageState, repoKey, serverID string, enabled bool) {
	if u.Client == nil || u.State == nil {
		return
	}
	draft := st.ensureDraft(u.State.Scopes.Rules, repoKey)
	if draft.saving || draft.dirty {
		return
	}
	// Materialize inherited defaults before editing a single cell. An empty rule
	// must remain explicit so disabling the last service cannot restore inheritance.
	grants := cloneGrants(repoMatrixGrants(u.State.Scopes, repoKey))
	grants = slices.DeleteFunc(grants, func(g apitypes.ServiceGrant) bool { return g.ServerID == serverID })
	if enabled {
		grants = append(grants, apitypes.ServiceGrant{ServerID: serverID, Access: "ro"})
	}
	draft.saving = true
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := u.Client.SaveScopeRule(ctx, apitypes.ScopeRule{RepoKey: repoKey, Grants: grants})
		if err == nil {
			err = u.RefreshSync()
		}
		u.updateUI(func() { draft.saving = false })
		return err
	})
}
