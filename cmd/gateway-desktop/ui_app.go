package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
	"gatewaylab/internal/i18n"
)

// ViewID identifies a top-level control plane page.
type ViewID string

const (
	ViewOverview   ViewID = "overview"
	ViewServices   ViewID = "services"
	ViewCatalog    ViewID = "catalog"
	ViewRepos      ViewID = "repos"
	ViewPlayground ViewID = "playground"
	ViewLogs       ViewID = "logs"
	ViewSettings   ViewID = "settings"
	ViewClients    ViewID = "clients"
)

func viewTitle(v ViewID) string {
	switch v {
	case ViewOverview:
		return i18n.T("nav.overview")
	case ViewServices:
		return i18n.T("nav.services")
	case ViewCatalog:
		return i18n.T("nav.catalog")
	case ViewRepos:
		return i18n.T("nav.repos")
	case ViewPlayground:
		return i18n.T("nav.playground")
	case ViewLogs:
		return i18n.T("nav.logs")
	case ViewClients:
		return i18n.T("nav.clients")
	case ViewSettings:
		return i18n.T("nav.settings")
	default:
		return i18n.T("nav.overview")
	}
}

// DesktopUI owns the native mygo/ui application state, API client, routing,
// background polling, and thread-safe async updates for the desktop window.
type DesktopUI struct {
	ClientConfigs     *clientconfig.Manager
	ClientPlugins     *clientplugin.Manager
	Runtime           *gatewayRuntime
	Client            *apiclient.Client
	OnLanguageChanged func(i18n.Lang)

	window windowHost

	// Core application data (read/written on the UI thread via win.Update).
	State     *apitypes.Bootstrap
	Telemetry GatewayTelemetry
	ViewMode  ViewID

	// Encapsulated cross-page navigation & handoff intents.
	nav navIntent

	// Per-page UI state owned directly by this DesktopUI instance.
	overview   *overviewLocalState
	services   *servicesPageState
	market     *marketPageState
	catalog    *catalogPageState
	repos      *reposPageState
	playground *playgroundState
	logs       *logsPageState
	settings   *settingsState
	clientsPageState
	hookModal *hookSnippetModalState
	credCache serverCredentialCache

	// Auth & bootstrap lifecycle state.
	sessionState

	// Per-page scroll state so switching tabs preserves scroll position.
	scrollStates map[ViewID]*ui.ScrollState

	// Pending toast messages and headless frame synchronization.
	toasts   toastQueue
	headless headlessSync
}

// NewDesktopUI constructs a DesktopUI wired to the given gatewayRuntime.
func NewDesktopUI(rt *gatewayRuntime) *DesktopUI {
	uiURL := ""
	if rt != nil {
		uiURL = rt.UIURL
	}
	d := &DesktopUI{
		Runtime:      rt,
		Client:       apiclient.New(uiURL),
		ViewMode:     ViewOverview,
		sessionState: sessionState{Loading: true},
		scrollStates: make(map[ViewID]*ui.ScrollState),
	}
	home, err := os.UserHomeDir()
	configEnv := os.Getenv
	if testHome := os.Getenv("GATEWAY_CLIENT_CONFIG_HOME"); testHome != "" {
		home, err = testHome, nil
		configEnv = func(string) string { return "" }
	}
	if err == nil {
		d.ClientConfigs = clientconfig.New(home, filepath.Join(home, "."+buildinfo.Current().Slug, "client-config"), configEnv)
		dataDir := ""
		if rt != nil {
			dataDir = rt.DataDir
		}
		d.ClientPlugins = clientplugin.New(clientplugin.Options{
			HomeDir:       home,
			StateDir:      filepath.Join(home, "."+buildinfo.Current().Slug, "plugins"),
			DataDir:       dataDir,
			Getenv:        configEnv,
			ClientConfigs: d.ClientConfigs,
		})
	} else {
		d.ClientConfigError = err.Error()
		d.PluginError = err.Error()
	}
	d.nav.setView(ViewOverview)
	return d
}

