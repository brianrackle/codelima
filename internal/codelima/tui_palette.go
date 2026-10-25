package codelima

import (
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiPaletteCommand struct {
	label, hint string
	run         func() error
}

type tuiPalette struct {
	title    string
	input    *textinput.Model
	commands []tuiPaletteCommand
	matches  []tuiPaletteCommand
	index    int
	help     bool
}

func newTUIPalette(title string, commands []tuiPaletteCommand) *tuiPalette {
	p := &tuiPalette{title: title, input: textinput.New(), commands: commands}
	p.filter()
	return p
}

func tuiFuzzyScore(text, query string) int {
	text, query = strings.ToLower(text), strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0
	}
	if index := strings.Index(text, query); index >= 0 {
		return index
	}
	runes := []rune(text)
	position, score := 0, len(runes)
	for _, r := range query {
		index := slices.Index(runes[position:], r)
		if index < 0 {
			return -1
		}
		score += index
		position += index + 1
	}
	return score
}

func (p *tuiPalette) filter() {
	query := p.input.String()
	p.matches = p.matches[:0]
	for _, command := range p.commands {
		if tuiFuzzyScore(command.label+" "+command.hint, query) >= 0 {
			p.matches = append(p.matches, command)
		}
	}
	slices.SortStableFunc(p.matches, func(a, b tuiPaletteCommand) int {
		return tuiFuzzyScore(a.label+" "+a.hint, query) - tuiFuzzyScore(b.label+" "+b.hint, query)
	})
	p.index = min(p.index, max(0, len(p.matches)-1))
}

func (p *tuiPalette) Update(event vaxis.Event) (bool, error) {
	key, ok := event.(vaxis.Key)
	if !ok {
		p.input.Update(event)
		return false, nil
	}
	if key.EventType != vaxis.EventPaste && !tuiKeyActivates(key, false) {
		return false, nil
	}
	switch {
	case key.Matches(vaxis.KeyEsc) && tuiKeyActivates(key, true):
		return true, nil
	case key.Matches(vaxis.KeyUp) && key.EventType != vaxis.EventPaste:
		p.index = max(0, p.index-1)
	case key.Matches(vaxis.KeyDown) && key.EventType != vaxis.EventPaste:
		p.index = min(max(0, len(p.matches)-1), p.index+1)
	case key.Matches(vaxis.KeyTab) && tuiKeyActivates(key, true):
		if len(p.matches) > 0 {
			p.input.SetContent(p.matches[p.index].label)
			p.filter()
		}
	case key.Matches(vaxis.KeyEnter) && tuiKeyActivates(key, true):
		if len(p.matches) > 0 && !p.help && p.matches[p.index].run != nil {
			return true, p.matches[p.index].run()
		}
	default:
		p.input.Update(key)
		p.filter()
	}
	return false, nil
}

func (p *tuiPalette) Draw(win vaxis.Window, header, muted, _ vaxis.Style) {
	inner := border.All(win, muted)
	width, height := inner.Size()
	tuiLine(inner, 0, p.title, header)
	p.input.SetPrompt("> ")
	p.input.Draw(inner.New(0, 2, width, 1))
	start := max(0, p.index-max(1, height-5)+1)
	for i := start; i < len(p.matches) && i-start < height-5; i++ {
		style := muted
		if i == p.index {
			style.Attribute |= vaxis.AttrReverse
		}
		command := p.matches[i]
		tuiLine(inner, 4+i-start, command.label+"    "+command.hint, style)
	}
	if len(p.matches) == 0 {
		tuiLine(inner, 4, "No matches", muted)
	}
}

func (p *tuiPalette) FooterHint() string {
	return "Type to search · ↑↓ select · Enter run · Tab complete · Esc close"
}

