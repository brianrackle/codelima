package codelima

import (
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

// tuiTextView displays an immutable, wrapped detail snapshot without I/O.
type tuiTextView struct {
	title, text      string
	scroll, viewport int
	end              bool
}

func (v *tuiTextView) Update(event vaxis.Event) (bool, error) {
	if mouse, ok := event.(vaxis.Mouse); ok && mouse.EventType == vaxis.EventPress {
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			v.scroll -= 3
		case vaxis.MouseWheelDown:
			v.scroll += 3
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if !ok || !tuiKeyActivates(key, false) {
		return false, nil
	}
	if isOverlayCancelKey(key) && tuiKeyActivates(key, true) {
		return true, nil
	}
	switch {
	case key.Matches(vaxis.KeyUp), key.MatchString("k"):
		v.scroll--
	case key.Matches(vaxis.KeyDown), key.MatchString("j"):
		v.scroll++
	case key.Matches(vaxis.KeyPgUp):
		v.scroll -= max(1, v.viewport-1)
	case key.Matches(vaxis.KeyPgDown):
		v.scroll += max(1, v.viewport-1)
	case key.Matches(vaxis.KeyHome):
		v.scroll = 0
	case key.Matches(vaxis.KeyEnd):
		v.end = true
	}
	return false, nil
}

func (v *tuiTextView) FooterHint() string {
	return "↑↓ scroll · PgUp/PgDn page · Home/End · Esc close"
}

func (v *tuiTextView) Draw(win vaxis.Window, header, muted, _ vaxis.Style) {
	body := border.All(win, muted)
	w, h := body.Size()
	tuiLine(body, 0, plainLogText(v.title), header)
	var lines []string
	for _, line := range strings.Split(v.text, "\n") {
		for _, row := range commandVisualRows(vaxis.Characters(plainLogText(line)), max(1, w)) {
			lines = append(lines, row.text)
		}
	}
	v.viewport = max(1, h-3)
	if v.end {
		v.scroll, v.end = len(lines), false
	}
	v.scroll = max(0, min(v.scroll, len(lines)-v.viewport))
	for i, line := range lines[v.scroll:min(len(lines), v.scroll+v.viewport)] {
		tuiLine(body, i+2, line, muted)
	}
}
