package i18n

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var fmtVerbRe = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*(?:\*|\d+)?(?:\.(?:\*|\d+))?[vTtbcdoqxXUeEfFgGsp%]`)

func formatVerbs(s string) []string {
	matches := fmtVerbRe.FindAllString(s, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if m == "%%" {
			continue
		}
		out = append(out, m)
	}
	return out
}

func TestNormalizeLang(t *testing.T) {
	cases := []struct {
		in   string
		want Lang
		ok   bool
	}{
		{"zh-CN", LangZH, true},
		{"zh_CN", LangZH, true},
		{"zh", LangZH, true},
		{"zh-Hans", LangZH, true},
		{"en", LangEN, true},
		{"en-US", LangEN, true},
		{"en_GB", LangEN, true},
		{"fr-FR", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := NormalizeLang(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Fatalf("NormalizeLang(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestLocaleParity(t *testing.T) {
	zh := catalog[LangZH]
	en := catalog[LangEN]
	if len(zh) == 0 || len(en) == 0 {
		t.Fatalf("expected non-empty catalogs, got zh=%d en=%d", len(zh), len(en))
	}

	for k, zhVal := range zh {
		if strings.TrimSpace(zhVal) == "" {
			t.Errorf("zh-CN key %q has empty translation", k)
		}
		enVal, ok := en[k]
		if !ok {
			t.Errorf("key %q exists in zh-CN but missing in en", k)
			continue
		}
		if strings.TrimSpace(enVal) == "" {
			t.Errorf("en key %q has empty translation", k)
		}
		if zhVerbs, enVerbs := formatVerbs(zhVal), formatVerbs(enVal); !slices.Equal(zhVerbs, enVerbs) {
			t.Errorf("key %q format verbs mismatch: zh=%v en=%v", k, zhVerbs, enVerbs)
		}
	}
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("key %q exists in en but missing in zh-CN", k)
		}
	}
}

func TestTranslationLookupAndSwitch(t *testing.T) {
	WithLang(LangEN, func() {
		if got := T("nav.overview"); got != "Overview" {
			t.Fatalf("T(nav.overview) in en = %q, want Overview", got)
		}
	})
	WithLang(LangZH, func() {
		if got := T("nav.overview"); got != "概览" {
			t.Fatalf("T(nav.overview) in zh-CN = %q, want 概览", got)
		}
	})
	if got := TLang(LangEN, "missing.key"); got != "missing.key" {
		t.Fatalf("missing key fallback = %q, want missing.key", got)
	}
	if got := TLang(LangEN, "missing.fmt.%s", "abc"); got != "missing.fmt.abc" {
		t.Fatalf("missing formatted key = %q, want missing.fmt.abc", got)
	}
}
