package codelima

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/png"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
)

// Negotiate public Vaxis capabilities through actual terminal responses, not
// private-field reflection. Only the outer console's transport is simulated.
type graphicsPresenterConsole struct {
	*testutil.VaxisConsole
	mu     sync.Mutex
	output bytes.Buffer
}

func (c *graphicsPresenterConsole) Size() (int, int, int, int, error) {
	return 80, 24, 800, 480, nil
}

func (c *graphicsPresenterConsole) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.Write(data)
}

func (c *graphicsPresenterConsole) takeOutput() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	data := bytes.Clone(c.output.Bytes())
	c.output.Reset()
	return data
}

func newGraphicsPresenterVaxis(t *testing.T, kitty bool) (*vaxis.Vaxis, *graphicsPresenterConsole) {
	t.Helper()
	t.Setenv("VAXIS_GRAPHICS", "")
	responses := "\x1b[?1;2c"
	if kitty {
		responses = "\x1b_Gi=31;OK\x1b\\" + responses
	}
	console := &graphicsPresenterConsole{VaxisConsole: testutil.NewVaxisConsole(responses, 80, 24)}
	vx, err := vaxis.New(vaxis.Options{WithConsole: console, DisableMouse: true, NoSignals: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vx.Close)
	if vx.CanKittyGraphics() != kitty {
		t.Fatalf("outer terminal negotiation: Kitty=%v, want %v", vx.CanKittyGraphics(), kitty)
	}
	console.takeOutput() // Startup queries are not image-presentation traffic.
	return vx, console
}

func newGraphicsPresenterApp(t *testing.T, vx *vaxis.Vaxis) *vaxisTUIApp {
	t.Helper()
	app := &vaxisTUIApp{vx: vx}
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-vx.Events():
			if resize, ok := event.(vaxis.Resize); ok {
				app.handleResize(resize)
				if app.cellPixelWidth != 10 || app.cellPixelHeight != 20 {
					t.Fatalf("console pixel geometry did not reach presenter: %dx%d", app.cellPixelWidth, app.cellPixelHeight)
				}
				return app
			}
		case <-timeout.C:
			t.Fatal("outer console did not produce its initial resize event")
		}
	}
}

type graphicsPresenterRPC struct {
	mu      sync.Mutex
	pixels  []byte
	started chan struct{}
	allow   chan struct{}
	chunks  []daemon.TerminalGraphicsParams
}

func (r *graphicsPresenterRPC) Call(ctx context.Context, method string, params, result any) error {
	if method == "terminal.resize" || method == "terminal.close" {
		return nil
	}
	if method != "terminal.graphics" {
		return fmt.Errorf("unexpected presenter RPC %s", method)
	}
	select {
	case r.started <- struct{}{}:
	default:
	}
	select {
	case <-r.allow:
	case <-ctx.Done():
		return ctx.Err()
	}
	p := params.(daemon.TerminalGraphicsParams)
	if p.TerminalID != "presenter-terminal" || p.RendererGeneration != 7 || p.ImageID != 9 || p.Generation != 1 || p.Offset < 0 || p.Length <= 0 || p.Length > terminalgraphics.MaxChunkBytes || p.Offset+p.Length > len(r.pixels) {
		return fmt.Errorf("invalid graphics chunk request: %+v", p)
	}
	r.mu.Lock()
	r.chunks = append(r.chunks, p)
	r.mu.Unlock()
	*result.(*daemon.TerminalGraphicsChunk) = daemon.TerminalGraphicsChunk{
		RendererGeneration: p.RendererGeneration, ImageID: p.ImageID,
		Generation: p.Generation, Offset: p.Offset, Total: len(r.pixels),
		Data: bytes.Clone(r.pixels[p.Offset : p.Offset+p.Length]),
	}
	return nil
}

