package codelima

import (
	"fmt"
	"slices"
	"strings"

	"go.rockorager.dev/vaxis"
)

type tuiRecipeColumn struct {
	label string
	width int
}
type tuiRecipeDetailRow struct {
	text    string
	command int
	style   vaxis.Style
}

func (a *vaxisTUIApp) drawTemplates(win vaxis.Window, t tuiTheme) {
	w, h := win.Size()
	s := &a.recipes
	s.controls = nil
	s.listRect, s.detailRect = tuiRect{}, tuiRect{}
	labels := []string{fmt.Sprintf("Configurations %d", len(a.managerData.configurations)), fmt.Sprintf("Environments %d", len(a.managerData.environments))}
	x := 1
	for i, label := range labels {
		style := t.muted
		if (i == 1) == a.environmentsView {
			style = t.selected
		}
		width := len(label) + 2
		r := win.New(x, 2, width, 1)
		tuiFill(r, style)
		tuiLine(r, 0, " "+label, style)
		rx, ry := r.Origin()
		s.kindRects[i] = tuiRect{col: rx, row: ry, width: width, height: 1}
		x += width + 1
	}
	hint := "Recipes freeze when a node is created"
	if a.environmentsView {
		hint = "Ordered scripts · first start · root"
	}
	tuiLine(win.New(x+1, 2, max(0, w-x-2), 1), 0, hint, t.dim)
	body := win.New(1, 4, w-2, h-8)
	switch {
	case a.managerData.loading && len(a.managerData.configurations) == 0 && len(a.managerData.environments) == 0:
		tuiLine(body, 1, " Loading recipes…", t.dim)
	case w >= 130:
		left := min(94, w-42)
		if a.environmentsView {
			left = 56
		}
		a.drawRecipeList(body.New(0, 0, left, h-8), t)
		a.drawRecipeDetails(body.New(left+2, 0, w-left-4, h-8), t)
	case s.details:
		a.drawRecipeDetails(body, t)
	default:
		a.drawRecipeList(body, t)
	}
	note := "Existing nodes keep their frozen values · d names any node preventing deletion"
	if a.environmentsView {
		note = "Deleting an environment is refused while a template uses it"
	}
	if a.managerData.err != nil {
		note = "Recipe data unavailable: " + a.managerData.err.Error()
	}
	tuiLine(win.New(2, h-3, w-4, 1), 0, note, t.dim)
	a.drawRecipeActions(win.New(1, h-2, w-2, 1), t)
}

