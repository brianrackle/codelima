package codelima

import (
	"unicode"

	"go.rockorager.dev/vaxis"
)

// tuiKeyActivates separates shortcut identity from action activation. Repeats
// are useful for navigation; releases and pasted key-shaped text are never
// actions. Text widgets and terminal payload retain their own event handling.
func tuiKeyActivates(key vaxis.Key, pressOnly bool) bool {
	return key.EventType == vaxis.EventPress || (!pressOnly && key.EventType == vaxis.EventRepeat)
}

type tuiKeyContext struct {
	overlay tuiOverlay
	search  *tuiTerminalSearch
	focus   tuiFocus
}

func (a *vaxisTUIApp) keyContext() tuiKeyContext {
	ctx := tuiKeyContext{overlay: a.overlay, search: a.search}
	if a.state != nil {
		ctx.focus = a.state.focus
	}
	return ctx
}

// tuiKeyIdentity follows the key rather than its current modifiers: releasing
// Ctrl before the letter must not send the letter's remaining events into the
// form or shell that its shortcut just exposed. A new press always starts a new
// gesture, including in legacy protocols without distinct release events.
func tuiKeyIdentity(key vaxis.Key) rune {
	if key.Keycode != 0 {
		return unicode.ToLower(key.Keycode)
	}
	if text := []rune(key.Text); len(text) == 1 {
		return unicode.ToLower(text[0])
	}
	return 0
}

func (a *vaxisTUIApp) claimShortcutKey(key vaxis.Key) {
	id := tuiKeyIdentity(key)
	if id == 0 || key.EventType != vaxis.EventPress {
		return
	}
	if a.shortcutKeys == nil {
		a.shortcutKeys = make(map[rune]struct{})
	}
	a.shortcutKeys[id] = struct{}{}
}

func (a *vaxisTUIApp) handleKey(key vaxis.Key) bool {
	if key.EventType != vaxis.EventPaste {
		id := tuiKeyIdentity(key)
		switch key.EventType {
		case vaxis.EventPress:
			delete(a.shortcutKeys, id)
			before := a.keyContext()
			defer func() {
				// Retain the transition's triggering key even if the next surface
				// treats it as text. Ordinary typing/navigation needs no claim.
				if before != a.keyContext() {
					a.claimShortcutKey(key)
				}
			}()
		case vaxis.EventRepeat, vaxis.EventRelease:
			if _, claimed := a.shortcutKeys[id]; claimed {
				if key.EventType == vaxis.EventRelease {
					delete(a.shortcutKeys, id)
				}
				return false
			}
		}
	}
	return a.dispatchKey(key)
}
