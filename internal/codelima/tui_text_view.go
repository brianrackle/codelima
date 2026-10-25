package codelima

import (
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

// tuiTextView displays a wrapped detail snapshot without I/O.
type tuiTextView struct {
	tuiOverlayKeys
	title, text      string
	scroll, viewport int
	end              bool
	wrappedText      string
	wrappedWidth     int
	lines            []string
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
	if overlayKeyAction(v, key) == "dialog.cancel" && tuiKeyActivates(key, true) {
		return true, nil
	}
	switch {
	case overlayKeyAction(v, key) == "reader.up":
		v.scroll--
	case overlayKeyAction(v, key) == "reader.down":
		v.scroll++
	case overlayKeyAction(v, key) == "overlay.page_up":
		v.scroll -= max(1, v.viewport-1)
	case overlayKeyAction(v, key) == "overlay.page_down":
		v.scroll += max(1, v.viewport-1)
	case overlayKeyAction(v, key) == "overlay.first":
		v.scroll = 0
	case overlayKeyAction(v, key) == "overlay.last":
		v.end = true
	}
	return false, nil
}

func (v *tuiTextView) FooterHint() string {
	return v.bindingHints("{reader.up reader.down} scroll · {overlay.page_up overlay.page_down} page · {overlay.first overlay.last} · {dialog.cancel} close")
}

func (v *tuiTextView) Draw(win vaxis.Window, header, muted, _ vaxis.Style) {
	body := border.All(win, muted)
	w, h := body.Size()
	tuiLine(body, 0, plainLogText(v.title), header)
	if v.lines == nil || v.wrappedText != v.text || v.wrappedWidth != w {
		v.lines = nil
		for _, line := range strings.Split(v.text, "\n") {
			for _, row := range commandVisualRows(vaxis.Characters(plainLogText(line)), max(1, w)) {
				v.lines = append(v.lines, row.text)
			}
		}
		v.wrappedText, v.wrappedWidth = v.text, w
	}
	lines := v.lines
	v.viewport = max(1, h-3)
	if v.end {
		v.scroll, v.end = len(lines), false
	}
	v.scroll = max(0, min(v.scroll, len(lines)-v.viewport))
	for i, line := range lines[v.scroll:min(len(lines), v.scroll+v.viewport)] {
		tuiLine(body, i+2, line, muted)
	}
}
