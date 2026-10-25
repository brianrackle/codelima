package codelima

import (
	"time"

	"go.rockorager.dev/vaxis"
)

const tuiSpinnerInterval = time.Second / 10

var tuiSpinnerFrames = [...]rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

type tuiSpinnerCell struct {
	window vaxis.Window
	style  vaxis.Style
}

// Timers and cells belong to the UI event loop. No callback can paint while
// that loop is suspended for a raw shell or an external editor.
type tuiSpinnerState struct {
	cells     []tuiSpinnerCell
	timer     *time.Timer
	startedAt time.Time
	elapsed   time.Duration
}

func tuiSpinnerGlyph(elapsed time.Duration) string {
	return string(tuiSpinnerFrames[int(max(0, elapsed)/tuiSpinnerInterval)%len(tuiSpinnerFrames)])
}

func (a *vaxisTUIApp) drawSpinner(win vaxis.Window, style vaxis.Style) {
	// A nested window can lie outside a scrolled parent's clip even if its own
	// size is positive. Only visible cells should keep the timer alive.
	col, row := 0, 0
	for w := &win; w != nil; w = w.Parent {
		if col < 0 || row < 0 || col >= w.Width || row >= w.Height {
			return
		}
		col += w.Column
		row += w.Row
	}
	if a.spinners.startedAt.IsZero() {
		a.spinners.startedAt = time.Now()
	}
	cell := tuiSpinnerCell{window: win, style: style}
	a.spinners.cells = append(a.spinners.cells, cell)
	cell.draw(tuiSpinnerGlyph(a.spinners.elapsed))
}

func (c tuiSpinnerCell) draw(glyph string) {
	c.window.SetCell(0, 0, vaxis.Cell{Character: vaxis.Character{Grapheme: glyph, Width: 1}, Style: c.style})
}

func (a *vaxisTUIApp) spinnerTicks() <-chan time.Time {
	if a.spinners.timer == nil {
		return nil
	}
	return a.spinners.timer.C
}

func (a *vaxisTUIApp) stopSpinners() {
	if a.spinners.timer != nil {
		a.spinners.timer.Stop()
	}
	a.spinners = tuiSpinnerState{}
}

func (a *vaxisTUIApp) finishSpinnerFrame() {
	if len(a.spinners.cells) == 0 {
		a.stopSpinners()
		return
	}
	if a.spinners.timer == nil {
		a.spinners.timer = time.NewTimer(tuiSpinnerInterval)
	}
}

func (a *vaxisTUIApp) tickSpinners(now time.Time) {
	if len(a.spinners.cells) == 0 || a.vx == nil {
		a.stopSpinners()
		return
	}
	a.spinners.elapsed = max(a.spinners.elapsed, now.Sub(a.spinners.startedAt))
	glyph := tuiSpinnerGlyph(a.spinners.elapsed)
	for _, cell := range a.spinners.cells {
		cell.draw(glyph)
	}
	// Keep terminal images, cursor and other cells untouched; this is a partial
	// paint, not the start of a new graphics frame or a data refresh.
	a.vx.Render()
	if a.spinners.timer != nil {
		a.spinners.timer.Reset(tuiSpinnerInterval)
	} else {
		a.finishSpinnerFrame()
	}
}
