package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/web"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"
)

//go:embed assets/tray.png
var trayIconPNG []byte

type desktopApp struct {
	addr       string
	addrCustom bool
	dataDir    string
	assets     fs.FS

	appCtx    context.Context
	appCancel context.CancelFunc

	mu          sync.Mutex
	runtime     *gatewayRuntime
	ui          *DesktopUI
	win         *mygo.Window
	tray        *mygo.Tray
	trayMenu    *mygo.Menu
	releaseNap  func()
	stopControl func()

	quitting    atomic.Bool
	cleanupOnce sync.Once
}

func (d *desktopApp) setupMenus() {
	appMenu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: buildinfo.Current().Name, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			channelUpdateMenuItem(),
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "打开控制面窗口", Accelerator: "CmdOrCtrl+1", Click: func(*mygo.MenuItem, *mygo.Window) {
				d.showMainWindow()
			}},
			{Label: "刷新控制面数据", Accelerator: "CmdOrCtrl+R", Click: func(*mygo.MenuItem, *mygo.Window) {
				d.mu.Lock()
				uiApp := d.ui
				d.mu.Unlock()
				if uiApp != nil {
					uiApp.Refresh()
				}
			}},
			{Label: "复制 MCP 接入地址", Accelerator: "CmdOrCtrl+Shift+C", Click: func(*mygo.MenuItem, *mygo.Window) {
				d.copyMCPURL()
			}},
			{Label: "在浏览器中打开", Accelerator: "CmdOrCtrl+Shift+O", Click: func(*mygo.MenuItem, *mygo.Window) {
				d.openInBrowser()
			}},
			mygo.Separator(),
			{Role: mygo.RoleClose},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "视图", Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleToggleFullScreen},
		}},
		{Role: mygo.RoleWindowMenu},
	})
	mygo.App.SetMenu(appMenu)

	mygo.App.Dock.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "打开控制面窗口", Click: func(*mygo.MenuItem, *mygo.Window) { d.showMainWindow() }},
		{Label: "复制 MCP 接入地址", Click: func(*mygo.MenuItem, *mygo.Window) { d.copyMCPURL() }},
		{Label: "在浏览器中打开", Click: func(*mygo.MenuItem, *mygo.Window) { d.openInBrowser() }},
	}))
}

func (d *desktopApp) setupTray() {
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{ID: "open", Label: "打开控制面窗口", Click: func(*mygo.MenuItem, *mygo.Window) {
			d.showMainWindow()
		}},
		{ID: "status", Label: "状态：正在启动网关…", Disabled: true},
		mygo.Separator(),
		{ID: "copy-mcp", Label: "复制 MCP 接入地址", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
			d.copyMCPURL()
		}},
		{ID: "open-browser", Label: "在浏览器中打开", Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) {
			d.openInBrowser()
		}},
		mygo.Separator(),
		channelUpdateMenuItem(),
		mygo.Separator(),
		{ID: "quit", Label: "退出 " + buildinfo.Current().Name, Accelerator: "CmdOrCtrl+Q", Click: func(*mygo.MenuItem, *mygo.Window) {
			d.requestQuit()
		}},
	})

	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             buildinfo.Current().Slug,
		Icon:           trayIconPNG,
		IconIsTemplate: true,
		ToolTip:        buildinfo.Current().Name,
		Menu:           menu,
	})
	if err != nil {
		slog.Error("create system tray icon", "error", err)
		return
	}

	d.mu.Lock()
	d.tray = tray
	d.trayMenu = menu
	d.mu.Unlock()
}

func (d *desktopApp) updateTrayReady(rt *gatewayRuntime) {
	d.mu.Lock()
	tray := d.tray
	menu := d.trayMenu
	d.mu.Unlock()
	if tray == nil || menu == nil {
		return
	}

	statusLabel := fmt.Sprintf("状态：运行中 (%s)", rt.ListenAddr)
	if rt.Mode == modeAttached {
		statusLabel = fmt.Sprintf("状态：复用现有实例 (%s)", rt.ListenAddr)
	}

	if item := menu.ItemByID("status"); item != nil {
		item.SetLabel(statusLabel)
	}
	if item := menu.ItemByID("copy-mcp"); item != nil {
		item.SetSubtitle(rt.MCPURL)
		item.SetEnabled(true)
	}
	if item := menu.ItemByID("open-browser"); item != nil {
		item.SetEnabled(true)
	}
	tray.SetToolTip(buildinfo.Current().Name + " — " + rt.MCPURL)
}

