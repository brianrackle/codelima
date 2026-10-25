package codelima

import (
	"fmt"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func (f *tuiCreateNodeForm) PreferredSize() (int, int) { return 112, 28 }

func (a *vaxisTUIApp) managerTheme() tuiTheme {
	rgb := a.vx != nil && a.vx.CanRGB()
	switch a.managerSettings().Color {
	case "ansi":
		rgb = false
	case "truecolor":
		rgb = true
	}
	return newTUITheme(rgb)
}

func (t tuiTheme) modal() tuiTheme {
	for _, style := range []*vaxis.Style{&t.text, &t.muted, &t.dim, &t.border, &t.track, &t.mint, &t.amber, &t.coral, &t.blue, &t.violet} {
		style.Background = t.raised.Background
	}
	return t
}

func (t tuiTheme) dimmed() tuiTheme {
	for _, style := range []*vaxis.Style{&t.text, &t.muted, &t.dim, &t.track, &t.mint, &t.amber, &t.coral, &t.blue, &t.violet, &t.selected} {
		style.Foreground = t.border.Foreground
		style.Attribute &^= vaxis.AttrBold | vaxis.AttrReverse
	}
	return t
}

func (f *tuiCreateNodeForm) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := f.app.managerTheme().modal()
	width, height := win.Size()
	if width < 8 || height < 6 {
		return
	}
	tuiFill(win, t.raised)
	body := border.All(win, t.mint)
	title := " New node in " + f.value("directory") + " "
	tuiLine(win.New(2, 0, width-4, 1), 0, title, t.text)
	w, h := body.Size()
	fieldRows := max(2, h-8)
	visible := min(len(f.dialog.Fields), fieldRows/2)
	if f.dialog.FieldIndex < f.firstField {
		f.firstField = f.dialog.FieldIndex
	}
	if f.dialog.FieldIndex >= f.firstField+visible {
		f.firstField = f.dialog.FieldIndex - visible + 1
	}
	f.firstField = max(0, min(f.firstField, len(f.dialog.Fields)-visible))
	f.fieldRects = make([]tuiRect, len(f.dialog.Fields))
	x, y := body.Origin()
	for i := f.firstField; i < min(len(f.dialog.Fields), f.firstField+visible); i++ {
		row := 1 + (i-f.firstField)*2
		field := &f.dialog.Fields[i]
		focused := i == f.dialog.FieldIndex
		style := t.muted
		if focused {
			style = t.mint
		}
		tuiLine(body.New(2, row, 13, 1), 0, field.Label, style)
		inputWidth := max(8, w-19)
		if w >= 100 {
			inputWidth = 42
		}
		input := body.New(16, row, inputWidth, 1)
		f.fieldRects[i] = tuiRect{col: x + 2, row: y + row, width: w - 4, height: 2}
		if field.Input != nil {
			prompt := ""
			if field.Input.String() == "" {
				prompt = field.DefaultValue
			}
			if field.Key == "ports" && field.Input.String() == "" {
				prompt = "HOST:GUEST, … (optional)"
			}
			field.Input.SetPrompt(prompt)
			field.Input.Prompt, field.Input.Content = t.dim, t.text
			field.Input.HideCursor = !focused || f.pending
			field.Input.Draw(input)
		} else {
			text := f.fieldDisplay(field.Key)
			if focused {
				style.Attribute |= vaxis.AttrBold
			}
			tuiLine(input, 0, text, style)
		}
		hint, hintStyle := f.fieldHint(field.Key), t.dim
		if problem := f.fieldError(field.Key); problem != "" {
			hint, hintStyle = "✕ "+problem, t.coral
		}
		if w >= 100 {
			tuiLine(body.New(60, row, w-62, 1), 0, hint, hintStyle)
		} else {
			tuiLine(body.New(16, row+1, w-18, 1), 0, hint, hintStyle)
		}
	}
	row := h - 7
	tuiLine(body.New(2, row, w-4, 1), 0, "FIT ON THIS HOST · running/starting + this node", t.muted)
	if c, ok := f.template(); ok {
		for index, line := range createHostFit(f.app.host, c) {
			style := t.muted
			if line.warning {
				style = t.amber
			}
			if line.shortfall {
				style = t.coral
			}
			line.draw(body.New(2, row+1+index, w-4, 1), 0, style, t)
		}
	}
	tuiLine(body.New(2, h-3, w-4, 1), 0, "cli · "+f.cliPreview(), t.dim)
	status, style := f.bindingHints("{dialog.submit} create · {dialog.cancel} cancel"), t.mint
	if problem := f.validationError(); problem != "" {
		status, style = problem, t.coral
	}
	if f.dialog.Error != "" {
		status, style = f.dialog.Error, t.coral
	}
	if f.pending {
		status, style = "Submitting… · accepted work continues after quit", t.amber
	}
	if f.dialog.confirmDiscard {
		status, style = "Discard unsaved changes? y discard · n keep editing", t.coral
	}
	tuiLine(body.New(2, h-1, w-4, 1), 0, status, style)
	f.submitRect = tuiRect{}
	if f.validationError() == "" && !f.pending && !f.dialog.confirmDiscard {
		f.submitRect = tuiRect{col: x + 2, row: y + h - 1, width: 13, height: 1}
	}
}

