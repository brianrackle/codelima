package codelima

import (
	"strings"

	"go.rockorager.dev/vaxis"
)

type tuiActivityControl struct {
	rect     tuiRect
	action   string
	severity bool
	level    int
}

func (a *vaxisTUIApp) drawActivityHistoryTitle(win vaxis.Window, title string, theme tuiTheme) {
	width, _ := win.Size()
	labels := []string{" all ", " warnings ", " errors "}
	choicesWidth := 4 // trailing keyboard hint
	for _, label := range labels {
		choicesWidth += renderedTextWidth(a.vx, label)
	}
	left := max(2, width-choicesWidth-2)
	tuiLine(win.New(2, 0, max(0, left-3), 1), 0, title, theme.muted)
	col, row := win.Origin()
	for i, label := range labels {
		cells := renderedTextWidth(a.vx, label)
		if left+cells > width-1 {
			break
		}
		style := theme.dim
		if a.activity.level == i {
			style = theme.selected
			style.Foreground = theme.mint.Foreground
		}
		tuiLine(win.New(left, 0, cells, 1), 0, label, style)
		a.activity.controls = append(a.activity.controls, tuiActivityControl{rect: tuiRect{col: col + left, row: row, width: cells, height: 1}, severity: true, level: i})
		left += cells
	}
	tuiLine(win.New(left, 0, max(0, width-left-1), 1), 0, a.keyHints("· {activity.level} "), theme.dim)
	if left+3 < width {
		a.activity.controls = append(a.activity.controls, tuiActivityControl{rect: tuiRect{col: col + left, row: row, width: 3, height: 1}, action: "activity.level"})
	}
}

func (a *vaxisTUIApp) drawActivityFooter(win vaxis.Window, theme tuiTheme) {
	type item struct {
		label  string
		action string
	}
	items := []item{
		{"", "navigation.up"}, {"move", "navigation.down"}, {"open", "activity.open"},
		{"cancel", "activity.cancel"}, {"level", "activity.level"}, {"filter", "activity.filter"}, {"copy", "activity.copy"}, {"help", "help.open"},
	}
	width, _ := win.Size()
	if width >= 118 {
		items = append(items, item{"newer", "activity.newer"}, item{"older", "activity.older"}, item{"top", "activity.first"})
	}
	if a.activity.input != nil {
		items = []item{{"keep filter", "input.accept"}, {"cancel", "input.cancel"}}
	}
	col, row := win.Origin()
	left := 1
	for _, item := range items {
		item.label = strings.TrimSpace(a.activityControlKey(item.action) + " " + item.label)
		cells := renderedTextWidth(a.vx, item.label)
		if left+cells > width {
			break // Never expose a target for a clipped or invisible control.
		}
		keyStyle := theme.chrome
		keyStyle.Foreground = theme.mint.Foreground
		label, rest, _ := strings.Cut(item.label, " ")
		if rest != "" {
			rest = " " + rest
		}
		win.New(left, 0, cells, 1).PrintTruncate(0, vaxis.Segment{Text: label, Style: keyStyle}, vaxis.Segment{Text: rest, Style: theme.chrome})
		a.activity.controls = append(a.activity.controls, tuiActivityControl{rect: tuiRect{col: col + left, row: row, width: cells, height: 1}, action: item.action})
		left += cells + 2
	}
}

func (a *vaxisTUIApp) activityControlKey(action string) string {
	keys := a.keyMap()
	if action == "activity.open" && len(keys.actions[action]) > 0 && keys.actions[action][0] == (keyBindingIdentity{code: vaxis.KeyEnter}) {
		return "⏎"
	}
	return keys.PrimaryHint(action)
}