func graphicsPresenterScene() (terminalgraphics.Frame, []byte) {
	const width, height = 300, 200
	pixels := make([]byte, width*height*4)
	random := uint32(0x1a2b3c4d)
	for i := 0; i < len(pixels); i += 4 {
		for channel := range 3 {
			random ^= random << 13
			random ^= random >> 17
			random ^= random << 5
			pixels[i+channel] = byte(random)
		}
		pixels[i+3] = 255
	}
	return terminalgraphics.Frame{
		RendererGeneration: 7, Generation: 1,
		Assets: []terminalgraphics.Asset{{ImageID: 9, Generation: 1, Width: width, Height: height, ByteLength: uint32(len(pixels))}},
		Placements: []terminalgraphics.Placement{
			{ImageID: 9, AssetGeneration: 1, ViewportCol: 25, ViewportRow: 10, PixelWidth: 100, PixelHeight: 80, Z: 7},
			{ImageID: 9, AssetGeneration: 1, ViewportCol: -2, ViewportRow: -1, PixelWidth: 400, PixelHeight: 200, Z: -4},
		},
	}, pixels
}

func graphicsPresenterTerm(t *testing.T, rpc *graphicsPresenterRPC, frame terminalgraphics.Frame) *daemonTUITerminal {
	t.Helper()
	term := &daemonTUITerminal{client: rpc, id: "presenter-terminal", stop: make(chan struct{}), snapshot: daemon.Snapshot{Cols: 1, Rows: 1, Cells: []daemon.SnapshotCell{{Grapheme: "x", Width: 1}}, Graphics: frame}}
	t.Cleanup(term.Detach)
	return term
}

