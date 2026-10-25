package codelima

import (
	"strings"

	"go.rockorager.dev/vaxis"
)

type tuiActivityControl struct {
	rect     tuiRect
	key      vaxis.Key
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
	tuiLine(win.New(left, 0, max(0, width-left-1), 1), 0, "· f ", theme.dim)
	if left+3 < width {
		a.activity.controls = append(a.activity.controls, tuiActivityControl{rect: tuiRect{col: col + left, row: row, width: 3, height: 1}, key: vaxis.Key{Keycode: 'f', Text: "f"}})
	}
}

func (a *vaxisTUIApp) drawActivityFooter(win vaxis.Window, theme tuiTheme) {
	type item struct {
		label string
		key   rune
	}
	items := []item{
		{"↑", vaxis.KeyUp}, {"↓ move", vaxis.KeyDown}, {"⏎ open", vaxis.KeyEnter},
		{"K cancel", 'K'}, {"f level", 'f'}, {"/ filter", '/'}, {"y copy", 'y'}, {"? help", '?'},
	}
	width, _ := win.Size()
	if width >= 118 {
		items = append(items, item{"[ newer", '['}, item{"] older", ']'}, item{"g top", 'g'})
	}
	if a.activity.input != nil {
		items = []item{{"Enter keep filter", vaxis.KeyEnter}, {"Esc cancel", vaxis.KeyEsc}}
	}
	col, row := win.Origin()
	left := 1
	for _, item := range items {
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
		key := vaxis.Key{Keycode: item.key}
		if item.key < 128 {
			key.Text = string(item.key)
		}
		a.activity.controls = append(a.activity.controls, tuiActivityControl{rect: tuiRect{col: col + left, row: row, width: cells, height: 1}, key: key})
		left += cells + 2
	}
}
