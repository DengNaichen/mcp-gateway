package main

import (
	_ "embed"
	"github.com/egoist/mygo/ui"
)

//go:embed assets/mcp.svg
var mcpLogoSVG []byte

// Stripe Sail light design system tokens (aligned with web/src/sail.css).
var (
	SailBgCanvas  = ui.Hex("#ffffff")
	SailBgSidebar = ui.Hex("#f7fafc")
	SailBgSubtle  = ui.Hex("#f7fafc")
	SailBgMuted   = ui.Hex("#eef2f6")
	SailBgHover   = ui.Hex("#f3f6f9")

	SailNavyBg     = ui.Hex("#0a2540")
	SailNavyHeader = ui.Hex("#0d2e4d")
	SailNavyBorder = ui.RGBA(255, 255, 255, 0.14)
	SailNavyText   = ui.Hex("#e6edf5")
	SailNavyMuted  = ui.Hex("#93a7be")

	SailInkStrong = ui.Hex("#1a1f36")
	SailInkBody   = ui.Hex("#3c4257")
	SailInkMuted  = ui.Hex("#697386")
	SailInkFaint  = ui.Hex("#8792a2")

	SailBorder       = ui.Hex("#e3e8ee")
	SailBorderStrong = ui.Hex("#cbd5e1")

	SailAccent        = ui.Hex("#635bff")
	SailAccentHover   = ui.Hex("#5248f5")
	SailAccentPressed = ui.Hex("#4338ca")
	SailAccentSoft    = ui.Hex("#f5f6ff")
	SailAccentBorder  = ui.Hex("#d5d2ff")

	SailSuccessBg     = ui.Hex("#dcfce7")
	SailSuccessBorder = ui.Hex("#a7f3d0")
	SailSuccessInk    = ui.Hex("#0e6245")

	SailWarningBg     = ui.Hex("#fef3c7")
	SailWarningBorder = ui.Hex("#fde68a")
	SailWarningInk    = ui.Hex("#92400e")

	SailDangerBg     = ui.Hex("#fee2e2")
	SailDangerBorder = ui.Hex("#fecaca")
	SailDangerInk    = ui.Hex("#b91c1c")

	SailInfoBg     = ui.Hex("#e0e7ff")
	SailInfoBorder = ui.Hex("#c7d2fe")
	SailInfoInk    = ui.Hex("#3730a3")

	SailNeutralBg     = ui.Hex("#f1f5f9")
	SailNeutralBorder = ui.Hex("#e2e8f0")
	SailNeutralInk    = ui.Hex("#475569")

	SailCardShadow = ui.RGBA(50, 50, 93, 0.06)
)

// BadgeTone identifies semantic status styling for pills, KPIs, and alerts.
type BadgeTone uint8

const (
	ToneNeutral BadgeTone = iota
	ToneSuccess
	ToneWarning
	ToneDanger
	ToneInfo
	ToneAccent
)

type toneColors struct {
	Bg     ui.Color
	Border ui.Color
	Ink    ui.Color
}

func colorsForTone(tone BadgeTone) toneColors {
	switch tone {
	case ToneSuccess:
		return toneColors{Bg: SailSuccessBg, Border: SailSuccessBorder, Ink: SailSuccessInk}
	case ToneWarning:
		return toneColors{Bg: SailWarningBg, Border: SailWarningBorder, Ink: SailWarningInk}
	case ToneDanger:
		return toneColors{Bg: SailDangerBg, Border: SailDangerBorder, Ink: SailDangerInk}
	case ToneInfo:
		return toneColors{Bg: SailInfoBg, Border: SailInfoBorder, Ink: SailInfoInk}
	case ToneAccent:
		return toneColors{Bg: SailAccentSoft, Border: SailAccentBorder, Ink: SailAccent}
	default:
		return toneColors{Bg: SailNeutralBg, Border: SailNeutralBorder, Ink: SailNeutralInk}
	}
}

