package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

type tuiCreateDirectoryEvent struct {
	form     *tuiCreateNodeForm
	selector *tuiSelector
	path     string
	names    []string
	err      error
}

func (f *tuiCreateNodeForm) browseDirectory(path string) {
	selector := newTUISelector("Browse directory", []string{path, "Enter opens a folder; choose ‘Use this directory’ to select it."}, nil, nil, false, nil)
	selector.Parent, selector.EmptyText = f, "Loading directories…"
	f.app.overlay = selector
	load := func() tuiCreateDirectoryEvent {
		e := tuiCreateDirectoryEvent{form: f, selector: selector}
		e.path, e.err = canonicalPath(path)
		if e.err != nil {
			return e
		}
		entries, err := os.ReadDir(e.path)
		e.err = err
		for _, entry := range entries {
			isDir := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				info, err := os.Stat(filepath.Join(e.path, entry.Name()))
				isDir = err == nil && info.IsDir()
			}
			if isDir {
				e.names = append(e.names, entry.Name())
			}
		}
		return e
	}
	if f.app.postEvent == nil {
		f.app.applyCreateDirectory(load())
		return
	}
	go func() { f.app.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyCreateDirectory(e tuiCreateDirectoryEvent) {
	if a.overlay != e.selector || !e.form.visible() {
		return
	}
	s := e.selector
	s.Description[0] = e.path
	s.Options = []tuiSelectorOption{{Value: "use", Label: "✓ Use this directory"}, {Value: "parent", Label: "..  Parent directory"}}
	if e.err != nil {
		s.Description = append(s.Description, e.err.Error())
		s.Options = s.Options[1:]
	}
	for _, name := range e.names {
		s.Options = append(s.Options, tuiSelectorOption{Value: "dir:" + name, Label: name + "/"})
	}
	s.OnSubmit = func(values []string) error {
		if len(values) == 0 {
			return nil
		}
		switch values[0] {
		case "use":
			e.form.dialog.SetFieldValue("directory", e.path)
			e.form.checkPath()
		case "parent":
			e.form.browseDirectory(filepath.Dir(e.path))
		default:
			e.form.browseDirectory(filepath.Join(e.path, strings.TrimPrefix(values[0], "dir:")))
		}
		return nil
	}
}

type tuiTemplatePicker struct {
	form          *tuiCreateNodeForm
	filter        *textinput.Model
	selected      string
	index, scroll int
	details       bool
	rows          []tuiRect
	rowIDs        []string
}

func (f *tuiCreateNodeForm) openTemplatePicker() {
	f.app.overlay = &tuiTemplatePicker{form: f, filter: textinput.New(), selected: f.value("configuration")}
}

func (p *tuiTemplatePicker) ParentOverlay() tuiOverlay { return p.form }
func (p *tuiTemplatePicker) PreferredSize() (int, int) { return 102, 20 }
func (p *tuiTemplatePicker) FooterHint() string {
	return "Type filter · ↑↓ move · Enter choose · → details · Esc back"
}

func (p *tuiTemplatePicker) choices() []Configuration {
	var rows []Configuration
	query := strings.ToLower(strings.TrimSpace(p.filter.String()))
	for _, c := range p.form.templates {
		if strings.Contains(strings.ToLower(c.Slug+" "+strings.Join(c.Environments, " ")), query) {
			rows = append(rows, c)
		}
	}
	if p.selected == "+" {
		p.index = len(rows)
		return rows
	}
	for index, c := range rows {
		if c.ID == p.selected || c.Slug == p.selected {
			p.index = index
			p.selected = c.ID
			return rows
		}
	}
	p.index = 0
	if len(rows) > 0 {
		p.selected = rows[0].ID
	} else {
		p.selected = "+"
	}
	return rows
}

func (p *tuiTemplatePicker) Update(event vaxis.Event) (bool, error) {
	rows := p.choices()
	if key, ok := event.(vaxis.Key); ok && key.EventType != vaxis.EventPaste {
		if !tuiKeyActivates(key, false) {
			return false, nil
		}
		switch {
		case key.Matches(vaxis.KeyEsc):
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			if p.details {
				p.details = false
				return false, nil
			}
			return true, nil
		case key.Matches(vaxis.KeyDown), key.Matches(vaxis.KeyTab):
			p.index = (p.index + 1) % (len(rows) + 1)
		case key.Matches(vaxis.KeyUp):
			p.index = (p.index + len(rows)) % (len(rows) + 1)
		case key.Matches(vaxis.KeyRight):
			p.details = true
			return false, nil
		case key.Matches(vaxis.KeyEnter):
			if !tuiKeyActivates(key, true) {
				return false, nil
			}
			if p.index == len(rows) {
				p.openNewTemplate()
				return false, nil
			}
			p.form.dialog.SetFieldValue("configuration", rows[p.index].Slug)
			return true, nil
		default:
			p.filter.Update(event)
			p.details = false
			return false, nil
		}
		if p.index == len(rows) {
			p.selected = "+"
		} else {
			p.selected = rows[p.index].ID
		}
		return false, nil
	}
	if mouse, ok := event.(vaxis.Mouse); ok && mouse.EventType == vaxis.EventPress && mouse.Button == vaxis.MouseLeftButton {
		for i, rect := range p.rows {
			if rect.contains(mouse.Col, mouse.Row) {
				if i >= len(p.rowIDs) {
					return false, nil
				}
				id := p.rowIDs[i]
				if id == "+" {
					p.openNewTemplate()
					return false, nil
				}
				for _, c := range rows {
					if c.ID == id {
						p.form.dialog.SetFieldValue("configuration", c.Slug)
						return true, nil
					}
				}
				return false, nil
			}
		}
		return false, nil
	}
	p.filter.Update(event)
	return false, nil
}

func (p *tuiTemplatePicker) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := p.form.app.managerTheme().modal()
	tuiFill(win, t.raised)
	body := border.All(win, t.mint)
	w, h := body.Size()
	if w < 10 || h < 6 {
		return
	}
	p.filter.SetPrompt("› ")
	if p.filter.String() == "" {
		p.filter.SetPrompt("› type to filter ")
	}
	p.filter.Prompt, p.filter.Content = t.dim, t.text
	p.filter.Draw(body.New(1, 0, w-2, 1))
	rows := p.choices()
	if p.details && p.index < len(rows) {
		c := rows[p.index]
		lines := []string{c.Slug, "Image: " + c.Image, "Agent: " + c.AgentProfileName, "Environments: " + strings.Join(c.Environments, ", "), "Bootstrap commands:"}
		lines = append(lines, c.BootstrapCommands...)
		for i, text := range lines[:min(len(lines), h-3)] {
			tuiLine(body.New(1, i+2, w-2, 1), 0, text, t.text)
		}
		tuiLine(body, h-1, "Esc back to choices · Enter selects", t.dim)
		return
	}
	envWidth := max(0, w-66)
	tuiLine(body.New(2, 2, w-4, 1), 0, fmt.Sprintf("%-18s %4s %8s %8s %-*s %s", "NAME", "vCPU", "MEM", "DISK", envWidth, "ENVIRONMENTS", "MEMORY FIT"), t.dim)
	visible := max(1, h-7)
	if p.index < p.scroll {
		p.scroll = p.index
	}
	if p.index >= p.scroll+visible {
		p.scroll = p.index - visible + 1
	}
	p.scroll = max(0, min(p.scroll, len(rows)+1-visible))
	p.rows = make([]tuiRect, len(rows)+1)
	p.rowIDs = make([]string, len(rows)+1)
	x, y := body.Origin()
	for i := p.scroll; i < min(len(rows)+1, p.scroll+visible); i++ {
		row := i - p.scroll + 3
		style := t.text
		if i == p.index {
			style = t.selected
		}
		rowWin := body.New(1, row, w-2, 1)
		tuiFill(rowWin, style)
		p.rows[i] = tuiRect{col: x + 1, row: y + row, width: w - 2, height: 1}
		if i == len(rows) {
			p.rowIDs[i] = "+"
			tuiLine(rowWin, 0, "+ new template…", style)
			continue
		}
		c := rows[i]
		p.rowIDs[i] = c.ID
		name := c.Slug
		if c.Slug == DefaultConfigurationSlug {
			name += " ★"
		}
		if modifiedBuiltInTemplate(c, p.form.app.service.cfg) {
			name += " •"
		}
		fit := createHostFit(p.form.app.host, c)[1]
		fitText := strings.TrimPrefix(fit.text, "memory  ")
		if strings.Contains(fitText, "unavailable") {
			fitText = "— unavailable"
		}
		if index := strings.Index(fitText, " · "); index >= 0 {
			fitText = fitText[index+3:]
		}
		nameStyle, fitStyle := style, style
		nameStyle.Foreground = t.violet.Foreground
		if fit.shortfall {
			fitStyle.Foreground = t.coral.Foreground
		} else if fit.warning {
			fitStyle.Foreground = t.amber.Foreground
		}
		tuiLine(rowWin.New(0, 0, 18, 1), 0, name, nameStyle)
		tuiLine(rowWin.New(19, 0, 4, 1), 0, strconv.Itoa(int(c.VCPUs)), style)
		tuiLine(rowWin.New(24, 0, 8, 1), 0, createMiB(c.MemoryMiB), style)
		tuiLine(rowWin.New(33, 0, 8, 1), 0, createMiB(c.DiskMiB), style)
		tuiLine(rowWin.New(42, 0, envWidth, 1), 0, strings.Join(c.Environments, ", "), style)
		tuiLine(rowWin.New(43+envWidth, 0, max(0, w-45-envWidth), 1), 0, fitText, fitStyle)
	}
	if p.index < len(rows) {
		c := rows[p.index]
		tuiLine(body.New(1, h-3, w-2, 1), 0, c.Slug+" · "+c.Image+" · agent "+c.AgentProfileName, t.dim)
	}
	tuiLine(body.New(1, h-1, w-2, 1), 0, "Capacity warnings never block selection · Enter choose · Esc back", t.dim)
}

