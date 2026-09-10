package codelima

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

func TestTUITabOpenCloseKeyLifecycle(t *testing.T) {
	t.Parallel()
	for _, input := range []struct {
		name  string
		open  string
		close string
		kind  terminal.TerminalKind
	}{
		{"alt guest", "\x1b[116;3:%du", "\x1b[119;3:%du", terminal.NodeShell},
		{"meta guest", "\x1b[116;33:%du", "\x1b[119;33:%du", terminal.NodeShell},
		{"alt meta guest", "\x1b[116;35:%du", "\x1b[119;35:%du", terminal.NodeShell},
		{"alt host", "\x1b[116;4:%du", "\x1b[119;3:%du", terminal.NodeHostShell},
		{"meta host", "\x1b[116;34:%du", "\x1b[119;33:%du", terminal.NodeHostShell},
		{"alt meta host", "\x1b[116;36:%du", "\x1b[119;35:%du", terminal.NodeHostShell},
		{"option guest glyph", "\x1b[8224;1:%du", "\x1b[8721;1:%du", terminal.NodeShell},
		{"option host glyph", "\x1b[711;1:%du", "\x1b[8721;1:%du", terminal.NodeHostShell},
	} {
		for _, focus := range []tuiFocus{tuiFocusTree, tuiFocusTerminal} {
			for _, route := range []string{"handleKey", "handleEvent"} {
				t.Run(input.name+"/"+string(focus)+"/"+route, func(t *testing.T) {
					ctx := context.Background()
					service, _ := newTestService(t)
					sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
					t.Cleanup(sessions.Close)
					manager := newSharedFakeTUISessionManager(sessions)
					state, err := newTUIState(testTUINodes(t), manager)
					if err != nil {
						t.Fatal(err)
					}
					app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: sessions}
					if err := app.openTerminalTab(); err != nil {
						t.Fatal(err)
					}
					if focus == tuiFocusTerminal {
						if err := state.focusTerminal(); err != nil {
							t.Fatal(err)
						}
					}
					target := state.activeTerminalTargetKey()
					initial := state.activeSessionKey()
					var terms []*fakeTUITerminal
					captureTerminal := func(key string) {
						t.Helper()
						term, ok := sessions.SessionTerminal(key)
						if !ok {
							t.Fatalf("missing terminal %q", key)
						}
						terms = append(terms, term.(*fakeTUITerminal))
					}
					captureTerminal(initial)
					dispatch := func(sequence string, eventType vaxis.EventType) {
						t.Helper()
						key := decodedVaxisInputKey(t, fmt.Sprintf(sequence, int(eventType)+1))
						if key.EventType != eventType || isTUITerminalPayloadKey(key) {
							t.Fatalf("decoded shortcut = %#v, want event type %v", key, eventType)
						}
						if route == "handleKey" {
							if app.handleKey(key) {
								t.Fatal("tab shortcut quit the TUI")
							}
						} else if quit, err := app.handleEvent(key); quit || err != nil {
							t.Fatalf("handleEvent() = (%v, %v)", quit, err)
						}
					}
					assertTabs := func(want []string, active string, wantFocus tuiFocus) {
						t.Helper()
						got := sessions.TargetSessionKeys(target)
						if !reflect.DeepEqual(got, want) || state.activeSessionKey() != active || state.focus != wantFocus {
							t.Fatalf("tabs = %v, active = %q, focus = %q; want %v, %q, %q", got, state.activeSessionKey(), state.focus, want, active, wantFocus)
						}
					}
					want := []string{initial}
					// Orphan repeats/releases must neither open nor close a tab.
					for _, sequence := range []string{input.open, input.close} {
						for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
							dispatch(sequence, kind)
							assertTabs(want, initial, focus)
						}
					}
					// A tap and a held key each open once. Fresh presses work
					// immediately, without a time-based debounce.
					for cycle := range 2 {
						dispatch(input.open, vaxis.EventPress)
						opened := state.activeSessionKey()
						want = append(want, opened)
						assertTabs(want, opened, focus)
						session, ok := sessions.Session(opened)
						if !ok || session.shellKind != input.kind {
							t.Fatalf("opened session = %#v, want kind %v", session, input.kind)
						}
						captureTerminal(opened)
						if cycle == 1 {
							for range 3 {
								dispatch(input.open, vaxis.EventRepeat)
								assertTabs(want, opened, focus)
							}
						}
						dispatch(input.open, vaxis.EventRelease)
						assertTabs(want, opened, focus)
					}
					// Closing protects the adjacent tab from repeats/releases,
					// including when closing the last tab returns focus to the tree.
					for len(want) > 0 {
						dispatch(input.close, vaxis.EventPress)
						want = want[:len(want)-1]
						active, wantFocus := "", focus
						if len(want) > 0 {
							active = want[len(want)-1]
						} else {
							wantFocus = tuiFocusTree
						}
						assertTabs(want, active, wantFocus)
						for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRepeat, vaxis.EventRelease} {
							dispatch(input.close, kind)
							assertTabs(want, active, wantFocus)
						}
					}
					for _, term := range terms {
						if len(term.events) != 0 {
							t.Fatalf("shortcut leaked into a terminal: %#v", term.events)
						}
					}
					if app.status != "" {
						t.Fatalf("shortcut followup left an error: %s", app.status)
					}
				})
			}
		}
	}
}

func TestTUITabShortcutsPreserveTerminalPayloadEvents(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	t.Cleanup(sessions.Close)
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatal(err)
	}
	if err := state.focusTerminal(); err != nil {
		t.Fatal(err)
	}
	app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: sessions}
	term := targetTestTerminal(t, sessions, state.activeTerminalTargetKey())
	initial := state.activeSessionKey()
	for _, code := range []rune{'t', 'T', 'w', '†', 'ˇ', '∑'} {
		for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPaste} {
			// Option-layer glyphs are always shortcuts unless pasted.
			if code > 127 && kind != vaxis.EventPaste {
				continue
			}
			key := vaxis.Key{Keycode: code, EventType: kind}
			if kind == vaxis.EventPaste && code < 127 {
				key.Modifiers = vaxis.ModAlt
			}
			if !isTUITerminalPayloadKey(key) {
				t.Fatalf("payload misclassified: %#v", key)
			}
			if quit, err := app.handleEvent(key); quit || err != nil {
				t.Fatalf("handleEvent() = (%v, %v)", quit, err)
			}
			if state.activeSessionKey() != initial || len(sessions.TargetSessionKeys(state.activeTerminalTargetKey())) != 1 {
				t.Fatal("terminal payload changed the tabs")
			}
			if len(term.events) != 1 || !reflect.DeepEqual(term.events[0], normalizeTUITerminalEvent(key)) {
				t.Fatalf("forwarded events = %#v, want %#v", term.events, normalizeTUITerminalEvent(key))
			}
			term.events = nil
		}
	}
}
