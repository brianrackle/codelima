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
	s.mouseRows, s.controls = nil, nil
	if s.input != nil || s.query != "" {
		tuiLine(win.New(2, 1, width-4, 1), 0, "/ "+s.query, theme.mint)
	}
	runningRows := len(running)
	if width-6 < 118 {
		runningRows *= 2
	}
	runningHeight := min(max(5, runningRows+4), max(6, height/3))
	runningStyle, historyStyle := theme.border, theme.mint
	if slices.ContainsFunc(running, func(row tuiActivityRow) bool { return row.id == s.selected }) {
		runningStyle, historyStyle = theme.mint, theme.border
	}
	runningOuter := win.New(1, 2, width-2, runningHeight)
	runningBox := border.All(runningOuter, runningStyle)
	title := fmt.Sprintf(" Running %d · owned by the daemon, keeps going if you quit ", len(running))
	if width < 100 {
		title = fmt.Sprintf(" Running %d · daemon owned; continues after quit ", len(running))
	}
	tuiLine(runningOuter.New(2, 0, width-6, 1), 0, title, theme.muted)
	if len(running) == 0 {
		text := "No operations running."
		if !s.loaded {
			text = "Loading shared operations…"
		}
		if a.service == nil || a.service.daemonClient == nil {
			text = "Shared operations require a daemon connection."
		}
		tuiLine(runningBox.New(1, 1, width-6, 1), 0, text, theme.dim)
	} else {
		a.drawActivityRows(runningBox.New(0, 1, width-4, runningHeight-4), running, &s.runningScroll, true, theme)
	}
	tuiLine(runningBox.New(1, runningHeight-3, width-6, 1), 0, "One operation per node; another request is refused with the reason.", theme.dim)
	historyY := 3 + runningHeight
	historyHeight := max(3, height-historyY-4)
	historyOuter := win.New(1, historyY, width-2, historyHeight)
	historyBox := border.All(historyOuter, historyStyle)
	title = " History · newest first "
	if !activityHistoryNeedsDates(history, time.Now()) {
		title = " History today · newest first "
	}
	if s.before != "" {
		title = fmt.Sprintf(" History · page %d ", len(s.previous)+1)
	}
	a.drawActivityHistoryTitle(historyOuter, title, theme)
	s.viewport = max(1, historyHeight-3)
	a.drawActivityRows(historyBox.New(0, 1, width-4, s.viewport), history, &s.historyScroll, false, theme)
	if len(history) == 0 {
		text := "No matching records on this page."
		if !s.loaded {
			text = "Loading history…"
		}
		tuiLine(historyBox.New(1, 1, width-6, 1), 0, text, theme.dim)
	}
	first, last := 0, min(len(history), s.historyScroll+s.viewport)
	if len(history) > 0 {
		first = s.historyScroll + 1
	}
	tuiLine(win.New(2, height-4, width-4, 1), 0, fmt.Sprintf("Rows %d–%d of %d · page %d · [] pages · g top", first, last, len(history), len(s.previous)+1), theme.dim)
	status, style := "Footer notices last 6s · kept up to 7 days · local: this window", theme.dim
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

func activityHistoryNeedsDates(rows []tuiActivityRow, now time.Time) bool {
	today := now.Local().Format("2006-01-02")
	return slices.ContainsFunc(rows, func(row tuiActivityRow) bool { return row.at.Local().Format("2006-01-02") != today })
}

