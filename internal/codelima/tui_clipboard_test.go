package codelima

import (
	"encoding/base64"
	"testing"
)

func TestOSC52ClipboardScannerDecodesBELTerminatedClipboard(t *testing.T) {
	t.Parallel()

	var got []string
	scanner := newOSC52ClipboardScanner(func(text string) {
		got = append(got, text)
	})

	payload := base64.StdEncoding.EncodeToString([]byte("hello clipboard"))
	scanner.Write([]byte("prefix\x1b]52;c;" + payload + "\x07suffix"))

	if len(got) != 1 || got[0] != "hello clipboard" {
		t.Fatalf("expected one decoded clipboard payload, got %#v", got)
	}
}

func TestOSC52ClipboardScannerDecodesSTTerminatedClipboardAcrossChunks(t *testing.T) {
	t.Parallel()

	var got []string
	scanner := newOSC52ClipboardScanner(func(text string) {
		got = append(got, text)
	})

	payload := base64.StdEncoding.EncodeToString([]byte("split clipboard"))
	scanner.Write([]byte("\x1b]52;c;"))
	scanner.Write([]byte(payload[:4]))
	scanner.Write([]byte(payload[4:] + "\x1b\\"))

	if len(got) != 1 || got[0] != "split clipboard" {
		t.Fatalf("expected one decoded clipboard payload, got %#v", got)
	}
}