func (f *tuiCreateNodeForm) fieldDisplay(key string) string {
	checkbox := func(on bool, label string) string {
		if on {
			return "[x] " + label
		}
		return "[ ] " + label
	}
	switch key {
	case "configuration":
		return "‹ " + f.value(key) + f.bindingHints(" ›  {dialog.accept} list")
	case "workspace_mode":
		if f.value(key) == WorkspaceModeCopy {
			return "( ) mounted   (●) copy"
		}
		return "(●) mounted   ( ) copy"
	case "import_host_auth":
		return checkbox(f.value(key) == hostAuthImportOn, "import on first start")
	case "start":
		return checkbox(f.value(key) == "true", "start now")
	case "attach":
		return checkbox(f.value(key) == "true", "attach when ready")
	}
	return f.value(key)
}

func (f *tuiCreateNodeForm) fieldHint(key string) string {
	switch key {
	case "slug":
		return "✓ available"
	case "directory":
		return f.bindingHints("existing · outside CODELIMA_HOME · {dialog.accept} browse")
	case "configuration":
		if c, ok := f.template(); ok {
			return fmt.Sprintf("%d vCPU · %s mem · %s disk · %s", c.VCPUs, createMiB(c.MemoryMiB), createMiB(c.DiskMiB), environmentConfigSelectionSummary(c.Environments))
		}
	case "workspace_mode":
		if f.value(key) == WorkspaceModeCopy {
			return "guest-local · seeded once on first start"
		}
		return "same path, live"
	case "import_host_auth":
		return "SSH keys, known_hosts, git and agent logins"
	case "ports":
		return "raw TCP · HTTP/WebSocket forwarded automatically"
	case "start":
		return "creation and first boot continue after quit"
	case "attach":
		if f.value("start") != "true" {
			return "enable start now to attach"
		}
		return "only after successful startup; leaving cancels attachment"
	}
	return ""
}

type tuiCreateFitLine struct {
	tuiGaugeLine
	warning, shortfall bool
}

func createMiB(value uint32) string { return fmt.Sprintf("%g GiB", float64(value)/1024) }

func createHostFit(host tuiHostSummary, c Configuration) []tuiCreateFitLine {
	lines := []tuiCreateFitLine{{tuiGaugeLine: tuiGaugeLine{prefix: "vCPU    allocation unavailable"}}, {tuiGaugeLine: tuiGaugeLine{prefix: "memory  allocation unavailable"}}, {tuiGaugeLine: tuiGaugeLine{prefix: "disk    " + createMiB(c.DiskMiB) + " thin · free space unavailable"}}}
	if host.AllocationsKnown && host.UnknownNodes == 0 {
		if host.Capacity.CPUs > 0 {
			allocated := host.AllocatedCPUs + uint64(c.VCPUs)
			lines[0].tuiGaugeLine = tuiGaugeLine{prefix: "vCPU    ", percent: float64(allocated) / float64(host.Capacity.CPUs) * 100, cells: 10, suffix: fmt.Sprintf(" %d → %d/%d", host.AllocatedCPUs, allocated, host.Capacity.CPUs)}
			if allocated > uint64(host.Capacity.CPUs) {
				lines[0].suffix += " · ⚠ overcommit; contended under load"
				lines[0].warning = true
			} else {
				lines[0].suffix += " · ✓ fits"
			}
		}
		if host.Capacity.MemoryBytes != nil {
			total, used := float64(*host.Capacity.MemoryBytes)/(1<<30), float64(host.AllocatedMemoryBytes)/(1<<30)
			after := used + float64(c.MemoryMiB)/1024
			lines[1].tuiGaugeLine = tuiGaugeLine{prefix: "memory  ", percent: after / total * 100, cells: 10, suffix: fmt.Sprintf(" %.1f → %.1f/%.1f GiB", used, after, total)}
			left := total - after
			switch {
			case left < 0:
				lines[1].suffix += fmt.Sprintf(" · ✕ %.1f GiB short", -left)
				lines[1].shortfall = true
			case left == 0:
				lines[1].suffix += " · ⚠ no memory left for host"
				lines[1].warning = true
			default:
				lines[1].suffix += fmt.Sprintf(" · ✓ %.1f GiB left for host", left)
			}
		}
	}
	if host.Capacity.DiskAvailableBytes != nil {
		free := float64(*host.Capacity.DiskAvailableBytes) / (1 << 30)
		lines[2].prefix = fmt.Sprintf("disk    %s thin · %.1f GiB free on LIMA_HOME volume", createMiB(c.DiskMiB), free)
		if uint64(c.DiskMiB)<<20 > *host.Capacity.DiskAvailableBytes {
			lines[2].prefix += " · ⚠ growth may exhaust space"
			lines[2].warning = true
		}
	}
	return lines
}
