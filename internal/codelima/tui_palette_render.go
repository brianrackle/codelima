package codelima

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiPaletteVisualRow struct {
	index int
	group string
	hint  bool
}
type tuiPaletteControl struct {
	rect tuiRect
	key  vaxis.Key
}

func (p *tuiPalette) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	p.filter()
	p.rows = nil
	p.controls = nil
	t := newTUITheme(false).modal()
	if p.app != nil {
		t = p.app.managerTheme().modal()
	}
	tuiFill(win, t.raised)
	body := border.All(win, t.mint)
	w, h := body.Size()
	if w < 24 || h < 7 {
		tuiLine(body, 0, "Palette · resize to search", t.text)
		return
	}
	count := fmt.Sprintf("%d results", len(p.matches))
	p.input.SetPrompt(": ")
	p.input.Content, p.input.Prompt = t.text, t.mint
	p.input.Draw(body.New(1, 0, w-len(count)-3, 1))
	tuiLine(body.New(w-len(count)-1, 0, len(count), 1), 0, count, t.dim)
	tuiLine(body, 1, strings.Repeat("─", w), t.border)
	tuiLine(body, h-2, strings.Repeat("─", w), t.border)
	p.drawFooter(body.New(1, h-1, w-2, 1), t)
	p.height = h - 4
	if len(p.matches) == 0 {
		tuiLine(body.New(2, 3, w-4, 1), 0, "No matches in "+strings.ToLower(p.title), t.muted)
		return
	}
	compact := w < 94
	var rows []tuiPaletteVisualRow
	group := ""
	selectedStart, selectedEnd := 0, 0
	for i, c := range p.matches {
		if c.group != group {
			group = c.group
			rows = append(rows, tuiPaletteVisualRow{group: group, index: -1})
		}
		if i == p.index {
			selectedStart = len(rows)
		}
		rows = append(rows, tuiPaletteVisualRow{index: i})
		if compact && c.hint != "" {
			rows = append(rows, tuiPaletteVisualRow{index: i, hint: true})
		}
		if i == p.index {
			selectedEnd = len(rows) - 1
		}
	}
	p.scroll = min(max(0, p.scroll), max(0, len(rows)-p.height))
	p.scroll = min(p.scroll, selectedStart)
	if selectedEnd >= p.scroll+p.height {
		p.scroll = selectedEnd - p.height + 1
	}
	for i, row := range rows[p.scroll:min(len(rows), p.scroll+p.height)] {
		line := body.New(1, 2+i, w-2, 1)
		if row.group != "" {
			tuiLine(line, 0, row.group, t.dim)
			continue
		}
		c := p.matches[row.index]
		p.drawCommand(line, c, row.index == p.index, row.hint, compact, t)
		col, top := line.Origin()
		p.rows = append(p.rows, tuiPaletteTarget{rect: tuiRect{col: col, row: top, width: w - 2, height: 1}, id: c.id})
	}
}

func (p *tuiPalette) drawCommand(win vaxis.Window, c tuiPaletteCommand, selected, hintOnly, compact bool, t tuiTheme) {
	w, _ := win.Size()
	base := t.text
	if selected {
		base = t.selected
	}
	tuiFill(win, base)
	hint := base
	hint.Foreground = t.dim.Foreground
	if c.warning {
		hint.Foreground = t.amber.Foreground
	}
	if c.unavailable {
		hint.Foreground = t.coral.Foreground
	}
	match := base
	match.Foreground = t.mint.Foreground
	match.UnderlineStyle = vaxis.UnderlineSingle
	labelRunes := utf8.RuneCountInString(c.label)
	if hintOnly {
		win.New(3, 0, w-3, 1).PrintTruncate(0, paletteSegments(c.hint, c.positions, labelRunes+1, hint, match)...)
		return
	}
	if selected {
		arrow := base
		arrow.Foreground = t.mint.Foreground
		tuiLine(win, 0, "▸", arrow)
	}
	keyWidth := min(9, w/4)
	labelWidth := w - keyWidth - 4
	if !compact {
		labelWidth = 30
	}
	win.New(2, 0, labelWidth, 1).PrintTruncate(0, paletteSegments(c.label, c.positions, 0, base, match)...)
	if !compact {
		win.New(labelWidth+4, 0, w-labelWidth-keyWidth-5, 1).PrintTruncate(0, paletteSegments(c.hint, c.positions, labelRunes+1, hint, match)...)
	}
	key := base
	key.Foreground = t.dim.Foreground
	padding := max(0, keyWidth-utf8.RuneCountInString(c.shortcut))
	win.New(w-keyWidth+padding, 0, keyWidth-padding, 1).PrintTruncate(0, paletteSegments(c.shortcut, c.positions, labelRunes+utf8.RuneCountInString(c.hint)+2, key, match)...)
}

func paletteSegments(text string, positions []int, offset int, normal, match vaxis.Style) []vaxis.Segment {
	var segments []vaxis.Segment
	for _, char := range vaxis.Characters(text) {
		end := offset + utf8.RuneCountInString(char.Grapheme)
		style := normal
		if slices.ContainsFunc(positions, func(pos int) bool { return pos >= offset && pos < end }) {
			style = match
		}
		if len(segments) > 0 && segments[len(segments)-1].Style == style {
			segments[len(segments)-1].Text += char.Grapheme
		} else {
			segments = append(segments, vaxis.Segment{Text: char.Grapheme, Style: style})
		}
		offset = end
	}
	return segments
}

func (p *tuiPalette) drawFooter(win vaxis.Window, t tuiTheme) {
	items := []struct {
		key   rune
		label string
	}{{vaxis.KeyEnter, "Enter run"}, {vaxis.KeyUp, "↑"}, {vaxis.KeyDown, "↓ move"}, {vaxis.KeyTab, "Tab complete"}, {vaxis.KeyEsc, "Esc close"}}
	w, _ := win.Size()
	left := 0
	col, row := win.Origin()
	for _, item := range items {
		key, rest, _ := strings.Cut(item.label, " ")
		width := utf8.RuneCountInString(item.label)
		if left+width > w {
			break
		}
		if rest != "" {
			rest = " " + rest
		}
		win.New(left, 0, width, 1).PrintTruncate(0, vaxis.Segment{Text: key, Style: t.mint}, vaxis.Segment{Text: rest, Style: t.muted})
		p.controls = append(p.controls, tuiPaletteControl{rect: tuiRect{col: col + left, row: row, width: width, height: 1}, key: vaxis.Key{Keycode: item.key}})
		left += width + 2
	}
	if left+26 < w {
		hint := "empty query: recent commands"
		if strings.TrimSpace(p.input.String()) == "" && !slices.ContainsFunc(p.matches, func(c tuiPaletteCommand) bool { return c.group == "RECENT" }) {
			hint = "No recent commands available"
		}
		tuiLine(win.New(left, 0, w-left, 1), 0, hint, t.dim)
	}
}
