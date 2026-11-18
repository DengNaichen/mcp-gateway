// Package i18n provides embedded bilingual (zh-CN / en) localization catalogs
// and thread-safe lookup helpers shared across the desktop UI and local packages.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync/atomic"
)

// Lang identifies a supported UI/message language.
type Lang string

const (
	// LangZH is Simplified Chinese (default).
	LangZH Lang = "zh-CN"
	// LangEN is English.
	LangEN Lang = "en"
)

//go:embed locales/*/*.json
var localeFS embed.FS

var (
	catalog     = mustLoadCatalog()
	currentLang atomic.Value
)

func init() {
	currentLang.Store(LangZH)
}

// SupportedLangs returns all supported languages in display order.
func SupportedLangs() []Lang {
	return []Lang{LangZH, LangEN}
}

// NormalizeLang maps common language tags (e.g. "zh", "zh_CN", "en-US")
// to a canonical supported Lang.
func NormalizeLang(raw string) (Lang, bool) {
	s := strings.ToLower(strings.TrimSpace(strings.ReplaceAll(raw, "_", "-")))
	switch {
	case s == "zh" || s == "zh-cn" || s == "zh-hans" || strings.HasPrefix(s, "zh-hans-"):
		return LangZH, true
	case s == "en" || s == "en-us" || s == "en-gb" || strings.HasPrefix(s, "en-"):
		return LangEN, true
	default:
		return "", false
	}
}

// Current returns the active process-wide language, defaulting to LangZH.
func Current() Lang {
	if v := currentLang.Load(); v != nil {
		if l, ok := v.(Lang); ok && l != "" {
			return l
		}
	}
	return LangZH
}

// Set updates the active process-wide language. Unsupported values are ignored.
func Set(raw Lang) bool {
	norm, ok := NormalizeLang(string(raw))
	if !ok {
		return false
	}
	currentLang.Store(norm)
	return true
}

// WithLang temporarily sets the active language for the duration of fn.
// Intended for deterministic tests; not safe to run concurrently with other
// tests that mutate the global language.
func WithLang(lang Lang, fn func()) {
	prev := Current()
	Set(lang)
	defer Set(prev)
	fn()
}

// T translates key using the active language. If args are provided, the
// resolved template is formatted with fmt.Sprintf.
func T(key string, args ...any) string {
	return TLang(Current(), key, args...)
}

// TLang translates key for an explicit language, falling back to English,
// Simplified Chinese, and finally the raw key itself.
func TLang(lang Lang, key string, args ...any) string {
	norm, ok := NormalizeLang(string(lang))
	if !ok {
		norm = LangZH
	}
	tmpl, found := lookup(norm, key)
	if !found && norm != LangEN {
		tmpl, found = lookup(LangEN, key)
	}
	if !found && norm != LangZH {
		tmpl, found = lookup(LangZH, key)
	}
	if !found {
		tmpl = key
	}
	if len(args) == 0 {
		return tmpl
	}
	return fmt.Sprintf(tmpl, args...)
}

func lookup(lang Lang, key string) (string, bool) {
	m := catalog[lang]
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	return v, ok
}

func mustLoadCatalog() map[Lang]map[string]string {
	out := map[Lang]map[string]string{
		LangZH: {},
		LangEN: {},
	}
	for _, lang := range SupportedLangs() {
		dir := path.Join("locales", string(lang))
		entries, err := fs.ReadDir(localeFS, dir)
		if err != nil {
			panic(fmt.Sprintf("i18n: read locale dir %s: %v", dir, err))
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			filePath := path.Join(dir, entry.Name())
			raw, err := fs.ReadFile(localeFS, filePath)
			if err != nil {
				panic(fmt.Sprintf("i18n: read %s: %v", filePath, err))
			}
			var dict map[string]string
			if err := json.Unmarshal(raw, &dict); err != nil {
				panic(fmt.Sprintf("i18n: decode %s: %v", filePath, err))
			}
			for k, v := range dict {
				if _, exists := out[lang][k]; exists {
					panic(fmt.Sprintf("i18n: duplicate key %q in %s", k, filePath))
				}
				out[lang][k] = v
			}
		}
	}
	return out
}
