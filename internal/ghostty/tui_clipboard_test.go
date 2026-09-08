//go:build cgo && (darwin || linux)

package ghostty

import (
	"encoding/base64"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestNativeClipboardDecodesFragmentedOSC52(t *testing.T) {
	for _, terminator := range []string{"\x07", "\x1b\\"} {
		t.Run("terminator_"+base64.StdEncoding.EncodeToString([]byte(terminator)), func(t *testing.T) {
			var got []string
			base, err := newGhosttyTUITerminal("native-clipboard", func(event vaxis.Event) {
				if value, ok := event.(tuiClipboardEvent); ok {
					got = append(got, value.Text)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			terminal := base.(*ghosttyTUITerminal)
			defer terminal.Close()
			data := "before\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("split clipboard 界")) + terminator + "after"
			for _, value := range []byte(data) {
				terminal.ingestPTY([]byte{value})
			}
			if len(got) != 1 || got[0] != "split clipboard 界" {
				t.Fatalf("clipboard writes = %q", got)
			}
		})
	}
}

func TestNativeClipboardRejectsMalformedOversizedReadsAndUnsupportedDestination(t *testing.T) {
	var writes int
	base, err := newGhosttyTUITerminal("native-clipboard-policy", func(event vaxis.Event) {
		if _, ok := event.(tuiClipboardEvent); ok {
			writes++
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	terminal := base.(*ghosttyTUITerminal)
	defer terminal.Close()
	for _, data := range []string{
		"\x1b]52;c;???\x07",
		"\x1b]52;c;?\x07",
		"\x1b]52;p;" + base64.StdEncoding.EncodeToString([]byte("primary")) + "\x07",
		"\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", terminalMaxPasteBytes+1))) + "\x07",
	} {
		terminal.ingestPTY([]byte(data))
	}
	if writes != 0 {
		t.Fatalf("policy rejected data caused %d writes", writes)
	}
}
