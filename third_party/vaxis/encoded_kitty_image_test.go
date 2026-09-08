package vaxis

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"slices"
	"strings"
	"testing"
)

func encodedKittyFixture(t *testing.T) (*Vaxis, *bytes.Buffer, []byte) {
	t.Helper()
	var encoded bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.NoCompression}
	if err := encoder.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 64, 32))); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	vx := newWriterTestVaxis(out)
	vx.graphicsProtocol = kitty
	vx.winSize = Resize{Cols: 80, Rows: 24, XPixel: 800, YPixel: 480}
	vx.screenNext.resize(80, 24)
	vx.screenLast.resize(80, 24)
	return vx, out, encoded.Bytes()
}

func TestEncodedKittyUploadOwnsBytesAndHonorsWireBudget(t *testing.T) {
	vx, out, source := encodedKittyFixture(t)
	expected := bytes.Clone(source)
	img, err := vx.NewEncodedKittyImage(source, 64, 32)
	if err != nil {
		t.Fatal(err)
	}
	for i := range source {
		source[i] = 0
	}
	for _, budget := range []int{-1, 0, 1, 20} {
		if n, done := img.Upload(budget); n != 0 || done || out.Len() != 0 {
			t.Fatalf("budget %d advanced upload: n=%d, done=%v", budget, n, done)
		}
	}
	for calls := 0; ; calls++ {
		before := out.Len()
		n, done := img.Upload(128)
		if n <= 0 || n > 128 || out.Len()-before != n {
			t.Fatalf("wire budget exceeded or progress lost: n=%d, delta=%d", n, out.Len()-before)
		}
		if done {
			break
		}
		if calls > 1000 {
			t.Fatal("bounded upload did not converge")
		}
	}
	if img.Err() != nil || img.png != nil {
		t.Fatalf("completed image retained data/error: %v", img.Err())
	}
	var payload strings.Builder
	packets := strings.Split(out.String(), "\x1b\\")
	for i, packet := range packets[:len(packets)-1] {
		header, data, ok := strings.Cut(packet, ";")
		if !ok || !strings.HasPrefix(header, "\x1b_G") || len(data) > 4096 {
			t.Fatalf("invalid Kitty chunk %q", packet)
		}
		wantM := ",m=1"
		if i == len(packets)-2 {
			wantM = ",m=0"
		}
		if !strings.HasSuffix(header, wantM) {
			t.Fatalf("bad continuation flag: %q", header)
		}
		if i > 0 && header != "\x1b_Gq=2"+wantM {
			t.Fatalf("continuation carried keys other than q/m: %q", header)
		}
		payload.WriteString(data)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.String())
	if err != nil || !bytes.Equal(decoded, expected) {
		t.Fatalf("multipart upload changed owned PNG: %v", err)
	}
	if n, done := img.Upload(128); n != 0 || !done {
		t.Fatal("completed image uploaded twice")
	}
}

func TestEncodedKittySerializesMultipartAndReleasesDestroyedOwner(t *testing.T) {
	vx, out, source := encodedKittyFixture(t)
	first, _ := vx.NewEncodedKittyImage(source, 64, 32)
	second, _ := vx.NewEncodedKittyImage(source, 64, 32)
	if n, done := first.Upload(128); n == 0 || done {
		t.Fatal("fixture did not start a multipart transfer")
	}
	before := out.Len()
	if n, done := second.Upload(128); n != 0 || done || before != out.Len() {
		t.Fatal("second image interleaved multipart data")
	}
	first.Destroy()
	if n, done := second.Upload(128); n == 0 || done {
		t.Fatal("destroyed image retained upload ownership")
	}
	if n, done := first.Upload(128); n != 0 || !done {
		t.Fatal("destroyed image resumed upload")
	}
}