// Crisp Lucide-style SVG icons for navigation and actions.
var (
	IconLogo = ui.MustParseSVG(mcpLogoSVG)

	IconHome = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M15 21v-8a1 1 0 0 0-1-1h-4a1 1 0 0 0-1 1v8"/><path d="M3 10a2 2 0 0 1 .709-1.528l7-5.999a2 2 0 0 1 2.582 0l7 5.999A2 2 0 0 1 21 10v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>`))

	IconServer = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect width="20" height="8" x="2" y="2" rx="2" ry="2"/><rect width="20" height="8" x="2" y="14" rx="2" ry="2"/><line x1="6" x2="6.01" y1="6" y2="6"/><line x1="6" x2="6.01" y1="18" y2="18"/></svg>`))

	IconLayers = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m12.83 2.18a2 2 0 0 0-1.66 0L2.6 6.08a1 1 0 0 0 0 1.83l8.58 3.91a2 2 0 0 0 1.66 0l8.58-3.9a1 1 0 0 0 0-1.83Z"/><path d="m22 17.65-9.17 4.16a2 2 0 0 1-1.66 0L2 17.65"/><path d="m22 12.65-9.17 4.16a2 2 0 0 1-1.66 0L2 12.65"/></svg>`))

	IconShield = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/></svg>`))

	IconActivity = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M22 12h-2.48a2 2 0 0 0-1.93 1.46l-2.35 8.36a.25.25 0 0 1-.48 0L9.24 2.18a.25.25 0 0 0-.48 0l-2.35 8.36A2 2 0 0 1 4.49 12H2"/></svg>`))

	IconWaypoints = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="4.5" r="2.5"/><path d="m10.2 6.3-3.9 3.9"/><circle cx="4.5" cy="12" r="2.5"/><path d="M7 12h10"/><circle cx="19.5" cy="12" r="2.5"/><path d="m13.8 17.7 3.9-3.9"/><circle cx="12" cy="19.5" r="2.5"/></svg>`))

	IconRefresh = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/></svg>`))
)

// applySailTheme installs the Stripe Sail light theme on the UI context.
func applySailTheme(c *ui.Context) {
	t := *ui.LightTheme().WithAccent(SailAccent)
	t.Background = SailBgCanvas
	t.Surface = SailBgSubtle
	t.SurfaceHover = SailBgHover
	t.SurfacePressed = SailBgMuted
	t.Border = SailBorder
	t.Text = SailInkBody
	t.TextMuted = SailInkMuted
	t.Accent = SailAccent
	t.AccentHover = SailAccentHover
	t.AccentPressed = SailAccentPressed
	t.AccentText = ui.Hex("#ffffff")
	t.InactiveAccent = SailAccent
	t.Danger = SailDangerInk
	t.Warning = SailWarningInk
	t.Success = SailSuccessInk
	t.Focus = SailAccent
	t.Inverse = SailNavyBg
	t.InverseText = SailNavyText
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 13.5
	c.SetTheme(&t)
}

// SailCard renders a standard Stripe Sail white surface card with subtle border and elevation.
func SailCard(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).
		FillWidth().
		Padding(18).
		Gap(12).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow).
		Children(fn)
}

// SailSubtleCard renders a light gray (#f7fafc) inset card for secondary sections.
func SailSubtleCard(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).
		FillWidth().
		Padding(14).
		Gap(10).
		Radius(6).
		Background(SailBgSubtle).
		Border(1, SailBorder).
		Children(fn)
}

// SailCardTitle renders a consistent card header row with optional right-aligned actions.
func SailCardTitle(c *ui.Context, title, subtitle string, right func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, title).FontSize(14.5).FontWeight(600).TextColor(SailInkStrong)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12).TextColor(SailInkMuted)
			}
		})
		if right != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// SailSectionHeader renders a top-of-page title, description, and right-aligned action buttons.
func SailSectionHeader(c *ui.Context, title, subtitle string, right func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, title).FontSize(20).Bold().TextColor(SailInkStrong)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(13).TextColor(SailInkMuted)
			}
		})
		if right != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// SailBadge renders a semantic pill badge.
func SailBadge(c *ui.Context, label string, tone BadgeTone) ui.Element {
	tc := colorsForTone(tone)
	return ui.Row(c).
		Padding(2, 8).
		Radius(999).
		Background(tc.Bg).
		Border(1, tc.Border).
		AlignItems(ui.Center).
		Gap(5).
		Children(func() {
			ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(tc.Ink).SingleLine()
		})
}

// SailBadgeDot renders a semantic pill badge with a leading status dot.
func SailBadgeDot(c *ui.Context, label string, tone BadgeTone) ui.Element {
	tc := colorsForTone(tone)
	return ui.Row(c).
		Padding(2, 8).
		Radius(999).
		Background(tc.Bg).
		Border(1, tc.Border).
		AlignItems(ui.Center).
		Gap(5).
		Children(func() {
			ui.Box(c).Size(6, 6).Radius(999).Background(tc.Ink)
			ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(tc.Ink).SingleLine()
		})
}

// SailStatusDot renders a small circular status dot.
func SailStatusDot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(7, 7).Radius(999).Background(color).Shrink(0)
}

// SailKPICard renders a metric card with label, prominent figure, hint, and optional click handler.
func SailKPICard(c *ui.Context, label, value, hint string, tone BadgeTone, onClick func()) {
	tc := colorsForTone(tone)
	card := ui.Column(c).
		Grow(1).
		MinWidth(180).
		Padding(16).
		Gap(6).
		Radius(8).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 3, 0, SailCardShadow)
	if onClick != nil {
		card.Cursor(ui.CursorPointer)
		if card.Hovered() {
			card.Background(SailBgHover).Border(1, SailBorderStrong)
		}
		card.OnClick(onClick)
	}
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(SailInkMuted).SingleLine()
			if tone != ToneNeutral {
				SailStatusDot(c, tc.Ink)
			}
		})
		ui.Text(c, value).FontSize(24).Bold().TextColor(SailInkStrong).FontFeatures("tnum")
		if hint != "" {
			ui.Text(c, hint).FontSize(11.5).TextColor(SailInkMuted)
		}
	})
}

