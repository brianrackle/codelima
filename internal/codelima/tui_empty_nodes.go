package codelima

import (
	"fmt"
	"path/filepath"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiEmptyNodesState struct {
	scroll, height int
	controls       []tuiEmptyNodeControl
}

type tuiEmptyNodeControl struct {
	rect tuiRect
	key  string
}

type tuiEmptyNodeLine struct {
	text, key string
	style     vaxis.Style
}

func (a *vaxisTUIApp) emptyNodesVisible() bool {
	return a.view == tuiViewNodes && !a.nodeDetail && a.state != nil && a.state.focus != tuiFocusTerminal &&
		len(a.state.nodes) == 0 && a.filterInput == nil && (a.fleet == nil || a.fleet.filter == "")
}

func (a *vaxisTUIApp) createNodeDirectory() string {
	if a.treeWorkspaceRoot != "" {
		return a.treeWorkspaceRoot
	}
	return coalesce(a.launchScope, ".")
}

// Only an observed failure blocks the guide after a successful check. Warnings
// such as oversubscription or optional credentials are not prerequisites.
func (a *vaxisTUIApp) emptyNodeBlockReason() string {
	if a.doctor.err != nil {
		return "Host checks unavailable: " + plainLogText(a.doctor.err.Error())
	}
	if a.doctor.checkedAt.IsZero() {
		return "Host checks pending · R re-check"
	}
	for _, check := range a.doctor.report.Checks {
		if check.Status == "fail" {
			return plainLogText(check.Name + ": " + check.Message)
		}
	}
	return ""
}

func (a *vaxisTUIApp) emptyNodeLines(theme tuiTheme) ([]tuiEmptyNodeLine, []tuiEmptyNodeLine) {
	directory := a.createNodeDirectory()
	if directory == "." {
		directory = "the current directory"
	}
	blocked := a.emptyNodeBlockReason()
	createStyle := theme.selected
	if blocked != "" {
		createStyle = theme.dim
	}
	left := []tuiEmptyNodeLine{
		{text: "A node is a Linux VM bound to a directory.", style: theme.muted},
		{text: "Choose its resources, workspace mode and agent setup.", style: theme.muted},
		{},
		{text: "n  Create a node for " + directory, key: "n", style: createStyle},
	}
	defaults := "   Loading default template…"
	if a.managerData.err != nil {
		defaults = "   Template defaults unavailable · R re-check"
	} else if a.managerData.configurationsLoaded || len(a.managerData.configurations) != 0 {
		defaults = "   Default template small unavailable · 2 Templates"
		for _, c := range a.managerData.configurations {
			if c.Slug == DefaultConfigurationSlug {
				defaults = fmt.Sprintf("   %s · %d vCPU · %s memory · %s disk · %s", c.Slug, c.VCPUs, formatTUIResourceSize(c.MemoryMiB), formatTUIResourceSize(c.DiskMiB), DefaultWorkspaceMode)
				if path := a.createNodeDirectory(); path != "." {
					defaults = "   name " + coalesce(slugify(filepath.Base(path)), "required") + " · " + strings.TrimSpace(defaults)
				}
				break
			}
		}
	}
	left = append(left,
		tuiEmptyNodeLine{text: defaults, key: "n", style: theme.dim},
		tuiEmptyNodeLine{},
		tuiEmptyNodeLine{text: "2  Browse templates · built-ins or your own", key: "2", style: theme.text},
		tuiEmptyNodeLine{text: "5  System · doctor, daemon, settings", key: "5", style: theme.text},
	)
	if a.treeWorkspaceRoot != "" {
		left = append(left, tuiEmptyNodeLine{text: "0  Show all nodes · leave this directory scope", key: "0", style: theme.text})
	}
	left = append(left, tuiEmptyNodeLine{}, tuiEmptyNodeLine{text: "HOST CHECK", style: theme.muted})
	if a.doctor.checkedAt.IsZero() && a.doctor.err == nil {
		left = append(left, tuiEmptyNodeLine{text: "◐ Checking host prerequisites…", style: theme.dim})
	}
	for _, check := range a.doctor.report.Checks {
		glyph, style := "✓", theme.muted
		if check.Status == "fail" {
			glyph, style = "✕", theme.coral
		} else if check.Status != "ok" {
			glyph, style = "⚠", theme.amber
		}
		left = append(left, tuiEmptyNodeLine{text: glyph + " " + plainLogText(check.Message), style: style})
	}
	connection := "○ local control · no daemon connection"
	if a.daemonDisconnected {
		connection = "○ daemon disconnected · reconnecting"
	} else if a.service != nil && a.service.daemonClient != nil {
		connection = "✓ daemon connected"
	}
	left = append(left, tuiEmptyNodeLine{text: connection, style: theme.muted})
	var right []tuiEmptyNodeLine
	if blocked != "" {
		right = append(right, tuiEmptyNodeLine{text: blocked, style: theme.coral}, tuiEmptyNodeLine{},
			tuiEmptyNodeLine{text: "Creation is unavailable until the host checks succeed. Review the finding in Doctor, correct it, then re-check.", style: theme.muted})
	} else {
		right = append(right, tuiEmptyNodeLine{text: "Host checks passed", style: theme.mint}, tuiEmptyNodeLine{},
			tuiEmptyNodeLine{text: "The first start downloads the configured image and runs the template's setup. Time depends on the image, commands and network.", style: theme.muted})
	}
	if a.doctor.loading {
		right = append(right, tuiEmptyNodeLine{}, tuiEmptyNodeLine{text: "◐ Re-checking · showing the last result", style: theme.amber})
	}
	if count := len(a.doctor.report.Warnings); count != 0 {
		right = append(right, tuiEmptyNodeLine{}, tuiEmptyNodeLine{text: fmt.Sprintf("%d additional findings · 5 Doctor", count), key: "5", style: theme.amber})
	}
	right = append(right, tuiEmptyNodeLine{}, tuiEmptyNodeLine{text: "R  Re-check host and template defaults", key: "R", style: theme.mint},
		tuiEmptyNodeLine{text: "5  Open full Doctor findings", key: "5", style: theme.mint})
	return left, right
}

func (a *vaxisTUIApp) drawEmptyNodes(win vaxis.Window, theme tuiTheme) {
	w, h := win.Size()
	a.emptyNodes.controls = nil
	if w < 35 || h < 12 {
		tuiLine(win.New(1, 2, w-2, 1), 0, "No nodes · enlarge the terminal", theme.muted)
		return
	}
	title := "No nodes yet"
	if a.treeWorkspaceRoot != "" {
		title = "No nodes in " + a.treeWorkspaceRoot + " yet"
	}
	left, right := a.emptyNodeLines(theme)
	x, y, width, height := 2, 2, w-4, h-6
	wide := w >= 140
	indent := 1
	if wide {
		x, y, width, height, indent = 6, 4, w-68, h-8, 2
	}
	outer := win.New(x, y, width, height)
	body := border.All(outer, theme.mint)
	tuiLine(outer.New(2, 0, width-4, 1), 0, " "+title+" ", theme.text)
	rightWidth := 52
	overflowRight := wide && emptyNodeWrappedCount(right, max(1, rightWidth-4)) > height-2
	if !wide || overflowRight {
		left = append(left, tuiEmptyNodeLine{})
		left = append(left, right...)
	}
	a.drawEmptyNodeLines(body, left, true, indent)
	if wide {
		outer := win.New(x+width+4, y, rightWidth, height)
		bw, _ := outer.Size()
		label, style := "First start", theme.muted
		frame := theme.border
		if a.emptyNodeBlockReason() != "" {
			label, style = "Host not ready", theme.coral
			frame = theme.coral
		}
		body := border.All(outer, frame)
		tuiLine(outer.New(2, 0, bw-4, 1), 0, " "+label+" ", style)
		// A long failure is always available in the scrollable main panel.
		if overflowRight {
			tuiLine(outer.New(2, height-1, bw-4, 1), 0, " PgUp/PgDn for full details ", theme.dim)
		}
		a.drawEmptyNodeLines(body, right, false, 1)
	}
	reason := a.emptyNodeBlockReason()
	if reason == "" {
		reason = "PgUp/PgDn scroll · R re-check host and template defaults"
	} else {
		reason = "n unavailable · PgUp/PgDn scroll · " + reason
	}
	tuiLine(win.New(2, h-3, w-4, 1), 0, reason, theme.dim)
}

func emptyNodeWrappedCount(lines []tuiEmptyNodeLine, width int) int {
	count := 0
	for _, line := range lines {
		count += max(1, len(commandVisualRows(vaxis.Characters(plainLogText(line.text)), width)))
	}
	return count
}

func (a *vaxisTUIApp) drawEmptyNodeLines(win vaxis.Window, lines []tuiEmptyNodeLine, scroll bool, indent int) {
	w, h := win.Size()
	var rows []tuiEmptyNodeLine
	for _, line := range lines {
		wrapped := commandVisualRows(vaxis.Characters(plainLogText(line.text)), max(1, w-2*indent))
		if len(wrapped) == 0 {
			rows = append(rows, line)
		}
		for _, row := range wrapped {
			rows = append(rows, tuiEmptyNodeLine{text: row.text, key: line.key, style: line.style})
		}
	}
	start := 0
	if scroll {
		a.emptyNodes.height = max(1, h)
		a.emptyNodes.scroll = max(0, min(a.emptyNodes.scroll, len(rows)-h))
		start = a.emptyNodes.scroll
	}
	col, top := win.Origin()
	for i, line := range rows[start:min(len(rows), start+h)] {
		tuiLine(win.New(indent, i, w-2*indent, 1), 0, line.text, line.style)
		if line.key != "" && strings.TrimSpace(line.text) != "" {
			a.emptyNodes.controls = append(a.emptyNodes.controls, tuiEmptyNodeControl{key: line.key, rect: tuiRect{col: col + indent, row: top + i, width: w - 2*indent, height: 1}})
		}
	}
}

func (a *vaxisTUIApp) handleEmptyNodesKey(key vaxis.Key) bool {
	if !a.emptyNodesVisible() {
		return false
	}
	switch {
	case key.Matches(vaxis.KeyPgDown):
		a.emptyNodes.scroll += max(1, a.emptyNodes.height-1)
	case key.Matches(vaxis.KeyPgUp):
		a.emptyNodes.scroll = max(0, a.emptyNodes.scroll-max(1, a.emptyNodes.height-1))
	case key.Matches(vaxis.KeyDown), key.MatchString("j"):
		a.emptyNodes.scroll++
	case key.Matches(vaxis.KeyUp), key.MatchString("k"):
		a.emptyNodes.scroll = max(0, a.emptyNodes.scroll-1)
	case key.MatchString("R"):
		if tuiKeyActivates(key, true) {
			a.claimShortcutKey(key)
			a.startDoctorCheck(false, true)
			a.loadManagerData(false)
		}
	default:
		return false
	}
	return true
}

func (a *vaxisTUIApp) handleEmptyNodesMouse(mouse vaxis.Mouse) bool {
	if !a.emptyNodesVisible() {
		return false
	}
	if mouse.EventType != vaxis.EventPress {
		return true
	}
	switch mouse.Button {
	case vaxis.MouseWheelDown:
		a.emptyNodes.scroll++
	case vaxis.MouseWheelUp:
		a.emptyNodes.scroll = max(0, a.emptyNodes.scroll-1)
	case vaxis.MouseLeftButton:
		for _, control := range a.emptyNodes.controls {
			if control.rect.contains(mouse.Col, mouse.Row) {
				a.handleKey(vaxis.Key{Keycode: []rune(control.key)[0], Text: control.key})
				break
			}
		}
	}
	return true
}
