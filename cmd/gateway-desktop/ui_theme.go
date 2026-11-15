package main

import (
	_ "embed"
	"regexp"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/mcp.svg
var mcpLogoSVG []byte

// Stripe Sail / Linear design tokens; refreshed from the system appearance each frame.
var (
	SailBgCanvas  = ui.Hex("#ffffff")
	SailBgSidebar = ui.Hex("#f8fafc")
	SailBgSubtle  = ui.Hex("#f7f9fc")
	SailBgMuted   = ui.Hex("#eef2f6")
	SailBgHover   = ui.Hex("#f3f6fa")

	SailNavyBg     = ui.Hex("#0a2540")
	SailNavyHeader = ui.Hex("#0d2e4d")
	SailNavyBorder = ui.RGBA(255, 255, 255, 0.14)
	SailNavyText   = ui.Hex("#e6edf5")
	SailNavyMuted  = ui.Hex("#93a7be")

	SailInkStrong = ui.Hex("#111827")
	SailInkBody   = ui.Hex("#374151")
	SailInkMuted  = ui.Hex("#6b7280")
	SailInkFaint  = ui.Hex("#9ca3af")

	SailBorder       = ui.Hex("#e5e9f0")
	SailDivider      = ui.Hex("#edf1f6")
	SailBorderStrong = ui.Hex("#cbd5e1")

	SailAccent        = ui.Hex("#635bff")
	SailAccentHover   = ui.Hex("#5248f5")
	SailAccentPressed = ui.Hex("#4338ca")
	SailAccentSoft    = ui.Hex("#f5f6ff")
	SailAccentBorder  = ui.Hex("#d5d2ff")

	SailSuccessBg     = ui.Hex("#dcfce7")
	SailSuccessBorder = ui.Hex("#bbf7d0")
	SailSuccessInk    = ui.Hex("#15803d")

	SailWarningBg     = ui.Hex("#fef3c7")
	SailWarningBorder = ui.Hex("#fde68a")
	SailWarningInk    = ui.Hex("#b45309")

	SailDangerBg     = ui.Hex("#fee2e2")
	SailDangerBorder = ui.Hex("#fecaca")
	SailDangerInk    = ui.Hex("#b91c1c")

	SailInfoBg     = ui.Hex("#e0e7ff")
	SailInfoBorder = ui.Hex("#c7d2fe")
	SailInfoInk    = ui.Hex("#3730a3")

	SailNeutralBg     = ui.Hex("#f1f5f9")
	SailNeutralBorder = ui.Hex("#e2e8f0")
	SailNeutralInk    = ui.Hex("#475569")

	SailCardShadow = ui.RGBA(15, 23, 42, 0.035)
)

// setSailPalette swaps the Sail tokens for the light or dark palette.
func setSailPalette(dark bool) {
	if !dark {
		SailBgCanvas = ui.Hex("#ffffff")
		SailBgSidebar = ui.Hex("#f8fafc")
		SailBgSubtle = ui.Hex("#f7f9fc")
		SailBgMuted = ui.Hex("#eef2f6")
		SailBgHover = ui.Hex("#f3f6fa")
		SailNavyBg = ui.Hex("#0a2540")
		SailNavyHeader = ui.Hex("#0d2e4d")
		SailNavyBorder = ui.RGBA(255, 255, 255, 0.14)
		SailNavyText = ui.Hex("#e6edf5")
		SailNavyMuted = ui.Hex("#93a7be")
		SailInkStrong = ui.Hex("#111827")
		SailInkBody = ui.Hex("#374151")
		SailInkMuted = ui.Hex("#6b7280")
		SailInkFaint = ui.Hex("#9ca3af")
		SailBorder = ui.Hex("#e5e9f0")
		SailDivider = ui.Hex("#edf1f6")
		SailBorderStrong = ui.Hex("#cbd5e1")
		SailAccent = ui.Hex("#635bff")
		SailAccentHover = ui.Hex("#5248f5")
		SailAccentPressed = ui.Hex("#4338ca")
		SailAccentSoft = ui.Hex("#f5f6ff")
		SailAccentBorder = ui.Hex("#d5d2ff")
		SailSuccessBg = ui.Hex("#dcfce7")
		SailSuccessBorder = ui.Hex("#bbf7d0")
		SailSuccessInk = ui.Hex("#15803d")
		SailWarningBg = ui.Hex("#fef3c7")
		SailWarningBorder = ui.Hex("#fde68a")
		SailWarningInk = ui.Hex("#b45309")
		SailDangerBg = ui.Hex("#fee2e2")
		SailDangerBorder = ui.Hex("#fecaca")
		SailDangerInk = ui.Hex("#b91c1c")
		SailInfoBg = ui.Hex("#e0e7ff")
		SailInfoBorder = ui.Hex("#c7d2fe")
		SailInfoInk = ui.Hex("#3730a3")
		SailNeutralBg = ui.Hex("#f1f5f9")
		SailNeutralBorder = ui.Hex("#e2e8f0")
		SailNeutralInk = ui.Hex("#475569")
		SailCardShadow = ui.RGBA(15, 23, 42, 0.035)
		return
	}
	SailDivider = ui.Hex("#1d293c")
	SailBgCanvas, SailBgSidebar, SailBgSubtle = ui.Hex("#0f1626"), ui.Hex("#0b1120"), ui.Hex("#131c2e")
	SailBgMuted, SailBgHover = ui.Hex("#1b263b"), ui.Hex("#18223a")
	SailNavyBg, SailNavyHeader = ui.Hex("#060d1a"), ui.Hex("#0a1426")
	SailInkStrong, SailInkBody, SailInkMuted, SailInkFaint = ui.Hex("#f1f5f9"), ui.Hex("#cbd5e1"), ui.Hex("#94a3b8"), ui.Hex("#64748b")
	SailBorder, SailBorderStrong = ui.Hex("#243049"), ui.Hex("#34425f")
	SailAccent, SailAccentHover, SailAccentPressed = ui.Hex("#7a73ff"), ui.Hex("#8f89ff"), ui.Hex("#635bff")
	SailAccentSoft, SailAccentBorder = ui.Hex("#1e1d4a"), ui.Hex("#3b3894")
	SailSuccessBg, SailSuccessBorder, SailSuccessInk = ui.Hex("#0d2f22"), ui.Hex("#14573f"), ui.Hex("#4ade9d")
	SailWarningBg, SailWarningBorder, SailWarningInk = ui.Hex("#33260a"), ui.Hex("#5c4413"), ui.Hex("#fbbf24")
	SailDangerBg, SailDangerBorder, SailDangerInk = ui.Hex("#381416"), ui.Hex("#661f24"), ui.Hex("#f87171")
	SailInfoBg, SailInfoBorder, SailInfoInk = ui.Hex("#1a2150"), ui.Hex("#2f3a8a"), ui.Hex("#a5b4fc")
	SailNeutralBg, SailNeutralBorder, SailNeutralInk = ui.Hex("#1b263b"), ui.Hex("#2b3750"), ui.Hex("#a3b1c6")
	SailCardShadow = ui.RGBA(0, 0, 0, 0.35)
}

// BadgeTone identifies semantic status styling for high-signal pills and alerts.
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

	IconChevronRight = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m9 18 6-6-6-6"/></svg>`))
)

