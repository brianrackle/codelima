package terminalstate

import "testing"

func TestVTBoundaryAcrossEverySplit(t *testing.T) {
	for _, sequence := range []string{"界", "\x1b[38;2;12;34;56m", "\x1b]2;title\x07", "\x1b]8;;https://example.test\x1b\\", "\x1bP$qm\x1b\\", "\x1b_Gdata\x1b\\", "\x1b(B", "\x9b?25h"} {
		for split := 1; split < len(sequence); split++ {
			var boundary VTBoundary
			boundary.Feed([]byte(sequence[:split]))
			if boundary.Ground() {
				t.Fatalf("accepted incomplete %q at byte %d", sequence, split)
			}
			boundary.Feed([]byte(sequence[split:]))
			if !boundary.Ground() {
				t.Fatalf("did not finish %q at byte %d", sequence, split)
			}
		}
	}
}

func TestVTBoundaryCancellationAndUTF8InsideStrings(t *testing.T) {
	for _, text := range []string{"plain text\r\n", "\x1b[12\x18", "\x1bPdata\x1a", "\x1b]2;\xc3\x9c\x07", "\x1b]title\x1b[0m", "\x1b[31m界\x1b[0m"} {
		var boundary VTBoundary
		for _, b := range []byte(text) {
			boundary.Feed([]byte{b})
		}
		if !boundary.Ground() {
			t.Fatalf("did not finish %q", text)
		}
	}
	var boundary VTBoundary
	boundary.Feed([]byte("\x1bPdata\x07"))
	if boundary.Ground() {
		t.Fatal("BEL terminated a DCS")
	}
}
