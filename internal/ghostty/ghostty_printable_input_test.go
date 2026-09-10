//go:build cgo && (darwin || linux)

package ghostty

import (
	"testing"

	"github.com/brianrackle/codelima/internal/testutil"
	"go.rockorager.dev/vaxis"
)

func TestGhosttyKeyEncoderPreservesDecodedPrintableInput(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "printable-input")
	t.Cleanup(terminal.Close)
	for _, char := range "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~é中🙂" {
		t.Run(string(char), func(t *testing.T) {
			key := testutil.DecodedVaxisInputKey(t, string(char))
			for _, eventType := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
				key.EventType = eventType
				want := string(char)
				if eventType == vaxis.EventRelease {
					want = ""
				}
				got, handled := terminal.keyEncoder.Encode(key, terminal.term, false, false)
				if !handled || got != want {
					t.Fatalf("key %+v encoded as %q, handled=%v; want %q", key, got, handled, want)
				}
			}
		})
	}
}

func TestGhosttyKeyEncoderPreservesModifiedUnidentifiedInput(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "modified-printable-input")
	t.Cleanup(terminal.Close)
	for _, tc := range []struct{ input, want string }{
		{"\x1b>", "\x1b>"},
		{"\x1b[37;3u", "\x1b%"},
		{"\x1b[233;3u", "\x1bé"},
	} {
		key := testutil.DecodedVaxisInputKey(t, tc.input)
		got, handled := terminal.keyEncoder.Encode(key, terminal.term, false, false)
		if !handled || got != tc.want {
			t.Errorf("key %+v encoded as %q, handled=%v; want %q", key, got, handled, tc.want)
		}
	}
	terminal.ingestPTY([]byte("\x1b[>31u"))
	key := testutil.DecodedVaxisInputKey(t, ">")
	for _, tc := range []struct {
		action vaxis.EventType
		want   string
	}{
		{vaxis.EventPress, "\x1b[62;;62u"},
		{vaxis.EventRepeat, "\x1b[62;1:2;62u"},
		{vaxis.EventRelease, "\x1b[62;1:3u"},
	} {
		key.EventType = tc.action
		got, handled := terminal.keyEncoder.Encode(key, terminal.term, false, false)
		if !handled || got != tc.want {
			t.Errorf("Kitty key %+v encoded as %q, handled=%v; want %q", key, got, handled, tc.want)
		}
	}
}