func (a *vaxisTUIApp) drawRecipeList(win vaxis.Window, t tuiTheme) {
	s := &a.recipes
	title := "Configurations · ★ default · • modified built-in"
	kind, count, index := 0, len(a.managerData.configurations), a.templateIndex
	if a.environmentsView {
		title = "Environments · • modified built-in"
		kind, count, index = 1, len(a.managerData.environments), a.environmentIndex
	}
	style := t.mint
	if s.details {
		style = t.border
	}
	body := templatePanel(win, title, style, t)
	w, h := body.Size()
	x, y := body.Origin()
	s.listRect = tuiRect{col: x, row: y, width: w, height: h}
	columns := []tuiRecipeColumn{{"NAME", 16}, {"KIND", 10}, {"CPU", 4}, {"MEM", 6}, {"DISK", 6}}
	if w-56 >= 16 {
		columns = append(columns, tuiRecipeColumn{"ENVIRONMENTS", w - 56})
	}
	columns = append(columns, tuiRecipeColumn{"NODES", 5})
	if a.environmentsView {
		columns = []tuiRecipeColumn{{"NAME", 20}, {"KIND", 11}, {"CMDS", 5}, {"USED BY", max(1, w-42)}}
	}
	col := 2
	for _, c := range columns {
		tuiLine(body.New(col, 0, c.width, 1), 0, c.label, t.dim)
		col += c.width + 1
	}
	visible := max(1, h-4)
	scroll := &s.listScroll[kind]
	if index < *scroll {
		*scroll = index
	}
	if index >= *scroll+visible {
		*scroll = index - visible + 1
	}
	*scroll = max(0, min(*scroll, max(0, count-visible)))
	for i := *scroll; i < min(count, *scroll+visible); i++ {
		var values map[string]string
		if a.environmentsView {
			e := a.managerData.environments[i]
			used := a.environmentTemplates(e.Slug)
			usage := fmt.Sprintf("%d templates", len(used))
			if len(used) == 1 {
				usage = used[0].Slug
			}
			if a.managerData.err != nil {
				usage = "unavailable"
			}
			values = map[string]string{"NAME": e.Slug, "KIND": environmentKind(e), "CMDS": fmt.Sprint(len(e.BootstrapCommands)), "USED BY": usage}
		} else {
			c := a.managerData.configurations[i]
			name, kind := c.Slug, "custom"
			if c.Slug == DefaultConfigurationSlug {
				name += " ★"
			}
			if _, ok := builtInTemplateDefaults(c.Slug, a.managerSettings()); ok {
				kind = "built-in"
				if modifiedBuiltInTemplate(c, a.managerSettings()) {
					name += " •"
				}
			}
			envs := coalesce(strings.Join(c.Environments, ", "), "none")
			if len(c.BootstrapCommands) > 0 {
				envs += fmt.Sprintf(" + %d cmds", len(c.BootstrapCommands))
			}
			values = map[string]string{"NAME": name, "KIND": kind, "CPU": fmt.Sprint(c.VCPUs), "MEM": compactRecipeSize(c.MemoryMiB), "DISK": compactRecipeSize(c.DiskMiB), "ENVIRONMENTS": envs, "NODES": fmt.Sprint(len(a.templateNodes(c.ID)))}
			if a.managerData.err != nil {
				values["NODES"] = "—"
			}
		}
		row := body.New(0, i-*scroll+2, w, 1)
		style := t.text
		if i == index {
			style = t.selected
		}
		tuiFill(row, style)
		if i == index {
			tuiLine(row.New(0, 0, 1, 1), 0, "▸", style)
		}
		col := 2
		for _, c := range columns {
			cell := style
			if i != index && c.label == "NAME" {
				cell = t.violet
			}
			if i != index && c.label == "KIND" {
				cell = t.dim
			}
			tuiLine(row.New(col, 0, c.width, 1), 0, values[c.label], cell)
			col += c.width + 1
		}
		rx, ry := row.Origin()
		s.controls = append(s.controls, tuiRecipeControl{tuiRect{col: rx, row: ry, width: w, height: 1}, "select", i})
	}
	if count == 0 {
		tuiLine(body.New(1, 2, w-2, 1), 0, "No recipes · n creates one", t.dim)
	}
	notes := []string{"ON THIS HOST", hostRecipeHint(a), "Templates larger than the host remain allowed."}
	if a.environmentsView {
		notes = []string{"Built-ins follow shipped seed rev " + seedRevision + ".", "Edited built-ins keep their values until restored.", "ORDER IN A NODE", "Environments in template order, then direct commands.", "The first failing command stops startup."}
	}
	for i, line := range notes {
		row := count + 4 + i
		if row < h-2 {
			tuiLine(body.New(1, row, w-2, 1), 0, line, t.dim)
		}
	}
	position := "0 recipes"
	if count > 0 {
		position = fmt.Sprintf("%d–%d/%d", *scroll+1, min(count, *scroll+visible), count)
	}
	tuiLine(body.New(1, h-1, w-2, 1), 0, position+" · → details · PgUp/PgDn page", t.dim)
}
func compactRecipeSize(v uint32) string {
	if v%1024 == 0 {
		return fmt.Sprintf("%dG", v/1024)
	}
	return fmt.Sprintf("%dM", v)
}
func hostRecipeHint(a *vaxisTUIApp) string {
	text := "Host capacity unavailable"
	if a.host.Capacity.CPUs > 0 {
		text = fmt.Sprintf("%d vCPU", a.host.Capacity.CPUs)
	}
	if b := a.host.Capacity.MemoryBytes; b != nil {
		text += fmt.Sprintf(" · %.1f GiB RAM", float64(*b)/(1<<30))
	}
	return text
}