// SailEmptyState renders a centered placeholder box with optional primary action.
func SailEmptyState(c *ui.Context, title, description, actionLabel string, onAction func()) {
	ui.Column(c).
		FillWidth().
		Padding(28, 20).
		Gap(8).
		Radius(8).
		Background(SailBgSubtle).
		Border(1, SailBorder).
		AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(SailInkStrong)
			if description != "" {
				ui.Text(c, description).FontSize(12.5).TextColor(SailInkMuted).TextAlign(ui.Center).MaxWidth(480)
			}
			if actionLabel != "" && onAction != nil {
				if ui.PrimaryButton(c, actionLabel).Margin(6, 0, 0, 0).Clicked() {
					onAction()
				}
			}
		})
}

// SailKeyValueRow renders a two-column metadata row.
func SailKeyValueRow(c *ui.Context, key, value string, mono bool) {
	ui.Row(c).FillWidth().Padding(5, 0).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Text(c, key).Width(130).Shrink(0).FontSize(12).FontWeight(500).TextColor(SailInkMuted)
		val := ui.Text(c, value).Grow(1).MinWidth(0).FontSize(12.5).TextColor(SailInkStrong)
		if mono {
			val.Font("monospace")
		}
	})
}

// SailCodeBlock renders a Deep Navy (#0a2540) code/JSON viewer with a one-click copy button.
func SailCodeBlock(c *ui.Context, title, code string, onCopied func()) {
	ui.Column(c).
		FillWidth().
		Radius(8).
		Background(SailNavyBg).
		Border(1, SailNavyBorder).
		Clip().
		Children(func() {
			ui.Row(c).
				FillWidth().
				Padding(8, 12).
				Background(SailNavyHeader).
				BorderWidth(0, 0, 1, 0).
				BorderColor(SailNavyBorder).
				AlignItems(ui.Center).
				Justify(ui.SpaceBetween).
				Children(func() {
					label := title
					if label == "" {
						label = "CODE"
					}
					ui.Text(c, label).FontSize(11.5).FontWeight(600).TextColor(SailNavyMuted).Font("monospace")
					copyBtn := ui.ButtonBase(c).
						Padding(4, 10).
						Radius(5).
						Background(ui.RGBA(255, 255, 255, 0.08)).
						Border(1, SailNavyBorder).
						Cursor(ui.CursorPointer)
					if copyBtn.Hovered() {
						copyBtn.Background(ui.RGBA(255, 255, 255, 0.15))
					}
					copyBtn.Children(func() {
						ui.Text(c, "复制").FontSize(11.5).FontWeight(500).TextColor(SailNavyText).SingleLine()
					})
					if copyBtn.Clicked() {
						c.WriteClipboard(code)
						if onCopied != nil {
							onCopied()
						} else {
							c.Toast("已复制到剪贴板")
						}
					}
				})
			ui.ScrollBoth(c).FillWidth().MaxHeight(320).Padding(12).Children(func() {
				ui.Text(c, code).
					Font("monospace").
					FontSize(12).
					FixedLineHeight(18).
					TextColor(SailNavyText).
					Selectable().
					NoWrap()
			})
		})
}

// SailAlertBox renders a tinted semantic banner for errors, warnings, or info.
func SailAlertBox(c *ui.Context, tone BadgeTone, title, detail string) {
	if title == "" && detail == "" {
		return
	}
	tc := colorsForTone(tone)
	ui.Column(c).
		FillWidth().
		Padding(12, 14).
		Gap(4).
		Radius(6).
		Background(tc.Bg).
		Border(1, tc.Border).
		Children(func() {
			if title != "" {
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(tc.Ink)
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(tc.Ink).Selectable()
			}
		})
}

// SailSmallButton renders a compact secondary button styled with Stripe Sail keyline borders.
func SailSmallButton(c *ui.Context, label string) ui.Element {
	return ui.Button(c, label).Padding(5, 11).FontSize(12.5)
}

// SailDangerButton renders a compact red-tinted action button for destructive or warning actions.
func SailDangerButton(c *ui.Context, label string) ui.Element {
	btn := ui.ButtonBase(c).
		Padding(5, 11).
		Radius(6).
		Background(SailDangerBg).
		Border(1, SailDangerBorder).
		Cursor(ui.CursorPointer)
	if btn.Hovered() {
		btn.Background(ui.Hex("#fecaca"))
	}
	btn.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(SailDangerInk).SingleLine()
	})
	return btn
}
