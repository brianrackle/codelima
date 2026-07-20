package codelima

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
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

func TestDaemonTUITerminalChunksLargePasteOnUTF8Boundaries(t *testing.T) {
	t.Parallel()

	caller := &recordingDaemonPasteCaller{}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	payload := strings.Repeat("界", daemonTerminalPasteChunkBytes/3+100)

	term.Update(vaxis.PasteStartEvent{})
	term.Update(vaxis.Key{Text: payload, EventType: vaxis.EventPaste})
	term.Update(vaxis.PasteEndEvent{})
	term.Detach()

	if len(caller.calls) < 2 {
		t.Fatalf("large paste made %d RPC calls, want multiple bounded chunks", len(caller.calls))
	}
	var rebuilt strings.Builder
	for index, call := range caller.calls {
		text, _ := call.params["text"].(string)
		if len(text) > daemonTerminalPasteChunkBytes {
			t.Fatalf("paste chunk %d has %d bytes, limit is %d", index, len(text), daemonTerminalPasteChunkBytes)
		}
		if !utf8.ValidString(text) {
			t.Fatalf("paste chunk %d split a UTF-8 code point", index)
		}
		rebuilt.WriteString(text)
	}
	if rebuilt.String() != payload {
		t.Fatal("chunked paste did not preserve the original text")
	}
}

func TestDaemonHostWrapsSemanticPasteInBracketedPasteEvents(t *testing.T) {
	t.Parallel()

	term := &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()}
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

	if len(term.events) != 3 {
		t.Fatalf("daemon paste produced %d terminal events, want start, key, end", len(term.events))
	}
	if _, ok := term.events[0].(vaxis.PasteStartEvent); !ok {
		t.Fatalf("first paste event = %T, want vaxis.PasteStartEvent", term.events[0])
	}
	key, ok := term.events[1].(vaxis.Key)
	if !ok {
		t.Fatalf("paste payload event = %T, want vaxis.Key", term.events[1])
	}
	if key.EventType != vaxis.EventPaste || key.Text != "one\ntwo" {
		t.Fatalf("paste payload = %#v, want EventPaste with LF newlines", key)
	}
	if _, ok := term.events[2].(vaxis.PasteEndEvent); !ok {
		t.Fatalf("last paste event = %T, want vaxis.PasteEndEvent", term.events[2])
	}
}
