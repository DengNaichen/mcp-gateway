package main

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

func TestSailThemeTracksAppearance(t *testing.T) {
	defer setSailPalette(false)
	var dark bool
	tester := ui.NewTester(func(c *ui.Context) {
		applySailTheme(c)
		dark = c.Theme().Dark
		ui.Column(c).Size(100, 80).Background(SailBgCanvas).Children(func() { ui.Text(c, "Theme").TextColor(SailInkStrong) })
	}, 100, 80)
	lightCanvas, lightInk, lightDivider := SailBgCanvas, SailInkStrong, SailDivider
	tester.SetDark(true)
	if !dark || SailBgCanvas == lightCanvas || SailInkStrong == lightInk || SailDivider == lightDivider {
		t.Fatal("dark theme not applied")
	}
	tester.SetDark(false)
	if dark || SailBgCanvas != lightCanvas || SailInkStrong != lightInk || SailDivider != lightDivider {
		t.Fatal("light palette was not restored")
	}
}
