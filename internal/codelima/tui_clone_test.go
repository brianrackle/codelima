package codelima

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func testCloneReview(t *testing.T, width, height int) (*vaxisTUIApp, *tuiNodeConfirmation) {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	a.openCloneNodeDialog(a.state.nodes[0])
	d := a.overlay.(*tuiNodeConfirmation)
	a.applyCloneNames(tuiCloneNamesEvent{review: d, nodes: a.state.nodes})
	return a, d
}

func TestTUICloneSuggestionUsesAllNamesAndKeepsEdits(t *testing.T) {
	a, d := testCloneReview(t, 80, 24)
	base := d.node.Slug
	if got := d.dialog.Fields[0].Input.String(); got != base+"-2" {
		t.Fatal("missing default name", got)
	}
	d.clone.nameEdited = false
	d.dialog.SetFieldValue("node_slug", "")
	d.dialog.Error = "Name is required"
	all := slices.Clone(a.state.nodes)
	all = append(all, Node{Slug: base + "-2", DirectoryPath: "/another/workspace"})
	a.applyCloneNames(tuiCloneNamesEvent{review: d, nodes: all})
	if got := d.dialog.Fields[0].Input.String(); got != base+"-3" || d.dialog.Error != "" {
		t.Fatal("suggestion ignored a name outside the current workspace", got)
	}
	_, _ = d.Update(vaxis.Key{Keycode: 'x', Text: "x"})
	want := d.dialog.Fields[0].Input.String()
	a.applyCloneNames(tuiCloneNamesEvent{review: d, nodes: all})
	if got := d.dialog.Fields[0].Input.String(); got != want {
		t.Fatal("late names replaced an edit", got)
	}
	a.overlay = nil
	a.applyCloneNames(tuiCloneNamesEvent{review: d, err: errors.New("late failure")})
	if d.clone.err != nil {
		t.Fatal("dismissed review consumed a late result")
	}
}

func TestTUICloneValidationToggleAndCapturedSource(t *testing.T) {
	a, d := testCloneReview(t, 80, 24)
	var input NodeCloneInput
	d.run = func(n Node) error {
		input = NodeCloneInput{SourceNode: n.ID, NodeSlug: d.cloneName(), Start: d.clone.start}
		return nil
	}
	for _, name := range []string{"", "Bad Name", d.node.Slug} {
		d.dialog.SetFieldValue("node_slug", name)
		if done, _ := d.Update(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl}); done || input.SourceNode != "" {
			t.Fatal("invalid name submitted", name)
		}
	}
	d.dialog.SetFieldValue("node_slug", "clone-child")
	_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyTab})
	_, _ = d.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if !d.clone.start || d.cloneName() != "clone-child" {
		t.Fatal("checkbox edited the name instead of toggling start")
	}
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	if done, err := d.Update(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl}); !done || err != nil || input.SourceNode != d.node.ID || !input.Start {
		t.Fatal("clone lost its captured source or start choice", done, err, input)
	}
}

func TestTUICloneBlocksHiddenStaleAndRepeatedSubmission(t *testing.T) {
	for _, condition := range []string{"minimum size", "disconnected", "renamed", "loading", "names error", "repeat", "release", "paste"} {
		t.Run(condition, func(t *testing.T) {
			width, height := 80, 24
			if condition == "minimum size" {
				width, height = 60, 14
			}
			a, d := testCloneReview(t, width, height)
			called := false
			d.run = func(Node) error { called = true; return nil }
			key := vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl}
			switch condition {
			case "disconnected":
				a.daemonDisconnected = true
			case "renamed":
				a.state.nodes[0].Slug = "renamed"
			case "loading":
				d.clone.loaded = false
			case "names error":
				d.clone.err = errors.New("names unavailable")
			case "repeat":
				key.EventType = vaxis.EventRepeat
			case "release":
				key.EventType = vaxis.EventRelease
			case "paste":
				key.EventType = vaxis.EventPaste
			}
			a.draw()
			if done, _ := d.Update(key); done || called {
				t.Fatal("unreviewable clone submitted", condition)
			}
			if done, _ := d.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); !done {
				t.Fatal("Esc did not cancel")
			}
		})
	}
}

func TestTUICloneRendersTruthfulImpactAndMouseControls(t *testing.T) {
	for _, width := range []int{60, 80, 150} {
		a, d := testCloneReview(t, width, 28)
		a.state.nodes[0].LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
		a.state.nodes[0].Status = NodeStatusRunning
		a.draw()
		screen := renderedScreenText(t, a.vx, width, 28)
		for _, want := range []string{"Clone " + d.node.Slug, "Name", "Copies the disk", "stops for", "restarts", "Start clone when done", "Ctrl+s Clone", "Esc Cancel"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q at width %d:\n%s", want, width, screen)
			}
		}
		if strings.Contains(screen, "~40s") {
			t.Fatal("invented clone duration")
		}
		called := false
		d.run = func(Node) error { called = true; return nil }
		for _, id := range []string{"start-clone", "submit"} {
			found := false
			for _, control := range d.controls {
				if control.id == id {
					found = true
					_, _ = d.Update(vaxis.Mouse{Col: control.rect.col, Row: control.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
					break
				}
			}
			if !found {
				t.Fatal("missing mouse control", id)
			}
		}
		if !called || !d.clone.start {
			t.Fatal("mouse did not toggle and submit")
		}
	}
}
