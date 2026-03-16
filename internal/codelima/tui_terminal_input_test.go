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
