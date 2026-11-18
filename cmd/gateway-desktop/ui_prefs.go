package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/i18n"
)

const desktopPrefsFilename = "desktop-prefs.json"

type desktopPrefs struct {
	Lang string `json:"lang"`
}

func desktopPrefsPath(dataDir string) string {
	dir := strings.TrimSpace(dataDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, desktopPrefsFilename)
}

func loadDesktopPrefs(dataDir string) desktopPrefs {
	path := desktopPrefsPath(dataDir)
	if path == "" {
		return desktopPrefs{Lang: string(i18n.LangZH)}
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is resolved inside the user's local desktop data directory
	if err != nil {
		return desktopPrefs{Lang: string(i18n.LangZH)}
	}
	var prefs desktopPrefs
	if err := json.Unmarshal(raw, &prefs); err != nil {
		return desktopPrefs{Lang: string(i18n.LangZH)}
	}
	if norm, ok := i18n.NormalizeLang(prefs.Lang); ok {
		prefs.Lang = string(norm)
	} else {
		prefs.Lang = string(i18n.LangZH)
	}
	return prefs
}

func saveDesktopPrefs(dataDir string, prefs desktopPrefs) error {
	path := desktopPrefsPath(dataDir)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if norm, ok := i18n.NormalizeLang(prefs.Lang); ok {
		prefs.Lang = string(norm)
	} else {
		prefs.Lang = string(i18n.LangZH)
	}
	raw, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func applyDesktopPrefs(dataDir string) i18n.Lang {
	prefs := loadDesktopPrefs(dataDir)
	lang, ok := i18n.NormalizeLang(prefs.Lang)
	if !ok {
		lang = i18n.LangZH
	}
	i18n.Set(lang)
	return lang
}

// SetLanguage updates the active UI language, persists it to desktop-prefs.json
// when a runtime data directory is available, notifies listeners (such as menus),
// and requests a UI re-render.
func (u *DesktopUI) SetLanguage(lang i18n.Lang) {
	norm, ok := i18n.NormalizeLang(string(lang))
	if !ok || norm == i18n.Current() {
		return
	}
	i18n.Set(norm)
	if u.Runtime != nil && strings.TrimSpace(u.Runtime.DataDir) != "" {
		if err := saveDesktopPrefs(u.Runtime.DataDir, desktopPrefs{Lang: string(norm)}); err != nil {
			slog.Warn("save desktop language preference", "error", err)
		}
	}
	if u.OnLanguageChanged != nil {
		u.OnLanguageChanged(norm)
	}
	u.updateUI(func() {})
}

func (u *DesktopUI) renderSidebarLanguageSwitcher(c *ui.Context) {
	activeLang := i18n.Current()
	ui.Column(c).FillWidth().Padding(8, 12, 12, 12).Gap(6).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, i18n.T("app.lang.label")).
				Grow(1).
				FontSize(11.5).
				FontWeight(500).
				TextColor(SailInkMuted).
				SingleLine()

			ui.Row(c).
				Background(SailBgMuted).
				Border(1, SailBorder).
				Radius(6).
				Padding(2).
				Gap(2).
				Children(func() {
					renderLangPill := func(lang i18n.Lang, labelKey string) {
						selected := activeLang == lang
						btn := ui.ButtonBase(c.Key("lang-"+string(lang))).
							Padding(3, 8).
							Radius(4).
							Cursor(ui.CursorPointer)
						textColor := SailInkMuted
						weight := 500
						if selected {
							applyControlElevation(
								btn.Background(SailBgSurface).
									Border(SailElevationBorderWidth, SailSurfaceBorder),
								true,
							)
							textColor = SailInkStrong
							weight = 600
						} else if btn.Hovered() {
							btn.Background(SailBgHover)
							textColor = SailInkBody
						}
						btn.Children(func() {
							ui.Text(c, i18n.T(labelKey)).
								FontSize(11).
								FontWeight(weight).
								TextColor(textColor).
								SingleLine()
						})
						if btn.Clicked() {
							u.SetLanguage(lang)
						}
					}
					renderLangPill(i18n.LangZH, "app.lang.zh")
					renderLangPill(i18n.LangEN, "app.lang.en")
				})
		})
	})
}