type tuiTemplateCreateEditor struct {
	dialog  *tuiDialog
	picker  *tuiTemplatePicker
	pending bool
}
type tuiTemplateCreatedEvent struct {
	editor   *tuiTemplateCreateEditor
	template Configuration
	err      error
}

func (e *tuiTemplateCreateEditor) ParentOverlay() tuiOverlay { return e.picker }
func (e *tuiTemplateCreateEditor) Draw(win vaxis.Window, header, muted, problem vaxis.Style) {
	e.dialog.Draw(win, header, muted, problem)
}
func (e *tuiTemplateCreateEditor) FooterHint() string { return e.dialog.FooterHint() }
func (e *tuiTemplateCreateEditor) Update(event vaxis.Event) (bool, error) {
	if e.pending {
		return false, nil
	}
	done, err := e.dialog.Update(event)
	return done && !e.pending, err
}

func (p *tuiTemplatePicker) openNewTemplate() {
	c, ok := p.form.template()
	if !ok && len(p.form.templates) > 0 {
		c = p.form.templates[0]
	}
	e := &tuiTemplateCreateEditor{picker: p}
	e.dialog = newTUIDialog("New template", "Save", []string{"Changes apply to future nodes only. Comma-separated environments run in order."}, []tuiDialogField{
		newTUIInputField("slug", "Name", "", true),
		newTUIInputField("image", "Image", c.Image, true),
		newTUIInputField("agent", "Agent", c.AgentProfileName, true),
		newTUIInputField("vcpus", "vCPU", strconv.Itoa(int(c.VCPUs)), true),
		newTUIInputField("memory", "Memory MiB", strconv.FormatUint(uint64(c.MemoryMiB), 10), true),
		newTUIInputField("disk", "Disk MiB", strconv.FormatUint(uint64(c.DiskMiB), 10), true),
		newTUIInputField("environments", "Environments", strings.Join(c.Environments, ","), false),
	}, func(values map[string]string) error {
		cpu, err := strconv.ParseUint(values["vcpus"], 10, 8)
		if err != nil || cpu == 0 {
			return fmt.Errorf("vCPU must be a positive integer")
		}
		memory, err := parseSizeMiB(values["memory"])
		if err != nil {
			return err
		}
		disk, err := parseSizeMiB(values["disk"])
		if err != nil {
			return err
		}
		image, agent, vcpus := values["image"], values["agent"], uint8(cpu)
		input := ConfigurationCreateInput{Slug: values["slug"], Image: &image, AgentProfile: &agent, VCPUs: &vcpus, MemoryMiB: &memory, DiskMiB: &disk, Environments: parseCommaSeparatedValues(values["environments"]), BootstrapCommands: slices.Clone(c.BootstrapCommands)}
		e.pending = true
		ctx, cancel := p.form.workContext()
		service := p.form.app.service
		save := func() tuiTemplateCreatedEvent {
			defer cancel()
			template, err := service.ConfigurationCreate(ctx, input)
			return tuiTemplateCreatedEvent{editor: e, template: template, err: err}
		}
		if p.form.app.postEvent == nil {
			event := save()
			p.form.app.applyTemplateCreated(event)
			return event.err
		} else {
			go func() { p.form.app.postCompletion(save()) }()
		}
		return nil
	})
	p.form.app.overlay = e
}

func (a *vaxisTUIApp) applyTemplateCreated(event tuiTemplateCreatedEvent) {
	e := event.editor
	if a.overlay != e {
		return
	}
	e.pending = false
	if event.err != nil {
		e.dialog.Error = event.err.Error()
		return
	}
	p := e.picker
	p.form.dataSerial++
	p.form.templates = append(p.form.templates, event.template)
	p.selected, p.details = event.template.ID, false
	p.filter.SetContent("")
	a.overlay = p
}

func modifiedBuiltInTemplate(c Configuration, cfg Config) bool {
	for _, spec := range builtInConfigurationSpecs() {
		if c.Slug == spec.Slug {
			return c.VCPUs != spec.VCPUs || c.MemoryMiB != spec.MemoryMiB || c.DiskMiB != spec.DiskMiB ||
				c.Image != cfg.DefaultImage || c.AgentProfileName != cfg.DefaultAgentProfile ||
				!slices.Equal(c.Environments, defaultConfigurationEnvironmentSlugs()) || len(c.BootstrapCommands) != 0
		}
	}
	return false
}
