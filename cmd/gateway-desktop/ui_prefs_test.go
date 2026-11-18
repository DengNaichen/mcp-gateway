package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/i18n"
)

func TestDesktopPrefsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if got := loadDesktopPrefs(dir).Lang; got != string(i18n.LangZH) {
		t.Fatalf("default lang = %q, want %q", got, i18n.LangZH)
	}
	if err := saveDesktopPrefs(dir, desktopPrefs{Lang: "en-US"}); err != nil {
		t.Fatalf("saveDesktopPrefs: %v", err)
	}
	if got := loadDesktopPrefs(dir).Lang; got != string(i18n.LangEN) {
		t.Fatalf("loaded lang = %q, want %q", got, i18n.LangEN)
	}
	i18n.WithLang(i18n.LangZH, func() {
		if got := applyDesktopPrefs(dir); got != i18n.LangEN || i18n.Current() != i18n.LangEN {
			t.Fatalf("applyDesktopPrefs = %q (current %q), want %q", got, i18n.Current(), i18n.LangEN)
		}
	})
}

func TestDesktopUISetLanguagePersistsAndNotifies(t *testing.T) {
	dir := t.TempDir()
	i18n.WithLang(i18n.LangZH, func() {
		u := NewDesktopUI(&gatewayRuntime{DataDir: dir})
		var notified i18n.Lang
		u.OnLanguageChanged = func(lang i18n.Lang) {
			notified = lang
		}

		u.SetLanguage(i18n.LangEN)
		if i18n.Current() != i18n.LangEN {
			t.Fatalf("current lang = %q, want %q", i18n.Current(), i18n.LangEN)
		}
		if notified != i18n.LangEN {
			t.Fatalf("notified lang = %q, want %q", notified, i18n.LangEN)
		}
		if got := loadDesktopPrefs(dir).Lang; got != string(i18n.LangEN) {
			t.Fatalf("persisted lang = %q, want %q", got, i18n.LangEN)
		}

		tester := ui.NewTester(func(c *ui.Context) {
			applySailTheme(c)
			u.renderSidebarLanguageSwitcher(c)
		}, 240, 80)
		if !tester.HasText("Language") {
			t.Fatalf("expected English language switcher label")
		}
	})
}
