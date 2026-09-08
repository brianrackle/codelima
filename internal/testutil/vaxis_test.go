package testutil

import (
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestVaxisConsoleReportsCellAndPixelGeometry(t *testing.T) {
	console := NewVaxisConsole("", 80, 24)
	console.size.XPixel, console.size.YPixel = 800, 480
	cols, rows, x, y, err := console.Size()
	if err != nil || cols != 80 || rows != 24 || x != 800 || y != 480 {
		t.Fatalf("console geometry = %d,%d,%d,%d,%v", cols, rows, x, y, err)
	}
}

func TestVaxisFlatCellFixturePreservesGraphemeAndStyle(t *testing.T) {
	vx := NewRenderVaxis(t, 8, 4)
	defer vx.Close()
	style := vaxis.Style{Foreground: vaxis.ColorBlue, Background: vaxis.ColorRed, Attribute: vaxis.AttrBold | vaxis.AttrOverline, Hyperlink: "https://example.test/fixture"}
	vx.Window().SetCell(3, 2, vaxis.Cell{Character: vaxis.Character{Grapheme: "X", Width: 1}, Style: style})
	if got := RenderedCellGrapheme(t, vx, 3, 2); got != "X" {
		t.Fatalf("rendered cell=%q", got)
	}
	if got := RenderedCellStyle(t, vx, 3, 2); got != style {
		t.Fatalf("rendered style=%+v, want %+v", got, style)
	}
	if got := RenderedCellGrapheme(t, vx, 2, 3); got == "X" {
		t.Fatal("flat row/column indexing transposed the cell")
	}
}