// applySailTheme follows the system appearance while preserving the decluttered palette.
func applySailTheme(c *ui.Context) {
	dark := c.Theme().Dark
	setSailPalette(dark)
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	t := *base.WithAccent(SailAccent)
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

// SailCard renders a spacious white surface card with single-layer border and light shadow.
func SailCard(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).
		FillWidth().
		Padding(20, 22).
		Gap(16).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Children(fn)
}

// SailFlushCard renders a zero-padding card container for hairline-divided lists (no card-in-card borders).
func SailFlushCard(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).
		FillWidth().
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow).
		Clip().
		Children(fn)
}

// SailListCardHeader renders a clean header row at the top of a SailFlushCard.
func SailListCardHeader(c *ui.Context, title, note string, right func()) {
	ui.Row(c).
		FillWidth().
		Padding(16, 20).
		Gap(12).
		BorderWidth(0, 0, 1, 0).
		BorderColor(SailDivider).
		AlignItems(ui.Center).
		Justify(ui.SpaceBetween).
		Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Gap(10).AlignItems(ui.Baseline).Children(func() {
				ui.Text(c, title).FontSize(14.5).FontWeight(600).TextColor(SailInkStrong).SingleLine()
				if note != "" {
					ui.Text(c, note).FontSize(12).TextColor(SailInkMuted).SingleLine()
				}
			})
			if right != nil {
				ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(right)
			}
		})
}

