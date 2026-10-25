package codelima

import (
	"fmt"
	"strings"
	"time"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiSpecRow struct {
	text, command string
	logQuery      string
	step          int
	style         vaxis.Style
	action        string
	spinner       bool
}

func (a *vaxisTUIApp) specRows(panel, width int, theme tuiTheme) []tuiSpecRow {
	s, n := &a.spec.snapshot, a.spec.snapshot.Node
	var rows []tuiSpecRow
	add := func(text string, style vaxis.Style, command string) {
		for line := range strings.SplitSeq(text, "\n") {
			iterator := vaxis.NewCharacterIterator(plainLogText(line))
			var part strings.Builder
			cols := 0
			for {
				char, ok := iterator.Next()
				if !ok {
					break
				}
				cells := renderedTextWidth(a.vx, char.Grapheme)
				if cols+cells > max(1, width) && cols > 0 {
					rows = append(rows, tuiSpecRow{text: part.String(), command: command, style: style})
					part.Reset()
					cols = 0
				}
				part.WriteString(char.Grapheme)
				cols += cells
			}
			rows = append(rows, tuiSpecRow{text: part.String(), command: command, style: style})
		}
	}
	field := func(name, value string) { add(fmt.Sprintf("%-15s %s", name, coalesce(value, "none")), theme.text, "") }
	action := func(id string, label string) {
		start := len(rows)
		add(a.keyHint(id)+" "+label, theme.mint, "")
		for i := start; i < len(rows); i++ {
			rows[i].action = id
		}
	}
	switch panel {
	case 0:
		created := "creation time unavailable"
		if !n.CreatedAt.IsZero() {
			created = n.CreatedAt.Format("Jan 02 15:04")
		}
		add(created+" · from "+coalesce(n.ConfigurationSlug, n.ConfigurationID), theme.dim, "")
		field("image", n.Image)
		field("vCPUs", fmt.Sprint(n.VCPUs))
		field("memory", formatTUIResourceSize(n.MemoryMiB))
		field("disk", formatTUIResourceSize(n.DiskMiB))
		field("agent profile", n.AgentProfileName)
		var envs []string
		for i, name := range n.Environments {
			envs = append(envs, fmt.Sprintf("%d %s", i+1, name))
		}
		field("environments", strings.Join(envs, " · "))
		field("workspace", nodeWorkspaceMode(n)+" · "+n.DirectoryPath+" → "+coalesce(n.GuestWorkspacePath, "same path"))
		field("ports", coalesce(strings.Join(n.Ports, ", "), "none published · HTTP forwarded dynamically"))
		auth := "disabled"
		if n.ImportHostAuth {
			auth = "import · pending"
			if n.AuthImportCompleted {
				auth = "import · done, never repeats"
			}
		}
		field("host auth", auth)
		add("", theme.text, "")
		add("RESOLVED BOOTSTRAP", theme.muted, "")
		status := "not completed"
		if n.BootstrapCompleted {
			status = "completed"
		}
		if n.BootstrapCompletedAt != nil {
			status += " · " + n.BootstrapCompletedAt.Format("Jan 02 15:04")
		}
		add(status+" · runs once as root (sudo -H)", theme.dim, "")
		if s.BootstrapError != nil {
			add("Bootstrap metadata: "+s.BootstrapError.Error(), theme.coral, "")
		}
		if len(s.Bootstrap.Steps) > 0 {
			add(a.keyHints("{spec.open} opens command output · — means timing was not recorded"), theme.dim, "")
			for i, step := range s.Bootstrap.Steps {
				first := len(rows)
				label, style := bootstrapStepDisplay(step, i, n.ID, theme)
				add(label+"\n"+step.Command, style, step.Command)
				rows[first].spinner = step.State == BootstrapStepRunning
				if step.Error != "" {
					add(step.Error, theme.coral, step.Command)
				}
				for j := first; j < len(rows); j++ {
					rows[j].logQuery = step.AttemptID
					rows[j].step = i + 1
				}
			}
		} else {
			add(a.keyHints("Per-step source/timing not recorded. {spec.open} opens captured output."), theme.dim, "")
			if len(n.BootstrapCommands) == 0 {
				add("No bootstrap commands.", theme.dim, "")
			}
			for i, command := range n.BootstrapCommands {
				add(fmt.Sprintf("%d  %s", i+1, command), theme.text, command)
			}
			if s.Bootstrap.ValidationCommand != "" {
				add("validate  "+s.Bootstrap.ValidationCommand, theme.text, s.Bootstrap.ValidationCommand)
			}
		}
	case 1:
		if s.Template != nil {
			add(s.Template.Slug+" now · edited "+s.Template.UpdatedAt.Format("Jan 02 15:04"), theme.violet, "")
		}
		add("This node keeps its frozen values.", theme.dim, "")
		if s.TemplateError != nil {
			add("Template unavailable: "+s.TemplateError.Error(), theme.coral, "")
		}
		if s.BootstrapError != nil {
			add("Frozen agent settings unavailable; comparison is incomplete.", theme.amber, "")
		}
		if len(s.Differences) == 0 && s.TemplateError == nil && s.BootstrapError == nil {
			add("No template drift.", theme.mint, "")
		}
		for _, diff := range s.Differences {
			add("", theme.text, "")
			add(diff.Name, theme.muted, "")
			add("  this node  "+coalesce(diff.Frozen, "none"), theme.text, "")
			add("  now        "+coalesce(diff.Current, "none"), theme.amber, "")
		}
		add("", theme.text, "")
		action("spec.new_node", "new from current template")
		action("node.clone", "clone this frozen node")
		action("spec.edit", "edit template")
	case 2:
		add("instance.lima.yaml", theme.dim, "")
		if s.LimaError != nil {
			add(s.LimaError.Error(), theme.coral, "")
		}
		for _, f := range s.LimaFields {
			field(f.Name, f.Value)
		}
		add("", theme.text, "")
		action("spec.pager", "view full YAML")
		action("spec.copy_path", "copy path")
		add(s.LimaPath, theme.dim, "")
	}
	return rows
}

func bootstrapStepDisplay(step BootstrapStep, index int, nodeID string, theme tuiTheme) (string, vaxis.Style) {
	symbol, style := "○", theme.dim
	switch step.State {
	case BootstrapStepSucceeded:
		symbol, style = "✓", theme.mint
	case BootstrapStepRunning:
		symbol, style = "◐", theme.amber
	case BootstrapStepFailed:
		symbol, style = "✕", theme.coral
	case BootstrapStepCanceled:
		symbol, style = "⊘", theme.amber
	case BootstrapStepInterrupted, BootstrapStepUnrecorded:
		symbol, style = "?", theme.dim
	}
	duration := "—"
	if step.DurationMillis != nil {
		duration = (time.Duration(*step.DurationMillis) * time.Millisecond).Round(time.Millisecond).String()
	}
	if step.State == BootstrapStepRunning && step.StartedAt != nil {
		duration = max(time.Duration(0), time.Since(*step.StartedAt)).Round(time.Second).String()
	}
	number := fmt.Sprint(index + 1)
	if step.Kind == "validation" {
		number = "validate"
	}
	label := fmt.Sprintf("%s %s · %s · %s · %s", symbol, number, step.SourceName, step.State, duration)
	if step.ExitCode != nil && *step.ExitCode != 0 {
		label += fmt.Sprintf(" · exit %d", *step.ExitCode)
	}
	if step.ExecutionNodeID != "" && step.ExecutionNodeID != nodeID {
		label += " · inherited"
	}
	return label, style
}

func (a *vaxisTUIApp) drawNodeSpec(win vaxis.Window, theme tuiTheme) {
	s := &a.spec
	s.rects, s.tabs = [tuiSpecPanelCount]tuiRect{}, [tuiSpecPanelCount]tuiRect{}
	width, height := win.Size()
	if s.nodeID != a.state.selectedEntry().node.ID || !s.loaded {
		tuiLine(win, 0, "Loading frozen specification…", theme.dim)
		return
	}
	if s.err != nil {
		tuiLine(win, 0, "Specification: "+plainLogText(s.err.Error()), theme.coral)
		return
	}
	titles := [tuiSpecPanelCount]string{"Frozen at creation", "Template drift", "Lima instance"}
	if width < 118 || height < 22 {
		col := 0
		for i, title := range []string{"Frozen values", "Template drift", "Lima YAML"} {
			style := theme.muted
			if s.panel == i {
				style = theme.selected
			}
			w := win.New(col, 0, len(title)+3, 1)
			x, y := w.Origin()
			s.tabs[i] = tuiRect{col: x, row: y, width: len(title) + 3, height: 1}
			tuiLine(w, 0, " "+title+" ", style)
			col += len(title) + 3
		}
		a.drawSpecPanel(win.New(0, 1, width, max(1, height-2)), s.panel, titles[s.panel], theme)
	} else {
		left := width * 56 / 100
		a.drawSpecPanel(win.New(0, 0, left, height-1), 0, titles[0], theme)
		top := max(9, (height-2)/2)
		a.drawSpecPanel(win.New(left+1, 0, width-left-1, top), 1, titles[1], theme)
		a.drawSpecPanel(win.New(left+1, top+1, width-left-1, height-top-2), 2, titles[2], theme)
	}
	tuiLine(win, height-1, a.keyHints("{spec.previous_panel spec.next_panel} panel · {navigation.up navigation.down} scroll · {spec.open} step output · frozen values are read-only"), theme.dim)
}

func (a *vaxisTUIApp) drawSpecPanel(win vaxis.Window, panel int, title string, theme tuiTheme) {
	s := &a.spec
	style := theme.border
	if panel == 1 && len(s.snapshot.Differences) > 0 {
		style = theme.amber
	}
	if panel == s.panel {
		style = theme.mint
	}
	inner := border.All(win, style)
	width, height := inner.Size()
	win.New(2, 0, max(0, width-2), 1).PrintTruncate(0, vaxis.Segment{Text: " " + title + " ", Style: style})
	inner = inner.New(1, 0, max(0, width-2), height)
	width, height = inner.Size()
	x, y := inner.Origin()
	s.rects[panel] = tuiRect{col: x, row: y, width: width, height: height}
	rows := a.specRows(panel, width, theme)
	if cursor := s.cursor[panel]; cursor >= 0 && cursor < len(s.rows[panel]) {
		selected := s.rows[panel][cursor]
		if selected.step > 0 {
			first := cursor
			for first > 0 && s.rows[panel][first-1].step == selected.step {
				first--
			}
			for i, row := range rows {
				if row.step != selected.step {
					continue
				}
				last := i
				for last+1 < len(rows) && rows[last+1].step == selected.step {
					last++
				}
				s.cursor[panel] = min(i+cursor-first, last)
				break
			}
		}
	}
	s.rows[panel] = rows
	s.cursor[panel] = max(0, min(s.cursor[panel], len(s.rows[panel])-1))
	if s.cursor[panel] < s.offset[panel] {
		s.offset[panel] = s.cursor[panel]
	}
	if s.cursor[panel] >= s.offset[panel]+height {
		s.offset[panel] = s.cursor[panel] - height + 1
	}
	s.offset[panel] = max(0, min(s.offset[panel], len(s.rows[panel])-max(1, height)))
	for i, row := range s.rows[panel][s.offset[panel]:] {
		if i >= height {
			break
		}
		style := row.style
		if panel == s.panel && i+s.offset[panel] == s.cursor[panel] {
			style = theme.selected
		}
		tuiLine(inner, i, row.text, style)
		if row.spinner {
			spinnerStyle := style
			spinnerStyle.Foreground = theme.amber.Foreground
			a.drawSpinner(inner.New(0, i, 1, 1), spinnerStyle)
		}
	}
	if len(s.rows[panel]) > height {
		width, height := win.Size()
		text := fmt.Sprintf(" %d/%d ", s.cursor[panel]+1, len(s.rows[panel]))
		tuiLine(win.New(max(1, width-len(text)-2), height-1, len(text), 1), 0, text, theme.dim)
	}
}
