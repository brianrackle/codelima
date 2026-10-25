package codelima

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func TestTUIManagerShellChooserGroupsAvailabilityAndDefault(t *testing.T) {
	t.Setenv("TMUX", "")
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a, _ := newTestManager(t, size[0], size[1])
		cfg := DefaultConfig("")
		cfg.Shell.Window = "codelima-missing-window-for-test {cmd}"
		a.preferences = &cfg
		a.openShellChooser()
		h := a.overlay.(*tuiShellChooser)
		a.draw()
		text := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Open a shell", "NEW SESSION", "Attach here", "New window", "Host shell here", "x does attach"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %q at %v:\n%s", want, size, text)
			}
		}
		for _, id := range []string{"window", "tmux", "embedded"} {
			choice := h.choices[slices.IndexFunc(h.choices, func(c tuiShellChoice) bool { return c.id == id })]
			if choice.unavailable == "" {
				t.Fatal("unavailable mode enabled", id)
			}
		}
		_, _ = h.Update(vaxis.Key{Keycode: 'e', Text: "e"})
		if h.err == "" || a.rawShellPending || a.nodeDetail {
			t.Fatal("disabled embedded choice changed view or opened shell")
		}
	}
}

func TestTUIManagerShellChooserSessionShortcutsNeverRetarget(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.selectedEntry().node
	first := &tuiSession{label: "first", shellKind: terminal.NodeHostShell}
	second := &tuiSession{label: "second", shellKind: terminal.NodeHostShell}
	firstKey := putTestSession(a.sessions, nodeTargetKey(node.ID), first, newFakeTUITerminal())
	putTestSession(a.sessions, nodeTargetKey(node.ID), second, newFakeTUITerminal())
	a.openShellChooser()
	h := a.overlay.(*tuiShellChooser)
	var ran tuiShellChoice
	h.run = func(choice tuiShellChoice, _ bool) error { ran = choice; return nil }
	a.draw()
	if err := a.sessions.MoveTab(nodeTargetKey(node.ID), firstKey, 1); err != nil {
		t.Fatal(err)
	}
	_, _ = h.Update(vaxis.Key{Keycode: '1', Text: "1"})
	if ran.terminalID != string(first.terminalID) {
		t.Fatal("reordering redirected the original shortcut", ran)
	}
	ran = tuiShellChoice{}
	a.handleTerminalClosed(tuiTerminalClosedEvent{SessionKey: firstKey})
	_, _ = h.Update(vaxis.Key{Keycode: '1', Text: "1"})
	if ran.id != "" || !strings.Contains(h.err, "ended") {
		t.Fatal("closed ordinal attached a replacement", ran, h.err)
	}
	_, _ = h.Update(vaxis.Key{Keycode: '2', Text: "2"})
	if ran.terminalID != string(second.terminalID) {
		t.Fatal("remaining ordinal changed identity", ran)
	}
}

func TestTUIManagerShellChooserNewChoicesAreFreshAndPressOnly(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.openShellChooser()
	h := a.overlay.(*tuiShellChooser)
	runs := 0
	h.run = func(choice tuiShellChoice, save bool) error {
		runs++
		if !choice.fresh || choice.mode != "attach" || !save {
			t.Fatal("new-session default request lost intent", choice, save)
		}
		return nil
	}
	for _, event := range []vaxis.EventType{vaxis.EventPaste, vaxis.EventRelease, vaxis.EventRepeat, vaxis.EventPress} {
		_, _ = h.Update(vaxis.Key{Keycode: vaxis.KeyEnter, Modifiers: vaxis.ModShift, EventType: event})
	}
	if runs != 1 {
		t.Fatal("activation was repeated or consumed pasted input", runs)
	}
}