// SailSubtleCard renders a borderless soft inset surface so nested sections never stack double borders.
func SailSubtleCard(c *ui.Context, fn func()) ui.Element {
	return ui.Column(c).
		FillWidth().
		Padding(14, 16).
		Gap(10).
		Radius(8).
		Background(SailBgSubtle).
		Children(fn)
}

// SailCardTitle renders a consistent card header row with optional short note and right-aligned actions.
func SailCardTitle(c *ui.Context, title, subtitle string, right func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(12).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600).TextColor(SailInkStrong)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12).TextColor(SailInkMuted)
			}
		})
		if right != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// SailSectionHeader renders a restrained top-of-page title and right-aligned primary actions.
func SailSectionHeader(c *ui.Context, title, subtitle string, right func()) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(16).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(21).Bold().LetterSpacing(-0.2).TextColor(SailInkStrong)
			if subtitle != "" {
				ui.Text(c, subtitle).FontSize(12.5).TextColor(SailInkMuted)
			}
		})
		if right != nil {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(right)
		}
	})
}

// SailBadge renders a semantic pill badge. Reserve high-saturation tones for RW, warnings, or errors.
func SailBadge(c *ui.Context, label string, tone BadgeTone) ui.Element {
	tc := colorsForTone(tone)
	return ui.Row(c).
		Padding(2, 7).
		Radius(999).
		Background(tc.Bg).
		Border(1, tc.Border).
		AlignItems(ui.Center).
		Gap(4).
		Children(func() {
			ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(tc.Ink).SingleLine()
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
			ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(tc.Ink).SingleLine()
		})
}

// SailQuietStatus renders a low-noise dot + plain text indicator for normal healthy states (Badge Diet).
func SailQuietStatus(c *ui.Context, label string, dotColor, textColor ui.Color) ui.Element {
	return ui.Row(c).Gap(6).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(c).Size(6, 6).Radius(999).Background(dotColor).Shrink(0)
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(textColor).SingleLine()
	})
}

// SailStatusDot renders a small circular status dot.
func SailStatusDot(c *ui.Context, color ui.Color) ui.Element {
	return ui.Box(c).Size(6.5, 6.5).Radius(999).Background(color).Shrink(0)
}

// SailKPICard renders a clean metric card with generous padding and optional click-to-filter state.
func SailKPICard(c *ui.Context, label, value, hint string, tone BadgeTone, onClick func()) {
	tc := colorsForTone(tone)
	card := ui.Column(c).
		Grow(1).
		MinWidth(176).
		Padding(18, 20).
		Gap(6).
		Radius(10).
		Background(SailBgCanvas).
		Border(1, SailBorder).
		Shadow(0, 1, 2, 0, SailCardShadow)
	if onClick != nil {
		card.Cursor(ui.CursorPointer)
		if card.Hovered() {
			card.Background(SailBgHover).Border(1, SailBorderStrong)
		}
		card.OnClick(onClick)
	}
	card.Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			if tone == ToneWarning || tone == ToneDanger {
				SailStatusDot(c, tc.Ink)
			}
		})
		ui.Text(c, value).FontSize(24).Bold().TextColor(SailInkStrong).FontFeatures("tnum")
		if hint != "" {
			ui.Text(c, hint).FontSize(11.5).TextColor(SailInkFaint).SingleLine()
		}
	})
}