// AttachWindow binds the mygo.Window to DesktopUI, triggers initial bootstrap,
// and starts a low-overhead background refresh loop.
func (u *DesktopUI) AttachWindow(win *mygo.Window) {
	pollCtx := u.window.attach(win)

	u.Refresh()
	u.refreshClientConfigs()

	go u.pollLoop(pollCtx)
	if u.ClientPlugins != nil && os.Getenv("GATEWAY_CLIENT_CONFIG_HOME") == "" {
		go u.ClientPlugins.WatchUpdates(pollCtx, func() {
			u.updateUI(func() { u.refreshClientConfigs() })
		})
	}
}

// DetachWindow stops background polling when the window is destroyed.
func (u *DesktopUI) DetachWindow() {
	u.window.detach()
}

// CurrentView returns the active page ID safely from any goroutine.
func (u *DesktopUI) CurrentView() ViewID {
	return u.nav.currentView()
}

func (u *DesktopUI) currentWindow() *mygo.Window {
	return u.window.current()
}

// updateUI executes fn on the main UI thread and triggers a window redraw when attached.
func (u *DesktopUI) updateUI(fn func()) {
	if win := u.window.current(); win != nil {
		win.Update(fn)
		return
	}
	u.headless.runOrQueue(fn)
}

func (u *DesktopUI) enterHeadlessFrame() func() {
	if u.window.current() != nil {
		return func() {}
	}
	return u.headless.enterFrame()
}

// Notify queues a toast message and wakes the UI to display it.
// Safe to call from both UI handlers and background goroutines.
func (u *DesktopUI) Notify(msg string) {
	if u.toasts.push(msg) {
		u.window.wake()
	}
}

func (u *DesktopUI) drainToasts(c *ui.Context) {
	u.toasts.drain(c)
}

// RunAsync executes fn in a background goroutine, tracks busy state,
// surfaces errors via Notify (or transitions to NeedsLogin on 401),
// and triggers a thread-safe window redraw on completion.
func (u *DesktopUI) RunAsync(fn func() error) {
	u.updateUI(func() {
		u.beginBusy()
	})
	go func() {
		err := fn()
		u.updateUI(func() {
			u.endBusy()
			if err != nil {
				if apiclient.IsUnauthorized(err) {
					u.markUnauthorized()
					u.State = nil
				} else {
					u.toasts.push(err.Error())
				}
			}
		})
	}()
}

// Refresh fetches the latest bootstrap state asynchronously and updates the UI.
func (u *DesktopUI) Refresh() {
	if u.Client == nil {
		return
	}
	u.updateUI(func() {
		u.beginRefresh(u.State != nil)
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		data, tel, creds, err := u.fetchBootstrapSnapshot(ctx)
		u.updateUI(func() {
			u.finishRefresh()
			if err != nil {
				if apiclient.IsUnauthorized(err) {
					u.markUnauthorized()
					u.State = nil
				} else if u.State == nil {
					u.BootError = err.Error()
				} else {
					u.toasts.push(i18n.T("app.toast.refresh_failed", err.Error()))
				}
				return
			}
			u.applyBootstrapSnapshot(data, tel, creds)
		})
	}()
}

// RefreshSync fetches bootstrap synchronously (suitable inside RunAsync) and updates UI state.
func (u *DesktopUI) RefreshSync() error {
	if u.Client == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	data, tel, creds, err := u.fetchBootstrapSnapshot(ctx)
	if err != nil {
		return err
	}
	u.updateUI(func() {
		u.applyBootstrapSnapshot(data, tel, creds)
	})
	return nil
}

func (u *DesktopUI) fetchBootstrapSnapshot(ctx context.Context) (apitypes.Bootstrap, GatewayTelemetry, map[string]apitypes.ServerCredentials, error) {
	data, err := u.Client.Bootstrap(ctx)
	if err != nil {
		return apitypes.Bootstrap{}, GatewayTelemetry{}, nil, err
	}
	tel := ComputeGatewayTelemetry(data)
	creds := u.fetchReferencedServerCredentials(ctx, data.Registry.Servers)
	return data, tel, creds, nil
}

func (u *DesktopUI) applyBootstrapSnapshot(data apitypes.Bootstrap, tel GatewayTelemetry, creds map[string]apitypes.ServerCredentials) {
	u.State = &data
	u.Telemetry = tel
	u.applyServerCredentialsSnapshot(data.Registry.Servers, creds)
	u.markAuthenticated()
}

func (u *DesktopUI) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.pollOnce(ctx)
		}
	}
}