func (a *vaxisTUIApp) openCommandPalette() {
	commands := []tuiPaletteCommand{}
	for i, label := range []string{"Go to nodes", "Go to templates", "Go to ports", "Go to activity", "Go to system"} {
		view := tuiView(i)
		commands = append(commands, tuiPaletteCommand{label: label, hint: string(rune('1' + i)), run: func() error { a.switchView(view); return nil }})
	}
	commands = append(commands,
		tuiPaletteCommand{label: "Edit settings", hint: "System · shell, colors, confirmations", run: func() error { a.switchView(tuiViewSystem); a.systemPanel = tuiSystemSettings; return nil }},
		tuiPaletteCommand{label: "Create node", hint: "n", run: a.openCreateNodeDialog},
		tuiPaletteCommand{label: "Open shell chooser", hint: "X", run: func() error { a.openShellChooser(); return nil }},
		tuiPaletteCommand{label: "Show keybindings", hint: "?", run: func() error { a.openKeyHelp(); return nil }},
	)
	for _, action := range [][3]string{
		{"update", "Update daemon, keep sessions", "System · u"},
		{"restart", "Restart daemon", "System · S · ends live shells"},
		{"log", "Open daemon log", "System · L"},
		{"diagnose", "Diagnose terminal freeze", "System · D · save local evidence"},
	} {
		commands = append(commands, tuiPaletteCommand{label: action[1], hint: action[2], run: func() error {
			a.switchView(tuiViewSystem)
			a.runSystemDaemonAction(action[0])
			return nil
		}})
	}
	for _, record := range a.fleet.records {
		id := record.Node.ID
		commands = append(commands, tuiPaletteCommand{label: "Node: " + record.Node.Slug, hint: record.Node.DirectoryPath, run: func() error {
			a.fleet.setFilter("")
			a.fleet.selectID(id)
			a.selectFleetNode()
			a.openNodeDetail(tuiDetailOverview)
			return nil
		}})
	}
	if a.view == tuiViewPorts {
		for _, action := range [][2]string{{"o", "Open selected port in browser"}, {"y", "Copy selected port address"}, {"p", "Pin selected port's bare localhost owner"}, {"/", "Filter ports"}, {"C", "Refresh ports"}} {
			commands = append(commands, tuiPaletteCommand{label: action[1], hint: "Ports · " + action[0], run: func() error {
				a.handlePortsKey(vaxis.Key{Keycode: []rune(action[0])[0], Text: action[0]})
				return nil
			}})
		}
	}
	a.overlay = newTUIPalette("Command palette", commands)
}

func (a *vaxisTUIApp) openKeyHelp() {
	commands := []tuiPaletteCommand{
		{label: "1–5", hint: "Nodes · Templates · Ports · Activity · System"},
		{label: "0", hint: "Toggle all nodes / launch directory scope"},
		{label: "↑↓ / j k", hint: "Select node; Enter opens detail; Esc returns"},
		{label: "/", hint: "Filter names and directories; status: template: dir: port:"},
		{label: "G / < > / =", hint: "Group directories / sort field / reverse sort"},
		{label: "Space / *", hint: "Mark node / mark every visible node; marks survive filtering"},
		{label: "n / c / d", hint: "Create / clone / delete node"},
		{label: "s", hint: "Start or stop selected/marked nodes"},
		{label: "r", hint: "Failed node: retry from its recorded failed bootstrap or validation step"},
		{label: "x / X", hint: "Open shell / shell chooser"},
		{label: "Tab / Shift-Tab", hint: "Cycle detail tabs or templates/environments"},
		{label: "+ − / Space", hint: "Metrics: 1m / 5m / 15m / 1h window; pause samples"},
		{label: "p / y", hint: "Metrics: sort processes by CPU or memory / copy selected PID"},
		{label: "[ ] / f / w / L", hint: "Logs: source / follow / wrap / severity"},
		{label: "/ / n N / g G / y / o", hint: "Logs: search / matches / top-end / copy line / PAGER"},
		{label: "N / e / v / y", hint: "Spec: new from current template / edit template / YAML pager / copy YAML path"},
		{label: "← → / Ctrl-↑↓ / Enter", hint: "Spec: focus panel / focus panel / open selected bootstrap command output"},
		{label: "F6", hint: "Embedded shell focus; every other key reaches the shell while focused"},
		{label: "[ ] / t / T / w", hint: "Shell tabs / new guest / new host / close (TUI focus only)"},
		{label: "F7", hint: "Search embedded terminal (TUI focus only)"},
		{label: ": / ?", hint: "Command palette / searchable key help"},
		{label: "u", hint: "System: update daemon, keep sessions"},
		{label: "S", hint: "System: restart daemon; ends live shell processes"},
		{label: "L", hint: "System: open daemon log"},
		{label: "D", hint: "System: diagnose freeze; save local evidence"},
		{label: "o / y / p", hint: "Ports: open HTTP / copy address / pin bare localhost"},
		{label: "Enter / / / C", hint: "Ports: go to node / filter / refresh"},
		{label: "PgUp / PgDn", hint: "Ports: scroll selected route details and compact By node list"},
		{label: "Tab / C / R", hint: "System: panel / check / Doctor repair"},
		{label: "q", hint: "Quit from a global view; return from node detail"},
	}
	p := newTUIPalette("Keyboard shortcuts · type to filter", commands)
	p.help = true
	a.overlay = p
}
