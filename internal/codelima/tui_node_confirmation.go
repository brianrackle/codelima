package codelima

import (
	"fmt"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

// A review owns its node and optional attachment identity until it closes.
// Refreshes update evidence, never the target of a pending destructive action.
type tuiNodeConfirmation struct {
	app                       *vaxisTUIApp
	node                      Node
	action                    tuiActionID
	dialog                    *tuiDialog
	skipIdle, initialSkipIdle bool
	attachID, attachLabel     string
	scroll, height, totalRows int
	controls                  []tuiCommandTarget
	tooSmall                  bool
	run                       func(Node) error
	attach                    func(string) error
}

func (a *vaxisTUIApp) newNodeConfirmation(node Node, action tuiActionID, run func(Node) error) *tuiNodeConfirmation {
	d := &tuiNodeConfirmation{app: a, node: node, action: action, run: run}
	label := "Stop"
	var fields []tuiDialogField
	if action == tuiActionNodeDelete {
		label = "Delete"
		fields = []tuiDialogField{newTUIInputField("confirm", "Type "+node.Slug+" to confirm", "", true)}
	}
	d.dialog = newTUIDialog(label+" "+node.Slug+"?", label, nil, fields, d.submit)
	d.skipIdle = a.managerSettings().ConfirmStop == "busy"
	d.initialSkipIdle = d.skipIdle
	d.attach = func(id string) error { return a.startShellModeForNode(node.ID, "attach", false, false, id) }
	// Prefer observed work; otherwise let the user inspect the first guest.
	for _, record := range a.fleetSessions(node) {
		if !record.kindKnown || record.kind != terminal.NodeShell {
			continue
		}
		if session, ok := a.sessions.Session(record.key); ok && session.terminalID != "" {
			if d.attachID == "" || record.activity == "working" {
				d.attachID, d.attachLabel = string(session.terminalID), plainLogText(record.label)
			}
			if record.activity == "working" {
				break
			}
		}
	}
	return d
}

func (a *vaxisTUIApp) openStopNodeDialog(node Node) {
	a.overlay = a.newNodeConfirmation(node, tuiActionNodeStop, a.stopNode)
}

func (d *tuiNodeConfirmation) currentNode() (Node, error) {
	if d.app.daemonDisconnected {
		return Node{}, fmt.Errorf("daemon unavailable; reconnect before confirming")
	}
	entry, err := d.app.paletteNodeAction(d.node.ID, d.action)
	if err != nil {
		return Node{}, err
	}
	if entry.node.Slug != d.node.Slug {
		return Node{}, fmt.Errorf("node name changed; cancel and review it again")
	}
	return entry.node, nil
}

func (d *tuiNodeConfirmation) ready() bool {
	if _, err := d.currentNode(); err != nil {
		return false
	}
	return d.action != tuiActionNodeDelete || d.dialog.Fields[0].Input.String() == d.node.Slug
}

func (d *tuiNodeConfirmation) submit(map[string]string) error {
	node, err := d.currentNode()
	if err != nil {
		return err
	}
	// Do not use rawValue: trimming would accept a different confirmation text.
	if d.action == tuiActionNodeDelete && d.dialog.Fields[0].Input.String() != d.node.Slug {
		return fmt.Errorf("type %s exactly to confirm", d.node.Slug)
	}
	if d.action == tuiActionNodeStop && d.skipIdle != d.initialSkipIdle {
		value := "always"
		if d.skipIdle {
			value = "busy"
		}
		return d.app.saveManagerSetting("confirm_stop", value, func(*vaxisTUIApp) error {
			// The settings write is asynchronous. Revalidate before the stop.
			current, err := d.currentNode()
			if err != nil {
				return err
			}
			return d.run(current)
		})
	}
	return d.run(node)
}

func (d *tuiNodeConfirmation) attachFirst() error {
	if _, err := d.currentNode(); err != nil {
		return err
	}
	if d.attachID == "" {
		return fmt.Errorf("no guest session available to attach")
	}
	for _, record := range d.app.fleetSessions(d.node) {
		if session, ok := d.app.sessions.Session(record.key); ok && string(session.terminalID) == d.attachID && record.kindKnown && record.kind == terminal.NodeShell {
			return d.attach(d.attachID)
		}
	}
	return fmt.Errorf("that session has ended; cancel and review again")
}

func (d *tuiNodeConfirmation) activate(id string) (bool, error) {
	if id != "cancel" && !d.readable() {
		d.dialog.Error = "resize to review before confirming"
		return false, nil
	}
	switch id {
	case "cancel":
		return true, nil
	case "submit":
		return d.dialog.submit()
	case "skip-idle":
		d.skipIdle = !d.skipIdle
	case "attach":
		d.dialog.Error = ""
		if err := d.attachFirst(); err != nil {
			d.dialog.Error = err.Error()
		}
	}
	return false, nil
}

func (d *tuiNodeConfirmation) readable() bool {
	if d.tooSmall {
		return false
	}
	if d.app.vx != nil {
		width, height := d.app.vx.Window().Size()
		// The manager skips overlays below its global minimum. Also reserve
		// its four-row margin and the border around ten usable review rows.
		return width >= tuiMinWidth && height >= max(tuiMinHeight, 16)
	}
	return true
}

func (d *tuiNodeConfirmation) Update(event vaxis.Event) (bool, error) {
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
	case key.Matches(vaxis.KeyPgUp):
		d.scroll = max(0, d.scroll-max(1, d.height-1))
	case key.Matches(vaxis.KeyPgDown):
		d.scroll += max(1, d.height-1)
	case key.Matches(vaxis.KeyHome) && d.action == tuiActionNodeStop:
		d.scroll = 0
	case key.Matches(vaxis.KeyEnd) && d.action == tuiActionNodeStop:
		d.scroll = d.totalRows
	case isOverlayCancelKey(key):
		if tuiKeyActivates(key, true) {
			return d.activate("cancel")
		}
	case key.Matches(vaxis.KeyEnter), key.Matches('s', vaxis.ModCtrl), d.action == tuiActionNodeStop && key.Matches('s'):
		if tuiKeyActivates(key, true) {
			return d.activate("submit")
		}
	case d.action == tuiActionNodeStop && key.Matches(' '):
		if tuiKeyActivates(key, true) {
			return d.activate("skip-idle")
		}
	case d.action == tuiActionNodeStop && key.Matches('x'):
		if tuiKeyActivates(key, true) {
			return d.activate("attach")
		}
	default:
		d.dialog.updateInput(event)
	}
	return false, nil
}

func (d *tuiNodeConfirmation) PreferredSize() (int, int) {
	width := 84
	if d.app.vx != nil {
		cols, _ := d.app.vx.Window().Size()
		width = min(width, cols-4)
	}
	rows := 0
	for _, line := range d.lines(d.app.managerTheme()) {
		rows += max(1, len(helpDescriptionRows(plainLogText(line.text), max(1, width-4))))
	}
	return 84, min(26, max(14, rows+8))
}

func (d *tuiNodeConfirmation) FooterHint() string {
	if d.action == tuiActionNodeDelete {
		return "Enter delete after exact name · PgUp/PgDn review · Esc cancel"
	}
	return "s stop · x inspect guest · Space toggle · PgUp/PgDn review · Esc cancel"
}