func (u *DesktopUI) pollOnce(ctx context.Context) {
	if u.window.isHidden() || u.NeedsLogin || u.Client == nil {
		return
	}
	view := u.CurrentView()
	if view == ViewClients {
		u.updateUI(func() { u.refreshClientConfigs() })
		return
	}
	if view != ViewOverview && view != ViewServices && view != ViewLogs && view != ViewRepos {
		return
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	data, tel, creds, err := u.fetchBootstrapSnapshot(reqCtx)
	cancel()
	if err != nil {
		if apiclient.IsUnauthorized(err) {
			u.updateUI(func() {
				u.markUnauthorized()
				u.State = nil
			})
		}
		return
	}
	u.updateUI(func() {
		u.applyBootstrapSnapshot(data, tel, creds)
	})
}

// --- Cross-page navigation helpers ---

func (u *DesktopUI) Navigate(view ViewID) {
	if view == ViewClients {
		u.refreshClientConfigs()
	}
	u.ViewMode = view
	u.nav.navigate(view)
}

func (u *DesktopUI) OpenServiceDetail(serverID string) {
	u.ViewMode = ViewServices
	u.nav.openService(serverID)
}

func (u *DesktopUI) activeServiceID() string {
	return u.nav.serviceID
}

func (u *DesktopUI) clearActiveService() {
	u.nav.serviceID = ""
}

func (u *DesktopUI) OpenAddServiceModal() {
	u.ViewMode = ViewServices
	u.nav.openAddService()
}

func (u *DesktopUI) consumePendingAddService() bool {
	return u.nav.consumeAddService()
}

func (u *DesktopUI) OpenCatalogFilter(serverID, access string) {
	u.ViewMode = ViewCatalog
	u.nav.openCatalogFilter(serverID, access)
}

func (u *DesktopUI) consumePendingCatalogFilter() (string, string, bool) {
	intent, ok := takeIntent(&u.nav.catalogFilter)
	if !ok {
		return "", "", false
	}
	return intent.serverID, intent.access, true
}

// OpenCatalogTool navigates to the Catalog page focused on a specific tool and service.
func (u *DesktopUI) OpenCatalogTool(toolID, serverID string) {
	u.ViewMode = ViewCatalog
	u.nav.openCatalogTool(toolID, serverID)
}

func (u *DesktopUI) consumePendingCatalogTool() (string, string, bool) {
	intent, ok := takeIntent(&u.nav.catalogTool)
	if !ok {
		return "", "", false
	}
	return intent.toolID, intent.serverID, true
}

func (u *DesktopUI) OpenPlaygroundReplay(intent, actor string, scope ...string) {
	u.ViewMode = ViewPlayground
	u.nav.openReplay(intent, actor, scope...)
}

func (u *DesktopUI) consumePendingReplay() (playgroundReplayIntent, bool) {
	return takeIntent(&u.nav.replay)
}

func (u *DesktopUI) OpenRepos(repoKey string) {
	u.ViewMode = ViewRepos
	u.nav.openRepos(repoKey)
}

func (u *DesktopUI) consumePendingRepoKey() string {
	return u.nav.consumeRepoKey()
}

func (u *DesktopUI) OpenLogsEvent(eventID string) {
	u.ViewMode = ViewLogs
	u.nav.openLogsEvent(eventID)
}

func (u *DesktopUI) consumePendingEventID() string {
	return u.nav.consumeEventID()
}

func (u *DesktopUI) OpenClients() {
	u.Navigate(ViewClients)
}

func (u *DesktopUI) scrollState(view ViewID) *ui.ScrollState {
	if u.scrollStates == nil {
		u.scrollStates = make(map[ViewID]*ui.ScrollState)
	}
	s, ok := u.scrollStates[view]
	if !ok {
		s = &ui.ScrollState{}
		u.scrollStates[view] = s
	}
	return s
}

func (u *DesktopUI) submitLogin() {
	token, ok := u.prepareLogin(u.Client != nil)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := u.Client.Login(ctx, token)
		if err != nil {
			u.updateUI(func() { u.failLogin(err) })
			return
		}
		data, tel, creds, bootErr := u.fetchBootstrapSnapshot(ctx)
		u.updateUI(func() {
			u.LoggingIn = false
			if bootErr != nil {
				u.LoginError = bootErr.Error()
				return
			}
			u.applyBootstrapSnapshot(data, tel, creds)
		})
	}()
}

