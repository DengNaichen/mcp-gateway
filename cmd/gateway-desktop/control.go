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
)

// startControlSocket starts an optional local Unix domain socket server when
// GATEWAY_DESKTOP_CONTROL is explicitly set, allowing automated end-to-end
// verification of the packaged desktop app (window visibility, native UI view,
// clipboard, screenshot, tray actions, and clean shutdown).
func (d *desktopApp) startControlSocket() (func(), error) {
	sockPath := strings.TrimSpace(os.Getenv("GATEWAY_DESKTOP_CONTROL"))
	if sockPath == "" {
		return func() {}, nil
	}
	if info, err := os.Lstat(sockPath); err == nil && info.Mode()&os.ModeSocket != 0 {
		_ = os.Remove(sockPath)
	}
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, err
	}
	_ = os.Chmod(sockPath, 0o600)

	mux := http.NewServeMux()
	writeJSON := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		rt := d.runtime
		win := d.win
		uiApp := d.ui
		d.mu.Unlock()

		resp := map[string]any{
			"ready":         rt != nil,
			"window_exists": win != nil,
			"native_ui":     true,
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
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("POST /view", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			View string `json:"view"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		d.mu.Lock()
		uiApp := d.ui
		win := d.win
		d.mu.Unlock()
		if uiApp == nil || win == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "window not open"})
			return
		}
		win.Update(func() {
			uiApp.Navigate(ViewID(in.View))
		})
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "view": in.View})
	})

	mux.HandleFunc("GET /clipboard", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"text": mygo.Clipboard.ReadText()})
	})

	mux.HandleFunc("POST /clipboard", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		mygo.Clipboard.WriteText(in.Text)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /screenshot", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		win := d.win
		d.mu.Unlock()
		if win == nil {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "window not open"})
			return
		}
		pngBytes, err := win.CapturePage()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pngBytes)
	})

	mux.HandleFunc("POST /window/close", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		win := d.win
		d.mu.Unlock()
		if win != nil {
			win.Close()
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/open", func(w http.ResponseWriter, r *http.Request) {
		d.showMainWindow()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/copy-mcp", func(w http.ResponseWriter, r *http.Request) {
		d.copyMCPURL()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /tray/quit", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		go func() {
			time.Sleep(30 * time.Millisecond)
			d.requestQuit()
		}()
	})

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
		_ = os.Remove(sockPath)
	}, nil
}
