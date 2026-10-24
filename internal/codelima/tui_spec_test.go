package codelima

import (
	"context"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func testSpecApp(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	a.nodeDetail, a.detailTab = true, tuiDetailSpec
	node := a.state.selectedEntry().node
	node.Image, node.BootstrapCommands = "template:ubuntu", []string{"printf FIRST", "printf SECOND"}
	a.spec = tuiSpecState{nodeID: node.ID, loaded: true, snapshot: nodeSpecSnapshot{
		Node: node, Template: &Configuration{ID: "template-id", Slug: "current-template", MemoryMiB: 12288},
		Differences: []nodeSpecDifference{{"memory", "8 GiB", "12 GiB"}},
		LimaPath:    "/some path/instance.lima.yaml", LimaFields: []nodeSpecField{{"vmType", "qemu"}, {"mountType", "9p"}},
	}}
	return a
}

func TestTUISpecResponsivePanelsAndDriftIndicator(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a := testSpecApp(t, size[0], size[1])
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		if !strings.Contains(screen, "Spec •") || !strings.Contains(screen, "Frozen at creation") || !strings.Contains(screen, "template:ubuntu") {
			t.Fatal(screen)
		}
		if size[0] >= 120 && (!strings.Contains(screen, "12 GiB") || !strings.Contains(screen, "qemu")) {
			t.Fatal(screen)
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyRight})
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "12 GiB") {
			t.Fatal("compact drift panel inaccessible")
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyRight})
		a.draw()
		if !strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "qemu") {
			t.Fatal("Lima panel inaccessible")
		}
		a.spec.snapshot.Differences = nil
		a.draw()
		if strings.Contains(renderedScreenText(t, a.vx, size[0], size[1]), "Spec •") {
			t.Fatal("drift dot without drift")
		}
	}
}

func TestTUISpecActionsUseCurrentTemplateAndCopyYAMLPath(t *testing.T) {
	a := testSpecApp(t, 150, 38)
	var copied string
	a.clipboardPush = func(value string) error { copied = value; return nil }
	a.handleKey(vaxis.Key{Keycode: 'y', Text: "y"})
	if copied != a.spec.snapshot.LimaPath {
		t.Fatalf("copied %q", copied)
	}
	a.handleKey(vaxis.Key{Keycode: 'N', Text: "N"})
	if a.activeDialog() == nil || a.activeDialog().Fields[2].rawValue() != "current-template" || a.activeDialog().Fields[1].rawValue() != a.spec.snapshot.Node.DirectoryPath {
		t.Fatal("new node did not use current template and node directory")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != nil {
		t.Fatal("preset defaults prompted to discard unedited form")
	}
	a.handleKey(vaxis.Key{Keycode: 'e', Text: "e"})
	if a.activeDialog() == nil || a.activeDialog().Fields[4].rawValue() != "12288" {
		t.Fatal("edit did not use current template")
	}
	a.overlay = nil
	a.spec.snapshot.Template = nil
	a.handleKey(vaxis.Key{Keycode: 'N', Text: "N"})
	if a.overlay != nil {
		t.Fatal("missing template opened unrelated create")
	}
}

func TestTUISpecStepOpensPausedBootstrapLogsAndRejectsStaleData(t *testing.T) {
	a := testSpecApp(t, 80, 24)
	a.draw()
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnd})
	a.draw()
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if !a.logsVisible() || a.logs.source != 1 || a.logs.follow || a.logs.query != "printf SECOND" {
		t.Fatalf("step did not open command output: %+v", a.logs)
	}
	a.spec.serial, a.managerGeneration = 9, 4
	event := tuiSpecDataEvent{serial: 9, generation: 4, data: a.spec.snapshot}
	event.data.Differences = nil
	ctx, cancel := context.WithCancel(context.Background())
	a.spec.cancel = cancel
	a.switchView(tuiViewTemplates)
	a.applySpecData(event)
	if ctx.Err() == nil || len(a.spec.snapshot.Differences) == 0 {
		t.Fatal("navigation accepted stale spec")
	}
}

func TestTUISpecLongCommandsAndMouseActions(t *testing.T) {
	a := testSpecApp(t, 80, 24)
	a.spec.snapshot.Node.BootstrapCommands = []string{strings.Repeat("printf '界 👩🏽‍💻'\n", 60) + "printf COMMAND-END"}
	a.draw()
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnd})
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "COMMAND-END") {
		t.Fatal("end of long command is inaccessible")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown, Modifiers: vaxis.ModCtrl})
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown, Modifiers: vaxis.ModCtrl})
	a.draw()
	if a.spec.panel != 2 {
		t.Fatal("Ctrl-Down did not focus Lima")
	}
	var copied string
	a.clipboardPush = func(value string) error { copied = value; return nil }
	for i, row := range a.spec.rows[2] {
		if row.action != 'y' {
			continue
		}
		a.spec.cursor[2] = i
		a.draw()
		rect := a.spec.rects[2]
		a.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row + i - a.spec.offset[2], Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	}
	if copied != a.spec.snapshot.LimaPath {
		t.Fatalf("mouse copy=%q", copied)
	}
}
