package codelima

import (
	"context"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIResizeUpdatesScreenAndTerminalTogether(t *testing.T) {
	for _, mode := range []string{"terminal", "tree", "overlay"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			service, _ := newTestService(t)
			sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
			state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
			if err != nil {
				t.Fatal(err)
			}
			if err := state.focusTerminal(); err != nil {
				t.Fatal(err)
			}
			terminal := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))
			vx := newRenderTestVaxis(t, 100, 24)
			t.Cleanup(vx.Close)
			app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: sessions, vx: vx}
			switch mode {
			case "tree":
				state.focus = tuiFocusTree
			case "overlay":
				app.overlay = newTUIMessagesView(nil)
			}
			app.draw()
			// Exercise growth, shrinkage, the minimum-size notice, recovery,
			// and a pixel-only change through the actual event/draw path.
			for _, size := range []vaxis.Resize{
				{Cols: 140, Rows: 40, XPixel: 1400, YPixel: 800},
				{Cols: 80, Rows: 20, XPixel: 800, YPixel: 400},
				{Cols: 40, Rows: 10, XPixel: 400, YPixel: 200},
				{Cols: 120, Rows: 30, XPixel: 1200, YPixel: 600},
				{Cols: 120, Rows: 30, XPixel: 1440, YPixel: 720},
			} {
				if quit, err := app.handleEvent(size); quit || err != nil {
					t.Fatalf("resize %+v: quit=%v err=%v", size, quit, err)
				}
				if got := vx.Size(); got != size {
					t.Fatalf("outer geometry = %+v, want %+v", got, size)
				}
				if cols, rows := vx.Window().Size(); cols != size.Cols || rows != size.Rows {
					t.Fatalf("drawing surface = %dx%d, want %dx%d", cols, rows, size.Cols, size.Rows)
				}
				if size.Cols < 60 || size.Rows < 14 {
					if got := renderedCellGrapheme(t, vx, 0, 0); got != "C" {
						t.Fatalf("minimum-size notice missing: first cell = %q", got)
					}
					continue
				}
				cols, rows := app.activeTerminalSize(size.Cols, size.Rows, app.effectiveLayoutFocus())
				if terminal.startCols != cols || terminal.startRows != rows {
					t.Fatalf("draw reverted terminal to %dx%d, want %dx%d", terminal.startCols, terminal.startRows, cols, rows)
				}
				if sessions.preferredCols != cols || sessions.preferredRows != rows {
					t.Fatalf("preferred terminal size = %dx%d, want %dx%d", sessions.preferredCols, sessions.preferredRows, cols, rows)
				}
			}
		})
	}
}

func TestTUIResizeUpdatesScreenWithoutSessions(t *testing.T) {
	t.Parallel()
	vx := newRenderTestVaxis(t, 80, 24)
	t.Cleanup(vx.Close)
	app := &vaxisTUIApp{vx: vx}
	size := vaxis.Resize{Cols: 120, Rows: 40, XPixel: 1200, YPixel: 800}
	app.handleResize(size)
	if got := vx.Size(); got != size {
		t.Fatalf("resize without sessions = %+v, want %+v", got, size)
	}
}
