package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSailThemeTracksAppearance(t *testing.T) {
	defer setSailPalette(false)
	var dark bool
	tester := ui.NewTester(func(c *ui.Context) {
		applySailTheme(c)
		dark = c.Theme().Dark
		ui.Column(c).Size(100, 80).Background(SailBgCanvas).Children(func() { ui.Text(c, "Theme").TextColor(SailInkStrong) })
	}, 100, 80)
	lightCanvas, lightSurface, lightControl, lightInk, lightDivider := SailBgCanvas, SailBgSurface, SailBgControl, SailInkStrong, SailDivider
	tester.SetDark(true)
	if !dark || SailBgCanvas == lightCanvas || SailBgSurface == lightSurface || SailBgControl == lightControl || SailInkStrong == lightInk || SailDivider == lightDivider {
		t.Fatal("dark theme not applied")
	}
	tester.SetDark(false)
	if dark || SailBgCanvas != lightCanvas || SailBgSurface != lightSurface || SailBgControl != lightControl || SailInkStrong != lightInk || SailDivider != lightDivider {
		t.Fatal("light palette was not restored")
	}
}
