package codelima

import (
	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"testing"
)

func TestDaemonHostClipboardUsesOnlySeatSinkForLiveTerminal(t *testing.T) {
	count := 0
	host := &daemonHost{
		terminals: map[string]*daemonTerminalEntry{"term": {state: daemon.TerminalState{TerminalID: "term", TabID: "tab"}}},
		broadcast: func(string, any) { t.Fatal("clipboard was broadcast to observers") },
		emitInputOwner: func(name string, data any) {
			count++
			value, ok := data.(daemon.TerminalClipboardEvent)
			if name != daemon.EventTerminalClipboard || !ok || value.TerminalID != "term" || value.TabID != "tab" || value.Text != "private" {
				t.Fatalf("invalid private clipboard event: %s %+v", name, data)
			}
		},
	}
	host.handleTerminalEvent("term", tuiClipboardEvent{Text: "private"})
	if count != 1 {
		t.Fatal("clipboard did not reach seat sink")
	}
	host.handleTerminalEvent("closed-terminal", tuiClipboardEvent{Text: "late"})
	if count != 1 {
		t.Fatal("closed terminal emitted a clipboard effect")
	}
	host.emitInputOwner = nil
	host.handleTerminalEvent("term", tuiClipboardEvent{Text: "unwired"})
	if count != 1 {
		t.Fatal("unwired clipboard did not fail closed")
	}
}