func (u *DesktopUI) logout() {
	if u.Client == nil {
		return
	}
	u.RunAsync(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := u.Client.Logout(ctx); err != nil {
			return err
		}
		u.updateUI(func() {
			u.State = nil
			u.NeedsLogin = true
		})
		return nil
	})
}

// --- Root View ---

// View renders the entire native mygo/ui window.
func (u *DesktopUI) View(c *ui.Context) {
	defer u.enterHeadlessFrame()()
	applySailTheme(c)
	u.window.setHidden(c.WindowHidden())
	u.drainToasts(c)

	if u.NeedsLogin {
		u.renderLoginView(c)
		u.drainToasts(c)
		return
	}

	if u.State == nil {
		u.renderBootView(c)
		u.drainToasts(c)
		return
	}

	bar := c.TitleBar()

	ui.Row(c).Fill().AlignItems(ui.Stretch).Background(SailBgCanvas).Children(func() {
		u.renderSidebar(c, bar)
		u.renderMainWorkspace(c, bar)
	})

	u.drainToasts(c)
}

// renderBrandLogo renders the rounded full-color brand mark.
func renderBrandLogo(c *ui.Context, size, radius float32) {
	ui.Box(c).
		Size(size, size).
		Radius(radius).
		Border(1, SailSurfaceBorder).
		Clip().
		Center().
		Shrink(0).
		Children(func() {
			ui.Image(c, IconLogo).Size(size, size)
		})
}

func (u *DesktopUI) renderLoginView(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c).Fill().Background(SailBgSidebar).Children(func() {
		if bar.Height > 0 {
			ui.Box(c).FillWidth().Height(bar.Height).DragWindow()
		}
		ui.Column(c).Grow(1).FillWidth().Center().Padding(24).Children(func() {
			applyCardElevation(
				ui.Column(c).
					Width(380).
					Padding(28).
					Gap(16).
					Radius(10).
					Background(SailBgSurface),
			).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					renderBrandLogo(c, 32, 8)
					ui.Text(c, buildinfo.Current().Name).FontSize(16).Bold().TextColor(SailInkStrong)
				})

				if u.LoginError != "" {
					SailAlertBox(c, ToneDanger, i18n.T("app.login.failed_title"), u.LoginError)
				}

				ui.Column(c).Gap(6).FillWidth().Children(func() {
					ui.Text(c, i18n.T("app.login.token_label")).FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
					if ui.TextInput(c.Key("login-token"), &u.LoginToken).
						Password().
						AutoFocus().
						Placeholder(i18n.T("app.login.token_placeholder")).
						FillWidth().
						Submitted() {
						u.submitLogin()
					}
				})

				if SailPrimaryButton(c, i18n.T("app.login.submit")).FillWidth().Disabled(u.LoggingIn || strings.TrimSpace(u.LoginToken) == "").Clicked() {
					u.submitLogin()
				}
			})
		})
	})
}

