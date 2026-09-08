package codelima

import (
	"context"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
	"testing"
)

func TestGraphicsRasterClipsBeforeAllocationAndPreservesSubcellOffset(t *testing.T) {
	asset := terminalgraphics.Asset{Width: 2, Height: 1, ByteLength: 8}
	data := []byte{255, 0, 0, 255, 0, 255, 0, 255}
	key := tuiGraphicsKey{Cols: 2, Rows: 1, CellWidth: 2, CellHeight: 2}
	p := terminalgraphics.Placement{ViewportCol: -1, OffsetX: 1, PixelWidth: 4, PixelHeight: 2}
	img, col, row, err := rasterTUIGraphics(context.Background(), key, asset, p, data, 32)
	if err != nil || col != 0 || row != 0 || img.Rect.Dx() != 3 || img.Rect.Dy() != 2 {
		t.Fatalf("raster=%v %d,%d %v", img, col, row, err)
	}
	if img.Pix[0] != 255 || img.Pix[5] != 255 {
		t.Fatalf("source clipping=%v", img.Pix)
	}
	p.ViewportCol = 0
	p.OffsetX = 1
	p.PixelWidth = 1
	img, _, _, err = rasterTUIGraphics(context.Background(), key, asset, p, data, 32)
	if err != nil || img.Pix[3] != 0 || img.Pix[7] != 255 {
		t.Fatalf("subcell transparency=%v err=%v", img, err)
	}
	p.PixelWidth = ^uint32(0)
	p.PixelHeight = ^uint32(0)
	if _, _, _, err = rasterTUIGraphics(context.Background(), key, asset, p, data, 32); err != nil {
		t.Fatalf("large clipped placement: %v", err)
	}
	if _, _, _, err = rasterTUIGraphics(context.Background(), key, asset, p, data, 1); err == nil {
		t.Fatal("ignored raster budget")
	}
}

func TestGraphicsSceneKeyIgnoresTextButFencesRendererAndGeometry(t *testing.T) {
	frame := terminalgraphics.Frame{RendererGeneration: 1, Generation: 2, Assets: []terminalgraphics.Asset{{ImageID: 1, Generation: 1}}}
	key := tuiGraphicsSceneKey("one", frame, 10, 10, 8, 16)
	frame.Generation++
	if key != tuiGraphicsSceneKey("one", frame, 10, 10, 8, 16) {
		t.Fatal("text invalidates image cache")
	}
	frame.RendererGeneration++
	if key == tuiGraphicsSceneKey("one", frame, 10, 10, 8, 16) {
		t.Fatal("worker replacement reused stale scene")
	}
	if key == tuiGraphicsSceneKey("one", frame, 10, 10, 9, 16) {
		t.Fatal("pixel resize reused stale scene")
	}
}

func TestGraphicsAssetDimensionsRejectMultiplicationWraparound(t *testing.T) {
	for _, asset := range []terminalgraphics.Asset{{Width: 1 << 31, Height: 1 << 31}, {Width: ^uint32(0), Height: ^uint32(0), ByteLength: 4}, {Width: 1, Height: 1}, {Width: 0, Height: 1}} {
		if validTUIGraphicsAsset(asset) {
			t.Fatalf("accepted %+v", asset)
		}
		if _, _, _, err := rasterTUIGraphics(context.Background(), tuiGraphicsKey{Cols: 1, Rows: 1, CellWidth: 8, CellHeight: 16}, asset, terminalgraphics.Placement{PixelWidth: 1, PixelHeight: 1}, nil, 16); err == nil {
			t.Fatal("raster accepted invalid dimensions")
		}
	}
}