func (d *desktopApp) copyMCPURL() {
	d.mu.Lock()
	rt := d.runtime
	uiApp := d.ui
	d.mu.Unlock()
	if rt == nil || rt.MCPURL == "" {
		return
	}
	mygo.Clipboard.WriteText(rt.MCPURL)
	if uiApp != nil {
		uiApp.Notify("已复制 MCP 接入端点")
	}
}

func (d *desktopApp) openInBrowser() {
	d.mu.Lock()
	rt := d.runtime
	d.mu.Unlock()
	if rt == nil || rt.UIURL == "" {
		return
	}
	go func(target string) {
		if err := mygo.Shell.OpenExternal(target); err != nil {
			slog.Warn("open control plane in browser", "url", target, "error", err)
		}
	}(rt.UIURL)
}

func (d *desktopApp) showMainWindow() {
	mygo.RunOnMain(func() {
		d.mu.Lock()
		rt := d.runtime
		win := d.win
		uiApp := d.ui
		if rt != nil && uiApp == nil {
			uiApp = NewDesktopUI(rt)
			d.ui = uiApp
		}
		d.mu.Unlock()
		if rt == nil {
			return
		}
		if win != nil {
			if win.IsMinimized() {
				win.Restore()
			}
			win.Show()
			win.Focus()
			mygo.App.Focus()
			uiApp.Refresh()
			return
		}

		win = mygo.NewWindow(mygo.WindowOptions{
			Title:                buildinfo.Current().Name + " · " + buildinfo.Current().Version,
			Width:                1280,
			Height:               820,
			MinWidth:             960,
			MinHeight:            620,
			StateKey:             "main",
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 20, Y: 18},
			TitleBarHeight:       50,
			BackgroundColor:      "light-dark(#ffffff, #0f1626)",
			Content:              ui.View(uiApp.View),
		})

		win.OnClose(func(e *mygo.CloseEvent) {
			if !d.quitting.Load() {
				e.PreventDefault()
				win.Hide()
			}
		})
		win.OnClosed(func() {
			d.mu.Lock()
			if d.win == win {
				d.win = nil
			}
			d.mu.Unlock()
			uiApp.DetachWindow()
		})

		d.mu.Lock()
		d.win = win
		d.mu.Unlock()

		uiApp.AttachWindow(win)
		mygo.App.Focus()
	})
}

func (d *desktopApp) startBackend() {
	rt, conflict, err := startOrAttachGateway(d.appCtx, d.addr, d.dataDir, d.assets)
	if conflict != nil {
		res, dlgErr := mygo.Dialog.Message(mygo.MessageOptions{
			Type:          mygo.MessageWarning,
			Title:         "MCP Gateway 端口冲突",
			Message:       fmt.Sprintf("监听地址 %s 已被其他程序占用", d.addr),
			Detail:        "端口已占用，未找到与当前渠道、版本和数据目录匹配的 Gateway。您可以改用系统分配的本机空闲端口启动，或退出后再试。",
			Buttons:       []string{"使用空闲端口启动", "退出"},
			DefaultButton: 0,
			CancelButton:  1,
		})
		if dlgErr != nil || res.Button != 0 {
			d.requestQuit()
			return
		}
		rt, _, err = startOrAttachGateway(d.appCtx, "127.0.0.1:0", d.dataDir, d.assets)
	}
	if err != nil {
		slog.Error("start gateway desktop backend", "error", err)
		mygo.Dialog.Error("MCP Gateway 启动失败", err.Error())
		d.requestQuit()
		return
	}

	d.mu.Lock()
	d.runtime = rt
	d.mu.Unlock()

	d.updateTrayReady(rt)
	d.showMainWindow()
}

func (d *desktopApp) requestQuit() {
	d.quitting.Store(true)
	mygo.App.Quit()
}

