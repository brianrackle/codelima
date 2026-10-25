package codelima

import (
	"fmt"
	"strings"

	"go.rockorager.dev/vaxis"
)

// PrimaryHint keeps crowded controls readable. Help and settings inspection
// retain every alias; a disabled action is explicitly labelled unbound.
func (m *compiledKeyMap) PrimaryHint(action string) string {
	if m == nil || len(m.actions[action]) == 0 {
		return "unbound"
	}
	key := m.actions[action][0]
	for _, arrow := range []struct {
		code        rune
		name, glyph string
	}{{vaxis.KeyUp, "Up", "↑"}, {vaxis.KeyDown, "Down", "↓"}, {vaxis.KeyLeft, "Left", "←"}, {vaxis.KeyRight, "Right", "→"}} {
		if key.code == arrow.code {
			return strings.TrimSuffix(key.spelling(true), arrow.name) + arrow.glyph
		}
	}
	return key.spelling(true)
}

// A legend contains stable action IDs in braces, never authored key strings.
// Space-separated IDs within one token describe related controls.
func (m *compiledKeyMap) formatHints(legend string, full bool) string {
	return m.formatHintTokens(legend, full, false)
}

func (m *compiledKeyMap) formatHintTokens(legend string, full, escapeFormat bool) string {
	var result strings.Builder
	for {
		before, rest, found := strings.Cut(legend, "{")
		result.WriteString(before)
		if !found {
			break
		}
		actions, after, closed := strings.Cut(rest, "}")
		if !closed {
			result.WriteString("{" + rest)
			break
		}
		var token strings.Builder
		ids := strings.Fields(actions)
		for i, action := range ids {
			if i != 0 {
				token.WriteString("/")
			}
			if full {
				token.WriteString(m.Hint(action))
			} else {
				token.WriteString(m.PrimaryHint(action))
			}
		}
		label := token.String()
		if !full && len(ids) == 2 && len(m.actions[ids[0]]) > 0 && len(m.actions[ids[1]]) > 0 {
			first, second := m.actions[ids[0]][0], m.actions[ids[1]][0]
			if first.modifiers == second.modifiers {
				if first.code == vaxis.KeyUp && second.code == vaxis.KeyDown {
					label = m.PrimaryHint(ids[0]) + "↓"
				}
				if first.code == vaxis.KeyLeft && second.code == vaxis.KeyRight {
					label = m.PrimaryHint(ids[0]) + "→"
				}
			}
		}
		if escapeFormat {
			label = strings.ReplaceAll(label, "%", "%%")
		}
		result.WriteString(label)
		legend = after
	}
	return result.String()
}

func (a *vaxisTUIApp) keyHints(legend string) string { return a.keyMap().formatHints(legend, false) }
func (k *tuiOverlayKeys) bindingHints(legend string) string {
	return k.overlayKeys().formatHints(legend, false)
}

func (a *vaxisTUIApp) keyHintsFormat(legend string, args ...any) string {
	return fmt.Sprintf(a.keyMap().formatHintTokens(legend, false, true), args...)
}
func (k *tuiOverlayKeys) bindingHintsFormat(legend string, args ...any) string {
	return fmt.Sprintf(k.overlayKeys().formatHintTokens(legend, false, true), args...)
}
