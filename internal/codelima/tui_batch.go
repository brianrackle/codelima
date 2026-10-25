package codelima

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiBatchReview struct {
	app            *vaxisTUIApp
	targets        []NodeBatchTarget
	dialog         *tuiDialog
	clearMarks     bool
	scroll, height int
	tooSmall       bool
	controls       []tuiCommandTarget
	run            func([]NodeBatchTarget) error
}

// A failed or interrupted delete can retain its durable terminating marker.
// A fresh, revision-matched observation of the released claim permits another
// explicit deletion review. Admission still reclaims the token and recovers the
// marker under the existing service locks; the frontend never rewrites it.
func (a *vaxisTUIApp) fleetDeleteRetryable(node Node) bool {
	if node.Status != NodeStatusTerminating || a.fleetEvidenceStale() || a.operationDisplayStatus(tuiTreeEntry{node: node}) != "" {
		return false
	}
	evidence, ok := a.fleetEvidence.snapshot.Nodes[node.ID]
	return ok && evidence.Error == "" && evidence.ClaimState == "unclaimed" && evidence.NodeUpdatedAt.Equal(node.UpdatedAt)
}

func (a *vaxisTUIApp) batchReservation(nodeID string) (NodeBatchMember, bool) {
	for _, operation := range a.activity.page.Running {
		if operation.Batch == nil {
			continue
		}
		for _, member := range operation.Batch.Members {
			if member.NodeID == nodeID {
				return member, true
			}
		}
	}
	return NodeBatchMember{}, false
}

func (a *vaxisTUIApp) openBatchReview(title string, plan []tuiPaletteNodePlan, clearMarks bool) error {
	if len(plan) == 0 || len(plan) > nodeBatchLimit {
		return fmt.Errorf("review between 1 and %d nodes at a time", nodeBatchLimit)
	}
	d := &tuiBatchReview{app: a, clearMarks: clearMarks}
	for _, target := range plan {
		entry, err := a.paletteNodeAction(target.id, target.action)
		if err != nil {
			return err
		}
		d.targets = append(d.targets, NodeBatchTarget{NodeID: target.id, NodeSlug: entry.node.Slug, Kind: strings.TrimPrefix(string(target.action), "node.")})
	}
	var fields []tuiDialogField
	if slices.ContainsFunc(d.targets, func(t NodeBatchTarget) bool { return t.Kind == "delete" }) {
		fields = []tuiDialogField{newTUIInputField("confirm", d.deletePrompt(), "", true)}
	}
	d.dialog = newTUIDialog(title, "Run", nil, fields, d.submit)
	d.run = d.execute
	a.overlay = d
	return nil
}

func (d *tuiBatchReview) deletePrompt() string {
	return fmt.Sprintf("Type %d exactly to delete %s", len(d.targets), pluralForm(len(d.targets), "this node", "these nodes"))
}

func (d *tuiBatchReview) validate() error {
	if d.app.daemonDisconnected {
		return fmt.Errorf("daemon unavailable; reconnect before confirming")
	}
	for _, target := range d.targets {
		entry, err := d.app.paletteNodeAction(target.NodeID, tuiActionID("node."+target.Kind))
		if err != nil {
			return err
		}
		if entry.node.Slug != target.NodeSlug {
			return fmt.Errorf("node name changed; cancel and review again")
		}
	}
	return nil
}

func (d *tuiBatchReview) submit(map[string]string) error {
	if !tuiReviewReadable(d.app, d.tooSmall) {
		return fmt.Errorf("resize to review before confirming")
	}
	if err := d.validate(); err != nil {
		return err
	}
	if len(d.dialog.Fields) > 0 && d.dialog.Fields[0].Input.String() != strconv.Itoa(len(d.targets)) {
		return fmt.Errorf("type %d exactly to confirm deletion", len(d.targets))
	}
	return d.run(slices.Clone(d.targets))
}

func (d *tuiBatchReview) execute(targets []NodeBatchTarget) error {
	keys := make([]string, 0, len(targets))
	for _, target := range targets {
		keys = append(keys, terminal.NodeTarget(target.NodeID).String())
	}
	return d.app.startOperation(tuiOperationRequest{Title: d.dialog.Title, DisplayStatus: "queued", ResourceKeys: keys, EntryKeys: keys, Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
		record, err := service.NodeBatch(ctx, targets)
		if err != nil {
			return tuiOperationResult{}, err
		}
		return tuiOperationResult{ReloadData: true, Status: record.Batch.summary(), Apply: func(a *vaxisTUIApp) error {
			if d.clearMarks {
				for _, target := range targets {
					delete(a.fleet.marks, target.NodeID)
				}
			}
			return nil
		}}, nil
	}})
}

