package codelima

import (
	"fmt"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiLogRow struct {
	index int
	text  string
	start int
}

// Wrap on grapheme boundaries, using the host's cell-width convention. A
// continuation keeps its record identity for selection and clipboard actions.
func (a *vaxisTUIApp) logRows(width int) []tuiLogRow {
	l := &a.logs
	var rows []tuiLogRow
	for _, index := range l.visibleLines() {
		text := logLineText(l.snapshot.Lines[index])
		if !l.wrap || width < 1 {
			rows = append(rows, tuiLogRow{index: index, text: text})
			continue
		}
		iterator := vaxis.NewCharacterIterator(text)
		var row strings.Builder
		cols, start := 0, 0
		for {
			char, ok := iterator.Next()
			if !ok {
				break
			}
			cells := renderedTextWidth(a.vx, char.Grapheme)
			if cols+cells > width && cols > 0 {
				rows = append(rows, tuiLogRow{index: index, text: row.String(), start: start})
				start += row.Len()
				row.Reset()
				cols = 0
			}
			row.WriteString(char.Grapheme)
			cols += cells
		}
		if row.Len() > 0 || text == "" {
			rows = append(rows, tuiLogRow{index: index, text: row.String(), start: start})
		}
	}
	return rows
}

func logHighlightSegments(row tuiLogRow, matches [][]int, style, highlight vaxis.Style) []vaxis.Segment {
	if len(matches) == 0 {
		return []vaxis.Segment{{Text: row.text, Style: style}}
	}
	var segments []vaxis.Segment
	// Expand matches to whole graphemes, retaining highlights across wrapped
	// rows without breaking emoji or combining-character clusters into cells.
	iterator := vaxis.NewCharacterIterator(row.text)
	offset, matchIndex, start := row.start, 0, 0
	current := style
	for {
		char, ok := iterator.Next()
		if !ok {
			break
		}
		end := offset + len(char.Grapheme)
		for matchIndex < len(matches) && matches[matchIndex][1] <= offset {
			matchIndex++
		}
		next := style
		if matchIndex < len(matches) && matches[matchIndex][0] < end {
			next = highlight
		}
		if next != current {
			if offset-row.start > start {
				segments = append(segments, vaxis.Segment{Text: row.text[start : offset-row.start], Style: current})
			}
			start, current = offset-row.start, next
		}
		offset = end
	}
	if start < len(row.text) {
		segments = append(segments, vaxis.Segment{Text: row.text[start:], Style: current})
	}
	return segments
}

func (a *vaxisTUIApp) drawNodeLogs(win vaxis.Window, theme tuiTheme) {
	l := &a.logs
	width, height := win.Size()
	if l.layoutWidth != width {
		l.layoutWidth = width
		l.revealMatch = l.query != ""
	}
	l.mouseRows = nil
	l.bodyRect = tuiRect{}
	l.sourceRects = [4]tuiRect{}
	if width < 8 || height < 6 {
		return
	}
	col := 1
	tuiLine(win.New(col, 0, 7, 1), 0, "source", theme.dim)
	col += 7
	for i, source := range nodeLogSources {
		style := theme.muted
		if i == l.source {
			style = theme.selected
			style.Foreground = theme.mint.Foreground
		}
		label := " " + string(source) + " "
		tab := win.New(col, 0, len(label), 1)
		tuiLine(tab, 0, label, style)
		x, y := tab.Origin()
		l.sourceRects[i] = tuiRect{col: x, row: y, width: len(label), height: 1}
		col += len(label) + 1
	}
	if width-col > 12 {
		tuiLine(win.New(col, 0, width-col, 1), 0, a.keyHints("{logs.source_previous logs.source_next} cycle"), theme.dim)
	}
	status, style := "● following", theme.mint
	if !l.follow {
		status, style = "○ follow paused", theme.amber
	}
	wrap := "off"
	if l.wrap {
		wrap = "on"
	}
	tuiLine(win.New(1, 1, width-2, 1), 0, fmt.Sprintf("%s · f toggle   wrap %s · w   %s levels · L", status, wrap, tuiLogLevels[l.level]), style)
	panel := win.New(0, 3, width, height-5)
	inner := border.All(panel, theme.border)
	title := fmt.Sprintf(" %s · %d lines ", nodeLogSources[l.source], len(l.visibleLines()))
	if l.snapshot.Truncated {
		title += "· recent tail "
	}
	tuiLine(panel.New(2, 0, width-4, 1), 0, title, theme.muted)
	w, h := inner.Size()
	x, y := inner.Origin()
	l.bodyRect = tuiRect{col: x, row: y, width: w, height: h}
	rows := a.logRows(w)
	switch {
	case l.err != nil:
		tuiLine(inner, 0, "Cannot read log: "+plainLogText(l.err.Error()), theme.coral)
	case !l.loaded:
		tuiLine(inner, 0, "Loading log…", theme.dim)
	case l.snapshot.Missing:
		tuiLine(inner, 0, "This source has no log file yet.", theme.dim)
	case len(rows) == 0:
		tuiLine(inner, 0, "No lines at this severity.", theme.dim)
	default:
		pattern := l.searchPattern()
		first, last := -1, -1
		for i, row := range rows {
			if row.index == l.selected {
				if first < 0 {
					first = i
				}
				last = i
			}
		}
		if l.revealMatch && first >= 0 && pattern != nil {
			match := pattern.FindStringIndex(logLineText(l.snapshot.Lines[l.selected]))
			if len(match) > 0 {
				for i := first; i <= last; i++ {
					if rows[i].start+len(rows[i].text) > match[0] {
						l.selectedPart = i - first
						break
					}
				}
			}
			l.revealMatch = false
		}
		if l.follow {
			l.offset = max(0, len(rows)-h)
			l.selectedPart = max(0, last-first)
		} else if first >= 0 {
			l.selectedPart = min(l.selectedPart, last-first)
			cursor := first + l.selectedPart
			if cursor < l.offset {
				l.offset = cursor
			}
			if cursor >= l.offset+h {
				l.offset = max(0, cursor-h+1)
			}
		}
		l.offset = max(0, min(l.offset, max(0, len(rows)-h)))
		matchCache := make(map[int][][]int)
		for i, row := range rows[l.offset:min(len(rows), l.offset+max(0, h))] {
			style := theme.text
			switch logLevelRank(l.snapshot.Lines[row.index].Level) {
			case 2:
				style = theme.amber
			case 3:
				style = theme.coral
			}
			highlight := theme.mint
			highlight.Attribute |= vaxis.AttrReverse
			if row.index == l.selected {
				style.Background = theme.selected.Background
				style.Attribute |= theme.selected.Attribute
				tuiFill(inner.New(0, i, w, 1), style)
				highlight.Attribute |= vaxis.AttrBold
			}
			if pattern != nil {
				if _, ok := matchCache[row.index]; !ok {
					matchCache[row.index] = pattern.FindAllStringIndex(logLineText(l.snapshot.Lines[row.index]), -1)
				}
			}
			inner.PrintTruncate(i, logHighlightSegments(row, matchCache[row.index], style, highlight)...)
			l.mouseRows = append(l.mouseRows, row.index)
		}
	}
	searchRow := height - 2
	switch {
	case l.input != nil:
		tuiLine(win.New(1, searchRow, 2, 1), 0, "/", theme.mint)
		l.input.Draw(win.New(3, searchRow, max(1, width-30), 1))
		tuiLine(win.New(max(3, width-26), searchRow, 25, 1), 0, fmt.Sprintf("%d matching lines", len(l.matches())), theme.muted)
	case l.query != "":
		matches, current := l.matches(), 0
		for i, index := range matches {
			if index == l.selected {
				current = i + 1
				break
			}
		}
		tuiLine(win.New(1, searchRow, width-2, 1), 0, a.keyHintsFormat("/ %s  · %d/%d matching lines · {logs.match_next logs.match_previous} next/previous", l.query, current, len(matches)), theme.mint)
	default:
		tuiLine(win.New(1, searchRow, width-2, 1), 0, a.keyHints("/ search · {logs.match_next logs.match_previous} match · g/G top/end · y copy · o pager"), theme.dim)
	}
	path := l.snapshot.Path
	if path == "" {
		path = "Source path appears after loading"
	}
	tuiLine(win.New(1, height-1, width-2, 1), 0, plainLogText(path), theme.dim)
}

func (a *vaxisTUIApp) handleLogsMouse(mouse vaxis.Mouse) bool {
	if !a.logsVisible() || mouse.EventType != vaxis.EventPress {
		return false
	}
	l := &a.logs
	if mouse.Button == vaxis.MouseLeftButton {
		for i, rect := range l.sourceRects {
			if rect.contains(mouse.Col, mouse.Row) {
				a.changeLogSource(i)
				return true
			}
		}
	}
	if l.bodyRect.contains(mouse.Col, mouse.Row) {
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.moveLogSelection(-1)
		case vaxis.MouseWheelDown:
			a.moveLogSelection(1)
		case vaxis.MouseLeftButton:
			index := mouse.Row - l.bodyRect.row
			if index < len(l.mouseRows) {
				a.pauseLogs()
				l.selected = l.mouseRows[index]
				rows := a.logRows(l.bodyRect.width)
				cursor := l.offset + index
				first := cursor
				for first > 0 && rows[first-1].index == l.selected {
					first--
				}
				l.selectedPart = cursor - first
			}
		}
	}
	// Old fleet hit regions must not select another node behind the log view.
	return true
}
