//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

func TestRendererGraphicsChunkTransportDeletionAndReplayGeneration(t *testing.T) {
	terminal := newIsolatedDaemonTerminalWithOptions("graphics-transport", nil, rendererGhosttyWorkerOptions(t))
	t.Cleanup(terminal.Close)
	if err := terminal.ResizePixels(30, 5, 9, 18); err != nil {
		t.Fatal(err)
	}
	terminal.renderer = terminal.newRenderer()
	renderer := terminal.renderer
	if err := renderer.Start(context.Background(), 30, 5); err != nil {
		t.Fatal(err)
	}
	foreground := uint32(0x123456)
	if _, err := renderer.Interact(TerminalInteractionRequest{Action: "colors", Colors: &TerminalColors{Foreground: &foreground, Theme: 1}}); err != nil {
		t.Fatal(err)
	}
	pixels := []byte{201, 103, 51, 127}
	stream := fmt.Sprintf("\x1b_Ga=T,f=32,s=1,v=1,i=21,p=3,c=2,r=1,q=2;%s\x1b\\", base64.StdEncoding.EncodeToString(pixels))
	if err := renderer.SendOutput(terminal.journal.AppendOutput([]byte(stream))); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		cache := terminal.cache.Load()
		return cache != nil && len(cache.state.Snapshot.Graphics.Assets) == 1
	}, "graphics metadata publication")
	frame := terminal.Snapshot().Snapshot.Graphics
	if len(frame.Placements) != 1 || frame.Placements[0].PixelWidth != 18 || frame.Placements[0].PixelHeight != 18 {
		t.Fatalf("pixel geometry missing: %+v", frame)
	}
	asset := frame.Assets[0]
	request := daemon.TerminalGraphicsParams{RendererGeneration: frame.RendererGeneration, ImageID: asset.ImageID, Generation: asset.Generation, Length: terminalgraphics.MaxChunkBytes}
	chunk, err := terminal.GraphicsAsset(request)
	if err != nil || !bytes.Equal(chunk.Data, pixels) {
		t.Fatalf("pixel transport: %+v err=%v", chunk, err)
	}
	if err := renderer.captureCheckpoint(context.Background()); err == nil {
		t.Fatal("checkpoint accepted live image state without an image import contract")
	}
	renderer.Restart()
	waitForCondition(t, 5*time.Second, func() bool {
		cache := terminal.cache.Load()
		return cache != nil && !cache.state.Snapshot.Stale && cache.state.Snapshot.Graphics.RendererGeneration > request.RendererGeneration && len(cache.state.Snapshot.Graphics.Assets) == 1
	}, "graphics raw replay after replacement")
	if _, err := terminal.GraphicsAsset(request); err == nil {
		t.Fatal("old renderer asset identity survived replacement")
	}
	frame = terminal.Snapshot().Snapshot.Graphics
	request.RendererGeneration, request.Generation = frame.RendererGeneration, frame.Assets[0].Generation
	if chunk, err := terminal.GraphicsAsset(request); err != nil || !bytes.Equal(chunk.Data, pixels) {
		t.Fatalf("replayed asset: %+v err=%v", chunk, err)
	}
	if err := renderer.SendOutput(terminal.journal.AppendOutput([]byte("\x1b_Ga=d,d=I,i=21,q=2;\x1b\\"))); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool { return len(terminal.Snapshot().Snapshot.Graphics.Assets) == 0 }, "graphics deletion publication")
	if _, err := terminal.GraphicsAsset(request); err == nil {
		t.Fatal("deleted native image remained fetchable")
	}
	if err := renderer.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	renderer.mu.Lock()
	checkpoint := renderer.checkpoint
	renderer.mu.Unlock()
	if checkpoint.State.CellWidth != 9 || checkpoint.State.CellHeight != 18 {
		t.Fatalf("pixel geometry missing from checkpoint: %+v", checkpoint.State)
	}
	if checkpoint.State.Colors == nil || checkpoint.State.Colors.Foreground == nil || *checkpoint.State.Colors.Foreground != foreground || checkpoint.State.Colors.Theme != 1 {
		t.Fatalf("raw graphics recovery lost host color policy: %+v", checkpoint.State.Colors)
	}
}