func (d *tuiBatchReview) lines(t tuiTheme) []tuiConfirmationLine {
	var lines []tuiConfirmationLine
	add := func(text string, style vaxis.Style) { lines = append(lines, tuiConfirmationLine{text, style}) }
	endsGuests, releases := false, false
	var released []Node
	for _, target := range d.targets {
		node, err := d.app.managerNode(target.NodeID)
		if err != nil {
			add(target.NodeSlug+" · unavailable", t.coral)
			continue
		}
		guests, hosts, unknown := 0, 0, 0
		for _, session := range d.app.fleetSessions(node) {
			switch {
			case !session.kindKnown:
				unknown++
			case session.kind == terminal.NodeShell:
				guests++
			case session.kind == terminal.NodeHostShell:
				hosts++
			default:
				unknown++
			}
		}
		add(target.NodeSlug+" · "+target.Kind+" · "+d.app.fleetStatus(node), t.text)
		text := fmt.Sprintf("  %d guest · %d host", guests, hosts)
		if unknown > 0 {
			text += fmt.Sprintf(" · %d unclassified", unknown)
		}
		add(text+d.app.fleetSessionSummary(node), t.muted)
		if target.Kind == "stop" || target.Kind == "restart" {
			endsGuests = true
		}
		if target.Kind == "stop" || target.Kind == "delete" {
			releases = true
			released = append(released, node)
		}
	}
	add("", t.text)
	if endsGuests {
		add("Guest sessions end on stop or restart. Host sessions keep running.", t.amber)
	}
	if slices.ContainsFunc(d.targets, func(target NodeBatchTarget) bool { return target.Kind == "delete" }) {
		add(pluralForm(len(d.targets), "Guest and host sessions end for this node.", "Guest and host sessions end for deleted nodes."), t.coral)
	}
	if slices.ContainsFunc(d.targets, func(target NodeBatchTarget) bool { return target.Kind == "restart" }) {
		add("Restart keeps completed bootstrap; failed setup reruns from the beginning.", t.muted)
	}
	if slices.ContainsFunc(d.targets, func(target NodeBatchTarget) bool { return target.Kind == "retry" }) {
		add("Retry resumes the failed bootstrap step and keeps earlier completed steps.", t.muted)
	}
	if releases {
		if !d.app.host.AllocationsKnown {
			add("Resource release unavailable until host observations refresh.", t.dim)
		} else {
			allocation := summarizeHostAllocations(released)
			label := "Frees"
			if allocation.UnknownNodes > 0 {
				label = "Known release:"
			}
			add(fmt.Sprintf("%s %d vCPU and %s GiB.", label, allocation.AllocatedCPUs, tuiGiB(allocation.AllocatedMemoryBytes)), t.muted)
		}
	}
	if len(d.dialog.Fields) > 0 {
		add(pluralForm(len(d.targets), "Deletes this VM and its disk. Host workspace files remain.", "Deletes these VMs and their disks. Host workspace files remain."), t.coral)
	}
	add(pluralForm(len(d.targets), "Progress appears in this node's row and Activity.", "Runs up to 4 nodes in parallel; progress appears in each row and Activity."), t.muted)
	add("Accepted work continues after this TUI closes. Failures do not stop other targets.", t.dim)
	if err := d.validate(); err != nil {
		add(err.Error(), t.coral)
	}
	return lines
}

func (d *tuiBatchReview) activate(id string) (bool, error) {
	if id == "cancel" {
		return true, nil
	}
	if id == "submit" {
		return d.dialog.submit()
	}
	return false, nil
}

