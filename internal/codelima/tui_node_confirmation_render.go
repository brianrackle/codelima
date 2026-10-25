package codelima

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiConfirmationLine struct {
	text  string
	style vaxis.Style
}

func (d *tuiNodeConfirmation) lines(t tuiTheme) []tuiConfirmationLine {
	node := d.node
	if current, err := d.app.managerNode(node.ID); err == nil {
		node = current
	}
	sessions := d.app.fleetSessions(node)
	guests, hosts, unknown := 0, 0, 0
	for _, session := range sessions {
		switch {
		case session.kindKnown && session.kind == terminal.NodeShell:
			guests++
		case session.kindKnown && session.kind == terminal.NodeHostShell:
			hosts++
		default:
			unknown++
		}
	}
	var lines []tuiConfirmationLine
	add := func(text string, style vaxis.Style) { lines = append(lines, tuiConfirmationLine{text, style}) }
	if d.action == tuiActionNodeDelete {
		disk := "virtual disk (size unavailable)"
		if node.DiskMiB != 0 {
			disk = formatTUIResourceSize(node.DiskMiB) + " virtual disk"
		}
		add("Destroys the VM and its "+disk+".", t.text)
		if nodeWorkspaceMode(node) == "copy" {
			add("Workspace mode is copy: guest changes that were never copied out are lost.", t.coral)
		} else {
			add("Workspace mode is mounted: the host directory stays. Guest-only files are lost.", t.muted)
		}
		if len(node.Ports) > 0 {
			add("Configured ports: "+strings.Join(node.Ports, ", "), t.dim)
		}
		last := "Last stop: unavailable"
		if nodeIsRunning(node) {
			last = "VM is running; its guest processes will end."
		} else if evidence, ok := d.app.fleetEvidence.snapshot.Nodes[node.ID]; ok && d.app.fleetEvidence.loaded && evidence.Error == "" && evidence.NodeUpdatedAt.Equal(node.UpdatedAt) && evidence.StoppedAt != nil && !evidence.StoppedAt.After(time.Now()) {
			last = "Last confirmed stop " + overviewAge(*evidence.StoppedAt, time.Now()) + " ago"
			if d.app.fleetEvidenceStale() {
				last += " (retained observation)"
			}
		}
		add(last, t.dim)
		add(fmt.Sprintf("%d guest sessions · %d host sessions", guests, hosts), t.dim)
	} else {
		add(fmt.Sprintf("%d guest sessions will end:", guests), t.text)
		for _, session := range sessions {
			if !session.kindKnown || session.kind != terminal.NodeShell {
				continue
			}
			activity := session.activity
			if session.idleSince > 0 {
				activity = fleetIdleLabel(session.idleSince, time.Now())
			}
			style := t.muted
			if session.activity == "working" {
				style = t.amber
			}
			add("  shell · "+session.label+" · "+activity+" · age "+overviewAge(session.createdAt, time.Now()), style)
		}
		add("", t.text)
		allocation := summarizeHostAllocations([]Node{node})
		if allocation.UnknownNodes > 0 || node.VCPUs == 0 || node.MemoryMiB == 0 || d.app.daemonDisconnected || (d.app.fleetEvidence.loaded || d.app.fleetEvidence.err != nil) && d.app.fleetEvidenceStale() {
			add("Resource release unavailable: runtime observation is missing or stale.", t.dim)
		} else {
			add(fmt.Sprintf("Frees %d vCPU and %s GiB of allocated memory.", allocation.AllocatedCPUs, tuiGiB(allocation.AllocatedMemoryBytes)), t.muted)
		}
		add("Disk, workspace and frozen values stay. s starts the node again.", t.muted)
		if hosts > 0 {
			add(fmt.Sprintf("%d host sessions keep running.", hosts), t.dim)
		}
	}
	if unknown > 0 {
		add(fmt.Sprintf("%d sessions have unknown kind; inspect before proceeding.", unknown), t.amber)
	}
	return lines
}

func (d *tuiNodeConfirmation) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := d.app.managerTheme().modal()
	d.controls = nil
	tuiFill(win, t.raised)
	accent := t.amber
	if d.action == tuiActionNodeDelete {
		accent = t.coral
	}
	body := border.All(win, accent)
	w, h := body.Size()
	title := accent
	title.Attribute |= vaxis.AttrBold
	tuiLine(win.New(2, 0, max(0, w-2), 1), 0, " "+plainLogText(d.dialog.Title)+" ", title)
	d.tooSmall = w < 28 || h < 10
	if d.tooSmall {
		tuiLine(body, 1, "Resize to review · Esc cancel", t.text)
		return
	}
	content := body.New(1, 0, w-2, h)
	cw, _ := content.Size()
	var rows []tuiConfirmationLine
	for _, line := range d.lines(t) {
		if line.text == "" {
			rows = append(rows, line)
		}
		for _, text := range helpDescriptionRows(plainLogText(line.text), cw) {
			rows = append(rows, tuiConfirmationLine{text, line.style})
		}
	}
	d.height, d.totalRows = h-6, len(rows)
	d.scroll = min(max(0, d.scroll), max(0, len(rows)-d.height))
	for i, row := range rows[d.scroll:min(len(rows), d.scroll+d.height)] {
		tuiLine(content, i, row.text, row.style)
	}
	hint := ""
	if len(rows) > d.height {
		hint = fmt.Sprintf("PgUp/PgDn · %d–%d/%d", d.scroll+1, min(len(rows), d.scroll+d.height), len(rows))
	}
	tuiLine(content, h-6, hint, t.dim)
	if d.action == tuiActionNodeDelete {
		tuiLine(content, h-5, "Type "+d.node.Slug+" to confirm", t.muted)
		input := d.dialog.Fields[0].Input
		input.Content = d.app.managerTheme().text
		inputWin := content.New(0, h-4, min(cw, 36), 1)
		tuiFill(inputWin, input.Content)
		input.Draw(inputWin)
	} else {
		check := "[ ]"
		if d.skipIdle {
			check = "[x]"
		}
		d.drawControl(content, 0, h-5, cw, "skip-idle", check+" Don't ask for idle guests", t.muted)
		if d.attachID != "" {
			d.drawControl(content, 0, h-4, cw, "attach", "x Attach to "+d.attachLabel+" first", t.mint)
		}
	}
	errText := d.dialog.Error
	if _, err := d.currentNode(); err != nil {
		errText = err.Error()
	}
	tuiLine(content, h-3, plainLogText(errText), t.coral)
	buttonStyle := accent
	if !d.ready() {
		buttonStyle = t.dim
	}
	button := "s Stop"
	if d.action == tuiActionNodeDelete {
		button = "Enter Delete"
	}
	d.drawControl(content, cw-len(button)-16, h-1, len(button)+2, "submit", "["+button+"]", buttonStyle)
	d.drawControl(content, cw-12, h-1, 12, "cancel", "[Esc Cancel]", t.muted)
}

func (d *tuiNodeConfirmation) drawControl(win vaxis.Window, col, row, width int, id, text string, style vaxis.Style) {
	area := win.New(col, row, width, 1)
	tuiLine(area, 0, text, style)
	x, y := area.Origin()
	d.controls = append(d.controls, tuiCommandTarget{rect: tuiRect{col: x, row: y, width: width, height: 1}, id: id})
}