func (u *DesktopUI) renderBootView(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c).Fill().Background(SailBgSidebar).Children(func() {
		if bar.Height > 0 {
			ui.Box(c).FillWidth().Height(bar.Height).DragWindow()
		}
		ui.Column(c).Grow(1).FillWidth().Center().Padding(24).Children(func() {
			ui.Column(c).
				Width(380).
				Padding(28).
				Gap(14).
				Radius(10).
				Background(SailBgSurface).
				Border(1, SailBorder).
				AlignItems(ui.Center).
				Children(func() {
					renderBrandLogo(c, 36, 8)
					if u.BootError != "" {
						ui.Text(c, i18n.T("app.connect.failed_title")).FontSize(15.5).Bold().TextColor(SailInkStrong)
						ui.Text(c, u.BootError).FontSize(12.5).TextColor(SailInkMuted).TextAlign(ui.Center)
						if SailPrimaryButton(c, i18n.T("app.connect.retry")).Clicked() {
							u.Refresh()
						}
					} else {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Spinner(c).Label(i18n.T("app.connect.loading_label"))
							ui.Text(c, i18n.T("app.connect.loading")).FontSize(13.5).TextColor(SailInkMuted)
						})
					}
				})
		})
	})
}

func countRealRegistryStats(reg apitypes.Registry) (activeServers, totalTools int) {
	realServerSet := make(map[string]bool)
	for _, s := range reg.Servers {
		if s.Demo {
			continue
		}
		realServerSet[s.ID] = true
		if s.Enabled {
			activeServers++
		}
	}
	for _, t := range reg.Tools {
		if realServerSet[t.ServerID] {
			totalTools++
		}
	}
	return activeServers, totalTools
}

func (u *DesktopUI) renderSidebar(c *ui.Context, bar ui.TitleBar) {
	state := u.State
	activeRealServers, realToolCount := countRealRegistryStats(state.Registry)

	topPad := bar.Bottom + 10
	if topPad < 16 {
		topPad = 16
	}

	ui.Column(c).
		Width(228).
		Shrink(0).
		FillHeight().
		Background(SailBgSidebar).
		BorderWidth(0, 1, 0, 0).
		BorderColor(SailBorder).
		Children(func() {
			// Top draggable brand header clearing macOS traffic lights.
			ui.Row(c).
				FillWidth().
				Padding(topPad, 16, 14, 16).
				Gap(9).
				AlignItems(ui.Center).
				DragWindow().
				Children(func() {
					renderBrandLogo(c, 24, 6)
					ui.Text(c, buildinfo.Current().Name).FontSize(13.5).Bold().TextColor(SailInkStrong).SingleLine()
				})

			// Scrollable navigation sections.
			ui.Scroll(c).Grow(1).FillWidth().Padding(4, 10, 10, 10).Gap(18).Children(func() {
				u.renderNavGroup(c, i18n.T("nav.group.workspace"), []navItem{
					{id: ViewOverview, label: i18n.T("nav.overview"), icon: IconHome},
					servicesNavItem(activeRealServers, u.servicesNeedingAttention()),
					{id: ViewCatalog, label: i18n.T("nav.catalog"), icon: IconLayers, badge: fmt.Sprintf("%d/%d", state.Setup.EnabledTools, realToolCount)},
					{id: ViewRepos, label: i18n.T("nav.repos"), icon: IconFolderGit, badge: reposNavBadge(state.Scopes)},
					{id: ViewClients, label: i18n.T("nav.clients"), icon: IconServer, badge: u.clientsNavBadge()},
					{id: ViewSettings, label: i18n.T("nav.settings"), icon: IconShield},
				})
				u.renderNavGroup(c, i18n.T("nav.group.observability"), []navItem{
					{id: ViewLogs, label: i18n.T("nav.logs"), icon: IconActivity, badge: countBadge(len(state.Events))},
					{id: ViewPlayground, label: i18n.T("nav.playground"), icon: IconWaypoints},
				})
			})

			ui.Box(c).FillWidth().Height(1).Background(SailDivider)
			u.renderSidebarLanguageSwitcher(c)

		})
}

type navItem struct {
	id    ViewID
	label string
	icon  *ui.SVG
	badge string
	// badgeDanger renders the badge in danger ink, e.g. services needing action.
	badgeDanger bool
}

