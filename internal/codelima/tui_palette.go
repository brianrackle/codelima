package codelima

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiPaletteCommand struct {
	id, group, label, hint, shortcut string
	run                              func() error
	unavailable                      bool
	warning                          bool
	score                            int
	positions                        []int
}

type tuiPaletteTarget struct {
	rect tuiRect
	id   string
}

type tuiPalette struct {
	title                 string
	app                   *vaxisTUIApp
	input                 *textinput.Model
	commands, matches     []tuiPaletteCommand
	index, scroll, height int
	query                 string
	refresh               func() []tuiPaletteCommand
	recents               func() []string
	record                func(string)
	rows                  []tuiPaletteTarget
	controls              []tuiPaletteControl
}

func newTUIPalette(title string, commands []tuiPaletteCommand) *tuiPalette {
	p := &tuiPalette{title: title, input: textinput.New(), commands: commands, height: 1}
	p.filter()
	return p
}

// Positions refer to runes in the original text, including non-ASCII names.
// Matching and highlighting share this result, so a hint match cannot invent
// highlighted letters in the command label.
func paletteFuzzyMatch(text, query string) (int, []int) {
	runes := []rune(text)
	for i, r := range runes {
		runes[i] = unicode.ToLower(r)
	}
	needle := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(needle) == 0 {
		return 0, nil
	}
	for start := 0; start+len(needle) <= len(runes); start++ {
		if slices.Equal(runes[start:start+len(needle)], needle) {
			positions := make([]int, len(needle))
			for i := range positions {
				positions[i] = start + i
			}
			return start, positions
		}
	}
	positions := make([]int, 0, len(needle))
	position, score := 0, len(runes)
	for _, r := range needle {
		index := slices.Index(runes[position:], r)
		if index < 0 {
			return -1, nil
		}
		score += index
		position += index + 1
		positions = append(positions, position-1)
	}
	return score, positions
}

func paletteGroupOrder(group string) int {
	switch group {
	case "RECENT":
		return -1
	case "NODES":
		return 0
	case "ACTIONS":
		return 1
	case "GO TO":
		return 2
	default:
		return 3
	}
}

func (p *tuiPalette) filter() {
	query := p.input.String()
	var selected tuiPaletteCommand
	if query == p.query && p.index >= 0 && p.index < len(p.matches) {
		selected = p.matches[p.index]
	}
	if p.refresh != nil {
		p.commands = p.refresh()
	}
	var recent []string
	if strings.TrimSpace(query) == "" && p.recents != nil {
		recent = p.recents()
	}
	p.matches = p.matches[:0]
	for _, c := range p.commands {
		if c.id == "" {
			c.id = c.label
		}
		if c.group == "" {
			c.group = "ACTIONS"
		}
		c.label, c.hint = plainLogText(c.label), plainLogText(c.hint)
		c.score, c.positions = paletteFuzzyMatch(c.label+" "+c.hint+" "+c.shortcut, query)
		if c.score < 0 {
			continue
		}
		if rank := slices.Index(recent, c.id); rank >= 0 {
			c.group, c.score = "RECENT", rank
		}
		p.matches = append(p.matches, c)
	}
	if slices.ContainsFunc(p.matches, func(c tuiPaletteCommand) bool { return c.group == "RECENT" }) {
		p.matches = slices.DeleteFunc(p.matches, func(c tuiPaletteCommand) bool { return c.group != "RECENT" })
	}
	slices.SortStableFunc(p.matches, func(a, b tuiPaletteCommand) int {
		if order := cmp.Compare(paletteGroupOrder(a.group), paletteGroupOrder(b.group)); order != 0 {
			return order
		}
		return cmp.Compare(a.score, b.score)
	})
	p.index = 0
	if selected.id != "" {
		index := slices.IndexFunc(p.matches, func(c tuiPaletteCommand) bool { return c.id == selected.id })
		if index < 0 {
			// Retain the identity the user was reading; a disappearing result
			// must never silently authorize its successor at the same row.
			selected.unavailable = true
			selected.group = "UNAVAILABLE"
			selected.hint = "No longer available · change search or choose another command"
			selected.positions = nil
			p.matches = append([]tuiPaletteCommand{selected}, p.matches...)
		} else {
			p.index = index
		}
	}
	if query != p.query {
		p.scroll = 0
	}
	p.query = query
}

