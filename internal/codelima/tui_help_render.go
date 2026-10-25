package codelima

import (
	"fmt"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
	"strings"
)

func (h *tuiKeyHelp) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	h.columns = nil
	h.searchRect, h.closeRect, h.findRect = tuiRect{}, tuiRect{}, tuiRect{}
	t := h.app.managerTheme().modal()
	tuiFill(win, t.raised)
	body := border.All(win, t.mint)
	w, height := body.Size()
	if w < 24 || height < 7 {
		tuiLine(body, 0, "Keys · resize to read", t.text)
		return
	}
	tuiLine(body, 0, "Keys · opened from "+h.groups[0].title+" · / searches all keys", t.text)
	h.pageSize = max(1, height-5)
	content := body.New(1, 3, w-2, h.pageSize)
	if h.searching {
		h.input.SetPrompt("/ ")
		h.input.Content, h.input.Prompt = t.text, t.mint
		h.input.Draw(body.New(1, 1, w-2, 1))
		lines, count := h.searchLines()
		tuiLine(body, 2, fmt.Sprintf("%d matching keys · Tab returns to columns", count), t.dim)
		if count == 0 {
			lines = []tuiHelpLine{{text: "No matching keys · Ctrl+u clears the search"}}
		}
		h.drawLines(content, lines, &h.searchScroll, t)
		col, row := content.Origin()
		h.searchRect = tuiRect{col: col, row: row, width: w - 2, height: h.pageSize}
	} else {
		h.drawColumns(content, t)
		last := h.columns[len(h.columns)-1].group + 1
		tuiLine(body, 1, fmt.Sprintf("Groups %d–%d of %d · Tab/←→ changes focus · ↑↓ scrolls", h.columns[0].group+1, last, len(h.groups)), t.dim)
		tuiLine(body.New(1, 2, w-2, 1), 0, "/ search keys", t.mint)
		col, row := body.Origin()
		h.findRect = tuiRect{col: col + 1, row: row + 2, width: 13, height: 1}
	}
	note := "Actions once per press · arrows/text repeat"
	if w >= 100 {
		note = "Keys act once per press; arrows and text editing repeat while held"
	}
	tuiLine(body.New(1, height-1, w-16, 1), 0, note, t.dim)
	tuiLine(body.New(w-13, height-1, 13, 1), 0, "Esc / ? close", t.mint)
	col, row := body.Origin()
	h.closeRect = tuiRect{col: col + w - 13, row: row + height - 1, width: 13, height: 1}
}

func (h *tuiKeyHelp) drawColumns(win vaxis.Window, t tuiTheme) {
	w, height := win.Size()
	count := 1
	if w >= 132 {
		count = 4
	} else if w >= 88 {
		count = 2
	}
	start := h.focus / count * count
	columnWidth := (w - (count-1)*2) / count
	for i := start; i < min(start+count, len(h.groups)); i++ {
		column := win.New((i-start)*(columnWidth+2), 0, columnWidth, height)
		col, row := column.Origin()
		h.columns = append(h.columns, tuiHelpColumn{i, tuiRect{col: col, row: row, width: columnWidth, height: height}})
		style := t.text
		columnTheme := t
		if i == h.focus {
			style = t.selected
			style.Foreground = t.mint.Foreground
			for _, s := range []*vaxis.Style{&columnTheme.text, &columnTheme.mint, &columnTheme.dim} {
				s.Background = t.selected.Background
			}
			tuiFill(column, columnTheme.text)
		}
		tuiFill(column.New(0, 0, columnWidth, 1), style)
		tuiLine(column, 0, h.groups[i].title, style)
		var lines []tuiHelpLine
		for _, section := range h.groups[i].sections {
			if section.title != "" {
				lines = append(lines, tuiHelpLine{text: section.title, heading: true})
			}
			for _, binding := range section.bindings {
				lines = append(lines, tuiHelpLine{key: binding.key, text: binding.description})
			}
		}
		h.drawLines(column.New(0, 1, columnWidth, height-2), lines, &h.scroll[i], columnTheme)
		tuiLine(column, height-1, fmt.Sprintf("↑↓ scroll · row %d", h.scroll[i]+1), columnTheme.dim)
	}
}

func (h *tuiKeyHelp) drawLines(win vaxis.Window, lines []tuiHelpLine, offset *int, t tuiTheme) {
	w, height := win.Size()
	keyWidth := min(14, max(9, w/4))
	var rows []tuiHelpLine
	for _, line := range lines {
		if line.heading || line.key == "" {
			for _, row := range commandVisualRows(vaxis.Characters(line.text), w) {
				rows = append(rows, tuiHelpLine{text: row.text, heading: line.heading})
			}
			continue
		}
		keys := helpDescriptionRows(line.key, keyWidth-1)
		values := helpDescriptionRows(line.text, w-keyWidth)
		for i := range max(len(keys), len(values)) {
			row := tuiHelpLine{}
			if i < len(keys) {
				row.key = keys[i]
			}
			if i < len(values) {
				row.text = values[i]
			}
			// Keep continuation descriptions in the value column.
			if row.key == "" {
				row.key = " "
			}
			rows = append(rows, row)
		}
	}
	*offset = min(max(0, *offset), max(0, len(rows)-height))
	for i, row := range rows[*offset:min(len(rows), *offset+height)] {
		switch {
		case row.heading:
			tuiLine(win, i, row.text, t.dim)
		case row.key != "":
			tuiLine(win.New(0, i, keyWidth-1, 1), 0, row.key, t.mint)
			tuiLine(win.New(keyWidth, i, w-keyWidth, 1), 0, row.text, t.text)
		default:
			tuiLine(win, i, row.text, t.muted)
		}
	}
}

// Descriptions are prose, unlike command buffers: prefer word boundaries and
// only split a word that cannot fit on a line, retaining terminal graphemes.
func helpDescriptionRows(text string, width int) []string {
	width = max(1, width)
	var rows []string
	line, cells := "", 0
	for _, word := range strings.Fields(text) {
		chars := vaxis.Characters(word)
		wordCells := 0
		for _, ch := range chars {
			wordCells += ch.Width
		}
		if line != "" && cells+1+wordCells <= width {
			line += " " + word
			cells += 1 + wordCells
			continue
		}
		if line != "" {
			rows = append(rows, line)
		}
		wrapped := commandVisualRows(chars, width)
		for _, row := range wrapped[:len(wrapped)-1] {
			rows = append(rows, row.text)
		}
		last := wrapped[len(wrapped)-1]
		line, cells = last.text, 0
		for _, ch := range chars[last.start:last.end] {
			cells += ch.Width
		}
	}
	if line != "" {
		rows = append(rows, line)
	}
	return rows
}
