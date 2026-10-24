package codelima

import (
	"fmt"
	"log/slog"
	"slices"
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
	runningHeight := min(max(5, len(running)+3), max(5, height/3))
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
	selected := slices.IndexFunc(rows, func(row tuiActivityRow) bool { return row.id == a.activity.selected })
	if selected >= 0 {
		if selected < *scroll {
			*scroll = selected
		} else if selected >= *scroll+height {
			*scroll = selected - height + 1
		}
	}
	*scroll = max(0, min(*scroll, len(rows)-height))
	for i := *scroll; i < min(len(rows), *scroll+height); i++ {
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
			stamp = time.Since(row.at).Truncate(time.Second).String()
		}
		if row.id == a.activity.selected {
			style.Background = theme.selected.Background
			style.Attribute |= vaxis.AttrBold
		}
		line := stamp + "  " + icon + "  " + plainLogText(row.node) + "  " + row.text
		tuiLine(win, i-*scroll, line, style)
		a.activity.mouseRows = append(a.activity.mouseRows, tuiActivityMouseRow{id: row.id, rect: tuiRect{col: col, row: top + i - *scroll, width: width, height: 1}})
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
