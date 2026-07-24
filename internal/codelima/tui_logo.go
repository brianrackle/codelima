package codelima

import (
	"context"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

const (
	tuiLogoText              = "CodeLima"
	tuiLogoSettleInterval    = time.Second / 3
	tuiLogoSpinInterval      = tuiLogoSettleInterval / 6
	tuiLogoAnimationDuration = time.Duration(len(tuiLogoText)) * tuiLogoSettleInterval
	tuiLogoSlotGlyphs        = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
)

type tuiLogoAnimationTickEvent struct {
	Elapsed time.Duration
}

// tuiLogoAnimation is presentation-only startup state. Elapsed time is
// monotonic so delayed or reordered UI events can never unlock a character
// that has already settled.
type tuiLogoAnimation struct {
	elapsed time.Duration
}

func newTUILogoAnimation() *tuiLogoAnimation {
	return &tuiLogoAnimation{}
}

func (a *tuiLogoAnimation) advance(elapsed time.Duration) bool {
	if elapsed > a.elapsed {
		a.elapsed = elapsed
	}
	if a.elapsed > tuiLogoAnimationDuration {
		a.elapsed = tuiLogoAnimationDuration
	}
	return a.elapsed >= tuiLogoAnimationDuration
}

func (a *tuiLogoAnimation) text() string {
	target := []rune(tuiLogoText)
	glyphs := []rune(tuiLogoSlotGlyphs)
	settled := min(int(a.elapsed/tuiLogoSettleInterval), len(target))
	frame := int(a.elapsed / tuiLogoSpinInterval)

	result := make([]rune, len(target))
	for index, targetRune := range target {
		if index < settled {
			result[index] = targetRune
			continue
		}

		// Different per-column rates and offsets keep the unresolved suffix
		// from looking like one repeated scrolling word. An unsettled column
		// deliberately skips its final rune so each stop is visually distinct.
		rate := index%3 + 1
		glyphIndex := (frame*rate + index*11) % len(glyphs)
		result[index] = glyphs[glyphIndex]
		if result[index] == targetRune {
			result[index] = glyphs[(glyphIndex+1)%len(glyphs)]
		}
	}
	return string(result)
}

func startTUILogoAnimation(ctx context.Context, postEvent func(vaxis.Event)) func() {
	if postEvent == nil {
		return func() {}
	}

	animationCtx, cancel := context.WithCancel(ctx)
	go func() {
		startedAt := time.Now()
		ticker := time.NewTicker(tuiLogoSpinInterval)
		defer ticker.Stop()

		for {
			select {
			case <-animationCtx.Done():
				return
			case <-ticker.C:
				elapsed := time.Since(startedAt)
				if elapsed >= tuiLogoAnimationDuration {
					postEvent(tuiLogoAnimationTickEvent{Elapsed: tuiLogoAnimationDuration})
					return
				}
				postEvent(tuiLogoAnimationTickEvent{Elapsed: elapsed})
			}
		}
	}()
	return cancel
}
