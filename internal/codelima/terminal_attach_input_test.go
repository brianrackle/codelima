package codelima

import (
	"bytes"
	"testing"
)

func TestRawAttachInputDetachesWithLegacyAndKittyControlBracket(t *testing.T) {
	for _, key := range []string{"\x1d", "\x1b[93;5u", "\x1b[93;5:1u", "\x1b[93;5;29u"} {
		for split := range len(key) + 1 {
			var filter rawAttachInput
			first, detach := filter.feed([]byte("before" + key[:split]))
			second, more := filter.feed([]byte(key[split:]))
			if !detach && !more {
				t.Fatalf("did not detach for %q split %d", key, split)
			}
			if string(append(first, second...)) != "before" {
				t.Fatal("detach escape leaked into PTY")
			}
		}
	}
}

func TestRawAttachInputPreservesPayloadAndBracketedPaste(t *testing.T) {
	payload := []byte("q\x1b[1;3D\x1b[17~\x1b[93;5:3u\x1b[200~paste\x1d\x1b[93;5u\x1b[201~界")
	var filter rawAttachInput
	var output []byte
	for _, b := range payload {
		part, detach := filter.feed([]byte{b})
		if detach {
			t.Fatal("intercepted guest input or pasted detach text")
		}
		output = append(output, part...)
	}
	output = append(output, filter.flush()...)
	if !bytes.Equal(output, payload) {
		t.Fatalf("changed raw input: %q", output)
	}
}