func TestEncodedKittyExactPixelsZAndLifecycle(t *testing.T) {
	vx, out, source := encodedKittyFixture(t)
	img, _ := vx.NewEncodedKittyImage(source, 64, 32)
	window := vx.Window().New(2, 3, 7, 2)
	img.DrawAtZ(window, -7)
	if len(vx.graphicsNext) != 0 {
		t.Fatal("image drawn before upload completed")
	}
	if _, done := img.Upload(64 << 10); !done {
		t.Fatal("fixture did not upload")
	}
	img.Resize(1, 1)
	if w, h := img.CellSize(); w != 7 || h != 2 {
		t.Fatalf("exact pixels were rescaled: %dx%d", w, h)
	}
	out.Reset()
	img.DrawAtZ(window, -7)
	if len(vx.graphicsNext) != 1 {
		t.Fatal("valid placement not added")
	}
	first := vx.graphicsNext[0]
	first.writeTo(out)
	if !strings.Contains(out.String(), ",z=-7,C=1") || strings.Contains(out.String(), ",c=") || strings.Contains(out.String(), ",r=") {
		t.Fatalf("placement changed pixel scaling or z: %q", out.String())
	}
	vx.RemoveImage(img)
	if len(vx.graphicsNext) != 0 {
		t.Fatal("RemoveImage retained encoded placement")
	}
	img.DrawAtZ(window, 3)
	if samePlacement(first, vx.graphicsNext[0]) {
		t.Fatal("z-only change was treated as the same placement")
	}
	vx.graphicsLast = slices.Clone(vx.graphicsNext)
	out.Reset()
	img.Destroy()
	img.Destroy()
	img.DrawAtZ(window, 3)
	if len(vx.graphicsNext) != 0 || len(vx.graphicsLast) != 0 || strings.Count(out.String(), "a=d,d=I") != 1 {
		t.Fatalf("destroy retained placements or repeated deletion: %q", out.String())
	}
	if !strings.Contains(out.String(), fmt.Sprintf("i=%d,", img.id)) {
		t.Fatal("destroy did not name the exact owned image")
	}
}

func TestEncodedKittyRejectsInvalidGeometryAndClips(t *testing.T) {
	vx, _, source := encodedKittyFixture(t)
	for _, dimensions := range [][2]int{{0, 32}, {64, 0}, {-1, 32}, {65, 32}, {1 << 30, 1 << 30}} {
		if _, err := vx.NewEncodedKittyImage(source, dimensions[0], dimensions[1]); err == nil {
			t.Fatalf("invalid dimensions accepted: %v", dimensions)
		}
	}
	for _, data := range [][]byte{nil, []byte("not PNG"), make([]byte, maxEncodedKittyBytes+1)} {
		if _, err := vx.NewEncodedKittyImage(data, 64, 32); err == nil {
			t.Fatal("invalid encoded input accepted")
		}
	}
	img, _ := vx.NewEncodedKittyImage(source, 64, 32)
	_, _ = img.Upload(64 << 10)
	for _, win := range []Window{vx.Window().New(0, 0, 1, 1), vx.Window().New(-1, 0, 7, 2), vx.Window().New(79, 0, 7, 2)} {
		img.DrawAtZ(win, 0)
	}
	if len(vx.graphicsNext) != 0 {
		t.Fatal("image escaped its clipped window")
	}
	vx.winSize.XPixel = 0
	img.Draw(vx.Window())
	if len(vx.graphicsNext) != 0 {
		t.Fatal("unknown pixel geometry accepted")
	}
}

type encodedKittyShortWriter struct{}

func (encodedKittyShortWriter) Write(data []byte) (int, error) { return len(data) / 2, nil }

func TestEncodedKittyWriteFailureIsTerminal(t *testing.T) {
	vx, _, source := encodedKittyFixture(t)
	img, _ := vx.NewEncodedKittyImage(source, 64, 32)
	vx.tw.terminal.w = encodedKittyShortWriter{}
	if n, done := img.Upload(128); n <= 0 || !done || !errors.Is(img.Err(), io.ErrShortWrite) || vx.encodedUpload != nil {
		t.Fatalf("short write did not terminate upload: n=%d done=%v err=%v", n, done, img.Err())
	}
	if n, done := img.Upload(128); n != 0 || !done {
		t.Fatal("failed transmission retried corrupt continuation")
	}
}

func TestEncodedKittyUploadSurvivesTextRedrawAndRestartsAfterDelete(t *testing.T) {
	vx, out, source := encodedKittyFixture(t)
	old, _ := vx.NewEncodedKittyImage(source, 64, 32)
	_, _ = old.Upload(64 << 10)
	old.Draw(vx.Window())
	vx.Render()
	next, _ := vx.NewEncodedKittyImage(source, 64, 32)
	_, _ = next.Upload(128)
	out.Reset()
	vx.RemoveImage(old)
	vx.Window().SetCell(0, 0, Cell{Character: Character{Grapheme: "text", Width: 1}})
	vx.Refresh()
	if strings.Contains(out.String(), "\x1b_G") || !strings.Contains(out.String(), "text") || len(vx.graphicsLast) != 1 {
		t.Fatalf("text redraw interrupted upload or discarded graphics cut: %q", out.String())
	}
	old.Destroy()
	if next.offset != 0 || vx.encodedUpload != nil {
		t.Fatal("deleting another image did not reset partial upload")
	}
	out.Reset()
	_, _ = next.Upload(128)
	if !strings.HasPrefix(out.String(), "\x1b_Ga=t,") {
		t.Fatalf("upload resumed a continuation after deletion: %q", out.String())
	}
}
