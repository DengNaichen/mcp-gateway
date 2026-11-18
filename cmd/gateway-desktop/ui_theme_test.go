package main

import (
	"reflect"
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

func TestParseStructuredDescriptionTableDriven(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want StructuredDescription
	}{
		{
			name: "empty",
			raw:  "   \r\n ",
			want: StructuredDescription{},
		},
		{
			name: "identifierHeadingSkippedAndBulletsExtracted",
			raw:  "## search_issues\r\n\r\nSearch issues quickly.\r\n\r\n- Filter by label\r\n1. Supports pagination\r\n",
			want: StructuredDescription{
				Lead:       "Search issues quickly.",
				Bullets:    []string{"Filter by label", "Supports pagination"},
				HasMoreRaw: true,
			},
		},
		{
			name: "bulletsPromoteFirstToLeadWhenNoProse",
			raw:  "- First bullet\n- Second bullet",
			want: StructuredDescription{
				Lead:    "First bullet",
				Bullets: []string{"Second bullet"},
			},
		},
		{
			name: "paramsWithIndentedContinuationAndMetaSection",
			raw:  "Run query.\n\nArgs:\n  sql (string): SQL query\n    with multiline detail\n\nReturns:\n- Read-only only\n",
			want: StructuredDescription{
				Lead:    "Run query.",
				Bullets: []string{"Read-only only"},
				Params: []DescParamItem{
					{Name: "sql", Type: "string", Desc: "SQL query with multiline detail"},
				},
				HasMoreRaw: true,
			},
		},
		{
			name: "inlineArgsTailAndCodeFence",
			raw:  "Fetch commits. Args: repo (string)\n```json\n{}\n```",
			want: StructuredDescription{
				Lead:       "Fetch commits.",
				HasMoreRaw: true,
			},
		},
		{
			name: "markdownLinkStrippedInProse",
			raw:  "See [docs](https://example.com) for details.",
			want: StructuredDescription{
				Lead: "See docs for details.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseStructuredDescription(tt.raw)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseStructuredDescription() =\n%#v\nwant:\n%#v", got, tt.want)
			}
		})
	}
}