// SailEmptyState renders a calm centered placeholder with optional primary action.
func SailEmptyState(c *ui.Context, title, description, actionLabel string, onAction func()) {
	ui.Column(c).
		FillWidth().
		Padding(32, 24).
		Gap(8).
		Radius(8).
		Background(SailBgSubtle).
		AlignItems(ui.Center).
		Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(SailInkStrong)
			if description != "" {
				ui.Text(c, description).FontSize(12.5).LineHeight(1.5).TextColor(SailInkMuted).TextAlign(ui.Center).MaxWidth(440)
			}
			if actionLabel != "" && onAction != nil {
				if ui.PrimaryButton(c, actionLabel).Margin(6, 0, 0, 0).Clicked() {
					onAction()
				}
			}
		})
}

// SailKeyValueRow renders a two-column hairline metadata row.
func SailKeyValueRow(c *ui.Context, key, value string, mono bool) {
	ui.Row(c).FillWidth().Padding(6, 0).Gap(12).AlignItems(ui.Start).Children(func() {
		ui.Text(c, key).Width(116).Shrink(0).FontSize(12).FontWeight(500).TextColor(SailInkMuted)
		val := ui.Text(c, value).Grow(1).MinWidth(0).FontSize(12.5).LineHeight(1.45).TextColor(SailInkStrong).Selectable()
		if mono {
			val.Font("monospace").FontSize(12)
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
						label = "JSON"
					}
					ui.Text(c, label).FontSize(11).FontWeight(600).TextColor(SailNavyMuted).Font("monospace")
					copyBtn := ui.ButtonBase(c).
						Padding(3, 9).
						Radius(5).
						Background(ui.RGBA(255, 255, 255, 0.08)).
						Border(1, SailNavyBorder).
						Cursor(ui.CursorPointer)
					if copyBtn.Hovered() {
						copyBtn.Background(ui.RGBA(255, 255, 255, 0.15))
					}
					copyBtn.Children(func() {
						ui.Text(c, "复制").FontSize(11).FontWeight(500).TextColor(SailNavyText).SingleLine()
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
			ui.ScrollBoth(c).FillWidth().MaxHeight(300).Padding(12, 14).Children(func() {
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

// SailAlertBox renders a tinted semantic banner for errors, warnings, or status feedback.
func SailAlertBox(c *ui.Context, tone BadgeTone, title, detail string) {
	if title == "" && detail == "" {
		return
	}
	tc := colorsForTone(tone)
	ui.Column(c).
		FillWidth().
		Padding(12, 14).
		Gap(4).
		Radius(8).
		Background(tc.Bg).
		Border(1, tc.Border).
		Children(func() {
			if title != "" {
				ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(tc.Ink)
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(12).LineHeight(1.45).TextColor(tc.Ink).Selectable()
			}
		})
}

// SailSmallButton renders a compact secondary button styled with subtle keyline borders.
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
		btn.Background(SailDangerBorder)
	}
	btn.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(SailDangerInk).SingleLine()
	})
	return btn
}

// --- Unified Description Cleaning & Structured Typography ---

var (
	descFenceRe      = regexp.MustCompile("(?s)```.*?```")
	descLinkRe       = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)
	descNumberedRe   = regexp.MustCompile(`^\d+[.)]\s+`)
	descInlineTailRe = regexp.MustCompile(`(?i)(?:^|\s+)(args|arguments|parameters|params|returns|return|examples?|参数|参数说明|输入参数|返回值?)\s*[:：].*$`)
	descParamLineRe  = regexp.MustCompile("^[-*•+]?\\s*`?([A-Za-z0-9_.-]+)`?(?:\\s*\\(([^)]*)\\))?\\s*[:：-]\\s*(.+)$")
	descSpaceRe      = regexp.MustCompile(`\s+`)
)

// DescParamItem represents an extracted parameter line from an Args:/Parameters: block.
type DescParamItem struct {
	Name string
	Type string
	Desc string
}

// StructuredDescription represents a parsed tool or service description for detail/inspector views.
type StructuredDescription struct {
	Lead       string
	Paragraphs []string
	Bullets    []string
	Params     []DescParamItem
	HasMoreRaw bool
}

func isDescSectionHeader(line string) (kind string, rest string) {
	trimmed := strings.TrimSpace(strings.TrimLeft(line, "#*>- "))
	trimmed = strings.Trim(trimmed, "*_")
	lower := strings.ToLower(trimmed)

	paramPrefixes := []string{
		"args:", "args：", "arguments:", "arguments：",
		"parameters:", "parameters：", "params:", "params：",
		"inputs:", "inputs：", "input:", "input：",
		"参数:", "参数：", "参数说明:", "参数说明：", "输入参数:", "输入参数：",
	}
	for _, p := range paramPrefixes {
		if strings.HasPrefix(lower, p) {
			return "params", strings.TrimSpace(trimmed[len(p):])
		}
	}

	otherPrefixes := []string{
		"returns:", "returns：", "return:", "return：",
		"outputs:", "outputs：", "output:", "output：",
		"example:", "example：", "examples:", "examples：",
		"usage:", "usage：", "raises:", "raises：",
		"返回:", "返回：", "返回值:", "返回值：", "示例:", "示例：",
	}
	for _, p := range otherPrefixes {
		if strings.HasPrefix(lower, p) {
			return "meta", strings.TrimSpace(trimmed[len(p):])
		}
	}
	return "", ""
}

func stripInlineMarkdown(s string) string {
	s = descLinkRe.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "**", "")
	s = strings.ReplaceAll(s, "__", "")
	s = strings.ReplaceAll(s, "`", "")
	s = descSpaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func isIdentifierOnlyHeading(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.' || r == '/') {
			return false
		}
		if r > 127 {
			return false
		}
	}
	return true
}