func TestTUIManagerShellChooserCompactSessionsResizeAndStaleMouse(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.selectedEntry().node
	var lastKey, lastID string
	for i := range 14 {
		session := &tuiSession{label: fmt.Sprintf("task-%02d", i), shellKind: terminal.NodeHostShell, createdAt: time.Now().Add(-time.Hour)}
		lastKey = putTestSession(a.sessions, nodeTargetKey(node.ID), session, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{Title: session.label}})
		lastID = string(session.terminalID)
	}
	a.openShellChooser()
	h := a.overlay.(*tuiShellChooser)
	runs := 0
	h.run = func(tuiShellChoice, bool) error { runs++; return nil }
	_, _ = h.Update(vaxis.Key{Keycode: vaxis.KeyEnd})
	for _, size := range []vaxis.Resize{{Cols: 150, Rows: 38}, {Cols: 80, Rows: 24}, {Cols: 60, Rows: 14}, {Cols: 40, Rows: 10}, {Cols: 150, Rows: 38}} {
		_, _ = a.handleEvent(size)
		a.draw()
		if h.choices[h.index].terminalID != lastID {
			t.Fatal("resize changed the selected session")
		}
		if size.Cols < 60 {
			continue
		}
		if !strings.Contains(renderedScreenText(t, a.vx, size.Cols, size.Rows), "task-13") {
			t.Fatal("unnumbered session became unreachable", size)
		}
		rows := h.visualRows(min(104, size.Cols-4) - 2)
		if row := rows[h.scroll]; row.index >= 0 && row.hint != "" {
			t.Fatal("viewport starts with orphaned context", size)
		}
		for _, row := range h.rows {
			if row.rect.col < 0 || row.rect.col+row.rect.width > size.Cols || row.rect.row >= size.Rows {
				t.Fatal("mouse target outside viewport", row)
			}
		}
	}
	row := h.rows[slices.IndexFunc(h.rows, func(r tuiCommandTarget) bool { return r.id == "session:"+lastID })]
	a.handleTerminalClosed(tuiTerminalClosedEvent{SessionKey: lastKey})
	_, _ = h.Update(vaxis.Mouse{Col: row.rect.col, Row: row.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if runs != 0 || !strings.Contains(h.err, "ended") {
		t.Fatal("stale mouse row attached another session", runs, h.err)
	}
	_, _ = h.Update(vaxis.Key{Keycode: vaxis.KeyHome})
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 150, 38), "NEW SESSION") {
		t.Fatal("Home hid the first group heading")
	}
}

func TestTUIManagerShellChooserObservedActivityAndRemovedNode(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.selectedEntry().node
	session := &tuiSession{label: "task", shellKind: terminal.NodeHostShell}
	putTestSession(a.sessions, nodeTargetKey(node.ID), session, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{Title: "task", ProgressState: 1, BellCount: 1}})
	a.openShellChooser()
	h := a.overlay.(*tuiShellChooser)
	choice := h.choices[5]
	if !strings.Contains(choice.hint, "bell") || !strings.Contains(choice.hint, "working") || !strings.Contains(choice.hint, "age —") || choice.kind != "host" || choice.label != "task" {
		t.Fatal("invented or lost session evidence", choice)
	}
	session.acknowledgedBells = 1
	h.refresh()
	if strings.Contains(h.choices[5].hint, "bell") || h.choices[5].label != "task" {
		t.Fatal("acknowledged bell remains advertised", h.choices[5])
	}
	a.daemonDisconnected = true
	h.refresh()
	if strings.Contains(h.choices[5].hint, "working") || h.choices[5].unavailable == "" {
		t.Fatal("disconnected metadata claimed current activity")
	}
	a.daemonDisconnected = false
	a.state.nodes = slices.DeleteFunc(a.state.nodes, func(n Node) bool { return n.ID == node.ID })
	runs := 0
	h.run = func(tuiShellChoice, bool) error { runs++; return nil }
	_, _ = h.Update(vaxis.Key{Keycode: 'h', Text: "h"})
	if runs != 0 || !strings.Contains(h.err, "no longer") {
		t.Fatal("removed node opened a shell in another directory")
	}
}

func TestTUIManagerShellChooserEmbeddedOptInCreatesFreshTab(t *testing.T) {
	a, manager := newTestManager(t, 80, 24)
	cfg := DefaultConfig("")
	cfg.Shell.Mode = "embedded"
	a.preferences = &cfg
	if err := a.openEmbeddedShell(false); err != nil {
		t.Fatal(err)
	}
	target := a.state.selectedEntry().key()
	before := manager.opened[target]
	a.state.focusTree()
	a.openShellChooser()
	h := a.overlay.(*tuiShellChooser)
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	if closed, err := h.Update(vaxis.Key{Keycode: 'e'}); err != nil || !closed || h.err != "" {
		t.Fatal(closed, err, h.err)
	}
	if manager.opened[target] != before+1 || a.state.focus != tuiFocusTerminal || a.state.selectedEntry().key() != target {
		t.Fatal("new embedded choice reused a session or lost focus", manager.opened)
	}
}
