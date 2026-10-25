package codelima

import (
	"fmt"
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func templatePanel(win vaxis.Window, title string, style vaxis.Style, t tuiTheme) vaxis.Window {
	w, _ := win.Size()
	box := border.All(win, style)
	tuiLine(win.New(2, 0, max(0, w-4), 1), 0, " "+title+" ", t.text)
	return box
}

type tuiTemplateRow struct {
	kind  string
	field int
	text  string
	env   EnvironmentConfig
}

func (e *tuiTemplateEditor) formRows() ([]tuiTemplateRow, int) {
	rows := []tuiTemplateRow{}
	focused := 0
	for i := 0; i < 6; i++ {
		if e.dialog.FieldIndex == i {
			focused = len(rows)
		}
		rows = append(rows, tuiTemplateRow{kind: "field", field: i}, tuiTemplateRow{})
	}
	rows = append(rows, tuiTemplateRow{kind: "heading", text: e.bindingHints("Environments · {dialog.toggle} toggle · {form.reorder_up form.reorder_down} order")})
	envs := e.environmentRows()
	if len(envs) == 0 {
		if e.dialog.FieldIndex == 6 {
			focused = len(rows)
		}
		rows = append(rows, tuiTemplateRow{kind: "empty", text: "No reusable environments available"})
	}
	for _, env := range envs {
		if e.dialog.FieldIndex == 6 && env.Slug == e.environment {
			focused = len(rows)
		}
		rows = append(rows, tuiTemplateRow{kind: "environment", field: 6, env: env})
	}
	rows = append(rows, tuiTemplateRow{})
	if e.dialog.FieldIndex == 7 {
		focused = len(rows)
	}
	rows = append(rows, tuiTemplateRow{kind: "bootstrap", field: 7, text: e.bindingHints("Bootstrap commands · {dialog.accept} opens the multiline editor")})
	commands := e.commands()
	if len(commands) == 0 {
		rows = append(rows, tuiTemplateRow{kind: "empty", field: 7, text: "(none) · commands run after environments, as root"})
	}
	for i, command := range commands {
		rows = append(rows, tuiTemplateRow{kind: "command", field: 7, text: fmt.Sprintf("%d │ %s", i+1, templateCommandLine(command))})
	}
	return rows, focused
}
func templateCommandLine(command string) string {
	return plainLogText(strings.ReplaceAll(normalizeTUITerminalPasteText(command), "\n", " ↵ "))
}

func (e *tuiTemplateEditor) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := e.app.managerTheme()
	tuiFill(win, t.text)
	w, h := win.Size()
	if w < 20 || h < 8 {
		return
	}
	title := "‹ Templates / Edit " + e.original.Slug
	if e.creating {
		title = "‹ Templates / New template"
	}
	if e.dialog.dirty() {
		title += "  • unsaved"
	}
	tuiLine(win.New(2, 0, w-4, 1), 0, title, t.mint)
	kind := "custom"
	if _, ok := builtInTemplateDefaults(e.original.Slug, e.app.managerSettings()); ok && !e.creating {
		kind = e.bindingHints("built-in · {form.restore} restore defaults")
	}
	tuiLine(win.New(2, 1, w-4, 1), 0, kind+" · "+e.impactSummary(), t.dim)
	e.controls = nil
	body := win.New(1, 3, w-2, h-6)
	switch {
	case e.preview:
		e.drawFullPreview(body, t)
	case w >= 118:
		left := min(82, (w-4)*3/5)
		e.drawForm(body.New(0, 0, left, h-6), t)
		e.drawSide(body.New(left+2, 0, w-left-4, h-6), t)
	default:
		e.drawForm(body, t)
	}
	status, style := e.bindingHints("{dialog.submit} save · {form.preview} resolved preview · {dialog.cancel} cancel"), t.mint
	if _, err := e.draft(); err != nil {
		status, style = err.Error(), t.coral
	}
	if e.notice != "" {
		status, style = e.notice, t.mint
	}
	if e.dialog.Error != "" {
		status, style = e.dialog.Error, t.coral
	}
	if e.pending {
		status, style = "Saving template…", t.amber
	}
	if e.dialog.confirmDiscard {
		status, style = e.bindingHints("Discard unsaved changes? {discard.confirm} discard · {discard.cancel} keep editing"), t.coral
	}
	tuiLine(win.New(2, h-2, w-4, 1), 0, status, style)
	tuiLine(win.New(2, h-1, w-4, 1), 0, e.cliPreview(), t.dim)
}
func (e *tuiTemplateEditor) drawForm(win vaxis.Window, t tuiTheme) {
	body := templatePanel(win, "Template", t.mint, t)
	w, h := body.Size()
	if w < 10 || h < 2 {
		return
	}
	rows, focused := e.formRows()
	visible := max(1, h-2)
	if focused < e.scroll {
		e.scroll = focused
	}
	if focused >= e.scroll+visible {
		e.scroll = focused - visible + 1
	}
	e.scroll = max(0, min(e.scroll, max(0, len(rows)-visible)))
	x, y := body.Origin()
	for i, row := range rows[e.scroll:min(len(rows), e.scroll+visible)] {
		r := i + 1
		line := body.New(1, r, w-2, 1)
		switch row.kind {
		case "field":
			field := &e.dialog.Fields[row.field]
			style := t.muted
			if e.dialog.FieldIndex == row.field {
				style = t.mint
			}
			tuiLine(line.New(0, 0, 14, 1), 0, field.Label, style)
			inputWidth := min(27, max(8, w-20))
			inputX := 16
			if row.field >= 3 {
				tuiLine(body.New(15, r, 1, 1), 0, "‹", t.dim)
				inputX = 17
				inputWidth--
			}
			input := body.New(inputX, r, inputWidth, 1)
			field.Input.Content = t.text
			field.Input.Prompt = t.dim
			field.Input.HideCursor = e.dialog.FieldIndex != row.field || e.pending || (row.field == 0 && !e.creating && e.original.Slug == DefaultConfigurationSlug)
			field.Input.Draw(input)
			e.controls = append(e.controls, tuiTemplateControl{rect: tuiRect{col: x + 1, row: y + r, width: w - 2, height: 1}, field: row.field})
			if row.field >= 3 {
				// Put step controls before the encompassing field hit target.
				e.controls = e.controls[:len(e.controls)-1]
				tuiLine(body.New(inputX+inputWidth, r, 1, 1), 0, "›", t.dim)
				e.controls = append(e.controls, tuiTemplateControl{rect: tuiRect{col: x + 15, row: y + r, width: 1, height: 1}, field: row.field, step: -1}, tuiTemplateControl{rect: tuiRect{col: x + inputX + inputWidth, row: y + r, width: 1, height: 1}, field: row.field, step: 1}, tuiTemplateControl{rect: tuiRect{col: x + 1, row: y + r, width: w - 2, height: 1}, field: row.field})
			}
			hintX := inputX + inputWidth + 2
			tuiLine(body.New(hintX, r, max(0, w-hintX-1), 1), 0, e.fieldHint(row.field), t.dim)
		case "environment":
			env := row.env
			index := slices.Index(e.environments(), env.Slug)
			mark, number := "[ ]", " "
			if index >= 0 {
				mark, number = "[x]", fmt.Sprint(index+1)
			}
			style := t.muted
			prefix := " "
			if e.dialog.FieldIndex == 6 && env.Slug == e.environment {
				style = t.selected
				prefix = "▸"
				tuiFill(line, style)
			}
			kind := "custom"
			for _, builtin := range builtInEnvironmentConfigs() {
				if builtin.Slug == env.Slug {
					kind = "built-in"
				}
			}
			if env.ID == "" {
				kind = "unavailable"
				style = t.coral
			}
			tuiLine(line, 0, fmt.Sprintf("%s %s %2s  %-18s %2d cmds · %s", prefix, mark, number, env.Slug, len(env.BootstrapCommands), kind), style)
			e.controls = append(e.controls, tuiTemplateControl{rect: tuiRect{col: x + 1, row: y + r, width: w - 2, height: 1}, field: 6, environment: env.Slug})
		case "heading":
			tuiLine(line, 0, row.text, t.text)
		case "bootstrap", "command":
			style := t.muted
			if e.dialog.FieldIndex == 7 {
				style = t.mint
			}
			tuiLine(line, 0, row.text, style)
			e.controls = append(e.controls, tuiTemplateControl{rect: tuiRect{col: x + 1, row: y + r, width: w - 2, height: 1}, field: 7})
		case "empty":
			tuiLine(line, 0, row.text, t.dim)
		}
	}
	if len(rows) > visible {
		tuiLine(body.New(1, h-1, w-2, 1), 0, e.bindingHintsFormat("Rows %d–%d/%d · focused field follows {dialog.next dialog.up dialog.down}", e.scroll+1, min(len(rows), e.scroll+visible), len(rows)), t.dim)
	}
}
func (e *tuiTemplateEditor) fieldHint(index int) string {
	switch index {
	case 0:
		if !e.creating && e.original.Slug == DefaultConfigurationSlug {
			return "default · name protected"
		}
		return "lowercase, unique"
	case 1:
		return e.bindingHints("{dialog.accept} list; or path/URL")
	case 2:
		return e.bindingHints("{dialog.accept} profiles")
	case 3:
		if e.app.host.Capacity.CPUs > 0 {
			return e.bindingHintsFormat("host %d · {dialog.left dialog.right} step", e.app.host.Capacity.CPUs)
		}
		return e.bindingHints("{dialog.left dialog.right} step; or type")
	case 4:
		if bytes := e.app.host.Capacity.MemoryBytes; bytes != nil {
			return fmt.Sprintf("host %.1f GiB; type 20G", float64(*bytes)/(1<<30))
		}
		return e.bindingHints("{dialog.left dialog.right} step; or type 20G")
	case 5:
		return "thin; grows as used"
	}
	return ""
}
func (e *tuiTemplateEditor) usedNodes() []string {
	var names []string
	if !e.creating {
		for _, n := range e.data.nodes {
			if n.ConfigurationID == e.original.ID {
				names = append(names, n.Slug)
			}
		}
	}
	slices.Sort(names)
	return names
}
func (e *tuiTemplateEditor) impactSummary() string {
	if !e.loaded {
		return "loading usage…"
	}
	return countLabel(len(e.usedNodes()), "existing node keeps frozen values", "existing nodes keep frozen values")
}
func (e *tuiTemplateEditor) changeLines() []string {
	var out []string
	before := map[string]string{"slug": e.original.Slug, "image": e.original.Image, "agent": e.original.AgentProfileName, "vcpus": fmt.Sprint(e.original.VCPUs), "memory": formatTemplateSize(e.original.MemoryMiB), "disk": formatTemplateSize(e.original.DiskMiB), "environments": strings.Join(e.original.Environments, ",")}
	for _, field := range e.dialog.Fields[:7] {
		old := before[field.Key]
		now := e.value(field.Key)
		if old != now {
			out = append(out, "− "+field.Label+"  "+old, "+ "+field.Label+"  "+now)
		}
	}
	if !slices.Equal(e.original.BootstrapCommands, e.commands()) {
		out = append(out, fmt.Sprintf("− bootstrap  %d commands", len(e.original.BootstrapCommands)), fmt.Sprintf("+ bootstrap  %d commands", len(e.commands())))
	}
	if len(out) == 0 {
		out = []string{"No changes"}
	}
	return out
}
func (e *tuiTemplateEditor) previewLines() []string {
	if !e.loaded {
		return []string{"Loading definitions…"}
	}
	steps, err := e.previewSteps()
	if err != nil {
		return []string{err.Error()}
	}
	var lines []string
	for i, s := range steps {
		label := s.SourceName
		if s.SourceKind == "template" {
			label = "direct"
		}
		if s.Kind == "validation" {
			label += " validation"
		}
		lines = append(lines, fmt.Sprintf("%d  %s · %s", i+1, label, templateCommandLine(s.Command)))
	}
	if len(lines) == 0 {
		lines = []string{"No commands"}
	}
	return lines
}
func (e *tuiTemplateEditor) drawSide(win vaxis.Window, t tuiTheme) {
	w, h := win.Size()
	previewHeight := max(5, h/2)
	impactHeight := min(7, max(4, h-previewHeight-4))
	panels := []struct {
		y, height int
		title     string
		lines     []string
	}{
		{0, previewHeight, "New nodes will run", e.previewLines()},
		{previewHeight + 1, impactHeight, "When you save", append([]string{"New nodes receive these definitions.", e.impactSummary()}, e.usedNodes()...)},
		{previewHeight + impactHeight + 2, max(3, h-previewHeight-impactHeight-2), "Changes", e.changeLines()},
	}
	for _, p := range panels {
		box := templatePanel(win.New(0, p.y, w, p.height), p.title, t.border, t)
		for i, line := range p.lines {
			if i >= p.height-3 {
				break
			}
			style := t.muted
			if strings.HasPrefix(line, "−") {
				style = t.coral
			}
			if strings.HasPrefix(line, "+") {
				style = t.mint
			}
			tuiLine(box.New(1, i+1, w-4, 1), 0, line, style)
		}
	}
}
func (e *tuiTemplateEditor) drawFullPreview(win vaxis.Window, t tuiTheme) {
	w, h := win.Size()
	box := templatePanel(win, e.bindingHints("Preview · current definitions · {form.preview} returns to form"), t.mint, t)
	lines := append([]string{"New nodes will run"}, e.previewLines()...)
	lines = append(lines, "", "When you save", e.impactSummary())
	lines = append(lines, e.usedNodes()...)
	lines = append(lines, "", "Changes")
	lines = append(lines, e.changeLines()...)
	var wrapped []string
	for _, line := range lines {
		for _, row := range commandVisualRows(vaxis.Characters(line), max(1, w-4)) {
			wrapped = append(wrapped, row.text)
		}
	}
	lines = wrapped
	visible := max(1, h-3)
	e.previewScroll = min(e.previewScroll, max(0, len(lines)-visible))
	for i, line := range lines[e.previewScroll:min(len(lines), e.previewScroll+visible)] {
		tuiLine(box.New(1, i+1, w-4, 1), 0, line, t.text)
	}
}
func (e *tuiTemplateEditor) cliPreview() string {
	c, err := e.draft()
	if err != nil {
		return "cli · complete valid fields to preview the command"
	}
	action := "update " + shellQuote(e.original.Slug)
	if e.creating {
		action = "create"
	}
	text := "cli · codelima configuration " + action + " --slug " + shellQuote(c.Slug) + " --image " + shellQuote(c.Image) + " --agent-profile " + shellQuote(c.AgentProfileName) + fmt.Sprintf(" --vcpus %d --memory %s --disk %s", c.VCPUs, shellQuote(formatTemplateSize(c.MemoryMiB)), shellQuote(formatTemplateSize(c.DiskMiB)))
	if len(c.Environments) == 0 {
		text += " --clear-environments"
	}
	for _, name := range c.Environments {
		text += " --environment " + shellQuote(name)
	}
	if len(c.BootstrapCommands) == 0 {
		text += " --clear-bootstrap-commands"
	}
	for _, command := range c.BootstrapCommands {
		text += " --bootstrap-command " + shellQuote(command)
	}
	return text
}