func extractFirstSentence(text string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return ""
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == '。' || r == '！' || r == '？' || r == '；' {
			if i >= 5 {
				if r == '；' {
					return strings.TrimSpace(string(runes[:i]))
				}
				return strings.TrimSpace(string(runes[:i+1]))
			}
		}
		if (r == '.' || r == '!' || r == '?') && i >= 6 {
			if i+1 == len(runes) {
				return strings.TrimSpace(string(runes[:i+1]))
			}
			if unicode.IsSpace(runes[i+1]) {
				// Avoid splitting version numbers (v1.0) or abbreviations (e.g., i.e.).
				prev := runes[i-1]
				if unicode.IsDigit(prev) {
					continue
				}
				tailLower := strings.ToLower(string(runes[max(0, i-3) : i+1]))
				if strings.HasSuffix(tailLower, "e.g.") || strings.HasSuffix(tailLower, "i.e.") {
					continue
				}
				return strings.TrimSpace(string(runes[:i+1]))
			}
		}
	}
	if maxRunes > 0 && len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes])) + "…"
	}
	return strings.TrimSpace(string(runes))
}

// CleanDescriptionSummary extracts a crisp one-sentence core summary suitable for 1–2 line list rows.
func CleanDescriptionSummary(raw string) string {
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	norm = strings.TrimSpace(norm)
	if norm == "" {
		return ""
	}

	norm = descFenceRe.ReplaceAllString(norm, "\n")
	lines := strings.Split(norm, "\n")

	var paraParts []string
	for idx, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || line == "---" || line == "===" || line == "***" {
			if len(paraParts) > 0 {
				break
			}
			continue
		}
		if kind, _ := isDescSectionHeader(line); kind != "" {
			break
		}

		isHeading := strings.HasPrefix(line, "#")
		cleanedLine := strings.TrimSpace(strings.TrimLeft(line, "#>"))
		if isHeading && isIdentifierOnlyHeading(cleanedLine) && idx+1 < len(lines) {
			continue
		}

		// Strip leading list markers if description starts directly with bullets.
		for _, prefix := range []string{"- ", "* ", "+ ", "• "} {
			if strings.HasPrefix(cleanedLine, prefix) {
				if len(paraParts) > 0 {
					goto doneLines
				}
				cleanedLine = strings.TrimSpace(strings.TrimPrefix(cleanedLine, prefix))
				break
			}
		}
		if loc := descNumberedRe.FindStringIndex(cleanedLine); loc != nil {
			if len(paraParts) > 0 {
				break
			}
			cleanedLine = strings.TrimSpace(cleanedLine[loc[1]:])
		}

		cleanedLine = descInlineTailRe.ReplaceAllString(cleanedLine, "")
		cleanedLine = stripInlineMarkdown(cleanedLine)
		if cleanedLine != "" {
			paraParts = append(paraParts, cleanedLine)
			// Stop after first complete heading line so heading + body don't run together without punctuation.
			if isHeading {
				break
			}
		}
	}
