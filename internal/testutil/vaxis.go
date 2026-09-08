package testutil

import (
	"bytes"
	"go.rockorager.dev/vaxis"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

type VaxisConsole struct {
	input bytes.Buffer
	size  vaxis.Resize
}

var _ vaxis.Console = (*VaxisConsole)(nil)

func NewVaxisConsole(input string, width, height uint16) *VaxisConsole {
	console := &VaxisConsole{
		size: vaxis.Resize{
			Cols: int(width),
			Rows: int(height),
		},
	}
	console.input.WriteString(input)
	return console
}

func (f *VaxisConsole) Read(p []byte) (int, error) {
	if f.input.Len() == 0 {
		return 0, io.EOF
	}
	return f.input.Read(p)
}

func (f *VaxisConsole) Write(p []byte) (int, error) {
	return len(p), nil
}

func (f *VaxisConsole) Close() error {
	return nil
}

func (f *VaxisConsole) Fd() uintptr {
	return 0
}

func (f *VaxisConsole) SetRaw() error {
	return nil
}

func (f *VaxisConsole) Reset() error {
	return nil
}

func (f *VaxisConsole) Size() (int, int, int, int, error) {
	return f.size.Cols, f.size.Rows, f.size.XPixel, f.size.YPixel, nil
}

func NewRenderVaxis(t *testing.T, width, height int) *vaxis.Vaxis {
	t.Helper()

	console := NewVaxisConsole("\x1b[?1;2c", uint16(width), uint16(height))
	vx, err := vaxis.New(vaxis.Options{
		WithConsole:  console,
		DisableMouse: true,
		NoSignals:    true,
	})
	if err != nil {
		t.Fatalf("vaxis.New() error = %v", err)
	}
	return vx
}

func DecodedVaxisInputKey(t *testing.T, input string) vaxis.Key {
	t.Helper()

	console := NewVaxisConsole("\x1b[?1;2c"+input, 80, 24)
	vx, err := vaxis.New(vaxis.Options{
		WithConsole:  console,
		DisableMouse: true,
		NoSignals:    true,
	})
	if err != nil {
		t.Fatalf("vaxis.New() error = %v", err)
	}
	defer vx.Close()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-vx.Events():
			key, ok := event.(vaxis.Key)
			if ok {
				return key
			}
		case <-deadline:
			t.Fatalf("timed out waiting for decoded key input %q", input)
		}
	}
}

func RenderedCellGrapheme(t *testing.T, vx *vaxis.Vaxis, col, row int) string {
	t.Helper()
	cell := renderedCell(t, vx, col, row)
	return cell.FieldByName("Character").FieldByName("Grapheme").String()
}

func RenderedCellStyle(t *testing.T, vx *vaxis.Vaxis, col, row int) vaxis.Style {
	t.Helper()

	cell := renderedCell(t, vx, col, row)
	style := cell.FieldByName("Style")
	return vaxis.Style{
		Hyperlink:       style.FieldByName("Hyperlink").String(),
		HyperlinkParams: style.FieldByName("HyperlinkParams").String(),
		Foreground:      vaxis.Color(style.FieldByName("Foreground").Uint()),
		Background:      vaxis.Color(style.FieldByName("Background").Uint()),
		UnderlineColor:  vaxis.Color(style.FieldByName("UnderlineColor").Uint()),
		UnderlineStyle:  vaxis.UnderlineStyle(style.FieldByName("UnderlineStyle").Uint()),
		Attribute:       vaxis.AttributeMask(style.FieldByName("Attribute").Uint()),
	}
}

// Vaxis v0.17 stores a flat cell slice, indexed by row*cols+col. Keep the
// dependency assumption in one fixture and fail loudly on future layout drift.
func renderedCell(t *testing.T, vx *vaxis.Vaxis, col, row int) reflect.Value {
	t.Helper()
	if vx == nil {
		t.Fatal("render fixture requires a non-nil Vaxis")
	}
	screen := reflect.ValueOf(vx).Elem().FieldByName("screenNext")
	if !screen.IsValid() || screen.Kind() != reflect.Pointer || screen.IsNil() {
		t.Fatal("Vaxis reflection fixture: screenNext pointer layout changed")
	}
	state := screen.Elem()
	cols, rows, buffer := state.FieldByName("cols"), state.FieldByName("rows"), state.FieldByName("buf")
	if !cols.IsValid() || cols.Kind() != reflect.Int || !rows.IsValid() || rows.Kind() != reflect.Int || !buffer.IsValid() || buffer.Kind() != reflect.Slice || buffer.Type().Elem() != reflect.TypeOf(vaxis.Cell{}) {
		t.Fatal("Vaxis reflection fixture: expected screen cols/rows and flat []Cell")
	}
	width, height := int(cols.Int()), int(rows.Int())
	if col < 0 || col >= width || row < 0 || row >= height || width*height != buffer.Len() {
		t.Fatalf("Vaxis reflection fixture: cell (%d,%d) outside %dx%d/%d-cell screen", col, row, width, height, buffer.Len())
	}
	return buffer.Index(row*width + col)
}

func RenderedScreenText(t *testing.T, vx *vaxis.Vaxis, width, height int) string {
	t.Helper()

	var lines []string
	for row := range height {
		var line strings.Builder
		for col := range width {
			line.WriteString(RenderedCellGrapheme(t, vx, col, row))
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}
