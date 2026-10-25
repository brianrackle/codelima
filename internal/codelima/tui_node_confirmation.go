package codelima

import (
	"fmt"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

// A review owns its node and optional attachment identity until it closes.
// Refreshes update evidence, never the target of a pending destructive action.
type tuiNodeConfirmation struct {
	tuiOverlayKeys
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
	clone                     *tuiCloneOptions
}

func (a *vaxisTUIApp) newNodeConfirmation(node Node, action tuiActionID, run func(Node) error) *tuiNodeConfirmation {
	d := &tuiNodeConfirmation{app: a, node: node, action: action, run: run}
	label := "Stop"
	var fields []tuiDialogField
	if action == tuiActionNodeDelete {
		label = "Delete"
		fields = []tuiDialogField{newTUIInputField("confirm", "Type "+node.Slug+" to confirm", "", true)}
	}
	title := label + " " + node.Slug + "?"
	if action == tuiActionNodeClone {
		label, title = "Clone", "Clone "+node.Slug
		fields = []tuiDialogField{newTUIInputField("node_slug", "Name", "", true)}
		d.clone = &tuiCloneOptions{}
	}
	d.dialog = newTUIDialog(title, label, nil, fields, d.submit)
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
	if d.clone != nil {
		return d.cloneNameError() == nil
	}
	return d.action != tuiActionNodeDelete || d.dialog.Fields[0].Input.String() == d.node.Slug
}

func (d *tuiNodeConfirmation) submit(values map[string]string) error {
	node, err := d.currentNode()
	if err != nil {
		return err
	}
	// Do not use rawValue: trimming would accept a different confirmation text.
	if d.action == tuiActionNodeDelete && d.dialog.Fields[0].Input.String() != d.node.Slug {
		return fmt.Errorf("type %s exactly to confirm", d.node.Slug)
	}
	if d.clone != nil {
		// Values is also the public dialog callback contract used by callers.
		d.dialog.SetFieldValue("node_slug", values["node_slug"])
		if err := d.cloneNameError(); err != nil {
			return err
		}
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
	case "start-clone":
		d.clone.start = !d.clone.start
		d.clone.checkFocus = true
	case "clone-name":
		d.clone.checkFocus = false
	case "attach":
		d.dialog.Error = ""
		if err := d.attachFirst(); err != nil {
			d.dialog.Error = err.Error()
		}
	}
	return false, nil
}

func (d *tuiNodeConfirmation) readable() bool {
	return tuiReviewReadable(d.app, d.tooSmall)
}

func tuiReviewReadable(app *vaxisTUIApp, tooSmall bool) bool {
	if tooSmall {
		return false
	}
	if app.vx != nil {
		width, height := app.vx.Window().Size()
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
		if d.clone != nil {
			d.updateCloneInput(event)
		} else {
			d.dialog.updateInput(event)
		}
		return false, nil
	}
	if !tuiKeyActivates(key, false) {
		return false, nil
	}
	switch {
	case overlayKeyAction(d, key) == "overlay.page_up":
		d.scroll = max(0, d.scroll-max(1, d.height-1))
	case overlayKeyAction(d, key) == "overlay.page_down":
		d.scroll += max(1, d.height-1)
	case overlayKeyAction(d, key) == "overlay.first" && d.action == tuiActionNodeStop:
		d.scroll = 0
	case overlayKeyAction(d, key) == "overlay.last" && d.action == tuiActionNodeStop:
		d.scroll = d.totalRows
	case overlayKeyAction(d, key) == "dialog.cancel":
		if tuiKeyActivates(key, true) {
			return d.activate("cancel")
		}
	case d.clone != nil && (overlayKeyAction(d, key) == "review.next" || overlayKeyAction(d, key) == "dialog.previous"):
		d.clone.checkFocus = !d.clone.checkFocus
	case d.clone != nil && overlayKeyAction(d, key) == "dialog.accept":
		if tuiKeyActivates(key, true) {
			if d.clone.checkFocus {
				return d.activate("start-clone")
			}
			d.clone.checkFocus = true
		}
	case d.clone != nil && d.clone.checkFocus && overlayKeyAction(d, key) == "dialog.toggle":
		if tuiKeyActivates(key, true) {
			return d.activate("start-clone")
		}
	case overlayKeyAction(d, key) == "dialog.accept", overlayKeyAction(d, key) == "dialog.submit", d.action == tuiActionNodeStop && overlayKeyAction(d, key) == "review.run":
		if tuiKeyActivates(key, true) {
			return d.activate("submit")
		}
	case d.action == tuiActionNodeStop && overlayKeyAction(d, key) == "review.skip_idle":
		if tuiKeyActivates(key, true) {
			return d.activate("skip-idle")
		}
	case d.action == tuiActionNodeStop && overlayKeyAction(d, key) == "review.attach":
		if tuiKeyActivates(key, true) {
			return d.activate("attach")
		}
	default:
		if d.clone != nil {
			d.updateCloneInput(event)
		} else {
			d.dialog.updateInput(event)
		}
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
	if d.clone != nil {
		rows += 3
	}
	return 84, min(26, max(14, rows+8))
}

func (d *tuiNodeConfirmation) FooterHint() string {
	if d.clone != nil {
		return d.bindingHints("{dialog.submit} clone · {review.next} name/start · {dialog.toggle} toggle · {overlay.page_up overlay.page_down} review · {dialog.cancel} cancel")
	}
	if d.action == tuiActionNodeDelete {
		return d.bindingHints("{dialog.accept} delete after exact name · {overlay.page_up overlay.page_down} review · {dialog.cancel} cancel")
	}
	return d.bindingHints("{review.run} stop · {review.attach} inspect guest · {review.skip_idle} toggle · {overlay.page_up overlay.page_down} review · {dialog.cancel} cancel")
}