doneLines:
	joined := strings.Join(paraParts, " ")
	joined = descInlineTailRe.ReplaceAllString(joined, "")
	joined = stripInlineMarkdown(joined)
	return extractFirstSentence(joined, 140)
}

// SailDescSummary renders a cleaned secondary summary in list rows without blowing up row height.
func SailDescSummary(c *ui.Context, raw, emptyFallback string, maxLines int) ui.Element {
	summary := CleanDescriptionSummary(raw)
	if summary == "" {
		if emptyFallback == "" {
			return ui.Element{}
		}
		return ui.Text(c, emptyFallback).
			FontSize(12).
			TextColor(SailInkFaint).
			SingleLine()
	}
	t := ui.Text(c, summary).
		FontSize(12.5).
		LineHeight(1.42).
		TextColor(SailInkMuted)
	if maxLines <= 1 {
		t.SingleLine()
	} else {
		t.MaxLines(maxLines)
	}
	return t
}

// ParseStructuredDescription splits a raw description into lead prose, bullets, and parameter blocks.
func ParseStructuredDescription(raw string) StructuredDescription {
	var out StructuredDescription
	norm := strings.ReplaceAll(raw, "\r\n", "\n")
	norm = strings.ReplaceAll(norm, "\r", "\n")
	norm = strings.TrimSpace(norm)
	if norm == "" {
		return out
	}

	hasCodeFence := strings.Contains(norm, "```")
	withoutFences := descFenceRe.ReplaceAllString(norm, "\n")
	lines := strings.Split(withoutFences, "\n")

	mode := "prose" // "prose", "params", "meta"
	var curPara []string
	flushPara := func() {
		if len(curPara) == 0 {
			return
		}
		p := strings.TrimSpace(strings.Join(curPara, " "))
		curPara = nil
		if p == "" {
			return
		}
		if out.Lead == "" {
			out.Lead = p
		} else {
			out.Paragraphs = append(out.Paragraphs, p)
		}
	}

	for idx, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || trimmed == "---" || trimmed == "===" {
			flushPara()
			continue
		}

		if secKind, rest := isDescSectionHeader(trimmed); secKind != "" {
			flushPara()
			mode = secKind
			if rest != "" && secKind == "params" {
				if m := descParamLineRe.FindStringSubmatch(rest); m != nil {
					out.Params = append(out.Params, DescParamItem{
						Name: m[1],
						Type: strings.TrimSpace(m[2]),
						Desc: stripInlineMarkdown(m[3]),
					})
				}
			} else if rest != "" && secKind == "meta" {
				out.Bullets = append(out.Bullets, rest)
			}
			continue
		}

		if mode == "params" {
			if m := descParamLineRe.FindStringSubmatch(trimmed); m != nil {
				out.Params = append(out.Params, DescParamItem{
					Name: m[1],
					Type: strings.TrimSpace(m[2]),
					Desc: stripInlineMarkdown(m[3]),
				})
				continue
			}
			isIndented := strings.HasPrefix(rawLine, "  ") || strings.HasPrefix(rawLine, "\t")
			if isIndented && len(out.Params) > 0 {
				last := &out.Params[len(out.Params)-1]
				last.Desc = strings.TrimSpace(last.Desc + " " + stripInlineMarkdown(trimmed))
				continue
			}
			mode = "prose"
		}

		if mode == "meta" {
			item := strings.TrimSpace(strings.TrimLeft(trimmed, "-*•+ "))
			if item != "" {
				out.Bullets = append(out.Bullets, item)
			}
			continue
		}

		// Prose / Heading / Bullet mode
		if strings.HasPrefix(trimmed, "#") {
			flushPara()
			h := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if isIdentifierOnlyHeading(h) && idx+1 < len(lines) {
				continue
			}
			if h != "" {
				curPara = append(curPara, h)
				flushPara()
			}
			continue
		}

		isBullet := false
		var bulletBody string
		for _, pfx := range []string{"- ", "* ", "+ ", "• "} {
			if strings.HasPrefix(trimmed, pfx) {
				isBullet = true
				bulletBody = strings.TrimSpace(strings.TrimPrefix(trimmed, pfx))
				break
			}
		}
		if !isBullet {
			if loc := descNumberedRe.FindStringIndex(trimmed); loc != nil {
				isBullet = true
				bulletBody = strings.TrimSpace(trimmed[loc[1]:])
			}
		}
		if isBullet {
			flushPara()
			if bulletBody != "" {
				out.Bullets = append(out.Bullets, bulletBody)
			}
			continue
		}

		// Check for inline "Args: ..." tail inside a single-line description
		if loc := descInlineTailRe.FindStringIndex(trimmed); loc != nil && loc[0] > 0 {
			head := strings.TrimSpace(trimmed[:loc[0]])
			tail := strings.TrimSpace(trimmed[loc[0]:])
			if head != "" {
				curPara = append(curPara, head)
			}
			flushPara()
			if _, rest := isDescSectionHeader(tail); rest != "" {
				out.HasMoreRaw = true
			}
			continue
		}

		curPara = append(curPara, descLinkRe.ReplaceAllString(trimmed, "$1"))
	}
	flushPara()

	if out.Lead == "" && len(out.Bullets) > 0 {
		out.Lead = out.Bullets[0]
		out.Bullets = out.Bullets[1:]
	}

	if hasCodeFence ||
		len(out.Paragraphs) > 1 ||
		len(out.Bullets) > 5 ||
		len(out.Params) > 0 ||
		(strings.Contains(norm, "\n") && len([]rune(norm)) > len([]rune(out.Lead))+20) {
		out.HasMoreRaw = true
	}
	return out
}

