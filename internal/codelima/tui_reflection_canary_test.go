package codelima

// Canary tests for the two places we reflect into unexported vaxis internals
// (plan §0.8 "Stop reflecting into vaxis internals"):
//
//  1. renderedHyperlinkAt (tui_runtime.go) reads
//     (*vaxis.Vaxis).screenNext.buf[row][col].Style.Hyperlink.
//  2. vaxisTUITerminal.CapturesMouse (tui_terminal_vaxis.go) reads
//     (*term.Model).mode.{mouseButtons,mouseDrag,mouseMotion,mouseSGR}
//     via vaxisTerminalModeField.
//
// Both reflection helpers deliberately degrade to "not found / false" when a
// field is missing, so an upstream vaxis rename would otherwise fail
// *silently*. These canaries drive the real reflection paths against a real
// vaxis instance / real term.Model so any internal rename fails loudly here
// instead. If one of these tests fails after a vaxis bump, do not "fix the
// test": the reflection helpers must be updated (or, better, replaced by the
// upstream accessor APIs proposed in plan §0.8).

import (
	"os/exec"
	"reflect"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"git.sr.ht/~rockorager/vaxis/widgets/term"
)

const hyperlinkReflectionBrokenMsg = "vaxis internals changed — renderedHyperlinkAt reflection broke; see tui_runtime.go and upstream accessor proposal (plan §0.8)"

const termModeReflectionBrokenMsg = "vaxis internals changed — CapturesMouse reflection broke; see tui_terminal_vaxis.go (vaxisTerminalModeField) and upstream accessor proposal (plan §0.8)"

// TestVaxisHyperlinkReflectionStillValid writes a hyperlink-styled cell into a
// real vaxis screen via the public Window.SetCell API and asserts the
// reflection helper recovers it from vaxis's unexported screen buffer.
func TestVaxisHyperlinkReflectionStillValid(t *testing.T) {
	vx := newRenderTestVaxis(t, 80, 24)
	defer vx.Close()

	const target = "https://example.com/reflection-canary"
	vx.Window().SetCell(3, 2, vaxis.Cell{
		Character: vaxis.Character{Grapheme: "L", Width: 1},
		Style: vaxis.Style{
			Hyperlink:       target,
			HyperlinkParams: "id=canary",
		},
	})

	got, ok := renderedHyperlinkAt(vx, 3, 2)
	if !ok {
		t.Fatalf("renderedHyperlinkAt(vx, 3, 2) found no hyperlink for a cell written via Window.SetCell: %s", hyperlinkReflectionBrokenMsg)
	}
	if got != target {
		t.Fatalf("renderedHyperlinkAt(vx, 3, 2) = %q, want %q: %s", got, target, hyperlinkReflectionBrokenMsg)
	}

	// Cells without a hyperlink (and out-of-bounds lookups) must keep
	// reporting "no link" — the existing consumer relies on that.
	if _, ok := renderedHyperlinkAt(vx, 0, 0); ok {
		t.Fatalf("renderedHyperlinkAt(vx, 0, 0) reported a hyperlink for an empty cell")
	}
	if _, ok := renderedHyperlinkAt(vx, 200, 2); ok {
		t.Fatalf("renderedHyperlinkAt out-of-bounds column reported a hyperlink")
	}
	if _, ok := renderedHyperlinkAt(vx, 3, 200); ok {
		t.Fatalf("renderedHyperlinkAt out-of-bounds row reported a hyperlink")
	}
	if _, ok := renderedHyperlinkAt(nil, 0, 0); ok {
		t.Fatalf("renderedHyperlinkAt(nil, 0, 0) reported a hyperlink")
	}
}

// TestVaxisTermModeReflectionStillValid asserts that the unexported mode
// fields CapturesMouse depends on still exist on term.Model with the expected
// kind. This presence check is done with direct reflection because the
// production helper (vaxisTerminalModeField) intentionally returns false both
// for "mode off" and "field missing" — a fresh model returning false proves
// nothing by itself.
func TestVaxisTermModeReflectionStillValid(t *testing.T) {
	t.Parallel()

	model := term.New()

	modeValue := reflect.ValueOf(model).Elem().FieldByName("mode")
	if !modeValue.IsValid() {
		t.Fatalf("term.Model has no field %q: %s", "mode", termModeReflectionBrokenMsg)
	}
	if modeValue.Kind() != reflect.Struct {
		t.Fatalf("term.Model field %q is %v, want struct: %s", "mode", modeValue.Kind(), termModeReflectionBrokenMsg)
	}

	for _, name := range []string{"mouseButtons", "mouseDrag", "mouseMotion", "mouseSGR"} {
		field := modeValue.FieldByName(name)
		if !field.IsValid() {
			t.Fatalf("term.Model mode has no field %q: %s", name, termModeReflectionBrokenMsg)
		}
		if field.Kind() != reflect.Bool {
			t.Fatalf("term.Model mode field %q is %v, want bool: %s", name, field.Kind(), termModeReflectionBrokenMsg)
		}
		// The production helper must agree with the direct lookup on a
		// fresh model: field present, mode off.
		if vaxisTerminalModeField(modeValue, name) {
			t.Fatalf("vaxisTerminalModeField(mode, %q) = true on a fresh term.Model, want false", name)
		}
	}

	// Behavior on a fresh model: no mouse mode set, so no capture.
	terminal := &vaxisTUITerminal{model: model}
	if terminal.CapturesMouse() {
		t.Fatalf("CapturesMouse() = true on a fresh term.Model, want false")
	}

	// Defensive paths must stay quiet rather than panic.
	if (&vaxisTUITerminal{}).CapturesMouse() {
		t.Fatalf("CapturesMouse() = true on a terminal with nil model, want false")
	}
	if vaxisTerminalModeField(modeValue, "noSuchModeField") {
		t.Fatalf("vaxisTerminalModeField(mode, noSuchModeField) = true, want false")
	}
}

// TestVaxisTermModeReflectionObservesMouseMode exercises the true case at the
// behavior level: mode fields are only ever mutated by term.Model's internal
// DECSET handling on the PTY parser goroutine (there is no public API to set
// them), so this test starts a real child that emits DECSET 1002 (mouse drag
// tracking) and exits. term.Model posts EventClosed strictly after parsing
// everything the child wrote, so once the close event arrives the mode write
// is visible and CapturesMouse must report true.
func TestVaxisTermModeReflectionObservesMouseMode(t *testing.T) {
	events := make(chan vaxis.Event, 64)
	terminal := newTUIVaxisTerminal("node:reflection-canary", func(event vaxis.Event) {
		select {
		case events <- event:
		default:
		}
	})
	terminal.Resize(80, 24)

	cmd := exec.Command("/bin/sh", "-c", `printf '\033[?1002h'`)
	if err := terminal.Start(cmd); err != nil {
		t.Skipf("cannot start PTY child in this environment: %v (field-presence canary TestVaxisTermModeReflectionStillValid still guards the reflection)", err)
	}
	defer terminal.Close()

	deadline := time.After(5 * time.Second)
	for {
		var event vaxis.Event
		select {
		case event = <-events:
		case <-deadline:
			t.Fatalf("timed out waiting for the DECSET child to exit")
		}
		if closed, ok := event.(tuiTerminalClosedEvent); ok {
			if closed.Err != nil {
				t.Fatalf("DECSET child exited with error: %v", closed.Err)
			}
			break
		}
	}

	if !terminal.CapturesMouse() {
		t.Fatalf("CapturesMouse() = false after the guest enabled DECSET 1002: %s", termModeReflectionBrokenMsg)
	}
}
