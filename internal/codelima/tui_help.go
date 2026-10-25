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
	tuiOverlayKeys
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
		return h.bindingHints("Type to search · {dialog.up dialog.down}/{overlay.page_up overlay.page_down} scroll · {help.next} browse · {dialog.cancel} close")
	}
	return h.bindingHints("{help.next help.left help.right} group · {reader.up reader.down}/{overlay.page_up overlay.page_down} scroll · {help.search} search · {dialog.cancel} close")
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
	if (overlayKeyAction(h, key) == "dialog.cancel" || overlayKeyAction(h, key) == "help.close") && tuiKeyActivates(key, true) {
		return true, nil
	}
	if overlayKeyAction(h, key) == "help.next" || overlayKeyAction(h, key) == "help.previous" || (!h.searching && (overlayKeyAction(h, key) == "help.left" || overlayKeyAction(h, key) == "help.right")) {
		if !tuiKeyActivates(key, true) {
			return false, nil
		}
		if h.searching {
			h.searching = false
			return false, nil
		}
		delta := 1
		if overlayKeyAction(h, key) == "help.previous" || overlayKeyAction(h, key) == "help.left" {
			delta = -1
		}
		h.focus = (h.focus + delta + len(h.groups)) % len(h.groups)
		return false, nil
	}
	offset := h.offset()
	switch {
	case overlayKeyAction(h, key) == "reader.down" || overlayKeyAction(h, key) == "dialog.down":
		*offset = min(math.MaxInt-1, *offset) + 1
	case overlayKeyAction(h, key) == "reader.up" || overlayKeyAction(h, key) == "dialog.up":
		*offset = max(0, *offset-1)
	case overlayKeyAction(h, key) == "overlay.page_down":
		*offset = min(math.MaxInt-h.pageSize, *offset) + h.pageSize
	case overlayKeyAction(h, key) == "overlay.page_up":
		*offset = max(0, *offset-h.pageSize)
	case !h.searching && overlayKeyAction(h, key) == "overlay.first":
		*offset = 0
	case !h.searching && overlayKeyAction(h, key) == "overlay.last":
		*offset = math.MaxInt
	case !h.searching && overlayKeyAction(h, key) == "help.search" && tuiKeyActivates(key, true):
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
				text := strings.ToLower(label + " " + binding.key + " " + binding.description + " " + binding.actions)
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

func (h *tuiKeyHelp) setKeyBindings(keys *compiledKeyMap) {
	if h.bindings == keys {
		return
	}
	h.bindings = keys
	if len(h.groups) == 0 {
		return
	}
	first := h.groups[0].title
	groups := managerHelpGroups(keys)
	for i := range groups {
		if groups[i].title == first {
			group := groups[i]
			copy(groups[1:i+1], groups[:i])
			groups[0] = group
			break
		}
	}
	h.groups = groups
}