// buildInlineSpans converts inline `code` and **bold** markers into crisp RichText spans.
func buildInlineSpans(text string, baseSize float32) []ui.Span {
	text = descLinkRe.ReplaceAllString(text, "$1")
	var spans []ui.Span
	rem := text
	for len(rem) > 0 {
		tickIdx := strings.IndexByte(rem, '`')
		boldIdx := strings.Index(rem, "**")

		if tickIdx == -1 && boldIdx == -1 {
			spans = append(spans, ui.Span{Text: rem})
			break
		}

		if tickIdx != -1 && (boldIdx == -1 || tickIdx < boldIdx) {
			end := strings.IndexByte(rem[tickIdx+1:], '`')
			if end > 0 {
				if tickIdx > 0 {
					spans = append(spans, ui.Span{Text: rem[:tickIdx]})
				}
				codeStr := rem[tickIdx+1 : tickIdx+1+end]
				spans = append(spans, ui.Span{
					Text:       codeStr,
					Font:       "monospace",
					Size:       baseSize - 1,
					Color:      SailInkStrong,
					Background: SailBgMuted,
					Radius:     4,
					PaddingX:   4,
					PaddingY:   1,
				})
				rem = rem[tickIdx+1+end+1:]
				continue
			}
		}

		if boldIdx != -1 {
			end := strings.Index(rem[boldIdx+2:], "**")
			if end > 0 {
				if boldIdx > 0 {
					spans = append(spans, ui.Span{Text: rem[:boldIdx]})
				}
				boldStr := rem[boldIdx+2 : boldIdx+2+end]
				spans = append(spans, ui.Span{
					Text:   boldStr,
					Weight: 600,
					Color:  SailInkStrong,
				})
				rem = rem[boldIdx+2+end+2:]
				continue
			}
		}

		// Unmatched delimiter: emit as plain text.
		spans = append(spans, ui.Span{Text: rem})
		break
	}
	return spans
}