func waitGraphicsPresenterResult(t *testing.T, app *vaxisTUIApp) {
	t.Helper()
	select {
	case result := <-app.graphicsResults():
		if result.err != nil {
			t.Fatal(result.err)
		}
		app.applyGraphicsResult(result)
		if app.graphics.installed != app.graphics.requested || len(app.graphics.images) != 2 {
			t.Fatalf("prepared scene did not install: %s", app.status)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("async graphics preparation did not complete")
	}
}

func drawGraphicsPresenterScene(t *testing.T, app *vaxisTUIApp, term *daemonTUITerminal, win vaxis.Window, console *graphicsPresenterConsole) []byte {
	t.Helper()
	var wire []byte
	for frame := range 32 {
		app.vx.Window().Clear()
		app.beginGraphicsFrame()
		win.Print(vaxis.Segment{Text: "responsive-text"})
		console.takeOutput()
		app.drawTerminalGraphics(win, term)
		upload := console.takeOutput()
		if len(upload) > terminalgraphics.MaxChunkBytes {
			t.Fatalf("presenter exceeded per-frame 64KiB wire budget: %d", len(upload))
		}
		wire = append(wire, upload...)
		app.finishGraphicsFrame()
		app.vx.Render()
		wire = append(wire, console.takeOutput()...)
		complete := true
		for _, img := range app.graphics.images {
			_, done := img.Upload(0)
			complete = complete && done
		}
		if complete {
			if frame == 0 {
				t.Fatal("fixture did not exercise incremental upload across redraws")
			}
			return wire
		}
		if app.graphics.tick == nil {
			t.Fatal("incomplete upload did not schedule another graphics frame")
		}
	}
	t.Fatal("presenter upload failed to converge")
	return nil
}

type presenterKittyPacket struct {
	header  map[string]string
	payload string
}

func presenterKittyPackets(t *testing.T, wire []byte) []presenterKittyPacket {
	t.Helper()
	var packets []presenterKittyPacket
	for text := string(wire); ; {
		_, tail, found := strings.Cut(text, "\x1b_G")
		if !found {
			return packets
		}
		packet, remainder, found := strings.Cut(tail, "\x1b\\")
		if !found {
			t.Fatal("unterminated graphics packet")
		}
		header, payload, _ := strings.Cut(packet, ";")
		fields := make(map[string]string)
		for entry := range strings.SplitSeq(header, ",") {
			key, value, ok := strings.Cut(entry, "=")
			if !ok {
				t.Fatalf("malformed Kitty header: %s", header)
			}
			fields[key] = value
		}
		packets = append(packets, presenterKittyPacket{header: fields, payload: payload})
		text = remainder
	}
}

func TestGraphicsPresenterDrawsPreparedSceneWithBoundedWireAndCleanup(t *testing.T) {
	vx, console := newGraphicsPresenterVaxis(t, true)
	frame, pixels := graphicsPresenterScene()
	rpc := &graphicsPresenterRPC{pixels: pixels, started: make(chan struct{}, 1), allow: make(chan struct{})}
	term := graphicsPresenterTerm(t, rpc, frame)
	app := newGraphicsPresenterApp(t, vx)
	t.Cleanup(func() {
		if app.graphics != nil {
			app.graphics.close()
		}
	})
	win := vx.Window().New(3, 2, 30, 12)
	start := time.Now()
	app.drawTerminalGraphics(win, term)
	if time.Since(start) > time.Second {
		t.Fatal("draw waited for blocked asset preparation")
	}
	select {
	case <-rpc.started:
	case <-time.After(time.Second):
		t.Fatal("presenter did not asynchronously request asset chunks")
	}
	if len(console.takeOutput()) != 0 || len(app.graphics.images) != 0 {
		t.Fatal("unprepared image reached host output")
	}
	close(rpc.allow)
	waitGraphicsPresenterResult(t, app)
	wire := drawGraphicsPresenterScene(t, app, term, win, console)
	if !bytes.Contains(wire, []byte("responsive-text")) {
		t.Fatal("text did not redraw while uploads progressed")
	}
	var encoded strings.Builder
	var imageID string
	var dimensions [2]int
	var uploadedIDs []string
	uploaded, placed := 0, 0
	for _, packet := range presenterKittyPackets(t, wire) {
		action := packet.header["a"]
		if imageID != "" && action != "" {
			t.Fatalf("graphics action %s interrupted multipart upload", action)
		}
		switch action {
		case "t":
			imageID = packet.header["i"]
			uploadedIDs = append(uploadedIDs, imageID)
			dimensions[0], _ = strconv.Atoi(packet.header["s"])
			dimensions[1], _ = strconv.Atoi(packet.header["v"])
		case "p":
			if placed >= 2 {
				t.Fatalf("unexpected extra placement: %+v", packet.header)
			}
			wantZ := []string{"-4", "7"}[placed]
			if packet.header["z"] != wantZ || packet.header["c"] != "" || packet.header["r"] != "" {
				t.Fatalf("incorrect exact-pixel placement: %+v", packet.header)
			}
			placed++
		case "":
			if imageID == "" || len(packet.header) != 2 || packet.header["q"] != "2" {
				t.Fatalf("invalid upload continuation: %+v", packet.header)
			}
		default:
			t.Fatalf("unexpected graphics action: %+v", packet.header)
		}
		if imageID == "" {
			continue
		}
		encoded.WriteString(packet.payload)
		if packet.header["m"] != "0" {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(encoded.String())
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if uploaded >= 2 {
			t.Fatal("unexpected extra uploaded image")
		}
		want := [][2]int{{300, 180}, {50, 40}}[uploaded]
		if dimensions != want || img.Bounds().Dx() != want[0] || img.Bounds().Dy() != want[1] {
			t.Fatalf("pane clipping changed: wire=%v PNG=%v want=%v", dimensions, img.Bounds(), want)
		}
		if uploaded == 0 {
			r, g, b, a := img.At(0, 0).RGBA()
			source := (20*300 + 15) * 4
			if byte(r>>8) != pixels[source] || byte(g>>8) != pixels[source+1] || byte(b>>8) != pixels[source+2] || a != 65535 {
				t.Fatal("clipped PNG did not preserve the expected source pixel")
			}
		}
		uploaded++
		imageID = ""
		encoded.Reset()
	}
	if uploaded != 2 || placed != 2 || imageID != "" {
		t.Fatalf("incomplete scene: uploaded=%d placed=%d pending=%s", uploaded, placed, imageID)
	}
	for _, origin := range []string{"\x1b[3;4H\x1b_Ga=p,", "\x1b[13;29H\x1b_Ga=p,"} {
		if !bytes.Contains(wire, []byte(origin)) {
			t.Fatalf("placement ignored pane origin: %q", origin)
		}
	}
	rpc.mu.Lock()
	if len(rpc.chunks) != (len(pixels)+terminalgraphics.MaxChunkBytes-1)/terminalgraphics.MaxChunkBytes {
		t.Errorf("shared source asset fetched redundantly: %d chunks", len(rpc.chunks))
	}
	rpc.mu.Unlock()

	// A hidden tab draws no terminal; production frame finalization must delete
	// both host assets, not retain them behind the next pane.
	app.beginGraphicsFrame()
	app.finishGraphicsFrame()
	assertGraphicsPresenterDeletes(t, app, console.takeOutput(), uploadedIDs)

	// Revisiting the tab recreates its host resources. Clearing the native scene
	// while the tab stays visible must also release those newly owned resources.
	app.beginGraphicsFrame()
	app.drawTerminalGraphics(win, term)
	app.finishGraphicsFrame()
	waitGraphicsPresenterResult(t, app)
	secondWire := drawGraphicsPresenterScene(t, app, term, win, console)
	uploadedIDs = nil
	for _, packet := range presenterKittyPackets(t, secondWire) {
		if packet.header["a"] == "t" {
			uploadedIDs = append(uploadedIDs, packet.header["i"])
		}
	}
	term.mu.Lock()
	term.snapshot.Graphics = terminalgraphics.Frame{}
	term.mu.Unlock()
	app.beginGraphicsFrame()
	app.drawTerminalGraphics(win, term)
	app.finishGraphicsFrame()
	assertGraphicsPresenterDeletes(t, app, console.takeOutput(), uploadedIDs)
}

func assertGraphicsPresenterDeletes(t *testing.T, app *vaxisTUIApp, wire []byte, ownedIDs []string) {
	t.Helper()
	deletions := presenterKittyPackets(t, wire)
	if len(ownedIDs) != 2 || len(deletions) != len(ownedIDs) || len(app.graphics.images) != 0 {
		t.Fatalf("scene cleanup retained images: %d owned, %d deletes, %d retained", len(ownedIDs), len(deletions), len(app.graphics.images))
	}
	for i, packet := range deletions {
		if packet.header["a"] != "d" || packet.header["d"] != "I" || packet.header["i"] != ownedIDs[i] {
			t.Fatalf("cleanup did not target owned host asset %s: %+v", ownedIDs[i], packet.header)
		}
	}
}

func TestGraphicsPresenterUnsupportedOuterTerminalPreservesText(t *testing.T) {
	vx, console := newGraphicsPresenterVaxis(t, false)
	frame, pixels := graphicsPresenterScene()
	rpc := &graphicsPresenterRPC{pixels: pixels, started: make(chan struct{}, 1), allow: make(chan struct{})}
	term := graphicsPresenterTerm(t, rpc, frame)
	app := newGraphicsPresenterApp(t, vx)
	win := vx.Window().New(3, 2, 30, 12)
	term.Draw(win)
	app.beginGraphicsFrame()
	app.drawTerminalGraphics(win, term)
	app.finishGraphicsFrame()
	vx.Render()
	wire := console.takeOutput()
	if app.graphics != nil || bytes.Contains(wire, []byte("\x1b_G")) || !bytes.Contains(wire, []byte("x")) {
		t.Fatalf("unsupported host lost text or attempted graphics: %q", wire)
	}
	select {
	case <-rpc.started:
		t.Fatal("unsupported host fetched image bytes")
	default:
	}
}
