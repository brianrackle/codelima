package codelima

import (
	"context"
	"reflect"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIFocusToggleKeyLifecycle(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		press   string
		repeat  string
		release string
	}{
		{"alt backtick", "\x1b[96;3:1u", "\x1b[96;3:2u", "\x1b[96;3:3u"},
		{"meta backtick", "\x1b[96;33:1u", "\x1b[96;33:2u", "\x1b[96;33:3u"},
		{"alt meta backtick", "\x1b[96;35:1u", "\x1b[96;35:2u", "\x1b[96;35:3u"},
		{"f6", "\x1b[17;1:1~", "\x1b[17;1:2~", "\x1b[17;1:3~"},
		{"legacy alt backtick", "\x1b`", "", ""},
		{"legacy f6", "\x1b[17~", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			press := decodedVaxisInputKey(t, test.press)
			if press.EventType != vaxis.EventPress {
				t.Fatalf("decoded press = %#v", press)
			}
			var followups []vaxis.Key
			for _, event := range []struct {
				input string
				kind  vaxis.EventType
			}{{test.repeat, vaxis.EventRepeat}, {test.release, vaxis.EventRelease}} {
				if event.input == "" {
					continue
				}
				key := decodedVaxisInputKey(t, event.input)
				if key.EventType != event.kind {
					t.Fatalf("decoded %q = %#v, want event type %v", event.input, key, event.kind)
				}
				followups = append(followups, key)
			}

			for _, route := range []string{"handleKey", "handleEvent"} {
				t.Run(route, func(t *testing.T) {
					ctx := context.Background()
					service, _ := newTestService(t)
					sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
					t.Cleanup(sessions.Close)
					state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
					if err != nil {
						t.Fatal(err)
					}
					app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: sessions}
					dispatch := func(key vaxis.Key) {
						t.Helper()
						if route == "handleKey" {
							if app.handleKey(key) {
								t.Fatal("focus toggle quit the TUI")
							}
						} else if quit, err := app.handleEvent(key); quit || err != nil {
							t.Fatalf("handleEvent(%#v) = (%v, %v)", key, quit, err)
						}
					}

					// Successive presses must work without a debounce interval.
					for cycle, want := range []tuiFocus{tuiFocusTerminal, tuiFocusTree, tuiFocusTerminal, tuiFocusTree} {
						dispatch(press)
						if state.focus != want {
							t.Fatalf("after press: focus = %q, want %q", state.focus, want)
						}
						for _, key := range followups {
							// Exercise both a quick tap and a held key in each direction.
							if cycle < 2 && key.EventType == vaxis.EventRepeat {
								continue
							}
							if isTUITerminalPayloadKey(key) {
								t.Fatalf("toggle followup classified as terminal payload: %#v", key)
							}
							dispatch(key)
							if state.focus != want {
								t.Fatalf("after event %v: focus = %q, want %q", key.EventType, state.focus, want)
							}
						}
					}
					// An unmatched repeat/release cannot toggle focus either.
					for _, key := range followups {
						dispatch(key)
						if state.focus != tuiFocusTree {
							t.Fatalf("unmatched event %v changed focus to %q", key.EventType, state.focus)
						}
					}
					term := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))
					if len(term.events) != 0 {
						t.Fatalf("focus shortcut leaked to terminal: %#v", term.events)
					}
				})
			}
		})
	}
}

func TestTUIFocusTogglePreservesTerminalPayloadEvents(t *testing.T) {
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
	term := targetTestTerminal(t, sessions, nodeTargetKey("node-root"))
	for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPaste} {
		key := vaxis.Key{Keycode: '`', EventType: kind}
		if kind == vaxis.EventPaste {
			// Even shortcut-shaped paste is payload.
			key.Modifiers = vaxis.ModAlt
		}
		if !isTUITerminalPayloadKey(key) {
			t.Fatalf("payload misclassified as a shortcut: %#v", key)
		}
		if quit, err := app.handleEvent(key); quit || err != nil {
			t.Fatalf("handleEvent(%#v) = (%v, %v)", key, quit, err)
		}
		if state.focus != tuiFocusTerminal {
			t.Fatalf("payload changed focus to %q", state.focus)
		}
		if len(term.events) != 1 || !reflect.DeepEqual(term.events[0], normalizeTUITerminalEvent(key)) {
			t.Fatalf("forwarded events = %#v, want %#v", term.events, normalizeTUITerminalEvent(key))
		}
		term.events = nil
	}
}
