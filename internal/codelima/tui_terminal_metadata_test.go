package codelima

import (
	"strings"
	"testing"
)

func TestTerminalMetadataIsBoundedInertText(t *testing.T) {
	got := terminalMetadataText("\x1b]2;evil\a\r\n\u202ehello", 12)
	if strings.ContainsAny(got, "\x1b\a\r\n\u202e") || len([]rune(got)) > 13 {
		t.Fatalf("unsafe label: %q", got)
	}
	badge := terminalMetadataBadge(TerminalMetadata{Title: "shell", BellCount: 2, ProgressState: 1, Progress: 999, NotificationBody: "notice"})
	if badge != "shell · bell · 100% · notice" {
		t.Fatalf("badge=%q", badge)
	}
}