func renderInlineRichText(c *ui.Context, text string, fontSize, lineHeight float32, color ui.Color) ui.Element {
	spans := buildInlineSpans(text, fontSize)
	return ui.RichText(c, spans...).
		FontSize(fontSize).
		LineHeight(lineHeight).
		TextColor(color).
		Pretty().
		Selectable()
}

// SailDescriptionDetail renders a structured, high-readability description block for detail/inspector views.
func SailDescriptionDetail(c *ui.Context, key, raw string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		ui.Text(c, "暂无功能描述").FontSize(12.5).TextColor(SailInkFaint)
		return
	}

	parsed := ParseStructuredDescription(trimmed)
	col := ui.Column(c.Key(key)).FillWidth().Gap(10)
	rawOpen := ui.Local(col, "raw-open", func() bool { return false })

	col.Children(func() {
		if parsed.Lead != "" {
			renderInlineRichText(c, parsed.Lead, 13, 1.55, SailInkStrong)
		}

		maxParas := min(len(parsed.Paragraphs), 2)
		for i := 0; i < maxParas; i++ {
			renderInlineRichText(c, parsed.Paragraphs[i], 12.5, 1.55, SailInkBody)
		}

		if len(parsed.Bullets) > 0 {
			maxBullets := min(len(parsed.Bullets), 6)
			ui.Column(c).FillWidth().Gap(6).Padding(2, 0).Children(func() {
				for i := 0; i < maxBullets; i++ {
					item := parsed.Bullets[i]
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
						ui.Box(c).Size(4.5, 4.5).Radius(999).Background(SailInkFaint).Margin(7, 0, 0, 2).Shrink(0)
						renderInlineRichText(c, item, 12.5, 1.5, SailInkBody).Grow(1).MinWidth(0)
					})
				}
			})
		}

		if len(parsed.Params) > 0 {
			maxParams := min(len(parsed.Params), 6)
			ui.Column(c).FillWidth().Padding(10, 12).Gap(6).Radius(8).Background(SailBgSubtle).Children(func() {
				ui.Text(c, "参数说明").FontSize(11.5).FontWeight(600).TextColor(SailInkMuted)
				for i := 0; i < maxParams; i++ {
					p := parsed.Params[i]
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
						label := p.Name
						if p.Type != "" {
							label += " (" + p.Type + ")"
						}
						ui.Text(c, label).Font("monospace").FontSize(11.5).FontWeight(600).TextColor(SailInkStrong).Shrink(0)
						ui.Text(c, p.Desc).Grow(1).MinWidth(0).FontSize(12).LineHeight(1.45).TextColor(SailInkMuted).Selectable()
					})
				}
			})
		}

		if parsed.HasMoreRaw {
			toggleBtn := ui.ButtonBase(c.Key("toggle-raw")).
				Padding(3, 6).
				Margin(0, 0, 0, -6).
				Radius(5).
				Gap(5).
				AlignItems(ui.Center).
				AlignSelf(ui.Start).
				Cursor(ui.CursorPointer)
			if toggleBtn.Hovered() {
				toggleBtn.Background(SailBgHover)
			}
			toggleBtn.Children(func() {
				rot := float32(0)
				lbl := "展开完整原始描述"
				if *rawOpen {
					rot = 90
					lbl = "收起原始描述"
				}
				ui.Icon(c, IconChevronRight).Size(12, 12).TextColor(SailInkMuted).Rotate(rot)
				ui.Text(c, lbl).FontSize(12).FontWeight(500).TextColor(SailInkMuted).SingleLine()
			})
			if toggleBtn.Clicked() {
				*rawOpen = !*rawOpen
			}

			if *rawOpen {
				ui.Scroll(c.Key("raw-scroll")).
					FillWidth().
					MaxHeight(200).
					Padding(10, 12).
					Radius(6).
					Background(SailBgSubtle).
					Border(1, SailDivider).
					Children(func() {
						ui.Text(c, trimmed).
							Font("monospace").
							FontSize(11.5).
							LineHeight(1.5).
							TextColor(SailInkBody).
							Selectable()
					})
			}
		}
	})
}