// servicesNavItem shows how many services need action when any do, otherwise
// how many are enabled, so the sidebar count never reads as "all healthy".
func servicesNavItem(enabled, attention int) navItem {
	item := navItem{id: ViewServices, label: i18n.T("nav.services"), icon: IconServer, badge: countBadge(enabled)}
	if attention > 0 {
		item.badge = countBadge(attention)
		item.badgeDanger = true
	}
	return item
}

func countBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

func reposNavBadge(scopes apitypes.ScopeOverview) string {
	if len(scopes.Repositories) > 0 {
		return countBadge(len(scopes.Repositories))
	}
	if len(scopes.Observed) > 0 {
		return fmt.Sprintf("+%d", len(scopes.Observed))
	}
	return ""
}

func (u *DesktopUI) renderNavGroup(c *ui.Context, heading string, items []navItem) {
	ui.Column(c).FillWidth().Gap(2).Children(func() {
		ui.Text(c, heading).
			Padding(2, 10).
			FontSize(11).
			FontWeight(600).
			TextColor(SailInkFaint)
		for _, item := range items {
			u.renderNavItem(c, item)
		}
	})
}

func (u *DesktopUI) renderNavItem(c *ui.Context, item navItem) {
	active := u.ViewMode == item.id
	btn := ui.ButtonBase(c.Key("nav-"+string(item.id))).
		FillWidth().
		Padding(7, 10).
		Radius(6).
		Gap(9).
		AlignItems(ui.Center).
		Cursor(ui.CursorPointer)
	if active {
		applyControlElevation(
			btn.Background(SailBgSurface).
				Border(SailElevationBorderWidth, SailSurfaceBorder),
			true,
		)
	} else if btn.Hovered() {
		btn.Background(SailBgMuted)
	}

	iconColor, textColor := SailInkMuted, SailInkBody
	lblWeight := 500
	if active {
		iconColor, textColor = SailAccent, SailInkStrong
		lblWeight = 600
	}

	btn.Children(func() {
		ui.Icon(c, item.icon).Size(15, 15).TextColor(iconColor).Shrink(0)
		ui.Text(c, item.label).Grow(1).FontSize(13).FontWeight(lblWeight).TextColor(textColor).SingleLine()
		if item.badge != "" {
			badgeInk, badgeWeight := SailInkFaint, 500
			if active {
				badgeInk = SailAccent
			}
			if item.badgeDanger {
				badgeInk, badgeWeight = SailDangerInk, 600
			}
			ui.Text(c, item.badge).
				FontSize(11.5).
				FontWeight(badgeWeight).
				FontFeatures("tnum").
				TextColor(badgeInk).
				SingleLine()
		}
	})

	if btn.Clicked() {
		u.Navigate(item.id)
	}
}

func (u *DesktopUI) activeBreadcrumbServerName(state *apitypes.Bootstrap) string {
	if u.ViewMode != ViewServices || u.nav.serviceID == "" {
		return ""
	}
	for _, s := range state.Registry.Servers {
		if s.ID == u.nav.serviceID {
			return s.Name
		}
	}
	return ""
}

func (u *DesktopUI) renderMainWorkspace(c *ui.Context, bar ui.TitleBar) {
	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(SailBgCanvas).Children(func() {
		u.renderTopBar(c, bar)

		// Main scrollable page viewport with generous breathing room.
		ui.Scroll(c.Key("main-scroll-"+string(u.ViewMode))).
			TrackScroll(u.scrollState(u.ViewMode)).
			Grow(1).
			FillWidth().
			Padding(28, 32).
			Children(func() {
				ui.Column(c).FillWidth().MaxWidth(1200).Gap(24).Children(func() {
					u.renderActivePage(c)
				})
			})
	})
}

