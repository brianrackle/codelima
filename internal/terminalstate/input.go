package terminalstate

import (
	"go.rockorager.dev/vaxis"
	"strings"
	"unicode"
)

func EncodePasteKey(key vaxis.Key) string {
	if key.Text != "" {
		return NormalizePasteText(key.Text)
	}

	switch key.Keycode {
	case vaxis.KeyEnter, vaxis.KeyKeyPadEnter:
		return "\r"
	case vaxis.KeyTab:
		return "\t"
	case vaxis.KeyEsc:
		return "\x1b"
	case vaxis.KeyBackspace:
		return "\x7f"
	}

	if key.Modifiers&vaxis.ModCtrl != 0 {
		// Legacy parsing decodes raw C0 bytes inside a bracketed paste as
		// Ctrl-modified keys (e.g. "\n" arrives as Ctrl+J), so recover the
		// original byte instead of emitting the decoded letter.
		switch {
		case key.Keycode == '@':
			return "\x00"
		case key.Keycode >= 'a' && key.Keycode <= 'z':
			return NormalizePasteText(string(key.Keycode - 0x60))
		case key.Keycode >= '[' && key.Keycode <= '_':
			return string(key.Keycode - 0x40)
		}
	}

	if key.Keycode <= 0 || key.Keycode >= unicode.MaxRune {
		return ""
	}
	return string(key.Keycode)
}

func NormalizePasteText(text string) string {
	return strings.ReplaceAll(text, "\r\n", "\n")
}
