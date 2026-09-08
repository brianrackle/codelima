package codelima

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

type graphicsEpochFixture struct {
	calls  int
	pixels []byte
}

func (f *graphicsEpochFixture) Call(_ context.Context, _ string, params, result any) error {
	request := params.(daemon.TerminalGraphicsParams)
	f.calls++
	*result.(*daemon.TerminalGraphicsChunk) = daemon.TerminalGraphicsChunk{RendererGeneration: request.RendererGeneration, ImageID: request.ImageID, Generation: request.Generation, Offset: request.Offset, Total: len(f.pixels), Data: slices.Clone(f.pixels[request.Offset : request.Offset+request.Length])}
	return nil
}

func TestGraphicsPreparedAssetCacheCannotReuseAnOldDaemonEpoch(t *testing.T) {
	fixture := &graphicsEpochFixture{pixels: []byte{255, 0, 0, 255}}
	request := tuiGraphicsRequest{
		key:      tuiGraphicsKey{Epoch: 1, Target: "term", Cols: 1, Rows: 1, CellWidth: 1, CellHeight: 1},
		terminal: &daemonTUITerminal{id: "term", client: fixture},
		frame: terminalgraphics.Frame{RendererGeneration: 1,
			Assets:     []terminalgraphics.Asset{{ImageID: 1, Generation: 1, Width: 1, Height: 1, ByteLength: 4}},
			Placements: []terminalgraphics.Placement{{ImageID: 1, AssetGeneration: 1, PixelWidth: 1, PixelHeight: 1}},
		},
	}
	cache := make(map[graphicsAssetKey][]byte)
	if _, err := prepareTUIGraphics(context.Background(), request, cache); err != nil {
		t.Fatal(err)
	}
	request.key.Epoch++
	fixture.pixels = []byte{0, 0, 255, 255}
	if _, err := prepareTUIGraphics(context.Background(), request, cache); err != nil {
		t.Fatal(err)
	}
	if fixture.calls != 2 || len(cache) != 1 {
		t.Fatalf("epoch collision reused or retained stale asset: calls=%d entries=%d", fixture.calls, len(cache))
	}
	for key, pixels := range cache {
		if key.Epoch != 2 || pixels[2] != 255 || pixels[0] != 0 {
			t.Fatalf("wrong epoch-owned pixels: key=%+v pixels=%v", key, pixels)
		}
	}
}

func TestGraphicsEpochResetDiscardsOldWorkAndIdenticalOldResults(t *testing.T) {
	old := tuiGraphicsKey{Epoch: 4, Target: "same-terminal", Cols: 80, Rows: 24, CellWidth: 9, CellHeight: 18}
	presenter := &tuiGraphicsPresenter{requests: make(chan tuiGraphicsRequest, 1), requested: old, installed: old, tick: time.NewTimer(time.Minute), retryCount: 3, retryKey: old}
	presenter.requests <- tuiGraphicsRequest{key: old}
	app := &vaxisTUIApp{graphics: presenter, graphicsEpoch: 4}
	app.resetGraphicsEpoch()
	if app.graphicsEpoch != 5 || presenter.requested != (tuiGraphicsKey{}) || presenter.installed != (tuiGraphicsKey{}) || presenter.tick != nil || presenter.retryCount != 0 || len(presenter.requests) != 0 {
		t.Fatalf("reset retained epoch-owned state: %+v", presenter)
	}
	current := old
	current.Epoch = app.graphicsEpoch
	presenter.requested = current
	// A result with the same terminal and graphics generation must still be
	// rejected after reconnect. Its sprites intentionally have no Vaxis host.
	app.applyGraphicsResult(tuiGraphicsResult{key: old, sprites: []tuiGraphicsSprite{{Width: 1, Height: 1, PNG: []byte("stale")}}})
	if presenter.installed != (tuiGraphicsKey{}) || len(presenter.images) != 0 {
		t.Fatal("previous epoch installed a host image")
	}
	app.applyGraphicsResult(tuiGraphicsResult{key: old, err: errors.New("old daemon failed")})
	if app.status != "" || presenter.retryCount != 0 {
		t.Fatal("previous epoch scheduled retries or overwrote status")
	}
}

func TestGraphicsRetryIsBoundedForAnUnchangingScene(t *testing.T) {
	key := tuiGraphicsKey{Epoch: 1, Target: "unavailable"}
	presenter := &tuiGraphicsPresenter{requested: key}
	app := &vaxisTUIApp{graphics: presenter}
	t.Cleanup(func() {
		if presenter.tick != nil {
			presenter.tick.Stop()
		}
	})
	for attempt := 1; attempt <= 4; attempt++ {
		presenter.requested = key
		app.applyGraphicsResult(tuiGraphicsResult{key: key, err: errors.New("transient asset failure")})
		if presenter.retryCount != attempt {
			t.Fatalf("attempt %d count=%d", attempt, presenter.retryCount)
		}
		if attempt <= 3 {
			if presenter.requested != (tuiGraphicsKey{}) || presenter.tick == nil || !time.Now().Before(presenter.retryAfter) {
				t.Fatal("retry lacked a bounded wake and backoff")
			}
			presenter.tick.Stop()
			presenter.tick = nil
		} else if presenter.requested != key || presenter.tick != nil {
			t.Fatal("unchanged failing scene retries without a bound")
		}
	}
}

func TestGraphicsRasterRejectsOverflowGeometryBeforePixelAccess(t *testing.T) {
	key := tuiGraphicsKey{Cols: 1, Rows: 1, CellWidth: 1, CellHeight: 1}
	placement := terminalgraphics.Placement{PixelWidth: 1, PixelHeight: 1}
	for _, asset := range []terminalgraphics.Asset{
		{Width: 1 << 31, Height: 1 << 31},
		{Width: ^uint32(0), Height: ^uint32(0), ByteLength: 4},
		{Width: 1, Height: 1, ByteLength: 3},
	} {
		if _, _, _, err := rasterTUIGraphics(context.Background(), key, asset, placement, nil, terminalgraphics.MaxBytes); err == nil {
			t.Fatalf("hostile asset geometry accepted: %+v", asset)
		}
	}
}
