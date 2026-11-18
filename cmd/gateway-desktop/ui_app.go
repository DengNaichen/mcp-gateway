package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apiclient"
	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin"
)

// ViewID identifies a top-level control plane page.
type ViewID string

const (
	ViewOverview   ViewID = "overview"
	ViewServices   ViewID = "services"
	ViewMarket     ViewID = "market"
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
		return "概览"
	case ViewServices:
		return "上游服务"
	case ViewMarket:
		return "MCP 市场"
	case ViewCatalog:
		return "工具目录"
	case ViewRepos:
		return "仓库绑定"
	case ViewPlayground:
		return "路由沙盒"
	case ViewLogs:
		return "请求日志"
	case ViewClients:
		return "连接客户端"
	case ViewSettings:
		return "访问与路由"
	default:
		return "概览"
	}
}

type catalogFilterIntent struct {
	serverID string
	access   string // "", "all", "ro", "rw"
}

type catalogToolIntent struct {
	toolID   string
	serverID string
}

type playgroundReplayIntent struct {
	intent   string
	actor    string
	repoKey  string
	hasScope bool
}

// DesktopUI owns the native mygo/ui application state, API client, routing,
// background polling, and thread-safe async updates for the desktop window.
type DesktopUI struct {
	ClientConfigs     *clientconfig.Manager
	ClientStatuses    []clientconfig.Status
	ClientConfigError string
	ClientsBusy       bool
	ClientPlugins     *clientplugin.Manager
	PluginStatuses    []clientplugin.Status
	PluginError       string
	PluginsBusy       bool
	Runtime           *gatewayRuntime
	Client            *apiclient.Client

	// Current window reference (guarded by mu for background goroutines).
	mu        sync.Mutex
	Win       *mygo.Window
	stopPoll  context.CancelFunc
	winHidden atomic.Bool

	// Thread-safe view snapshot for control socket / diagnostics.
	viewAtomic atomic.Value // stores ViewID

	// Core application data (read/written on the UI thread via win.Update).
	State     *apitypes.Bootstrap
	Telemetry GatewayTelemetry
	ViewMode  ViewID

	// Encapsulated cross-page navigation & handoff intents.
	pendingServiceID     string
	pendingEventID       string
	pendingRepoKey       string
	pendingAddService    bool
	pendingCatalogFilter *catalogFilterIntent
	pendingCatalogTool   *catalogToolIntent
	pendingReplay        *playgroundReplayIntent

	// Per-page UI state owned directly by this DesktopUI instance.
	overview        *overviewLocalState
	services        *servicesPageState
	market          *marketPageState
	catalog         *catalogPageState
	repos           *reposPageState
	playground      *playgroundState
	logs            *logsPageState
	settings        *settingsState
	clients         *clientsPageState
	hookModal       *hookSnippetModalState
	serverCreds     map[string]apitypes.ServerCredentials
	serverCredsSig  map[string]string
	serverCredsBusy map[string]bool

	// Auth & bootstrap lifecycle state.
	NeedsLogin bool
	LoginToken string
	LoginError string
	LoggingIn  bool
	Loading    bool
	BootError  string
	Refreshing bool
	BusyCount  int

	// Per-page scroll state so switching tabs preserves scroll position.
	scrollStates map[ViewID]*ui.ScrollState

	// Pending toast messages drained on the UI thread during View().
	toastMu       sync.Mutex
	pendingToasts []string

	// Headless synchronization when Win == nil (e.g. ui.Tester under -race).
	headlessViewMu         sync.Mutex
	headlessQueueMu        sync.Mutex
	pendingHeadlessUpdates []func()
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
		Loading:      true,
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
	d.viewAtomic.Store(ViewOverview)
	return d
}

// AttachWindow binds the mygo.Window to DesktopUI, triggers initial bootstrap,
// and starts a low-overhead background refresh loop.
func (u *DesktopUI) AttachWindow(win *mygo.Window) {
	u.mu.Lock()
	if u.stopPoll != nil {
		u.stopPoll()
		u.stopPoll = nil
	}
	u.Win = win
	pollCtx, cancel := context.WithCancel(context.Background())
	u.stopPoll = cancel
	u.mu.Unlock()

	u.Refresh()
	u.refreshClientConfigs()

	go u.pollLoop(pollCtx)
}

// DetachWindow stops background polling when the window is destroyed.
func (u *DesktopUI) DetachWindow() {
	u.mu.Lock()
	if u.stopPoll != nil {
		u.stopPoll()
		u.stopPoll = nil
	}
	u.Win = nil
	u.mu.Unlock()
}

