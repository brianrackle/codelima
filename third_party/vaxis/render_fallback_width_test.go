package vaxis

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRenderFallbackClustersClearReservedCellsAndReposition(t *testing.T) {
	for _, text := range []string{"👩🏽‍💻", "👩‍🚀"} {
		t.Run(text, func(t *testing.T) {
			var out bytes.Buffer
			vx := newWriterTestVaxis(&out)
			vx.screenNext.resize(12, 1)
			vx.screenLast.resize(12, 1)
			win := vx.Window()
			win.PrintTruncate(0, Segment{Text: "old contents"})
			vx.render()
			vx.tw.buf.Reset()
			win.Fill(Cell{Character: Character{Grapheme: " ", Width: 1}})
			win.PrintTruncate(0, Segment{Text: text + "NEXT"})
			vx.render()
			got := vx.tw.buf.String()
			width := vx.RenderedWidth(text)
			clear := strings.Repeat(" ", width) + "\x1b[1;1H" + text
			if !strings.Contains(got, clear) {
				t.Fatalf("reserved cells not cleared: %q", got)
			}
			position := text + fmt.Sprintf("\x1b[1;%dH", width+1) + "NEXT"
			if !strings.Contains(got, position) {
				t.Fatalf("following text not positioned: %q", got)
			}
		})
	}
}

func TestRenderNegotiatedUnicodeKeepsContiguousClusterOutput(t *testing.T) {
	var out bytes.Buffer
	vx := newWriterTestVaxis(&out)
	vx.caps.unicodeCore = true
	vx.screenNext.resize(12, 1)
	vx.screenLast.resize(12, 1)
	vx.Window().PrintTruncate(0, Segment{Text: "👩🏽‍💻NEXT"})
	vx.render()
	if !strings.Contains(vx.tw.buf.String(), "👩🏽‍💻NEXT") {
		t.Fatalf("negotiated output changed: %q", vx.tw.buf.String())
	}
}
