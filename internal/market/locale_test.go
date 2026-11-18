package market

import (
	"context"
	"strings"
	"testing"
	"unicode"

	"gatewaylab/internal/i18n"
)

func hasCJK(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

func findMarketItem(items []MarketServer, id string) (MarketServer, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return MarketServer{}, false
}

// TestCuratedMarketCopyFollowsLanguage proves the curated catalog is rebuilt per
// call, so switching the process-wide language changes what the API serves.
func TestCuratedMarketCopyFollowsLanguage(t *testing.T) {
	g, err := testMarket(t)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	defer g.Close()

	i18n.WithLang(i18n.LangZH, func() {
		notion, ok := findMarketItem(g.MarketServers(context.Background(), "").Items, "notion")
		if !ok {
			t.Fatal("expected notion in the curated catalog")
		}
		if !strings.Contains(notion.Description, "检索、读取并更新 Notion 工作区页面") {
			t.Fatalf("zh-CN description changed: %q", notion.Description)
		}
		filesystem, _ := findMarketItem(g.MarketServers(context.Background(), "").Items, "filesystem")
		if filesystem.Inputs[0].Label != "允许访问的目录路径" {
			t.Fatalf("zh-CN input label changed: %q", filesystem.Inputs[0].Label)
		}
	})

	i18n.WithLang(i18n.LangEN, func() {
		items := g.MarketServers(context.Background(), "").Items
		notion, ok := findMarketItem(items, "notion")
		if !ok {
			t.Fatal("expected notion in the curated catalog")
		}
		if !strings.Contains(notion.Description, "Search, read, and update Notion workspace pages") {
			t.Fatalf("en description = %q", notion.Description)
		}
		filesystem, _ := findMarketItem(items, "filesystem")
		if filesystem.Inputs[0].Label != "Directory path to allow" {
			t.Fatalf("en input label = %q", filesystem.Inputs[0].Label)
		}
		if filesystem.Inputs[0].Description != "Grants access only to this directory and its subdirectories" {
			t.Fatalf("en input description = %q", filesystem.Inputs[0].Description)
		}
		for _, item := range items {
			if hasCJK(item.Description) {
				t.Fatalf("en description for %q still contains CJK: %q", item.ID, item.Description)
			}
			for _, inp := range item.Inputs {
				if hasCJK(inp.Label) || hasCJK(inp.Description) {
					t.Fatalf("en input copy for %q still contains CJK: %+v", item.ID, inp)
				}
			}
		}
	})

	if _, _, err := g.MarketIcon(context.Background(), "unknown", ""); err == nil {
		t.Fatal("expected an error for an unknown market icon")
	} else {
		if !strings.Contains(err.Error(), "未找到该服务的图标") {
			t.Fatalf("zh icon error = %q", err)
		}
		i18n.WithLang(i18n.LangEN, func() {
			_, _, enErr := g.MarketIcon(context.Background(), "unknown", "")
			if enErr == nil || !strings.Contains(enErr.Error(), "Icon not found for this service") {
				t.Fatalf("en icon error = %v", enErr)
			}
		})
	}
}
