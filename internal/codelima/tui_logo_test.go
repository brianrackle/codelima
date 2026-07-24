package codelima

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

func TestTUILogoAnimationSettlesLeftToRightEveryThirdSecond(t *testing.T) {
	t.Parallel()

	animation := newTUILogoAnimation()
	targetRunes := []rune(tuiLogoText)

	for settled := 0; settled <= len(targetRunes); settled++ {
		elapsed := time.Duration(settled) * tuiLogoSettleInterval
		complete := animation.advance(elapsed)
		got := animation.text()
		gotRunes := []rune(got)

		if len(gotRunes) != len(targetRunes) {
			t.Fatalf("logo at step %d has %d runes, want %d: %q", settled, len(gotRunes), len(targetRunes), got)
		}
		if prefix := string(targetRunes[:settled]); !strings.HasPrefix(got, prefix) {
			t.Fatalf("logo at step %d = %q, want settled prefix %q", settled, got, prefix)
		}
		for index := settled; index < len(targetRunes); index++ {
			if gotRunes[index] == targetRunes[index] {
				t.Fatalf("logo at step %d prematurely settled rune %d in %q", settled, index, got)
			}
		}
		if complete != (settled == len(targetRunes)) {
			t.Fatalf("complete at step %d = %t", settled, complete)
		}
	}
}

func TestTUILogoAnimationShufflesOnlyUnsettledCharacters(t *testing.T) {
	t.Parallel()

	animation := newTUILogoAnimation()
	animation.advance(2 * tuiLogoSettleInterval)
	before := animation.text()

	animation.advance(2*tuiLogoSettleInterval + tuiLogoSpinInterval)
	after := animation.text()

	if before[:2] != "Co" || after[:2] != "Co" {
		t.Fatalf("settled prefix changed while shuffling: before %q, after %q", before, after)
	}
	if before[2:] == after[2:] {
		t.Fatalf("unsettled suffix did not shuffle: before %q, after %q", before, after)
	}
}

func TestTUILogoAnimationNeverRegressesOnOlderTick(t *testing.T) {
	t.Parallel()

	animation := newTUILogoAnimation()
	animation.advance(4 * tuiLogoSettleInterval)
	want := animation.text()

	animation.advance(tuiLogoSettleInterval)
	if got := animation.text(); got != want {
		t.Fatalf("older animation tick regressed logo from %q to %q", want, got)
	}
}

func TestTUILogoAnimationTickCompletesWithoutQuitting(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{logoAnimation: newTUILogoAnimation()}
	quit, err := app.handleEvent(tuiLogoAnimationTickEvent{Elapsed: tuiLogoAnimationDuration})
	if err != nil {
		t.Fatalf("handleEvent(logo tick) error = %v", err)
	}
	if quit {
		t.Fatal("logo tick requested TUI quit")
	}
	if app.logoAnimation != nil {
		t.Fatal("completed logo animation remained active")
	}
	if got := app.headerLogoText(); got != tuiLogoText {
		t.Fatalf("completed header logo = %q, want %q", got, tuiLogoText)
	}
}

func TestTUILogoAnimationTickRedrawsOnlyFixedWidthHeaderMark(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	state, err := newTUIState(testTUINodes(t), newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	vx := newRenderTestVaxis(t, 100, 24)
	defer vx.Close()

	app := &vaxisTUIApp{
		ctx:           ctx,
		service:       service,
		state:         state,
		sessions:      newTUISessionStore(ctx, service, func(vaxis.Event) {}),
		vx:            vx,
		logoAnimation: newTUILogoAnimation(),
	}
	defer app.sessions.Close()
	app.draw()

	quit, err := app.handleEvent(tuiLogoAnimationTickEvent{Elapsed: tuiLogoSpinInterval})
	if err != nil {
		t.Fatalf("handleEvent(logo tick) error = %v", err)
	}
	if quit {
		t.Fatal("logo tick requested TUI quit")
	}

	rendered := renderedScreenText(t, vx, 100, 24)
	if !strings.Contains(rendered, app.logoAnimation.text()+"  Node: root-node") {
		t.Fatalf("animated header lost its adjacent node context:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Nodes") {
		t.Fatalf("header-only animation redraw disturbed the TUI body:\n%s", rendered)
	}
}

func TestStartTUILogoAnimationPostsElapsedTick(t *testing.T) {
	t.Parallel()

	events := make(chan vaxis.Event, 1)
	stop := startTUILogoAnimation(t.Context(), func(event vaxis.Event) {
		select {
		case events <- event:
		default:
		}
	})
	defer stop()

	select {
	case event := <-events:
		tick, ok := event.(tuiLogoAnimationTickEvent)
		if !ok {
			t.Fatalf("posted event = %T, want tuiLogoAnimationTickEvent", event)
		}
		if tick.Elapsed <= 0 {
			t.Fatalf("posted elapsed time = %s, want positive", tick.Elapsed)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("startup logo animation did not post a frame")
	}
}