func (a *vaxisTUIApp) recipeDetailRows(t tuiTheme) (string, []tuiRecipeDetailRow) {
	var rows []tuiRecipeDetailRow
	add := func(text string, style vaxis.Style) { rows = append(rows, tuiRecipeDetailRow{text, -1, style}) }
	if a.environmentsView {
		if a.environmentIndex >= len(a.managerData.environments) {
			return "Environment", rows
		}
		e := a.managerData.environments[a.environmentIndex]
		for i, command := range e.BootstrapCommands {
			for j, line := range strings.Split(normalizeTUITerminalPasteText(command), "\n") {
				prefix := "    │ "
				if j == 0 {
					prefix = fmt.Sprintf("%3d │ ", i+1)
				}
				rows = append(rows, tuiRecipeDetailRow{prefix + plainLogText(line), i, t.text})
			}
		}
		if len(e.BootstrapCommands) == 0 {
			add("No commands · o adds a block", t.dim)
		}
		add("", t.dim)
		add("runs as    root · sudo -H -- sh -c", t.muted)
		used := a.environmentTemplates(e.Slug)
		names := []string{}
		for _, c := range used {
			names = append(names, c.Slug)
		}
		usage := coalesce(strings.Join(names, ", "), "no templates")
		if a.managerData.err != nil {
			usage = "unavailable"
		}
		add("used by    "+usage, t.violet)
		run := a.managerData.recipes.runs[e.ID]
		runStyle := t.muted
		if run.step.State == BootstrapStepFailed {
			runStyle = t.coral
		}
		add("last run   "+environmentRunText(run, a.managerData.recipes.incompleteRuns[e.ID] || a.managerData.err != nil), runStyle)
		add("", t.dim)
		add("Edits affect future nodes. Existing nodes keep frozen commands.", t.dim)
		add("", t.dim)
		add("TEST RUN", t.muted)
		add("t tests these commands on a disposable xsmall VM; output in Activity.", t.dim)
		add("The VM is deleted afterward. Real nodes are unaffected.", t.dim)
		if _, ok := builtInEnvironment(e.Slug); ok {
			add("R restores the shipped commands for review.", t.dim)
		}
		add("", t.dim)
		add("cli · codelima environment show "+shellQuote(e.Slug), t.dim)
		return e.Slug + " · " + environmentKind(e) + " · " + e.UpdatedAt.Local().Format("Jan 2"), rows
	}
	if a.templateIndex >= len(a.managerData.configurations) {
		return "Configuration", rows
	}
	c := a.managerData.configurations[a.templateIndex]
	add("image        "+c.Image, t.text)
	add("agent        "+c.AgentProfileName, t.text)
	add(fmt.Sprintf("resources    %d vCPU · %s · %s", c.VCPUs, formatTemplateSize(c.MemoryMiB), formatTemplateSize(c.DiskMiB)), t.text)
	add("", t.dim)
	add("ENVIRONMENTS", t.muted)
	count := len(c.BootstrapCommands)
	complete := true
	for i, name := range c.Environments {
		j := slices.IndexFunc(a.managerData.environments, func(e EnvironmentConfig) bool { return e.Slug == name })
		if j < 0 {
			add(fmt.Sprintf("%d  %s · unavailable", i+1, name), t.coral)
			complete = false
			continue
		}
		commands := len(a.managerData.environments[j].BootstrapCommands)
		count += commands
		add(fmt.Sprintf("%d  %s · %d commands", i+1, name, commands), t.violet)
	}
	if len(c.Environments) == 0 {
		add("none", t.dim)
	}
	for i, command := range c.BootstrapCommands {
		add(fmt.Sprintf("direct %d · %s", i+1, templateCommandLine(command)), t.text)
	}
	if complete {
		add(fmt.Sprintf("%d environment/direct commands, in that order", count), t.muted)
	}
	add("", t.dim)
	nodes := a.templateNodes(c.ID)
	if a.managerData.err != nil {
		add("NODE USAGE UNAVAILABLE", t.coral)
	} else {
		add(fmt.Sprintf("USED BY %d NODES", len(nodes)), t.muted)
	}
	for _, n := range nodes {
		spec, ok := a.managerData.recipes.specs[n.ID]
		state, style := "matches", t.dim
		if !ok || spec.TemplateError != nil || spec.BootstrapError != nil {
			state = "comparison unavailable"
		} else if len(spec.Differences) > 0 {
			state, style = "drifted", t.amber
		}
		add(n.Slug+" · frozen at "+formatTemplateSize(n.MemoryMiB)+" · "+state, style)
	}
	if defaults, ok := builtInTemplateDefaults(c.Slug, a.managerSettings()); ok {
		add("", t.dim)
		add(fmt.Sprintf("R restores: %d vCPU · %s · %s", defaults.VCPUs, formatTemplateSize(defaults.MemoryMiB), formatTemplateSize(defaults.DiskMiB)), t.dim)
	}
	add("", t.dim)
	add("cli · codelima configuration show "+shellQuote(c.Slug), t.dim)
	return c.Slug + " · edited " + c.UpdatedAt.Local().Format("Jan 2"), rows
}
func (a *vaxisTUIApp) drawRecipeDetails(win vaxis.Window, t tuiTheme) {
	s := &a.recipes
	title, raw := a.recipeDetailRows(t)
	style := t.border
	if s.details {
		style = t.mint
	}
	body := templatePanel(win, title, style, t)
	w, h := body.Size()
	x, y := body.Origin()
	s.detailRect = tuiRect{col: x, row: y, width: w, height: h}
	var rows []tuiRecipeDetailRow
	for _, r := range raw {
		for _, line := range commandVisualRows(vaxis.Characters(r.text), max(1, w-2)) {
			r.text = line.text
			rows = append(rows, r)
		}
	}
	visible := max(1, h-2)
	if s.followCommand {
		for i, r := range rows {
			if r.command == s.command {
				if i < s.detailScroll {
					s.detailScroll = i
				}
				if i >= s.detailScroll+visible {
					s.detailScroll = i - visible + 1
				}
				break
			}
		}
		s.followCommand = false
	}
	s.detailScroll = max(0, min(s.detailScroll, max(0, len(rows)-visible)))
	for i, r := range rows[s.detailScroll:min(len(rows), s.detailScroll+visible)] {
		line := body.New(1, i, w-2, 1)
		if r.command >= 0 && r.command == s.command && s.details {
			r.style = t.selected
			tuiFill(line, r.style)
		}
		tuiLine(line, 0, r.text, r.style)
		if r.command >= 0 {
			rx, ry := line.Origin()
			s.controls = append(s.controls, tuiRecipeControl{tuiRect{col: rx, row: ry, width: w - 2, height: 1}, "command", r.command})
		}
	}
	tuiLine(body.New(1, h-1, w-2, 1), 0, fmt.Sprintf("%d/%d · ← list · ↑↓/PgUp/PgDn scroll", min(len(rows), s.detailScroll+1), len(rows)), t.dim)
}
func (a *vaxisTUIApp) drawRecipeActions(win vaxis.Window, t tuiTheme) {
	items := [][2]string{{"e", "edit"}, {"n", "new"}, {"c", "clone"}, {"d", "delete"}, {"R", "restore"}, {"details", "details"}}
	switch {
	case !a.environmentsView:
		items = append(items, [2]string{"N", "new node"})
	case a.recipes.details:
		items = [][2]string{{"e", "edit"}, {"t", "test"}, {"o", "add"}, {"D", "remove"}, {"E", "$EDITOR"}, {"R", "restore"}, {"details", "details"}}
	default:
		items = slices.Insert(items, 1, [2]string{"t", "test"})
		items = append(items, [2]string{"o", "add"}, [2]string{"D", "remove"}, [2]string{"E", "$EDITOR"})
	}
	w, _ := win.Size()
	x := 0
	for _, item := range items {
		text := item[0] + " " + item[1]
		if item[0] == "details" {
			text = "←→ details"
		}
		width := len([]rune(text)) + 2
		if x+width > w {
			break
		}
		r := win.New(x, 0, width, 1)
		tuiLine(r, 0, text, t.mint)
		rx, ry := r.Origin()
		a.recipes.controls = append(a.recipes.controls, tuiRecipeControl{tuiRect{col: rx, row: ry, width: width, height: 1}, item[0], 0})
		x += width
	}
}
