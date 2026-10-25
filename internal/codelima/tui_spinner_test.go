package codelima

import (
	"context"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func TestTUISpinnerFrames(t *testing.T) {
	if tuiSpinnerInterval != 100*time.Millisecond {
		t.Fatal("spinner must run at 10 fps")
	}
	for i, glyph := range []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏⠋") {
		if got := tuiSpinnerGlyph(time.Duration(i) * tuiSpinnerInterval); got != string(glyph) {
			t.Fatalf("frame %d = %q", i, got)
		}
	}
	if tuiSpinnerGlyph(-time.Second) != "⠋" {
		t.Fatal("negative elapsed time regressed frame")
	}
}

func TestTUISpinnerTickOnlyChangesRegisteredCells(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	defer a.stopSpinners()
	a.draw()
	theme := newTUITheme(true)
	style := theme.selected
	style.Foreground = theme.amber.Foreground
	tuiLine(a.vx.Window(), 5, "keep text", theme.text)
	a.drawSpinner(a.vx.Window().New(10, 5, 1, 1), style)
	a.finishSpinnerFrame()
	if a.spinnerTicks() == nil {
		t.Fatal("visible work has no timer")
	}
	passes := a.drawPasses
	started := a.spinners.startedAt
	a.tickSpinners(started.Add(3 * tuiSpinnerInterval))
	if got := renderedCellGrapheme(t, a.vx, 10, 5); got != "⠸" {
		t.Fatalf("frame = %q", got)
	}
	if a.drawPasses != passes || renderedCellGrapheme(t, a.vx, 0, 5) != "k" {
		t.Fatal("spinner rebuilt the screen")
	}
	if renderedCellStyle(t, a.vx, 10, 5) != style {
		t.Fatal("spinner changed selection/colors")
	}
	a.tickSpinners(started.Add(tuiSpinnerInterval))
	if got := renderedCellGrapheme(t, a.vx, 10, 5); got != "⠸" {
		t.Fatalf("older tick regressed frame: %q", got)
	}
	a.draw()
	if a.spinnerTicks() != nil {
		t.Fatal("idle screen retained timer")
	}
	a.tickSpinners(started.Add(time.Second))
	if a.spinnerTicks() != nil || a.drawPasses != passes+1 {
		t.Fatal("stale tick restarted animation")
	}
}

func TestTUISpinnerClippingAndOverlayOwnership(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	defer a.stopSpinners()
	a.draw()
	if len(a.spinners.cells) == 0 {
		t.Fatal("running operation has no spinner")
	}
	a.overlay = newTUIPalette("Commands", nil)
	a.draw()
	if a.spinnerTicks() != nil {
		t.Fatal("covered spinners can overwrite overlay")
	}
	a.overlay = nil
	a.draw()
	if a.spinnerTicks() == nil {
		t.Fatal("closing overlay did not restore animation")
	}
	a.stopSpinners()
	a.drawSpinner(a.vx.Window().New(-1, 0, 2, 1), newTUITheme(false).amber)
	a.drawSpinner(a.vx.Window().New(0, 25, 1, 1), newTUITheme(false).amber)
	a.finishSpinnerFrame()
	if a.spinnerTicks() != nil {
		t.Fatal("clipped indicators started animation")
	}
}

func TestTUISpinnerServeCancelsTimer(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.draw()
	if a.spinnerTicks() == nil {
		t.Fatal("test needs active timer")
	}
	cancel()
	_ = a.serve(make(chan vaxis.Event))
	if a.spinnerTicks() != nil {
		t.Fatal("shutdown left spinner timer active")
	}
}

func TestTUISpinnerForegroundLoadingAndCompletion(t *testing.T) {
	a, form := testCreateForm(t)
	form.pending = true
	a.draw()
	if len(a.spinners.cells) != 1 {
		t.Fatalf("pending form has %d foreground spinners", len(a.spinners.cells))
	}
	form.pending = false
	a.draw()
	if a.spinnerTicks() != nil {
		t.Fatal("completed form still animates")
	}
	a, _ = newTestManager(t, 80, 24)
	a.view = tuiViewSystem
	a.doctor.loading = true
	a.draw()
	if len(a.spinners.cells) != 1 {
		t.Fatal("visible doctor check is not animated")
	}
	a.systemPanel = tuiSystemHost
	a.draw()
	if a.spinnerTicks() != nil {
		t.Fatal("hidden doctor check keeps drawing")
	}
	a.doctor.loading = false
	a.activity.page.Running = nil
	a.view = tuiViewActivity
	a.draw()
	if a.spinnerTicks() != nil {
		t.Fatal("history records animate as active work")
	}
	glyph, _ := newTUITheme(false).status("starting")
	if glyph != "◐" {
		t.Fatal("node status symbol became a spinner")
	}
}
