package codelima

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type daemonPasteCall struct {
	method string
	params map[string]any
}

type recordingDaemonPasteCaller struct {
	calls []daemonPasteCall
}

func (c *recordingDaemonPasteCaller) Call(_ context.Context, method string, params any, _ any) error {
	values, _ := params.(map[string]any)
	copyValues := make(map[string]any, len(values))
	maps.Copy(copyValues, values)
	c.calls = append(c.calls, daemonPasteCall{method: method, params: copyValues})
	return nil
}

func TestDaemonTUITerminalBatchesPasteIntoOneSemanticEvent(t *testing.T) {
	t.Parallel()

	caller := &recordingDaemonPasteCaller{}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}

	term.Update(vaxis.PasteStartEvent{})
	term.Update(vaxis.Key{Text: "one", EventType: vaxis.EventPaste})
	term.Update(vaxis.Key{Keycode: 'm', Modifiers: vaxis.ModCtrl, EventType: vaxis.EventPaste})
	term.Update(vaxis.Key{Keycode: 'j', Modifiers: vaxis.ModCtrl, EventType: vaxis.EventPaste})
	term.Update(vaxis.Key{Text: "two\nthree", EventType: vaxis.EventPaste})
	if len(caller.calls) != 0 {
		t.Fatalf("paste made %d RPC calls before PasteEnd, want none", len(caller.calls))
	}

	term.Update(vaxis.PasteEndEvent{})
	term.Detach()
	if len(caller.calls) != 1 {
		t.Fatalf("paste made %d RPC calls, want one batched call", len(caller.calls))
	}
	call := caller.calls[0]
	if call.method != "terminal.send_event" {
		t.Fatalf("paste RPC method = %q, want terminal.send_event", call.method)
	}
	if call.params["type"] != "paste" {
		t.Fatalf("paste event type = %#v, want paste", call.params["type"])
	}
	if call.params["text"] != "one\ntwo\nthree" {
		t.Fatalf("paste event text = %q, want preserved LF newlines", call.params["text"])
	}
}

func TestDaemonTUITerminalRejectsLargePasteWithoutSendingPrefix(t *testing.T) {
	t.Parallel()
	caller := &recordingDaemonPasteCaller{}
	var rejected error
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{}),
		postEvent: func(event vaxis.Event) {
			if event, ok := event.(tuiTerminalErrorEvent); ok {
				rejected = event.Err
			}
		},
	}
	payload := strings.Repeat("界", terminalMaxPasteBytes/3+100)
	term.Update(vaxis.PasteStartEvent{})
	term.Update(vaxis.Key{Text: "prefix", EventType: vaxis.EventPaste})
	term.Update(vaxis.Key{Text: payload, EventType: vaxis.EventPaste})
	term.Update(vaxis.PasteEndEvent{})
	term.Detach()
	if len(caller.calls) != 0 || rejected == nil {
		t.Fatalf("oversized paste calls=%d, error=%v; want no writes and explicit rejection", len(caller.calls), rejected)
	}
}

type semanticPasteTestTerminal struct {
	*resizeCountingDaemonTerminal
	pastes []string
}

func (t *semanticPasteTestTerminal) Paste(text string) error {
	t.pastes = append(t.pastes, text)
	return nil
}

func TestDaemonHostDeliversOneSemanticPaste(t *testing.T) {
	t.Parallel()

	term := &semanticPasteTestTerminal{resizeCountingDaemonTerminal: &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()}}
	host := &daemonHost{
		terminals: map[string]*daemonTerminalEntry{
			"term-1": {state: daemon.TerminalState{TerminalID: "term-1"}, term: term},
		},
		session: filepath.Join(t.TempDir(), "session.json"),
	}
	params, err := json.Marshal(map[string]any{
		"terminal_id": "term-1",
		"type":        "paste",
		"text":        "one\ntwo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.Handle(context.Background(), daemon.ClientContext{}, "terminal.send_event", params); err != nil {
		t.Fatalf("terminal.send_event paste error = %v", err)
	}

	if len(term.pastes) != 1 || term.pastes[0] != "one\ntwo" || len(term.events) != 0 {
		t.Fatalf("semantic pastes=%q, raw events=%d; want one intact paste", term.pastes, len(term.events))
	}
}
