package codelima

import (
	"fmt"
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

// Each buffer is one command, including embedded newlines. Editing a script
// never silently changes its execution boundary into several shell commands.
type tuiCommandEditor struct {
	app                  *vaxisTUIApp
	parent               tuiOverlay
	initial              []string
	buffers              []*textinput.Model
	index, scroll, width int
	onSave               func([]string)
	confirmDiscard       bool
	listRects            []tuiRect
	paste                strings.Builder
}

func newTUICommandEditor(a *vaxisTUIApp, parent tuiOverlay, commands []string, onSave func([]string)) *tuiCommandEditor {
	e := &tuiCommandEditor{app: a, parent: parent, initial: slices.Clone(commands), onSave: onSave, width: 60}
	for _, command := range commands {
		e.buffers = append(e.buffers, newTUICommandBuffer(command))
	}
	if len(e.buffers) == 0 {
		e.buffers = append(e.buffers, newTUICommandBuffer(""))
	}
	return e
}
func newTUICommandBuffer(command string) *textinput.Model {
	b := textinput.New()
	b.PreserveTabs = true
	return b.SetContent(command)
}
func (e *tuiCommandEditor) ParentOverlay() tuiOverlay { return e.parent }
func (e *tuiCommandEditor) PreferredSize() (int, int) { return 120, 28 }
func (e *tuiCommandEditor) commands() []string {
	commands := []string{}
	for _, b := range e.buffers {
		if strings.TrimSpace(b.String()) != "" {
			commands = append(commands, b.String())
		}
	}
	return commands
}
func (e *tuiCommandEditor) FooterHint() string {
	if e.confirmDiscard {
		return "Discard command edits? y discard · n keep editing"
	}
	return "Tab command · Ctrl+n add · Ctrl+x remove · Alt+↑↓ order · Ctrl+s use · Esc back"
}
func (e *tuiCommandEditor) Update(event vaxis.Event) (bool, error) {
	if !e.confirmDiscard {
		switch value := event.(type) {
		case vaxis.PasteStartEvent:
			e.paste.Reset()
			return false, nil
		case vaxis.Key:
			if value.EventType == vaxis.EventPaste {
				// Vaxis decodes pasted C0 bytes as keys without Text. Recover
				// them and normalize only after the complete paste, including
				// CRLF pairs delivered in separate events.
				e.paste.WriteString(encodeTUITerminalPasteKey(value))
				return false, nil
			}
		case vaxis.PasteEndEvent:
			b := e.buffers[e.index]
			b.Update(vaxis.Key{EventType: vaxis.EventPaste, Text: normalizeTUITerminalPasteText(e.paste.String())})
			b.Update(value)
			e.paste.Reset()
			return false, nil
		}
	}
	if m, ok := event.(vaxis.Mouse); ok && m.EventType == vaxis.EventPress && m.Button == vaxis.MouseLeftButton && !e.confirmDiscard {
		for i, r := range e.listRects {
			if r.contains(m.Col, m.Row) {
				e.index = i
				e.scroll = 0
				break
			}
		}
		return false, nil
	}
	if key, ok := event.(vaxis.Key); ok && key.EventType != vaxis.EventPaste {
		if !tuiKeyActivates(key, false) {
			return false, nil
		}
		if e.confirmDiscard {
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			if key.Matches('y') {
				return true, nil
			}
			if key.Matches('n') || key.Matches(vaxis.KeyEsc) {
				e.confirmDiscard = false
			}
			return false, nil
		}
		switch {
		case key.Matches(vaxis.KeyEsc):
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			if !slices.Equal(e.commands(), e.initial) {
				e.confirmDiscard = true
				return false, nil
			}
			return true, nil
		case key.Matches('s', vaxis.ModCtrl):
			if tuiKeyActivates(key, true) {
				e.onSave(e.commands())
				return true, nil
			}
			return false, nil
		case key.Matches('n', vaxis.ModCtrl):
			if tuiKeyActivates(key, true) {
				e.buffers = append(e.buffers, newTUICommandBuffer(""))
				e.index = len(e.buffers) - 1
				e.scroll = 0
			}
			return false, nil
		case key.Matches('x', vaxis.ModCtrl):
			if tuiKeyActivates(key, true) {
				e.buffers = slices.Delete(e.buffers, e.index, e.index+1)
				if len(e.buffers) == 0 {
					e.buffers = append(e.buffers, newTUICommandBuffer(""))
				}
				e.index = min(e.index, len(e.buffers)-1)
				e.scroll = 0
			}
			return false, nil
		case key.Matches(vaxis.KeyTab, vaxis.ModShift):
			e.index = (e.index + len(e.buffers) - 1) % len(e.buffers)
			e.scroll = 0
			return false, nil
		case key.Matches(vaxis.KeyTab):
			e.index = (e.index + 1) % len(e.buffers)
			e.scroll = 0
			return false, nil
		case key.Matches(vaxis.KeyUp, vaxis.ModAlt), key.Matches(vaxis.KeyDown, vaxis.ModAlt):
			delta := -1
			if key.Keycode == vaxis.KeyDown {
				delta = 1
			}
			j := e.index + delta
			if j >= 0 && j < len(e.buffers) {
				e.buffers[e.index], e.buffers[j] = e.buffers[j], e.buffers[e.index]
				e.index = j
			}
			return false, nil
		case key.Matches(vaxis.KeyEnter):
			e.buffers[e.index].Update(vaxis.Key{Text: "\n"})
			return false, nil
		case key.Matches(vaxis.KeyUp), key.Matches(vaxis.KeyDown), key.Matches(vaxis.KeyHome), key.Matches(vaxis.KeyEnd):
			b := e.buffers[e.index]
			rows := commandVisualRows(b.Characters(), e.width)
			row := commandCursorRow(rows, b.CursorPosition())
			target := row
			if key.Keycode == vaxis.KeyUp {
				target = max(0, row-1)
			}
			if key.Keycode == vaxis.KeyDown {
				target = min(len(rows)-1, row+1)
			}
			position := min(rows[target].end, rows[target].start+b.CursorPosition()-rows[row].start)
			if key.Keycode == vaxis.KeyHome {
				position = rows[row].start
			}
			if key.Keycode == vaxis.KeyEnd {
				position = rows[row].end
			}
			setCommandCursor(b, position)
			return false, nil
		}
	}
	if !e.confirmDiscard {
		e.buffers[e.index].Update(event)
	}
	return false, nil
}
func setCommandCursor(b *textinput.Model, position int) {
	b.Update(vaxis.Key{Keycode: vaxis.KeyHome})
	for i := 0; i < position; i++ {
		b.Update(vaxis.Key{Keycode: vaxis.KeyRight})
	}
}

type commandVisualRow struct {
	start, end int
	text       string
}

func commandVisualRows(chars []vaxis.Character, width int) []commandVisualRow {
	rows := []commandVisualRow{}
	start, cells := 0, 0
	var line strings.Builder
	for i, ch := range chars {
		if ch.Grapheme == "\n" || ch.Grapheme == "\r\n" {
			rows = append(rows, commandVisualRow{start, i, line.String()})
			start = i + 1
			cells = 0
			line.Reset()
			continue
		}
		if cells+ch.Width > max(1, width) && i > start {
			rows = append(rows, commandVisualRow{start, i, line.String()})
			start = i
			cells = 0
			line.Reset()
		}
		if ch.Grapheme == "\t" {
			line.WriteString("⇥")
		} else {
			line.WriteString(ch.Grapheme)
		}
		cells += ch.Width
	}
	return append(rows, commandVisualRow{start, len(chars), line.String()})
}
func commandCursorRow(rows []commandVisualRow, position int) int {
	for i := len(rows) - 1; i >= 0; i-- {
		if position >= rows[i].start {
			return i
		}
	}
	return 0
}
func (e *tuiCommandEditor) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := e.app.managerTheme().modal()
	tuiFill(win, t.raised)
	w, h := win.Size()
	if w < 20 || h < 6 {
		return
	}
	body := templatePanel(win, "Bootstrap commands · each block runs as root after environments", t.mint, t)
	left := min(24, w/3)
	list := body.New(1, 1, left, h-5)
	e.listRects = make([]tuiRect, len(e.buffers))
	x, y := list.Origin()
	start := max(0, e.index-(h-6)+1)
	for i := start; i < min(len(e.buffers), start+h-5); i++ {
		style := t.muted
		if i == e.index {
			style = t.selected
		}
		row := list.New(0, i-start, left, 1)
		tuiFill(row, style)
		tuiLine(row, 0, fmt.Sprintf("%d · %s", i+1, templateCommandLine(e.buffers[i].String())), style)
		e.listRects[i] = tuiRect{col: x, row: y + i - start, width: left, height: 1}
	}
	area := body.New(left+2, 1, w-left-6, h-5)
	e.width = max(1, w-left-6)
	b := e.buffers[e.index]
	rows := commandVisualRows(b.Characters(), e.width)
	current := commandCursorRow(rows, b.CursorPosition())
	visible := max(1, h-5)
	if current < e.scroll {
		e.scroll = current
	}
	if current >= e.scroll+visible {
		e.scroll = current - visible + 1
	}
	for i, row := range rows[e.scroll:min(len(rows), e.scroll+visible)] {
		tuiLine(area, i, row.text, t.text)
	}
	if !e.confirmDiscard {
		col := 0
		for _, ch := range b.Characters()[rows[current].start:b.CursorPosition()] {
			col += ch.Width
		}
		area.ShowCursor(min(col, e.width-1), current-e.scroll, vaxis.CursorBlock)
	}
	status := fmt.Sprintf("Command %d/%d · Enter newline · Ctrl+s returns changes to the template", e.index+1, len(e.buffers))
	if e.confirmDiscard {
		status = "Discard command edits? y discard · n keep editing"
	}
	tuiLine(body.New(1, h-3, w-4, 1), 0, status, t.mint)
}