func (p *tuiPalette) choose() (bool, error) {
	p.filter()
	if len(p.matches) == 0 {
		return false, nil
	}
	c := p.matches[p.index]
	if c.unavailable {
		return false, fmt.Errorf("%s is no longer available; search again", c.label)
	}
	if c.run == nil {
		return false, nil
	}
	if err := c.run(); err != nil {
		return false, err
	}
	if p.record != nil {
		p.record(c.id)
	}
	return true, nil
}

func (p *tuiPalette) Update(event vaxis.Event) (bool, error) {
	p.filter()
	if mouse, ok := event.(vaxis.Mouse); ok {
		if mouse.EventType != vaxis.EventPress {
			return false, nil
		}
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			p.index = max(0, p.index-1)
		case vaxis.MouseWheelDown:
			p.index = min(max(0, len(p.matches)-1), p.index+1)
		case vaxis.MouseLeftButton:
			for _, control := range p.controls {
				if control.rect.contains(mouse.Col, mouse.Row) {
					return p.Update(control.key)
				}
			}
			for _, row := range p.rows {
				if !row.rect.contains(mouse.Col, mouse.Row) {
					continue
				}
				index := slices.IndexFunc(p.matches, func(c tuiPaletteCommand) bool { return c.id == row.id })
				if index < 0 {
					return false, fmt.Errorf("command is no longer available; search again")
				}
				p.index = index
				return p.choose()
			}
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if !ok {
		p.input.Update(event)
		p.filter()
		return false, nil
	}
	if key.EventType == vaxis.EventPaste {
		p.input.Update(key)
		return false, nil
	}
	if !tuiKeyActivates(key, false) {
		return false, nil
	}
	switch {
	case key.Matches(vaxis.KeyEsc):
		return tuiKeyActivates(key, true), nil
	case key.Matches(vaxis.KeyUp):
		p.index = max(0, p.index-1)
	case key.Matches(vaxis.KeyDown):
		p.index = min(max(0, len(p.matches)-1), p.index+1)
	case key.Matches(vaxis.KeyPgUp):
		p.index = max(0, p.index-max(1, p.height/2))
	case key.Matches(vaxis.KeyPgDown):
		p.index = min(max(0, len(p.matches)-1), p.index+max(1, p.height/2))
	case key.Matches(vaxis.KeyTab):
		if tuiKeyActivates(key, true) && len(p.matches) > 0 {
			p.input.SetContent(p.matches[p.index].label)
			p.filter()
		}
	case key.Matches(vaxis.KeyEnter):
		if tuiKeyActivates(key, true) {
			return p.choose()
		}
	default:
		p.input.Update(key)
		p.filter()
	}
	return false, nil
}

func (p *tuiPalette) FooterHint() string {
	return "Enter run · ↑↓ move · Tab complete · Esc close · empty query: recent"
}
func (p *tuiPalette) PreferredSize() (int, int) {
	p.filter()
	width := 104
	if p.app != nil && p.app.vx != nil {
		cols, _ := p.app.vx.Window().Size()
		width = min(width, cols-4)
	}
	height, group := 6, "" // Border, query, dividers and footer.
	for _, command := range p.matches {
		if command.group != group {
			height++
			group = command.group
		}
		height++
		if width-2 < 94 && command.hint != "" {
			height++
		}
	}
	return 104, min(30, max(9, height))
}

func (a *vaxisTUIApp) openCommandPalette() {
	p := newTUIPalette("Command palette", nil)
	p.app, p.refresh = a, a.paletteCommands
	p.recents = func() []string { return a.paletteRecent }
	p.record = func(id string) {
		a.paletteRecent = append([]string{id}, slices.DeleteFunc(a.paletteRecent, func(old string) bool { return old == id })...)
		a.paletteRecent = a.paletteRecent[:min(20, len(a.paletteRecent))]
	}
	p.filter()
	a.overlay = p
}