func (a *vaxisTUIApp) drawActivityRows(win vaxis.Window, rows []tuiActivityRow, scroll *int, running bool, theme tuiTheme) {
	width, height := win.Size()
	if height <= 0 || width <= 0 {
		return
	}
	rowHeight := 1
	if running && width-2 < 118 && height >= 2 {
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
	dates := activityHistoryNeedsDates(rows, time.Now())
	col, top := win.Origin()
	for i := *scroll; i < min(len(rows), *scroll+visible); i++ {
		row := rows[i]
		y := (i - *scroll) * rowHeight
		rowWin := win.New(0, y, width, rowHeight)
		rowTheme := theme
		if row.id == a.activity.selected {
			// Apply selection to every cell, including spacing and unused columns.
			for _, style := range []*vaxis.Style{&rowTheme.text, &rowTheme.dim, &rowTheme.muted, &rowTheme.blue, &rowTheme.amber, &rowTheme.coral, &rowTheme.mint} {
				style.Background = theme.selected.Background
				style.Attribute |= theme.selected.Attribute
			}
			tuiFill(rowWin, rowTheme.text)
		}
		target := tuiActivityMouseRow{id: row.id, rect: tuiRect{col: col, row: top + y, width: width, height: rowHeight}}
		content := rowWin.New(1, 0, width-2, rowHeight)
		if running {
			drawActivityRunningRow(content, row, rowTheme)
			if width-2 >= 118 {
				target.open = tuiRect{col: col + width - 19, row: top + y, width: 6, height: 1}
				target.cancel = tuiRect{col: col + width - 10, row: top + y, width: 8, height: 1}
			}
		} else {
			target.open = drawActivityHistoryRow(content, row, dates, row.id == a.activity.selected, rowTheme)
		}
		a.activity.mouseRows = append(a.activity.mouseRows, target)
	}
}

func drawActivityHistoryRow(win vaxis.Window, row tuiActivityRow, dates, selected bool, theme tuiTheme) tuiRect {
	width, _ := win.Size()
	stampWidth, nodeWidth, actionWidth := 8, 16, 16
	if dates {
		stampWidth = 12
	}
	if width < 118 {
		nodeWidth, actionWidth = 14, 1
	}
	iconCol := stampWidth + 2
	nodeCol := iconCol + 3
	messageCol := nodeCol + nodeWidth + 2
	actionCol := width - actionWidth
	stamp := row.at.Local().Format("15:04:05")
	if dates {
		stamp = row.at.Local().Format("Jan 02 15:04")
	}
	icon, iconStyle, messageStyle := "i", theme.blue, theme.text
	switch {
	case row.level >= slog.LevelError:
		icon, iconStyle, messageStyle = "✕", theme.coral, theme.coral
	case row.level >= slog.LevelWarn:
		icon, iconStyle = "⚠", theme.amber
	case row.success:
		icon, iconStyle = "✓", theme.mint
	case row.requested:
		icon, iconStyle = "◐", theme.amber
	}
	nodeStyle := theme.text
	if row.nodeID == "" && !row.operation {
		nodeStyle = theme.dim
	}
	if selected {
		nodeStyle.Attribute |= vaxis.AttrBold
	}
	tuiLine(win.New(0, 0, stampWidth, 1), 0, stamp, theme.dim)
	tuiLine(win.New(iconCol, 0, 2, 1), 0, icon, iconStyle)
	tuiLine(win.New(nodeCol, 0, nodeWidth, 1), 0, plainLogText(row.node), nodeStyle)
	tuiLine(win.New(messageCol, 0, max(0, actionCol-messageCol-2), 1), 0, row.text, messageStyle)
	hint := activityRowActionHint(row)
	if hint == "" {
		return tuiRect{}
	}
	if actionWidth == 1 {
		hint = "⏎"
	}
	tuiLine(win.New(actionCol, 0, actionWidth, 1), 0, hint, theme.dim)
	col, top := win.Origin()
	return tuiRect{col: col + actionCol, row: top, width: actionWidth, height: 1}
}

func activityRowActionHint(row tuiActivityRow) string {
	switch row.action {
	case activityOpenOperation:
		return "⏎ output"
	case activityOpenTerminal:
		return "⏎ attach"
	case activityOpenHost, activityOpenDaemon:
		return "5 system"
	case activityOpenPorts:
		return "3 ports"
	}
	if row.nodeID != "" {
		if row.level >= slog.LevelError {
			return "⏎ details"
		}
		return "⏎ open"
	}
	return ""
}

func drawActivityRunningRow(win vaxis.Window, row tuiActivityRow, theme tuiTheme) {
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
		tuiLine(win.New(0, 0, 2, 1), 0, "◐", theme.amber)
		tuiLine(win.New(3, 0, 14, 1), 0, plainLogText(row.node), theme.text)
		tuiLine(win.New(19, 0, 8, 1), 0, plainLogText(row.kind), theme.text)
		tuiLine(win.New(29, 0, width-85, 1), 0, strings.Trim(strings.TrimSpace(phase+" · "+detail), " ·"), theme.text)
		if bar == "" {
			tuiLine(win.New(width-54, 0, 24, 1), 0, count, theme.dim)
		} else {
			tuiLine(win.New(width-54, 0, 12, 1), 0, bar, theme.amber)
			tuiLine(win.New(width-40, 0, 10, 1), 0, count, theme.dim)
		}
		tuiLine(win.New(width-28, 0, 8, 1), 0, elapsed, theme.amber)
		tuiLine(win.New(width-18, 0, 18, 1), 0, "⏎ open · K cancel", theme.dim)
		return
	}
	tuiLine(win.New(0, 0, 2, 1), 0, "◐", theme.amber)
	tuiLine(win.New(3, 0, width-12, 1), 0, plainLogText(row.node)+"  "+row.kind+" · "+phase, theme.text)
	tuiLine(win.New(width-8, 0, 8, 1), 0, elapsed, theme.amber)
	if height >= 2 {
		if bar == "" {
			tuiLine(win, 1, count, theme.dim)
		} else {
			tuiLine(win.New(0, 1, 12, 1), 0, bar, theme.amber)
			tuiLine(win.New(14, 1, width-14, 1), 0, count+" · "+detail, theme.muted)
		}
	}
}

func (a *vaxisTUIApp) handleActivityMouse(mouse vaxis.Mouse) bool {
	if a.view != tuiViewActivity {
		return false
	}
	if mouse.EventType != vaxis.EventPress {
		return true
	}
	switch mouse.Button {
	case vaxis.MouseWheelUp:
		a.moveActivitySelection(-1)
	case vaxis.MouseWheelDown:
		a.moveActivitySelection(1)
	case vaxis.MouseLeftButton:
		for _, control := range a.activity.controls {
			if control.rect.contains(mouse.Col, mouse.Row) {
				if control.severity {
					a.activity.level = control.level
					a.reconcileActivitySelection()
				} else {
					a.handleKey(control.key)
				}
				return true
			}
		}
		for _, row := range a.activity.mouseRows {
			if row.rect.contains(mouse.Col, mouse.Row) {
				a.activity.selected = row.id
				switch {
				case row.open.contains(mouse.Col, mouse.Row):
					a.openActivityNode()
				case row.cancel.contains(mouse.Col, mouse.Row):
					a.cancelActivityOperation()
				}
				break
			}
		}
	}
	return true
}