func (u *DesktopUI) renderTopBar(c *ui.Context, bar ui.TitleBar) {
	state := u.State
	topH := bar.Height
	if topH < 50 {
		topH = 50
	}
	activeServerName := u.activeBreadcrumbServerName(state)

	// Top Breadcrumb & Status Bar (draggable in empty spaces).
	ui.Row(c).
		FillWidth().
		Height(topH).
		Padding(0, 32).
		Gap(12).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Background(SailBgCanvas).
		BorderWidth(0, 0, 1, 0).
		BorderColor(SailDivider).
		DragWindow().
		Children(func() {
			u.renderBreadcrumbs(c, activeServerName)
			u.renderTopControls(c, state)
		})
}

func (u *DesktopUI) renderBreadcrumbs(c *ui.Context, activeServerName string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		if activeServerName == "" {
			ui.Text(c, viewTitle(u.ViewMode)).FontSize(13).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			return
		}
		svcLink := ui.ButtonBase(c.Key("bc-services")).Padding(2, 4).Radius(4).Cursor(ui.CursorPointer)
		if svcLink.Hovered() {
			svcLink.Background(SailBgHover)
		}
		svcLink.Children(func() {
			ui.Text(c, i18n.T("app.topbar.services")).FontSize(13).TextColor(SailInkMuted).SingleLine()
		})
		if svcLink.Clicked() {
			u.Navigate(ViewServices)
		}
		ui.Text(c, "/").FontSize(13).TextColor(SailInkFaint)
		ui.Text(c, activeServerName).FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
	})
}

func (u *DesktopUI) renderTopControls(c *ui.Context, state *apitypes.Bootstrap) {
	modelReady := state.Setup.ModelReady && (state.Setup.ModelCheck == nil || !state.Setup.ModelCheck.Stale)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		modelChip := ui.ButtonBase(c.Key("top-model-chip")).
			Height(SailControlHeightSm).
			Padding(0, 10).
			Radius(6).
			Gap(6).
			AlignItems(ui.Center).
			Cursor(ui.CursorPointer).
			Tooltip(i18n.T("app.topbar.routing_tooltip"))
		if modelChip.Hovered() {
			modelChip.Background(SailBgHover)
		}
		modelChip.Children(func() {
			dotColor := SailWarningInk
			if modelReady {
				dotColor = SailSuccessInk
			}
			SailStatusDot(c, dotColor)
			modelName := state.Settings.Model
			if modelName == "" {
				modelName = i18n.T("app.topbar.no_model")
			}
			ui.Text(c, modelName).Font("monospace").FontSize(12).TextColor(SailInkBody).SingleLine()
		})
		if modelChip.Clicked() {
			u.Navigate(ViewSettings)
		}

		refreshBtn := SailButton(c.Key("top-refresh-btn"), "").
			Height(SailControlHeightSm).
			Padding(0, 10).
			Radius(6).
			Gap(5).
			AlignItems(ui.Center).
			Disabled(u.Refreshing).
			Cursor(ui.CursorPointer)
		refreshBtn.Children(func() {
			ui.Icon(c, IconRefresh).Size(12.5, 12.5).TextColor(SailInkMuted)
			lbl := i18n.T("app.topbar.refresh")
			if u.Refreshing {
				lbl = i18n.T("app.topbar.syncing")
			}
			ui.Text(c, lbl).FontSize(12).TextColor(SailInkBody).SingleLine()
		})
		if refreshBtn.Clicked() {
			u.Refresh()
		}

		if state.Access.Shared {
			ui.Text(c, state.Access.Actor).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
			if SailSmallButton(c.Key("top-logout-btn"), i18n.T("app.topbar.logout")).Clicked() {
				u.logout()
			}
		}
	})
}

func (u *DesktopUI) renderActivePage(c *ui.Context) {
	switch u.ViewMode {
	case ViewOverview:
		u.renderOverview(c)
	case ViewServices:
		u.renderServices(c)
	case ViewCatalog:
		u.renderCatalog(c)
	case ViewRepos:
		u.renderRepos(c)
	case ViewPlayground:
		u.renderPlayground(c)
	case ViewLogs:
		u.renderLogs(c)
	case ViewClients:
		u.renderClients(c)
	case ViewSettings:
		u.renderSettings(c)
	default:
		u.renderOverview(c)
	}
}
