package codelima

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func (a *vaxisTUIApp) drawActivity(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	s := &a.activity
	a.reconcileActivitySelection()
	running, history := a.activityRows()
	s.mouseRows = nil
	title := "Activity"
	if s.input != nil || s.query != "" {
		title += "  / " + s.query
	}
	tuiLine(win.New(2, 2, width-4, 1), 0, title, theme.mint)
	runningRows := len(running)
	if width-6 < 118 {
		runningRows *= 2
	}
	runningHeight := min(max(5, runningRows+3), max(5, height/3))
	runningBox := border.All(win.New(1, 4, width-2, runningHeight), theme.border)
	tuiLine(runningBox, 0, fmt.Sprintf("Running · %d · daemon owned; continues after quit", len(running)), theme.muted)
	if len(running) == 0 {
		text := "No operations running."
		if !s.loaded {
			text = "Loading shared operations…"
		}
		if a.service == nil || a.service.daemonClient == nil {
			text = "Shared operations require a daemon connection."
		}
		tuiLine(runningBox, 2, text, theme.dim)
	} else {
		a.drawActivityRows(runningBox.New(1, 1, width-6, runningHeight-3), running, &s.runningScroll, true, 3, 6, theme)
	}
	historyY := 5 + runningHeight
	historyHeight := max(3, height-historyY-4)
	historyBox := border.All(win.New(1, historyY, width-2, historyHeight), theme.border)
	levels := []string{"all", "warn+", "error"}
	page := "newest"
	if s.before != "" {
		page = fmt.Sprintf("page %d", len(s.previous)+1)
	}
	tuiLine(historyBox, 0, fmt.Sprintf("History · %s · %s · kept up to 7 days", page, levels[s.level]), theme.muted)
	s.viewport = max(1, historyHeight-3)
	a.drawActivityRows(historyBox.New(1, 1, width-6, s.viewport), history, &s.historyScroll, false, 3, historyY+2, theme)
	if len(history) == 0 {
		text := "No matching records on this page."
		if !s.loaded {
			text = "Loading history…"
		}
		tuiLine(historyBox, 2, text, theme.dim)
	}
	status, style := "[] newer / older history · g newest · local messages marked ‘this window’", theme.dim
	if s.page.Next == "" {
		status = "End of retained history · local messages marked ‘this window’"
	}
	switch {
	case s.err != nil:
		status, style = "Activity unavailable: "+plainLogText(s.err.Error())+" · g reload", theme.coral
	case s.page.HistoryError != "":
		status, style = "History cursor unavailable · g reload newest", theme.amber
	case s.page.MessageError != "":
		status, style = "Messages unavailable: "+plainLogText(s.page.MessageError), theme.coral
	case a.messageDelivery.err != nil:
		status, style = "Messages awaiting persistence · this window · retrying", theme.amber
	}
	tuiLine(win.New(2, height-3, width-4, 1), 0, status, style)
}

func (a *vaxisTUIApp) drawActivityRows(win vaxis.Window, rows []tuiActivityRow, scroll *int, running bool, col, top int, theme tuiTheme) {
	width, height := win.Size()
	if height <= 0 || width <= 0 {
		return
	}
	rowHeight := 1
	if running && width < 118 && height >= 2 {
		rowHeight = 2
	}
	visible := max(1, height/rowHeight)
	selected := slices.IndexFunc(rows, func(row tuiActivityRow) bool { return row.id == a.activity.selected })
	if selected >= 0 {
		if selected < *scroll {
			*scroll = selected
		} else if selected >= *scroll+visible {
			*scroll = selected - visible + 1
		}
	}
	*scroll = max(0, min(*scroll, len(rows)-visible))
	for i := *scroll; i < min(len(rows), *scroll+visible); i++ {
		row := rows[i]
		style, icon := theme.text, "i"
		if row.success {
			style, icon = theme.mint, "✓"
		}
		if row.level >= slog.LevelError {
			style, icon = theme.coral, "✕"
		} else if row.level >= slog.LevelWarn {
			style, icon = theme.amber, "⚠"
		}
		stamp := row.at.Local().Format("15:04:05")
		if running {
			icon, style = "◐", theme.amber
		}
		if row.id == a.activity.selected {
			style.Background = theme.selected.Background
			style.Attribute |= vaxis.AttrBold
		}
		line := stamp + "  " + icon + "  " + plainLogText(row.node) + "  " + row.text
		y := (i - *scroll) * rowHeight
		if running {
			drawActivityRunningRow(win.New(0, y, width, rowHeight), row, style)
		} else {
			tuiLine(win, y, line, style)
		}
		a.activity.mouseRows = append(a.activity.mouseRows, tuiActivityMouseRow{id: row.id, rect: tuiRect{col: col, row: top + y, width: width, height: rowHeight}})
	}
}

func drawActivityRunningRow(win vaxis.Window, row tuiActivityRow, style vaxis.Style) {
	width, height := win.Size()
	elapsed := "—"
	if !row.at.IsZero() {
		elapsed = max(time.Duration(0), time.Since(row.at)).Truncate(time.Second).String()
	}
	phase, detail, count, bar := "", "", "progress unavailable", ""
	if p := row.progress; p != nil && p.Total > 0 {
		phase, detail = p.Phase, p.Detail
		completed := max(0, min(p.Completed, p.Total))
		count = fmt.Sprintf("%d/%d phases", completed, p.Total)
		filled := 12 * completed / p.Total
		bar = strings.Repeat("█", filled) + strings.Repeat("░", 12-filled)
	}
	if row.state == "canceling" {
		phase = "canceling"
	}
	if width >= 118 {
		tuiLine(win.New(0, 0, 21, 1), 0, "◐ "+plainLogText(row.node), style)
		tuiLine(win.New(22, 0, 9, 1), 0, plainLogText(row.kind), style)
		tuiLine(win.New(32, 0, width-74, 1), 0, strings.TrimSpace(phase+" · "+detail), style)
		if bar == "" {
			tuiLine(win.New(width-40, 0, 29, 1), 0, count, style)
		} else {
			tuiLine(win.New(width-40, 0, 12, 1), 0, bar, style)
			tuiLine(win.New(width-26, 0, 15, 1), 0, count, style)
		}
		tuiLine(win.New(width-10, 0, 10, 1), 0, elapsed, style)
		return
	}
	tuiLine(win.New(0, 0, width-9, 1), 0, "◐ "+plainLogText(row.node)+"  "+row.kind+" · "+phase, style)
	tuiLine(win.New(width-8, 0, 8, 1), 0, elapsed, style)
	if height >= 2 {
		if bar == "" {
			tuiLine(win, 1, count, style)
		} else {
			tuiLine(win, 1, bar+"  "+count+" · "+detail, style)
		}
	}
}

func (a *vaxisTUIApp) handleActivityMouse(mouse vaxis.Mouse) bool {
	if a.view != tuiViewActivity {
		return false
	}
	if mouse.EventType == vaxis.EventPress {
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.moveActivitySelection(-1)
		case vaxis.MouseWheelDown:
			a.moveActivitySelection(1)
		case vaxis.MouseLeftButton:
			for _, row := range a.activity.mouseRows {
				if row.rect.contains(mouse.Col, mouse.Row) {
					a.activity.selected = row.id
					break
				}
			}
		}
	}
	return true
}