func (d *desktopApp) cleanup() {
	d.cleanupOnce.Do(func() {
		d.appCancel()

		d.mu.Lock()
		stopCtrl := d.stopControl
		releaseNap := d.releaseNap
		tray := d.tray
		rt := d.runtime
		uiApp := d.ui
		d.stopControl = nil
		d.releaseNap = nil
		d.tray = nil
		d.mu.Unlock()

		if uiApp != nil {
			uiApp.DetachWindow()
		}
		if stopCtrl != nil {
			stopCtrl()
		}
		if releaseNap != nil {
			releaseNap()
		}
		if tray != nil {
			tray.Destroy()
		}
		if rt != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rt.Shutdown(shutdownCtx); err != nil {
				slog.Error("shutdown desktop gateway", "error", err)
			}
		}
	})
}

func parseDesktopFlags(rawArgs []string) (addr string, addrCustom bool, dataFlag string, err error) {
	fs := flag.NewFlagSet("gateway-desktop", flag.ContinueOnError)
	addrPtr := fs.String("addr", buildinfo.DefaultAddr(), "HTTP listening address")
	dataPtr := fs.String("data", "", "Local registry and settings directory (defaults to user config dir)")
	if err := fs.Parse(filterLaunchArgs(rawArgs)); err != nil {
		return "", false, "", err
	}
	custom := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "addr" {
			custom = true
		}
	})
	return *addrPtr, custom, *dataPtr, nil
}

func run() error {
	enrichProcessEnvironment()

	addr, addrCustom, dataFlag, err := parseDesktopFlags(os.Args[1:])
	if err != nil {
		return err
	}
	if err := validateListenAddr(addr); err != nil {
		return err
	}
	dataDir, err := resolveDataDir(dataFlag)
	if err != nil {
		return err
	}
	assets, err := fs.Sub(web.Assets, "dist")
	if err != nil {
		return fmt.Errorf("load embedded frontend: %w", err)
	}

	if mygo.App.IsPackaged() && mygo.App.Name() != buildinfo.Current().Name {
		return fmt.Errorf("package identity %q does not match compiled channel %q; rebuild using scripts/build-channel.mjs", mygo.App.Name(), buildinfo.Current().Channel)
	}
	if !mygo.App.IsPackaged() {
		mygo.App.SetName(buildinfo.Current().Name)
		mygo.App.SetVersion(buildinfo.Current().Version)
	}
	// Enforce single-instance lock for standard launches so double-clicking the
	// .app while running in the tray focuses the existing control plane window.
	if !addrCustom {
		if !mygo.App.RequestSingleInstanceLock() {
			return nil
		}
	}

	appCtx, appCancel := context.WithCancel(context.Background())
	d := &desktopApp{
		addr:       addr,
		addrCustom: addrCustom,
		dataDir:    dataDir,
		assets:     assets,
		appCtx:     appCtx,
		appCancel:  appCancel,
	}
	defer d.cleanup()

	mygo.Theme.SetSource(mygo.ThemeSystem)
	if buildinfo.Current().Channel == "stable" {
		mygo.Use(native.Plugin)
	}
	d.setupMenus()

	// Keep the Gateway + Tray alive when all windows are closed/hidden.
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			d.showMainWindow()
		}
	})
	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		d.showMainWindow()
	})
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		d.quitting.Store(true)
		if e.SessionEnding {
			d.cleanup()
		}
	})
	mygo.App.OnQuitCanceled(func() {
		d.quitting.Store(false)
	})
	mygo.App.OnQuit(func() {
		d.cleanup()
	})

	mygo.App.WhenReady(func() {
		d.mu.Lock()
		d.releaseNap = mygo.Power.KeepRunning("Serving local MCP Gateway")
		d.mu.Unlock()

		stopCtrl, ctrlErr := d.startControlSocket()
		if ctrlErr != nil {
			slog.Warn("start desktop control socket", "error", ctrlErr)
		} else {
			d.mu.Lock()
			d.stopControl = stopCtrl
			d.mu.Unlock()
		}

		d.setupTray()
		go d.startBackend()
	})

	return mygo.App.Run()
}

func main() {
	if err := run(); err != nil {
		slog.Error("gateway desktop stopped", "error", err)
		os.Exit(1)
	}
}

func channelUpdateMenuItem() *mygo.MenuItem {
	if buildinfo.Current().Channel == "stable" {
		return updater.MenuItem()
	}
	return &mygo.MenuItem{Label: "开发版 · 自动更新已关闭", Disabled: true}
}