// CurrentView returns the active page ID safely from any goroutine.
func (u *DesktopUI) CurrentView() ViewID {
	if v, ok := u.viewAtomic.Load().(ViewID); ok {
		return v
	}
	return ViewOverview
}

func (u *DesktopUI) currentWindow() *mygo.Window {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.Win
}

// updateUI executes fn on the main UI thread and triggers a window redraw when attached.
func (u *DesktopUI) updateUI(fn func()) {
	win := u.currentWindow()
	if win != nil {
		win.Update(fn)
		return
	}
	if u.headlessViewMu.TryLock() {
		u.drainHeadlessUpdatesLocked()
		fn()
		u.drainHeadlessUpdatesLocked()
		u.headlessViewMu.Unlock()
		return
	}
	u.headlessQueueMu.Lock()
	u.pendingHeadlessUpdates = append(u.pendingHeadlessUpdates, fn)
	u.headlessQueueMu.Unlock()
}

func (u *DesktopUI) drainHeadlessUpdatesLocked() {
	for {
		u.headlessQueueMu.Lock()
		if len(u.pendingHeadlessUpdates) == 0 {
			u.headlessQueueMu.Unlock()
			return
		}
		batch := u.pendingHeadlessUpdates
		u.pendingHeadlessUpdates = nil
		u.headlessQueueMu.Unlock()
		for _, fn := range batch {
			fn()
		}
	}
}

func (u *DesktopUI) enterHeadlessFrame() func() {
	if u.currentWindow() != nil {
		return func() {}
	}
	u.headlessViewMu.Lock()
	u.drainHeadlessUpdatesLocked()
	return func() {
		u.drainHeadlessUpdatesLocked()
		u.headlessViewMu.Unlock()
	}
}

// Notify queues a toast message and wakes the UI to display it.
// Safe to call from both UI handlers and background goroutines.
func (u *DesktopUI) Notify(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	u.toastMu.Lock()
	u.pendingToasts = append(u.pendingToasts, msg)
	u.toastMu.Unlock()

	if win := u.currentWindow(); win != nil {
		win.Update(func() {})
	}
}

func (u *DesktopUI) drainToasts(c *ui.Context) {
	u.toastMu.Lock()
	if len(u.pendingToasts) == 0 {
		u.toastMu.Unlock()
		return
	}
	msgs := u.pendingToasts
	u.pendingToasts = nil
	u.toastMu.Unlock()

	for _, msg := range msgs {
		c.Toast(msg)
	}
}

