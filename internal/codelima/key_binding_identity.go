package codelima

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.rockorager.dev/vaxis"
)

type keyBindingIdentity struct {
	code      rune
	modifiers vaxis.ModifierMask
}

var keyBindingNames = []struct {
	name, label string
	code        rune
}{
	{"up", "Up", vaxis.KeyUp}, {"down", "Down", vaxis.KeyDown},
	{"left", "Left", vaxis.KeyLeft}, {"right", "Right", vaxis.KeyRight},
	{"tab", "Tab", vaxis.KeyTab}, {"enter", "Enter", vaxis.KeyEnter},
	{"esc", "Esc", vaxis.KeyEsc}, {"space", "Space", vaxis.KeySpace},
	{"backspace", "Backspace", vaxis.KeyBackspace}, {"delete", "Delete", vaxis.KeyDelete},
	{"insert", "Insert", vaxis.KeyInsert}, {"home", "Home", vaxis.KeyHome}, {"end", "End", vaxis.KeyEnd},
	{"pageup", "PgUp", vaxis.KeyPgUp}, {"pagedown", "PgDn", vaxis.KeyPgDown},
	{"plus", "+", '+'},
}

var keyBindingModifiers = []struct {
	name, label string
	mask        vaxis.ModifierMask
}{
	{"ctrl", "Ctrl", vaxis.ModCtrl}, {"alt", "Alt", vaxis.ModAlt},
	{"shift", "Shift", vaxis.ModShift}, {"super", "Super", vaxis.ModSuper},
	{"hyper", "Hyper", vaxis.ModHyper}, {"meta", "Meta", vaxis.ModMeta},
}

func printableBindingRune(code rune) bool {
	return unicode.IsPrint(code) && !unicode.Is(unicode.M, code)
}

func parseKeyBinding(value string) (keyBindingIdentity, error) {
	if len(value) > 64 {
		return keyBindingIdentity{}, fmt.Errorf("maximum spelling length is 64 bytes")
	}
	if !utf8.ValidString(value) || value == "" {
		return keyBindingIdentity{}, fmt.Errorf("expected a named key or one printable character")
	}
	if code, size := utf8.DecodeRuneInString(value); size == len(value) && printableBindingRune(code) {
		return normalizeKeyBinding(keyBindingIdentity{code: code}), nil
	}
	parts := strings.Split(value, "+")
	var identity keyBindingIdentity
	for _, part := range parts[:len(parts)-1] {
		var mask vaxis.ModifierMask
		for _, modifier := range keyBindingModifiers {
			if strings.EqualFold(part, modifier.name) {
				mask = modifier.mask
				break
			}
		}
		if mask == 0 {
			return identity, fmt.Errorf("unknown modifier %q", part)
		}
		if identity.modifiers&mask != 0 {
			return identity, fmt.Errorf("duplicate modifier %q", part)
		}
		identity.modifiers |= mask
	}
	name := parts[len(parts)-1]
	if code, size := utf8.DecodeRuneInString(name); size > 0 && size == len(name) && printableBindingRune(code) {
		identity.code = code
		return normalizeKeyBinding(identity), nil
	}
	lower := strings.ToLower(name)
	switch lower {
	case "escape":
		lower = "esc"
	case "pgup":
		lower = "pageup"
	case "pgdn", "pgdown":
		lower = "pagedown"
	}
	for _, named := range keyBindingNames {
		if named.name == lower {
			identity.code = named.code
			return normalizeKeyBinding(identity), nil
		}
	}
	if strings.HasPrefix(lower, "f") {
		if number, err := strconv.Atoi(lower[1:]); err == nil && number >= 1 && number <= 63 && lower == "f"+strconv.Itoa(number) {
			identity.code = vaxis.KeyF01 + rune(number-1)
			return normalizeKeyBinding(identity), nil
		}
	}
	return identity, fmt.Errorf("unknown key %q", name)
}

func normalizeKeyBinding(identity keyBindingIdentity) keyBindingIdentity {
	identity.modifiers &^= vaxis.ModCapsLock | vaxis.ModNumLock
	if identity.modifiers&vaxis.ModShift != 0 && printableBindingRune(identity.code) && identity.code != ' ' {
		if unicode.IsLetter(identity.code) {
			identity.code = unicode.ToUpper(identity.code)
			identity.modifiers &^= vaxis.ModShift
		} else if strings.ContainsRune("~!@#$%^&*()_+{}|:\"<>?", identity.code) {
			identity.modifiers &^= vaxis.ModShift
		}
	}
	if identity.modifiers&vaxis.ModCtrl != 0 {
		switch identity.code {
		case 'i':
			identity.code = vaxis.KeyTab
		case 'm':
			identity.code = vaxis.KeyEnter
		case '[':
			identity.code = vaxis.KeyEsc
		case 'h':
			identity.code = vaxis.KeyBackspace
		default:
			return identity
		}
		identity.modifiers &^= vaxis.ModCtrl
	}
	return identity
}

func (identity keyBindingIdentity) spelling(display bool) string {
	parts := make([]string, 0, 7)
	for _, modifier := range keyBindingModifiers {
		if identity.modifiers&modifier.mask != 0 {
			name := modifier.name
			if display {
				name = modifier.label
			}
			parts = append(parts, name)
		}
	}
	name := string(identity.code)
	if identity.code >= vaxis.KeyF01 && identity.code <= vaxis.KeyF63 {
		name = fmt.Sprintf("f%d", identity.code-vaxis.KeyF01+1)
		if display {
			name = strings.ToUpper(name)
		}
	} else {
		for _, named := range keyBindingNames {
			if named.code == identity.code {
				name = named.name
				if display {
					name = named.label
				}
				break
			}
		}
	}
	return strings.Join(append(parts, name), "+")
}

func keyBindingCandidates(key vaxis.Key) []keyBindingIdentity {
	modifiers := key.Modifiers &^ (vaxis.ModCapsLock | vaxis.ModNumLock)
	candidates := make([]keyBindingIdentity, 0, 4)
	var logical rune
	if modifiers&^vaxis.ModShift == 0 {
		if code, size := utf8.DecodeRuneInString(key.Text); size > 0 && size == len(key.Text) && utf8.ValidString(key.Text) && printableBindingRune(code) {
			logical = code
			candidates = append(candidates, keyBindingIdentity{code: code})
		}
	}
	shiftedCode := rune(0)
	if modifiers&vaxis.ModShift != 0 {
		shiftedCode = key.ShiftedCode
	}
	for _, candidate := range []keyBindingIdentity{
		{code: shiftedCode, modifiers: modifiers &^ vaxis.ModShift},
		{code: key.Keycode, modifiers: modifiers},
		{code: key.BaseLayoutCode, modifiers: modifiers},
	} {
		if candidate.code == 0 {
			continue
		}
		candidate = normalizeKeyBinding(candidate)
		// Logical case is authoritative even when CapsLock reports an unshifted
		// physical code. Do not fall through from an unbound T to a bound t.
		if logical != 0 && candidate.code != logical && unicode.ToLower(candidate.code) == unicode.ToLower(logical) {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}
