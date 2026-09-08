package codelima

import (
	"context"
	"go.rockorager.dev/vaxis"
	"testing"
	"time"
)

type hostColorFixture struct {
	missing bool
	wait    bool
}

func (f hostColorFixture) QueryForegroundContext(ctx context.Context) vaxis.Color {
	if f.wait {
		<-ctx.Done()
		return vaxis.ColorDefault
	}
	return vaxis.RGBColor(0, 0, 0)
}
func (f hostColorFixture) QueryBackgroundContext(context.Context) vaxis.Color {
	return vaxis.ColorDefault
}
func (f hostColorFixture) QueryColorContext(_ context.Context, c vaxis.Color) vaxis.Color {
	if f.missing {
		return vaxis.ColorDefault
	}
	return vaxis.RGBColor(0, 0, c.Params()[0])
}
func TestHostColorsDistinguishUnknownBlackAndIncompletePalette(t *testing.T) {
	colors := queryTUIHostColors(context.Background(), hostColorFixture{}, 1)
	if colors.Foreground == nil || *colors.Foreground != 0 || colors.Background != nil || len(colors.Palette) != 256 || colors.Palette[255] != 255 {
		t.Fatalf("colors = %+v", colors)
	}
	if got := queryTUIHostColors(context.Background(), hostColorFixture{missing: true}, 0); got.Palette != nil {
		t.Fatal("invented incomplete palette")
	}
}
func TestHostColorQueryCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := queryTUIHostColors(ctx, hostColorFixture{wait: true}, 0)
	if got.Foreground != nil || got.Palette != nil || time.Since(start) > time.Second {
		t.Fatal("query ignored cancellation")
	}
}
func TestHostColorsPropagateOnceAndAgainOnSeatReclaim(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	app.hostColors = &TerminalColors{Theme: 1}
	app.propagateHostColors()
	app.propagateHostColors()
	app.handleInspectionResult(nextInspectionResult(t, app))
	if got := <-terminal.requests; got.Action != "colors" {
		t.Fatalf("request = %+v", got)
	}
	if len(terminal.requests) != 0 {
		t.Fatal("duplicate color propagation")
	}
	app.finishDaemonInputTakeover(tuiDaemonInputReclaimedEvent{})
	app.handleInspectionResult(nextInspectionResult(t, app))
	if got := <-terminal.requests; got.Action != "colors" {
		t.Fatal("no reclaim propagation")
	}
}
