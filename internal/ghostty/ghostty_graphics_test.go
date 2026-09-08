//go:build cgo && (darwin || linux)

package ghostty

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

func TestGhosttyGraphicsPNGNativeOwnershipAndCheckpointEligibility(t *testing.T) {
	terminal, err := New("graphics-png", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	if err := terminal.ResizePixels(30, 5, 9, 18); err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	source.SetNRGBA(0, 0, color.NRGBA{R: 201, G: 103, B: 51, A: 127})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	terminal.Output(1, []byte(fmt.Sprintf("\x1b_Ga=T,f=100,i=21,p=3,c=2,r=1,q=2;%s\x1b\\", base64.StdEncoding.EncodeToString(encoded.Bytes()))))
	frame, err := terminal.Graphics()
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Assets) != 1 || len(frame.Placements) != 1 {
		t.Fatalf("graphics frame: %+v", frame)
	}
	if !bytes.Equal(frame.Assets[0].RGBA, source.Pix) || frame.Assets[0].ByteLength != 4 {
		t.Fatalf("native RGBA=%v, want=%v", frame.Assets[0].RGBA, source.Pix)
	}
	if frame.Placements[0].PixelWidth != 18 || frame.Placements[0].PixelHeight != 18 {
		t.Fatalf("actual pixel geometry missing: %+v", frame.Placements[0])
	}
	metadata, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, []byte(base64.StdEncoding.EncodeToString(source.Pix))) || bytes.Contains(metadata, []byte("RGBA")) {
		t.Fatalf("pixel payload leaked into graphics metadata: %s", metadata)
	}
	if _, err := terminal.Checkpoint(); !errors.Is(err, terminalstate.ErrCheckpointGraphics) {
		t.Fatalf("live graphics checkpoint error=%v", err)
	}
	terminal.Output(2, []byte("\x1b_Ga=d,d=I,i=21,q=2;\x1b\\"))
	if !bytes.Equal(frame.Assets[0].RGBA, source.Pix) {
		t.Fatal("native deletion invalidated owned Go pixels")
	}
	deleted, err := terminal.Graphics()
	if err != nil || len(deleted.Assets) != 0 || len(deleted.Placements) != 0 {
		t.Fatalf("delete graphics: frame=%+v err=%v", deleted, err)
	}
	if _, err := terminal.Checkpoint(); err != nil {
		t.Fatalf("checkpoint remained ineligible after deletion: %v", err)
	}
	terminal.Close()
	if _, err := terminal.Graphics(); !errors.Is(err, terminalstate.ErrClosed) {
		t.Fatalf("closed graphics error=%v", err)
	}
}
