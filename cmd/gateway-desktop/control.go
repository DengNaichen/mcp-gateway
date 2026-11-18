package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/i18n"
)

func writeControlJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (d *desktopApp) controlSnapshot() (*gatewayRuntime, *mygo.Window, *DesktopUI) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.runtime, d.win, d.ui
}

// startControlSocket starts an optional local Unix domain socket server when
// GATEWAY_DESKTOP_CONTROL is explicitly set, allowing automated end-to-end
// verification of the packaged desktop app (window visibility, native UI view,
// clipboard, screenshot, tray actions, and clean shutdown).
func (d *desktopApp) startControlSocket() (func(), error) {
	sockPath := strings.TrimSpace(os.Getenv("GATEWAY_DESKTOP_CONTROL"))
	if sockPath == "" {
		return func() {}, nil
	}
	if info, err := os.Lstat(sockPath); err == nil && info.Mode()&os.ModeSocket != 0 { //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input
		_ = os.Remove(sockPath) //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sockPath, 0o600) //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input

	mux := http.NewServeMux()
	d.registerControlStatusRoutes(mux)
	d.registerControlClientRoutes(mux)
	d.registerControlWindowRoutes(mux)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Warn("control socket stopped", "error", err)
		}
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = os.Remove(sockPath) //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input
	}, nil
}

func (d *desktopApp) registerControlStatusRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		rt, win, uiApp := d.controlSnapshot()
		resp := map[string]any{
			"ready":           rt != nil,
			"window_exists":   win != nil,
			"native_ui":       true,
			"build":           buildinfo.Current(),
			"updates_enabled": buildinfo.Current().Channel == "stable",
			"dark":            mygo.Theme.IsDark(),
			"lang":            string(i18n.Current()),
		}
		if rt != nil {
			resp["mode"] = rt.Mode
			resp["listen_addr"] = rt.ListenAddr
			resp["ui_url"] = rt.UIURL
			resp["mcp_url"] = rt.MCPURL
			resp["data_dir"] = rt.DataDir
		}
		if win != nil {
			resp["window_visible"] = win.IsVisible()
			resp["window_focused"] = win.IsFocused()
			resp["window_title"] = win.Title()
		}
		if uiApp != nil {
			resp["current_view"] = string(uiApp.CurrentView())
		}
		writeControlJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /theme", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Source string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var source mygo.ThemeSource
		switch in.Source {
		case "system":
			source = mygo.ThemeSystem
		case "light":
			source = mygo.ThemeLight
		case "dark":
			source = mygo.ThemeDark
		default:
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown theme source"})
			return
		}
		mygo.Theme.SetSource(source)
		writeControlJSON(w, http.StatusOK, map[string]any{"source": in.Source, "dark": mygo.Theme.IsDark()})
	})

	mux.HandleFunc("POST /view", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			View string `json:"view"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		_, win, uiApp := d.controlSnapshot()
		if uiApp == nil || win == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "window not open"})
			return
		}
		win.Update(func() {
			uiApp.Navigate(ViewID(in.View))
		})
		writeControlJSON(w, http.StatusOK, map[string]any{"ok": true, "view": in.View})
	})
}

func (d *desktopApp) registerControlClientRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clients", func(w http.ResponseWriter, r *http.Request) {
		rt, _, app := d.controlSnapshot()
		if app == nil || app.ClientConfigs == nil || rt == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "not ready"})
			return
		}
		writeControlJSON(w, http.StatusOK, app.ClientConfigs.Statuses(rt.MCPURL))
	})

	mux.HandleFunc("POST /clients", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Action string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rt, _, app := d.controlSnapshot()
		if app == nil || app.ClientConfigs == nil || rt == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "not ready"})
			return
		}
		if err := app.changeClientConfig(in.ID, in.Action, rt.MCPURL); err != nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		app.updateUI(func() { app.refreshClientConfigs() })
		writeControlJSON(w, http.StatusOK, app.ClientConfigs.Statuses(rt.MCPURL))
	})

	mux.HandleFunc("GET /plugins", func(w http.ResponseWriter, r *http.Request) {
		rt, _, app := d.controlSnapshot()
		if app == nil || app.ClientPlugins == nil || rt == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "not ready"})
			return
		}
		writeControlJSON(w, http.StatusOK, app.ClientPlugins.Statuses(rt.MCPURL))
	})

	mux.HandleFunc("POST /plugins", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Action string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		rt, _, app := d.controlSnapshot()
		if app == nil || app.ClientPlugins == nil || rt == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "not ready"})
			return
		}
		if err := app.changeClientPlugin(in.ID, in.Action, rt.MCPURL); err != nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		app.updateUI(func() { app.refreshClientConfigs() })
		writeControlJSON(w, http.StatusOK, app.ClientPlugins.Statuses(rt.MCPURL))
	})
}

func (d *desktopApp) registerControlWindowRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /clipboard", func(w http.ResponseWriter, r *http.Request) {
		writeControlJSON(w, http.StatusOK, map[string]string{"text": mygo.Clipboard.ReadText()})
	})

	mux.HandleFunc("POST /clipboard", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeControlJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		mygo.Clipboard.WriteText(in.Text)
		writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /screenshot", func(w http.ResponseWriter, r *http.Request) {
		_, win, _ := d.controlSnapshot()
		if win == nil {
			writeControlJSON(w, http.StatusConflict, map[string]string{"error": "window not open"})
			return
		}
		pngBytes, err := win.CapturePage()
		if err != nil {
			writeControlJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes)
	})

	mux.HandleFunc("POST /window/close", func(w http.ResponseWriter, r *http.Request) {
		_, win, _ := d.controlSnapshot()
		if win != nil {
			win.Close()
		}
		writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/open", func(w http.ResponseWriter, r *http.Request) {
		d.showMainWindow()
		writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/copy-mcp", func(w http.ResponseWriter, r *http.Request) {
		d.copyMCPURL()
		writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/quit", func(w http.ResponseWriter, r *http.Request) {
		writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
		go func() {
			time.Sleep(30 * time.Millisecond)
			d.requestQuit()
		}()
	})
}
