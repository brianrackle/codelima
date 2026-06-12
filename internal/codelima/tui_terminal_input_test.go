package codelima

import (
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

func TestEncodeTUITerminalKeyUsesCursorMode(t *testing.T) {
	t.Parallel()

	if got := encodeTUITerminalKey(vaxis.Key{Keycode: vaxis.KeyUp}, false, false); got != "\x1b[A" {
		t.Fatalf("expected normal cursor up sequence, got %q", got)
	}
	if got := encodeTUITerminalKey(vaxis.Key{Keycode: vaxis.KeyUp}, false, true); got != "\x1bOA" {
		t.Fatalf("expected application cursor up sequence, got %q", got)
	}
}

func TestEncodeTUITerminalKeyEncodesCtrlC(t *testing.T) {
	t.Parallel()

	got := encodeTUITerminalKey(vaxis.Key{
		Keycode:   'c',
		Modifiers: vaxis.ModCtrl,
	}, false, false)
	if got != "\x03" {
		t.Fatalf("expected Ctrl+C to encode as ETX, got %q", got)
	}
}

func TestEncodeTUITerminalPasteKeyPreservesNewlinesAsCarriageReturns(t *testing.T) {
	t.Parallel()

	got := encodeTUITerminalKey(vaxis.Key{
		Text:      "one\ntwo\r\nthree",
		Keycode:   '\n',
		EventType: vaxis.EventPaste,
	}, false, false)
	if got != "one\rtwo\rthree" {
		t.Fatalf("expected pasted newlines to become carriage returns, got %q", got)
	}
}

func TestEncodeTUITerminalPasteKeyRecoversCtrlDecodedNewline(t *testing.T) {
	t.Parallel()

	// Vaxis decodes a raw "\n" inside a bracketed paste as Ctrl+J with no text.
	got := encodeTUITerminalKey(vaxis.Key{
		Keycode:   'j',
		Modifiers: vaxis.ModCtrl,
		EventType: vaxis.EventPaste,
	}, false, false)
	if got != "\r" {
		t.Fatalf("expected pasted Ctrl+J to encode as carriage return, got %q", got)
	}

	got = encodeTUITerminalKey(vaxis.Key{
		Keycode:   '@',
		Modifiers: vaxis.ModCtrl,
		EventType: vaxis.EventPaste,
	}, false, false)
	if got != "\x00" {
		t.Fatalf("expected pasted Ctrl+@ to encode as NUL, got %q", got)
	}
}

func TestEncodeTUITerminalMouseUsesSGRWhenRequested(t *testing.T) {
	t.Parallel()

	got := encodeTUITerminalMouse(vaxis.Mouse{
		Col:       4,
		Row:       2,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}, true, true, true)
	if got != "\x1b[<0;5;3M" {
		t.Fatalf("expected SGR mouse press sequence, got %q", got)
	}
}

func TestEncodeTUITerminalMouseDropsUnsupportedMotion(t *testing.T) {
	t.Parallel()

	got := encodeTUITerminalMouse(vaxis.Mouse{
		Col:       4,
		Row:       2,
		Button:    vaxis.MouseNoButton,
		EventType: vaxis.EventMotion,
	}, false, false, false)
	if got != "" {
		t.Fatalf("expected unsupported motion event to be dropped, got %q", got)
	}
}
