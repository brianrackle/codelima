package codelima

import (
	"math"
	"strings"

	"go.rockorager.dev/vaxis"
)

// Chrome colors are independent of the guest terminal's negotiated palette.
// Truecolor uses the design tokens; the fallback uses only the ANSI 16 colors.
type tuiTheme struct {
	text, muted, dim, border vaxis.Style
	mint, amber, coral       vaxis.Style
	blue, violet, selected   vaxis.Style
	chrome, raised           vaxis.Style
	track                    vaxis.Style
}

func newTUITheme(rgb bool) tuiTheme {
	color := func(hex uint32, ansi vaxis.Color) vaxis.Color {
		if !rgb {
			return ansi
		}
		return vaxis.RGBColor(uint8(hex>>16), uint8(hex>>8), uint8(hex))
	}
	background := color(0x0c1015, vaxis.ColorBlack)
	style := func(hex uint32, ansi vaxis.Color) vaxis.Style {
		return vaxis.Style{Foreground: color(hex, ansi), Background: background}
	}
	theme := tuiTheme{
		text: style(0xd9e2ec, vaxis.ColorSilver), muted: style(0x9aabb9, vaxis.ColorSilver),
		dim: style(0x6f8597, vaxis.ColorGray), border: style(0x263442, vaxis.ColorGray),
		mint: style(0x91e6c0, vaxis.ColorGreen), amber: style(0xf0c674, vaxis.ColorYellow),
		coral: style(0xf4877c, vaxis.ColorRed), blue: style(0x82b8ff, vaxis.ColorBlue),
		violet: style(0xc4a7ff, vaxis.ColorFuschia),
		track:  style(0x2b3a49, vaxis.ColorGray),
	}
	theme.selected = theme.text
	theme.chrome = theme.text
	theme.raised = theme.text
	if rgb {
		theme.selected.Background = color(0x1b2a36, 0)
		theme.chrome.Background = color(0x111820, 0)
		theme.raised.Background = color(0x121a22, 0)
	} else {
		theme.selected.Attribute = vaxis.AttrReverse
	}
	return theme
}

func (t tuiTheme) usage(percent float64) vaxis.Style {
	if percent >= 90 {
		return t.coral
	}
	if percent >= 70 {
		return t.amber
	}
	return t.mint
}

func (t tuiTheme) status(status string) (string, vaxis.Style) {
	switch status {
	case "running":
		return "●", t.mint
	case "starting", "cloning":
		return "◐", t.amber
	case "stopping":
		return "◑", t.amber
	case "stopped":
		return "○", t.dim
	case "created":
		return "◌", t.dim
	case "failed", "broken":
		return "✕", t.coral
	case "deleting", "terminated", "delete incomplete":
		return "⊘", t.coral
	default:
		return "?", t.dim
	}
}

// tuiGauge renders fractional eighth-blocks in a fixed number of terminal cells.
func tuiGauge(percent float64, cells int) string {
	if cells <= 0 {
		return ""
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent < 0 {
		return "—" + strings.Repeat(" ", cells-1)
	}
	eighths := int(math.Round(min(percent, 100) * float64(cells) * 8 / 100))
	full, part := eighths/8, eighths%8
	text := strings.Repeat("█", full)
	if part > 0 {
		text += string([]rune(" ▏▎▍▌▋▊▉")[part])
		full++
	}
	return text + strings.Repeat("░", cells-full)
}

func tuiFill(win vaxis.Window, style vaxis.Style) {
	win.Fill(vaxis.Cell{Character: vaxis.Character{Grapheme: " ", Width: 1}, Style: style})
}

func tuiLine(win vaxis.Window, row int, text string, style vaxis.Style) {
	tuiSegmentLine(win, row, vaxis.Segment{Text: text, Style: style})
}

func tuiSegmentLine(win vaxis.Window, row int, segments ...vaxis.Segment) {
	// Records can contain newlines (paths and command output). A table cell is
	// always exactly one row; rendering must not displace its neighbors.
	clean := strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
	for i := range segments {
		segments[i].Text = clean.Replace(segments[i].Text)
	}
	win.PrintTruncate(row, segments...)
}

// tuiGaugeLine keeps the optional gauge separate from surrounding text so
// literal block characters in labels never acquire metric colors.
type tuiGaugeLine struct {
	prefix, suffix string
	percent        float64
	cells          int
}

func (l tuiGaugeLine) String() string {
	return l.prefix + tuiGauge(l.percent, l.cells) + l.suffix
}

func (l tuiGaugeLine) draw(win vaxis.Window, row int, base vaxis.Style, theme tuiTheme) {
	filled := base
	filled.Foreground = theme.usage(l.percent).Foreground
	if math.IsNaN(l.percent) || math.IsInf(l.percent, 0) || l.percent < 0 {
		filled.Foreground = theme.dim.Foreground
	}
	track := base
	track.Foreground = theme.track.Foreground
	gauge := tuiGauge(l.percent, l.cells)
	fill := strings.TrimRight(gauge, "░")
	tuiSegmentLine(win, row,
		vaxis.Segment{Text: l.prefix, Style: base},
		vaxis.Segment{Text: fill, Style: filled},
		vaxis.Segment{Text: gauge[len(fill):], Style: track},
		vaxis.Segment{Text: l.suffix, Style: base})
}
