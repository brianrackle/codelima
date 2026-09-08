package codelima

import (
	"bytes"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
)

func TestRendererGraphicsAssetsAreBoundedImmutableAndGenerationScoped(t *testing.T) {
	cache := rendererGraphicsAssets{}
	pixels := bytes.Repeat([]byte{1, 2, 3, 255}, 20000)
	frame := terminalgraphics.Frame{RendererGeneration: 3, Generation: 8, Assets: []terminalgraphics.Asset{{ImageID: 7, Generation: 8, Width: 200, Height: 100, ByteLength: uint32(len(pixels)), RGBA: pixels}}}
	metadata, err := cache.installOwned(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Assets[0].RGBA) != 0 {
		t.Fatal("snapshot metadata includes asset pixels")
	}
	request := daemon.TerminalGraphicsParams{RendererGeneration: 3, ImageID: 7, Generation: 8, Length: terminalgraphics.MaxChunkBytes}
	chunk, err := cache.read(request)
	if err != nil || len(chunk.Data) != terminalgraphics.MaxChunkBytes || chunk.Total != len(pixels) {
		t.Fatalf("chunk=%+v err=%v", chunk, err)
	}
	chunk.Data[0] = 99
	if again, err := cache.read(request); err != nil || again.Data[0] != 1 {
		t.Fatal("caller mutated retained asset")
	}
	for _, bad := range []daemon.TerminalGraphicsParams{
		{RendererGeneration: 2, ImageID: 7, Generation: 8, Length: 1},
		{RendererGeneration: 3, ImageID: 7, Generation: 9, Length: 1},
		{RendererGeneration: 3, ImageID: 7, Generation: 8, Offset: -1, Length: 1},
		{RendererGeneration: 3, ImageID: 7, Generation: 8, Offset: len(pixels) + 1, Length: 1},
		{RendererGeneration: 3, ImageID: 7, Generation: 8, Length: terminalgraphics.MaxChunkBytes + 1},
	} {
		if _, err := cache.read(bad); err == nil {
			t.Fatalf("invalid request accepted: %+v", bad)
		}
	}
	if _, err := cache.installOwned(terminalgraphics.Frame{RendererGeneration: 3, Generation: 9}); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.read(request); err == nil {
		t.Fatal("deleted scene asset survived invalidation")
	}
}

func TestRendererGraphicsRejectsInvalidAssetsAndDanglingPlacements(t *testing.T) {
	cache := rendererGraphicsAssets{}
	for _, frame := range []terminalgraphics.Frame{
		{RendererGeneration: 1, Assets: []terminalgraphics.Asset{{ImageID: 1, Generation: 1, Width: 1 << 31, Height: 1 << 31}}},
		{RendererGeneration: 1, Assets: []terminalgraphics.Asset{{ImageID: 1, Generation: 1, Width: 65535, Height: 65535, ByteLength: 4, RGBA: []byte{1, 2, 3, 4}}}},
		{RendererGeneration: 1, Assets: make([]terminalgraphics.Asset, terminalgraphics.MaxAssets+1)},
		{RendererGeneration: 1, Placements: []terminalgraphics.Placement{{ImageID: 1, AssetGeneration: 1}}},
	} {
		if _, err := cache.installOwned(frame); err == nil {
			t.Fatalf("invalid graphics scene accepted: %+v", frame)
		}
		if err := validateRendererGraphics(frame, false); err == nil {
			t.Fatalf("invalid graphics metadata accepted: %+v", frame)
		}
	}
}