// RunAsync executes fn in a background goroutine, tracks busy state,
// surfaces errors via Notify (or transitions to NeedsLogin on 401),
// and triggers a thread-safe window redraw on completion.
func (u *DesktopUI) RunAsync(fn func() error) {
	u.updateUI(func() {
		u.BusyCount++
	})
	go func() {
		err := fn()
		u.updateUI(func() {
			if u.BusyCount > 0 {
				u.BusyCount--
			}
			if err != nil {
				if apiclient.IsUnauthorized(err) {
					u.NeedsLogin = true
					u.State = nil
				} else {
					u.toastMu.Lock()
					u.pendingToasts = append(u.pendingToasts, err.Error())
					u.toastMu.Unlock()
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
		u.Refreshing = true
		if u.State == nil {
			u.Loading = true
		}
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		data, err := u.Client.Bootstrap(ctx)
		var tel GatewayTelemetry
		var creds map[string]apitypes.ServerCredentials
		if err == nil {
			tel = ComputeGatewayTelemetry(data)
			creds = u.fetchReferencedServerCredentials(ctx, data.Registry.Servers)
		}
		u.updateUI(func() {
			u.Refreshing = false
			u.Loading = false
			if err != nil {
				if apiclient.IsUnauthorized(err) {
					u.NeedsLogin = true
					u.State = nil
					u.BootError = ""
				} else if u.State == nil {
					u.BootError = err.Error()
				} else {
					u.toastMu.Lock()
					u.pendingToasts = append(u.pendingToasts, "刷新失败: "+err.Error())
					u.toastMu.Unlock()
				}
				return
			}
			u.State = &data
			u.Telemetry = tel
			u.applyServerCredentialsSnapshot(data.Registry.Servers, creds)
			u.NeedsLogin = false
			u.BootError = ""
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
	data, err := u.Client.Bootstrap(ctx)
	if err != nil {
		return err
	}
	tel := ComputeGatewayTelemetry(data)
	creds := u.fetchReferencedServerCredentials(ctx, data.Registry.Servers)
	u.updateUI(func() {
		u.State = &data
		u.Telemetry = tel
		u.applyServerCredentialsSnapshot(data.Registry.Servers, creds)
		u.NeedsLogin = false
		u.BootError = ""
		u.Loading = false
	})
	return nil
}

func (u *DesktopUI) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if u.winHidden.Load() || u.NeedsLogin || u.Client == nil {
				continue
			}
			view := u.CurrentView()
			if view == ViewClients {
				u.updateUI(func() { u.refreshClientConfigs() })
				continue
			}
			if view != ViewOverview && view != ViewServices && view != ViewLogs && view != ViewRepos {
				continue
			}
			reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			data, err := u.Client.Bootstrap(reqCtx)
			var creds map[string]apitypes.ServerCredentials
			if err == nil {
				creds = u.fetchReferencedServerCredentials(reqCtx, data.Registry.Servers)
			}
			cancel()
			if err != nil {
				if apiclient.IsUnauthorized(err) {
					u.updateUI(func() {
						u.NeedsLogin = true
						u.State = nil
					})
				}
				continue
			}
			tel := ComputeGatewayTelemetry(data)
			u.updateUI(func() {
				u.State = &data
				u.Telemetry = tel
				u.applyServerCredentialsSnapshot(data.Registry.Servers, creds)
				u.NeedsLogin = false
				u.BootError = ""
			})
		}
	}
}

// --- Cross-page navigation helpers ---

func (u *DesktopUI) Navigate(view ViewID) {
	if view == ViewClients {
		u.refreshClientConfigs()
	}
	u.ViewMode = view
	if view != ViewServices {
		u.pendingServiceID = ""
	}
	if view != ViewLogs {
		u.pendingEventID = ""
	}
	u.viewAtomic.Store(view)
}

func (u *DesktopUI) OpenServiceDetail(serverID string) {
	u.pendingServiceID = serverID
	u.ViewMode = ViewServices
	u.viewAtomic.Store(ViewServices)
}

func (u *DesktopUI) activeServiceID() string {
	return u.pendingServiceID
}

func (u *DesktopUI) clearActiveService() {
	u.pendingServiceID = ""
}

func (u *DesktopUI) OpenAddServiceModal() {
	u.pendingAddService = true
	u.pendingServiceID = ""
	u.ViewMode = ViewServices
	u.viewAtomic.Store(ViewServices)
}

func (u *DesktopUI) consumePendingAddService() bool {
	if !u.pendingAddService {
		return false
	}
	u.pendingAddService = false
	return true
}

func (u *DesktopUI) OpenCatalogFilter(serverID, access string) {
	u.pendingCatalogFilter = &catalogFilterIntent{
		serverID: serverID,
		access:   access,
	}
	u.pendingCatalogTool = nil
	u.ViewMode = ViewCatalog
	u.viewAtomic.Store(ViewCatalog)
}

func (u *DesktopUI) consumePendingCatalogFilter() (string, string, bool) {
	if u.pendingCatalogFilter == nil {
		return "", "", false
	}
	intent := *u.pendingCatalogFilter
	u.pendingCatalogFilter = nil
	return intent.serverID, intent.access, true
}

// OpenCatalogTool navigates to the Catalog page focused on a specific tool and service.
func (u *DesktopUI) OpenCatalogTool(toolID, serverID string) {
	u.pendingCatalogTool = &catalogToolIntent{
		toolID:   toolID,
		serverID: serverID,
	}
	u.pendingCatalogFilter = nil
	u.ViewMode = ViewCatalog
	u.viewAtomic.Store(ViewCatalog)
}

func (u *DesktopUI) consumePendingCatalogTool() (string, string, bool) {
	if u.pendingCatalogTool == nil {
		return "", "", false
	}
	intent := *u.pendingCatalogTool
	u.pendingCatalogTool = nil
	return intent.toolID, intent.serverID, true
}

func (u *DesktopUI) OpenPlaygroundReplay(intent, actor string, scope ...string) {
	hasScope := len(scope) > 0
	if intent != "" || hasScope {
		next := &playgroundReplayIntent{
			intent:   intent,
			actor:    actor,
			hasScope: hasScope,
		}
		if len(scope) >= 1 {
			next.repoKey = scope[0]
		}
		u.pendingReplay = next
	}
	u.ViewMode = ViewPlayground
	u.viewAtomic.Store(ViewPlayground)
}

func (u *DesktopUI) consumePendingReplay() (playgroundReplayIntent, bool) {
	if u.pendingReplay == nil {
		return playgroundReplayIntent{}, false
	}
	intent := *u.pendingReplay
	u.pendingReplay = nil
	return intent, true
}

func (u *DesktopUI) OpenRepos(repoKey string) {
	if repoKey != "" {
		u.pendingRepoKey = repoKey
	}
	u.ViewMode = ViewRepos
	u.viewAtomic.Store(ViewRepos)
}

func (u *DesktopUI) consumePendingRepoKey() string {
	key := u.pendingRepoKey
	u.pendingRepoKey = ""
	return key
}

func (u *DesktopUI) OpenLogsEvent(eventID string) {
	u.pendingEventID = eventID
	u.ViewMode = ViewLogs
	u.viewAtomic.Store(ViewLogs)
}

func (u *DesktopUI) consumePendingEventID() string {
	id := u.pendingEventID
	u.pendingEventID = ""
	return id
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
	token := strings.TrimSpace(u.LoginToken)
	if token == "" || u.LoggingIn || u.Client == nil {
		return
	}
	u.LoggingIn = true
	u.LoginError = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, err := u.Client.Login(ctx, token)
		if err != nil {
			u.updateUI(func() {
				u.LoggingIn = false
				u.LoginError = err.Error()
			})
			return
		}
		data, bootErr := u.Client.Bootstrap(ctx)
		var tel GatewayTelemetry
		if bootErr == nil {
			tel = ComputeGatewayTelemetry(data)
		}
		u.updateUI(func() {
			u.LoggingIn = false
			if bootErr != nil {
				u.LoginError = bootErr.Error()
				return
			}
			u.LoginToken = ""
			u.LoginError = ""
			u.NeedsLogin = false
			u.State = &data
			u.Telemetry = tel
			u.BootError = ""
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
	u.winHidden.Store(c.WindowHidden())
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

func (u *DesktopUI) renderLoginView(c *ui.Context) {
	bar := c.TitleBar()
	ui.Column(c).Fill().Background(SailBgSidebar).Children(func() {
		if bar.Height > 0 {
			ui.Box(c).FillWidth().Height(bar.Height).DragWindow()
		}
		ui.Column(c).Grow(1).FillWidth().Center().Padding(24).Children(func() {
			ui.Column(c).
				Width(380).
				Padding(28).
				Gap(16).
				Radius(10).
				Background(SailBgCanvas).
				Border(1, SailSurfaceBorder).
				Shadow(0, 3, 10, 0, SailCardShadow).
				Children(func() {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(32, 32).Radius(8).Background(SailAccentSoft).Border(1, SailAccentBorder).Center().Children(func() {
							ui.Icon(c, IconLogo).Size(18, 18).TextColor(SailAccent)
						})
						ui.Text(c, buildinfo.Current().Name).FontSize(16).Bold().TextColor(SailInkStrong)
					})

					if u.LoginError != "" {
						SailAlertBox(c, ToneDanger, "登录失败", u.LoginError)
					}

					ui.Column(c).Gap(6).FillWidth().Children(func() {
						ui.Text(c, "Admin 访问令牌").FontSize(12.5).FontWeight(600).TextColor(SailInkBody)
						if ui.TextInput(c.Key("login-token"), &u.LoginToken).
							Password().
							AutoFocus().
							Placeholder("粘贴令牌").
							FillWidth().
							Submitted() {
							u.submitLogin()
						}
					})

					if SailPrimaryButton(c, "登录").FillWidth().Disabled(u.LoggingIn || strings.TrimSpace(u.LoginToken) == "").Clicked() {
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
				Background(SailBgCanvas).
				Border(1, SailBorder).
				AlignItems(ui.Center).
				Children(func() {
					ui.Box(c).Size(36, 36).Radius(8).Background(SailAccentSoft).Border(1, SailAccentBorder).Center().Children(func() {
						ui.Icon(c, IconLogo).Size(20, 20).TextColor(SailAccent)
					})
					if u.BootError != "" {
						ui.Text(c, "无法连接网关控制面").FontSize(15.5).Bold().TextColor(SailInkStrong)
						ui.Text(c, u.BootError).FontSize(12.5).TextColor(SailInkMuted).TextAlign(ui.Center)
						if SailPrimaryButton(c, "重试连接").Clicked() {
							u.Refresh()
						}
					} else {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Spinner(c).Label("正在加载")
							ui.Text(c, "正在加载网关控制面…").FontSize(13.5).TextColor(SailInkMuted)
						})
					}
				})
		})
	})
}

func (u *DesktopUI) renderSidebar(c *ui.Context, bar ui.TitleBar) {
	state := u.State
	realServers := 0
	activeRealServers := 0
	realServerSet := make(map[string]bool)
	for _, s := range state.Registry.Servers {
		if !s.Demo {
			realServers++
			realServerSet[s.ID] = true
			if s.Enabled {
				activeRealServers++
			}
		}
	}
	_ = realServers
	realToolCount := 0
	for _, t := range state.Registry.Tools {
		if realServerSet[t.ServerID] {
			realToolCount++
		}
	}

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
					ui.Box(c).
						Size(24, 24).
						Radius(6).
						Background(SailAccentSoft).
						Border(1, SailAccentBorder).
						Center().
						Children(func() {
							ui.Icon(c, IconLogo).Size(14, 14).TextColor(SailAccent)
						})
					ui.Text(c, buildinfo.Current().Name).FontSize(13.5).Bold().TextColor(SailInkStrong).SingleLine()
				})

			// Scrollable navigation sections.
			ui.Scroll(c).Grow(1).FillWidth().Padding(4, 10, 10, 10).Gap(18).Children(func() {
				u.renderNavGroup(c, "工作台", []navItem{
					{id: ViewOverview, label: "概览", icon: IconHome},
					servicesNavItem(activeRealServers, u.servicesNeedingAttention()),
					{id: ViewMarket, label: "MCP 市场", icon: IconMarket},
					{id: ViewCatalog, label: "工具目录", icon: IconLayers, badge: fmt.Sprintf("%d/%d", state.Setup.EnabledTools, realToolCount)},
					{id: ViewRepos, label: "仓库绑定", icon: IconFolderGit, badge: reposNavBadge(state.Scopes)},
					{id: ViewClients, label: "连接客户端", icon: IconServer, badge: clientsNavBadge(u)},
					{id: ViewSettings, label: "访问与路由", icon: IconShield},
				})
				u.renderNavGroup(c, "观测与调试", []navItem{
					{id: ViewLogs, label: "请求日志", icon: IconActivity, badge: countBadge(len(state.Events))},
					{id: ViewPlayground, label: "路由沙盒", icon: IconWaypoints},
				})
			})

			// Footer minimal MCP endpoint bar.
			ui.Column(c).
				FillWidth().
				Padding(12, 14).
				Gap(4).
				BorderWidth(1, 0, 0, 0).
				BorderColor(SailBorder).
				Children(func() {
					ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
						SailQuietStatus(c, "MCP 端点", SailSuccessInk, SailInkMuted)
						copyBtn := ui.ButtonBase(c.Key("sidebar-copy-mcp")).
							Padding(2, 6).
							Radius(4).
							Cursor(ui.CursorPointer)
						if copyBtn.Hovered() {
							copyBtn.Background(SailBgMuted)
						}
						copyBtn.Children(func() {
							ui.Text(c, "复制").FontSize(11).FontWeight(500).TextColor(SailAccent).SingleLine()
						})
						if copyBtn.Clicked() {
							c.WriteClipboard(state.MCPURL)
							u.Notify("已复制 MCP 接入端点")
						}
					})
					ui.Text(c, state.MCPURL).TruncateMiddle().Tooltip(state.MCPURL).
						Font("monospace").
						FontSize(11).
						TextColor(SailInkFaint).
						SingleLine()
				})
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
	item := navItem{id: ViewServices, label: "上游服务", icon: IconServer, badge: countBadge(enabled)}
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

func clientsNavBadge(u *DesktopUI) string {
	if len(u.PluginStatuses) > 0 {
		return countBadge(len(u.PluginStatuses))
	}
	if len(u.ClientStatuses) > 0 {
		return countBadge(len(u.ClientStatuses))
	}
	return ""
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
			active := u.ViewMode == item.id
			btn := ui.ButtonBase(c.Key("nav-"+string(item.id))).
				FillWidth().
				Padding(7, 10).
				Radius(6).
				Gap(9).
				AlignItems(ui.Center).
				Cursor(ui.CursorPointer)
			if active {
				btn.Background(SailBgCanvas).
					Border(1, SailSurfaceBorder).
					Shadow(0, 2, 6, 0, SailControlShadow)
			} else if btn.Hovered() {
				btn.Background(SailBgMuted)
			}

			iconColor := SailInkMuted
			textColor := SailInkBody
			if active {
				iconColor = SailAccent
				textColor = SailInkStrong
			}

			btn.Children(func() {
				ui.Icon(c, item.icon).Size(15, 15).TextColor(iconColor).Shrink(0)
				lbl := ui.Text(c, item.label).Grow(1).FontSize(13).TextColor(textColor).SingleLine()
				if active {
					lbl.FontWeight(600)
				} else {
					lbl.FontWeight(500)
				}
				if item.badge != "" {
					badgeInk := SailInkFaint
					if active {
						badgeInk = SailAccent
					}
					badgeWeight := 500
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
	})
}

func (u *DesktopUI) renderMainWorkspace(c *ui.Context, bar ui.TitleBar) {
	state := u.State
	topH := bar.Height
	if topH < 50 {
		topH = 50
	}

	var activeServerName string
	if u.ViewMode == ViewServices && u.pendingServiceID != "" {
		for _, s := range state.Registry.Servers {
			if s.ID == u.pendingServiceID {
				activeServerName = s.Name
				break
			}
		}
	}

	modelReady := state.Setup.ModelReady && (state.Setup.ModelCheck == nil || !state.Setup.ModelCheck.Stale)

	ui.Column(c).Grow(1).MinWidth(0).FillHeight().Background(SailBgCanvas).Children(func() {
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
				// Breadcrumbs.
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if activeServerName != "" {
						svcLink := ui.ButtonBase(c.Key("bc-services")).Padding(2, 4).Radius(4).Cursor(ui.CursorPointer)
						if svcLink.Hovered() {
							svcLink.Background(SailBgHover)
						}
						svcLink.Children(func() {
							ui.Text(c, "上游服务").FontSize(13).TextColor(SailInkMuted).SingleLine()
						})
						if svcLink.Clicked() {
							u.Navigate(ViewServices)
						}
						ui.Text(c, "/").FontSize(13).TextColor(SailInkFaint)
						ui.Text(c, activeServerName).FontSize(13).FontWeight(600).TextColor(SailInkStrong).SingleLine()
					} else {
						ui.Text(c, viewTitle(u.ViewMode)).FontSize(13).FontWeight(500).TextColor(SailInkMuted).SingleLine()
					}
				})

				// Right status & refresh controls.
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					modelChip := ui.ButtonBase(c.Key("top-model-chip")).
						Padding(4, 10).
						Radius(6).
						Gap(6).
						AlignItems(ui.Center).
						Cursor(ui.CursorPointer).
						Tooltip("路由模型配置")
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
							modelName = "未配置模型"
						}
						ui.Text(c, modelName).Font("monospace").FontSize(12).TextColor(SailInkBody).SingleLine()
					})
					if modelChip.Clicked() {
						u.Navigate(ViewSettings)
					}

					refreshBtn := SailButton(c.Key("top-refresh-btn"), "").
						Padding(5, 10).
						Radius(6).
						Gap(5).
						AlignItems(ui.Center).
						Disabled(u.Refreshing).
						Cursor(ui.CursorPointer)
					refreshBtn.Children(func() {
						ui.Icon(c, IconRefresh).Size(12.5, 12.5).TextColor(SailInkMuted)
						lbl := "刷新"
						if u.Refreshing {
							lbl = "同步中"
						}
						ui.Text(c, lbl).FontSize(12).TextColor(SailInkBody).SingleLine()
					})
					if refreshBtn.Clicked() {
						u.Refresh()
					}

					if state.Access.Shared {
						ui.Text(c, state.Access.Actor).Font("monospace").FontSize(12).TextColor(SailInkMuted).SingleLine()
						if SailSmallButton(c.Key("top-logout-btn"), "退出").Clicked() {
							u.logout()
						}
					}
				})
			})

		// Main scrollable page viewport with generous breathing room.
		ui.Scroll(c.Key("main-scroll-"+string(u.ViewMode))).
			TrackScroll(u.scrollState(u.ViewMode)).
			Grow(1).
			FillWidth().
			Padding(28, 32).
			Children(func() {
				ui.Column(c).FillWidth().MaxWidth(1200).Gap(24).Children(func() {
					switch u.ViewMode {
					case ViewOverview:
						u.renderOverview(c)
					case ViewServices:
						u.renderServices(c)
					case ViewMarket:
						u.renderMarket(c)
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
				})
			})
	})
}
