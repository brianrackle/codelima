package codelima

import (
	"math"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiHelpColumn struct {
	group int
	rect  tuiRect
}

type tuiHelpLine struct {
	key, text string
	heading   bool
}

type tuiKeyHelp struct {
	app                             *vaxisTUIApp
	groups                          []tuiHelpGroup
	input                           *textinput.Model
	focus                           int
	scroll                          []int
	searching                       bool
	searchScroll                    int
	pageSize                        int
	columns                         []tuiHelpColumn
	searchRect, closeRect, findRect tuiRect
}

func newTUIKeyHelp(app *vaxisTUIApp, groups []tuiHelpGroup) *tuiKeyHelp {
	return &tuiKeyHelp{app: app, groups: groups, input: textinput.New(), scroll: make([]int, len(groups)), pageSize: 1}
}

func (h *tuiKeyHelp) PreferredSize() (int, int) { return 144, 36 }

func (h *tuiKeyHelp) FooterHint() string {
	if h.searching {
		return "Type to search · ↑↓/PgUp/PgDn scroll · Tab browse · Esc/? close"
	}
	return "Tab/←→ group · ↑↓/PgUp/PgDn scroll · / search · Esc/? close"
}

func (h *tuiKeyHelp) offset() *int {
	if h.searching {
		return &h.searchScroll
	}
	return &h.scroll[h.focus]
}

func (h *tuiKeyHelp) Update(event vaxis.Event) (bool, error) {
	if mouse, ok := event.(vaxis.Mouse); ok {
		return h.updateMouse(mouse), nil
	}
	key, ok := event.(vaxis.Key)
	if !ok {
		if h.searching {
			h.input.Update(event)
		}
		return false, nil
	}
	if key.EventType == vaxis.EventPaste {
		if h.searching {
			h.input.Update(key)
			h.searchScroll = 0
		}
		return false, nil
	}
	if !tuiKeyActivates(key, false) {
		return false, nil
	}
	if (key.Matches(vaxis.KeyEsc) || key.MatchString("?")) && tuiKeyActivates(key, true) {
		return true, nil
	}
	if key.Matches(vaxis.KeyTab) || key.Matches(vaxis.KeyTab, vaxis.ModShift) || (!h.searching && (key.Matches(vaxis.KeyLeft) || key.Matches(vaxis.KeyRight))) {
		if !tuiKeyActivates(key, true) {
			return false, nil
		}
		if h.searching {
			h.searching = false
			return false, nil
		}
		delta := 1
		if key.Matches(vaxis.KeyTab, vaxis.ModShift) || key.Matches(vaxis.KeyLeft) {
			delta = -1
		}
		h.focus = (h.focus + delta + len(h.groups)) % len(h.groups)
		return false, nil
	}
	offset := h.offset()
	switch {
	case key.Matches(vaxis.KeyDown) || (!h.searching && key.MatchString("j")):
		*offset = min(math.MaxInt-1, *offset) + 1
	case key.Matches(vaxis.KeyUp) || (!h.searching && key.MatchString("k")):
		*offset = max(0, *offset-1)
	case key.Matches(vaxis.KeyPgDown):
		*offset = min(math.MaxInt-h.pageSize, *offset) + h.pageSize
	case key.Matches(vaxis.KeyPgUp):
		*offset = max(0, *offset-h.pageSize)
	case !h.searching && key.Matches(vaxis.KeyHome):
		*offset = 0
	case !h.searching && key.Matches(vaxis.KeyEnd):
		*offset = math.MaxInt
	case !h.searching && key.MatchString("/") && tuiKeyActivates(key, true):
		h.searching, h.searchScroll = true, 0
	case h.searching:
		before := h.input.String()
		h.input.Update(key)
		if before != h.input.String() {
			h.searchScroll = 0
		}
	}
	return false, nil
}

func (h *tuiKeyHelp) updateMouse(mouse vaxis.Mouse) bool {
	if mouse.EventType != vaxis.EventPress {
		return false
	}
	if mouse.Button == vaxis.MouseLeftButton {
		if h.closeRect.contains(mouse.Col, mouse.Row) {
			return true
		}
		if h.findRect.contains(mouse.Col, mouse.Row) {
			h.searching, h.searchScroll = true, 0
			return false
		}
	}
	inside := h.searching && h.searchRect.contains(mouse.Col, mouse.Row)
	for _, column := range h.columns {
		if column.rect.contains(mouse.Col, mouse.Row) {
			h.focus, inside = column.group, true
			break
		}
	}
	if inside {
		offset := h.offset()
		switch mouse.Button {
		case vaxis.MouseWheelDown:
			*offset = min(math.MaxInt-1, *offset) + 1
		case vaxis.MouseWheelUp:
			*offset = max(0, *offset-1)
		}
	}
	return false
}

func (h *tuiKeyHelp) searchLines() ([]tuiHelpLine, int) {
	terms := strings.Fields(strings.ToLower(h.input.String()))
	var lines []tuiHelpLine
	count := 0
	for _, group := range h.groups {
		for _, section := range group.sections {
			label := group.title
			if section.title != "" {
				label += " › " + section.title
			}
			heading := false
			for _, binding := range section.bindings {
				text := strings.ToLower(label + " " + binding.key + " " + binding.description)
				match := true
				for _, term := range terms {
					if !strings.Contains(text, term) {
						match = false
						break
					}
				}
				if !match {
					continue
				}
				if !heading {
					lines = append(lines, tuiHelpLine{text: label, heading: true})
					heading = true
				}
				lines = append(lines, tuiHelpLine{key: binding.key, text: binding.description})
				count++
			}
		}
	}
	return lines, count
}
