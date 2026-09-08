package codelima

import (
	"context"
	"time"

	"go.rockorager.dev/vaxis"
)

type tuiHostColorSource interface {
	QueryForegroundContext(context.Context) vaxis.Color
	QueryBackgroundContext(context.Context) vaxis.Color
	QueryColorContext(context.Context, vaxis.Color) vaxis.Color
}

type tuiHostColorResult struct {
	colors  TerminalColors
	version uint64
}

func (a *vaxisTUIApp) hostColorTicks() <-chan time.Time {
	if a.hostColorsTick == nil {
		return nil
	}
	return a.hostColorsTick.C
}

func terminalRGB(color vaxis.Color) *uint32 {
	values := color.Params()
	if len(values) != 3 {
		return nil
	}
	rgb := uint32(values[0])<<16 | uint32(values[1])<<8 | uint32(values[2])
	return &rgb
}

// Querying never parks the UI and never leaves an uncancellable Vaxis read.
// Unknown colors remain unspecified so native defaults stay distinguishable.
func queryTUIHostColors(ctx context.Context, source tuiHostColorSource, theme vaxis.ColorThemeMode) TerminalColors {
	colors := TerminalColors{Theme: int(theme)}
	colors.Foreground = terminalRGB(source.QueryForegroundContext(ctx))
	colors.Background = terminalRGB(source.QueryBackgroundContext(ctx))
	palette := make([]uint32, 256)
	for index := range palette {
		if ctx.Err() != nil {
			return colors
		}
		color := terminalRGB(source.QueryColorContext(ctx, vaxis.IndexColor(uint8(index))))
		if color == nil {
			return colors
		}
		palette[index] = *color
	}
	colors.Palette = palette
	return colors
}

func (a *vaxisTUIApp) startHostColorQuery(theme vaxis.ColorThemeMode) {
	a.desiredColorTheme = theme
	a.colorQueryVersion++
	if a.vx == nil || a.colorQueryBusy {
		return
	}
	a.launchHostColorQuery()
}

func (a *vaxisTUIApp) launchHostColorQuery() {
	if a.hostColorResults == nil {
		a.hostColorResults = make(chan tuiHostColorResult, 1)
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	a.colorQueryCancel, a.colorQueryBusy = cancel, true
	a.colorQueryDone = make(chan struct{})
	done := a.colorQueryDone
	version, theme, source, results := a.colorQueryVersion, a.desiredColorTheme, a.vx, a.hostColorResults
	go func() {
		defer close(done)
		defer cancel()
		colors := queryTUIHostColors(ctx, source, theme)
		// Exactly one query is outstanding, so its one-slot result never blocks.
		results <- tuiHostColorResult{colors: colors, version: version}
	}()
}

func (a *vaxisTUIApp) applyHostColorResult(result tuiHostColorResult) {
	a.colorQueryBusy = false
	if result.version != a.colorQueryVersion {
		a.launchHostColorQuery()
		return
	}
	a.hostColors = &result.colors
	a.hostColorsSent = make(map[string]bool)
	a.propagateHostColors()
}

func (a *vaxisTUIApp) propagateHostColors() {
	if a.sessions == nil || a.hostColors == nil || time.Now().Before(a.hostColorsRetry) {
		return
	}
	if a.hostColorsSent == nil {
		a.hostColorsSent = make(map[string]bool)
	}
	for target := range a.hostColorsSent {
		if _, ok := a.sessions.sessions[target]; !ok {
			delete(a.hostColorsSent, target)
		}
	}
	for target := range a.sessions.sessions {
		if a.hostColorsSent[target] || !a.supportsTerminalInspection(target) {
			continue
		}
		if err := a.queueTerminalInteraction(target, TerminalInteractionRequest{Action: "colors", Colors: a.hostColors}, false, 0); err == nil {
			a.hostColorsSent[target] = true
		}
	}
}