func (d *tuiBatchReview) Update(event vaxis.Event) (bool, error) {
	if mouse, ok := event.(vaxis.Mouse); ok {
		if mouse.EventType != vaxis.EventPress {
			return false, nil
		}
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			d.scroll = max(0, d.scroll-3)
		case vaxis.MouseWheelDown:
			d.scroll += 3
		case vaxis.MouseLeftButton:
			for _, control := range d.controls {
				if control.rect.contains(mouse.Col, mouse.Row) {
					return d.activate(control.id)
				}
			}
		}
		return false, nil
	}
	key, ok := event.(vaxis.Key)
	if !ok || key.EventType == vaxis.EventPaste {
		d.dialog.updateInput(event)
		return false, nil
	}
	if !tuiKeyActivates(key, false) {
		return false, nil
	}
	switch {
	case isOverlayCancelKey(key):
		if tuiKeyActivates(key, true) {
			return true, nil
		}
	case key.Matches(vaxis.KeyPgUp):
		d.scroll = max(0, d.scroll-max(1, d.height-1))
	case key.Matches(vaxis.KeyPgDown):
		d.scroll += max(1, d.height-1)
	case key.Matches(vaxis.KeyEnter), key.Matches('s', vaxis.ModCtrl), len(d.dialog.Fields) == 0 && key.Matches('s'):
		if tuiKeyActivates(key, true) {
			return d.activate("submit")
		}
	default:
		d.dialog.updateInput(event)
	}
	return false, nil
}

func (d *tuiBatchReview) PreferredSize() (int, int) { return 84, min(30, 14+len(d.targets)*2) }
func (d *tuiBatchReview) FooterHint() string {
	return "Ctrl+s run · PgUp/PgDn review all targets · Esc cancel"
}

func (d *tuiBatchReview) Draw(win vaxis.Window, _, _, _ vaxis.Style) {
	t := d.app.managerTheme().modal()
	accent := t.amber
	if len(d.dialog.Fields) > 0 {
		accent = t.coral
	}
	d.controls = nil
	tuiFill(win, t.raised)
	body := border.All(win, accent)
	w, h := body.Size()
	tuiLine(win.New(2, 0, max(0, w-2), 1), 0, " "+plainLogText(d.dialog.Title)+" ", accent)
	d.tooSmall = w < 28 || h < 10
	if d.tooSmall {
		tuiLine(body, 1, "Resize to review · Esc cancel", t.text)
		return
	}
	content := body.New(1, 0, w-2, h)
	cw, _ := content.Size()
	var rows []tuiConfirmationLine
	for _, line := range d.lines(t) {
		wrapped := helpDescriptionRows(plainLogText(line.text), cw)
		if len(wrapped) == 0 {
			wrapped = []string{""}
		}
		for _, text := range wrapped {
			rows = append(rows, tuiConfirmationLine{text, line.style})
		}
	}
	d.height = h - 6
	d.scroll = min(max(0, d.scroll), max(0, len(rows)-d.height))
	for i, row := range rows[d.scroll:min(len(rows), d.scroll+d.height)] {
		tuiLine(content, i, row.text, row.style)
	}
	if len(rows) > d.height {
		tuiLine(content, h-6, fmt.Sprintf("PgUp/PgDn · %d–%d/%d", d.scroll+1, min(len(rows), d.scroll+d.height), len(rows)), t.dim)
	}
	if len(d.dialog.Fields) > 0 {
		tuiLine(content, h-5, d.deletePrompt(), t.muted)
		input := d.dialog.Fields[0].Input
		input.Content = t.text
		input.Draw(content.New(0, h-4, min(36, cw), 1))
	}
	tuiLine(content, h-3, plainLogText(d.dialog.Error), t.coral)
	button := fmt.Sprintf("[Ctrl+s Run %d]", len(d.targets))
	kind := d.targets[0].Kind
	if !slices.ContainsFunc(d.targets, func(target NodeBatchTarget) bool { return target.Kind != kind }) {
		button = fmt.Sprintf("[Ctrl+s %s %d]", kind, len(d.targets))
	}
	for _, c := range []struct {
		id, text string
		col      int
	}{{"submit", button, max(0, cw-len(button)-16)}, {"cancel", "[Esc Cancel]", cw - 12}} {
		style := t.muted
		if c.id == "submit" && d.validate() == nil && (len(d.dialog.Fields) == 0 || d.dialog.Fields[0].Input.String() == strconv.Itoa(len(d.targets))) {
			style = accent
		}
		tuiLine(content.New(c.col, h-1, len(c.text), 1), 0, c.text, style)
		x, y := content.Origin()
		d.controls = append(d.controls, tuiCommandTarget{rect: tuiRect{col: x + c.col, row: y + h - 1, width: len(c.text), height: 1}, id: c.id})
	}
}
