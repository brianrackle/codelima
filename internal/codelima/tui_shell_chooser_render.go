package codelima

import (
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiShellVisualRow struct {
	index int
	group string
	hint  string
}

func (h *tuiShellChooser) visualRows(width int) []tuiShellVisualRow {
	rows := []tuiShellVisualRow{{index: -1, group: "NEW SESSION"}}
	for i, choice := range h.choices {
		if i == 5 {
			rows = append(rows, tuiShellVisualRow{index: -1}, tuiShellVisualRow{index: -1, group: "ATTACH TO A RUNNING SESSION"})
		}
		rows = append(rows, tuiShellVisualRow{index: i})
		if width < 94 {
			context := helpDescriptionRows(choice.hint, max(1, width-9))
			if len(context) > 2 {
				context = context[:2]
				context[1] += "…"
			}
			for _, text := range context {
				rows = append(rows, tuiShellVisualRow{index: i, hint: text})
			}
		}
	}
	if len(h.choices) == 5 {
		rows = append(rows, tuiShellVisualRow{index: -1}, tuiShellVisualRow{index: -1, group: "ATTACH TO A RUNNING SESSION"}, tuiShellVisualRow{index: -1, hint: "No running sessions"})
	}
	return rows
}

func (h *tuiShellChooser) PreferredSize() (int, int) {
	h.refresh()
	w := 104
	if h.app.vx != nil {
		cols, _ := h.app.vx.Window().Size()
		w = min(w, cols-4)
	}
	return 104, min(30, max(14, len(h.visualRows(w-2))+7))
}

func (h *tuiShellChooser) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	h.refresh()
	h.rows, h.closeRect = nil, tuiRect{}
	t := h.app.managerTheme().modal()
	tuiFill(win, t.raised)
	body := border.All(win, t.mint)
	w, height := body.Size()
	if w < 24 || height < 8 {
		tuiLine(body, 0, "Resize to choose a shell", t.text)
		return
	}
	title := t.text
	title.Attribute |= vaxis.AttrBold
	win.New(2, 0, w-2, 1).PrintTruncate(0, vaxis.Segment{Text: " Open a shell ", Style: title}, vaxis.Segment{Text: "· " + plainLogText(h.name) + " ", Style: t.dim})
	h.height = height - 5
	rows := h.visualRows(w)
	start, end := 0, 0
	for i, row := range rows {
		if row.index == h.index {
			if row.hint == "" {
				start = i
			}
			end = i
		}
	}
	h.scroll = min(max(0, h.scroll), max(0, len(rows)-h.height))
	visibleStart := start
	if start > 0 && rows[start-1].group != "" {
		visibleStart--
	}
	h.scroll = min(h.scroll, visibleStart)
	if end >= h.scroll+h.height {
		h.scroll = end - h.height + 1
	}
	// Do not begin a compact viewport with the previous choice's context.
	for h.scroll < start && rows[h.scroll].index >= 0 && rows[h.scroll].hint != "" {
		h.scroll++
	}
	for row, item := range rows[h.scroll:min(len(rows), h.scroll+h.height)] {
		line := body.New(1, row, w-2, 1)
		if item.group != "" {
			style := t.dim
			style.Attribute |= vaxis.AttrBold
			tuiLine(line, 0, item.group, style)
			continue
		}
		if item.index < 0 {
			tuiLine(line, 0, item.hint, t.dim)
			continue
		}
		choice := h.choices[item.index]
		h.drawChoice(line, choice, item.hint, item.index == h.index, w < 94, t)
		col, top := line.Origin()
		h.rows = append(h.rows, tuiCommandTarget{rect: tuiRect{col: col, row: top, width: w - 2, height: 1}, id: choice.id})
	}
	tuiLine(body, height-5, strings.Repeat("─", w), t.border)
	if h.err != "" {
		tuiLine(body.New(1, height-4, w-2, 1), 0, plainLogText(h.err), t.coral)
	}
	tuiLine(body.New(1, height-3, w-2, 1), 0, h.defaultHint(), t.text)
	tuiLine(body.New(1, height-2, w-12, 1), 0, "Shift+Enter/W/T makes it x's default", t.dim)
	close := body.New(w-10, height-2, 9, 1)
	close.PrintTruncate(0, vaxis.Segment{Text: "Esc", Style: t.mint}, vaxis.Segment{Text: " close", Style: t.muted})
	col, row := close.Origin()
	h.closeRect = tuiRect{col: col, row: row, width: 9, height: 1}
	tuiLine(body.New(1, height-1, w-2, 1), 0, "Sessions belong to the daemon; any window can attach later", t.dim)
}

func (h *tuiShellChooser) drawChoice(win vaxis.Window, choice tuiShellChoice, context string, selected, compact bool, t tuiTheme) {
	w, _ := win.Size()
	style := t.text
	if selected {
		style = t.selected
	}
	if choice.unavailable != "" {
		style.Foreground = t.dim.Foreground
	}
	tuiFill(win, style)
	hint := style
	hint.Foreground = t.muted.Foreground
	if choice.warning {
		hint.Foreground = t.amber.Foreground
	}
	if choice.unavailable != "" {
		hint.Foreground = t.dim.Foreground
	}
	if context != "" {
		tuiLine(win.New(7, 0, w-7, 1), 0, context, hint)
		return
	}
	key := style
	key.Foreground = t.mint.Foreground
	if choice.unavailable != "" {
		key.Foreground = t.dim.Foreground
	}
	if selected {
		tuiLine(win, 0, "▸", key)
	}
	shortcut := ""
	if choice.key == vaxis.KeyEnter {
		shortcut = "⏎"
	} else if choice.key != 0 {
		shortcut = string(choice.key)
	}
	tuiLine(win.New(2, 0, 3, 1), 0, shortcut, key)
	label := choice.label
	if choice.kind != "" {
		kind := style
		if choice.kind == "host" {
			kind.Foreground = t.coral.Foreground
		}
		tuiLine(win.New(6, 0, 5, 1), 0, choice.kind, kind)
	}
	labelCol := 6
	if choice.kind != "" {
		labelCol = 12
	}
	labelWidth := w - labelCol
	if !compact {
		labelWidth = 26 - labelCol
		if choice.terminalID != "" {
			labelWidth = w - labelCol - 28
		}
		contextCol := labelCol + labelWidth + 2
		tuiLine(win.New(contextCol, 0, max(0, w-contextCol), 1), 0, choice.hint, hint)
	}
	if selected && choice.unavailable == "" {
		style.Attribute |= vaxis.AttrBold
	}
	tuiLine(win.New(labelCol, 0, max(0, labelWidth), 1), 0, label, style)
}
