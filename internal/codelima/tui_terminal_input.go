package codelima

import (
	"bytes"
	"fmt"
	"unicode"

	"git.sr.ht/~rockorager/vaxis"
)

// These encoders intentionally match the Vaxis terminal widget behavior so the
// Ghostty backend can preserve the existing shell input contract.
func encodeTUITerminalKey(key vaxis.Key, applicationKeypad bool, cursorKeysApplication bool) string {
	xtermMods := key.Modifiers & vaxis.ModShift
	xtermMods |= key.Modifiers & vaxis.ModAlt
	xtermMods |= key.Modifiers & vaxis.ModCtrl
	if xtermMods == 0 {
		if val, ok := tuiTerminalKeymap[key.Keycode]; ok {
			return val
		}
		switch cursorKeysApplication {
		case true:
			if val, ok := tuiCursorKeysApplicationMode[key.Keycode]; ok {
				return val
			}
		case false:
			if val, ok := tuiCursorKeysNormalMode[key.Keycode]; ok {
				return val
			}
		}

		switch applicationKeypad {
		case true:
			if val, ok := tuiApplicationKeymap[key.Keycode]; ok {
				return val
			}
		case false:
			if val, ok := tuiNumericKeymap[key.Keycode]; ok {
				return val
			}
		}

		if key.Keycode < unicode.MaxRune {
			return string(key.Keycode)
		}
	}

	if val, ok := tuiTerminalXtermKeymap[key.Keycode]; ok {
		return fmt.Sprintf("\x1B[%d;%d%c", val.number, int(xtermMods)+1, val.final)
	}

	if key.Text != "" && key.Modifiers&vaxis.ModCtrl == 0 && key.Modifiers&vaxis.ModAlt == 0 {
		return key.Text
	}

	buf := bytes.NewBuffer(nil)
	if key.Keycode >= unicode.MaxRune {
		return ""
	}

	if xtermMods&vaxis.ModAlt != 0 {
		buf.WriteRune('\x1b')
	}
	if xtermMods&vaxis.ModCtrl != 0 {
		if unicode.IsLower(key.Keycode) {
			buf.WriteRune(key.Keycode - 0x60)
			return buf.String()
		}
		switch key.Keycode {
		case '1':
			buf.WriteRune('1')
		case '2':
			buf.WriteRune(0x00)
		case '3':
			buf.WriteRune(0x1b)
		case '4':
			buf.WriteRune(0x1c)
		case '5':
			buf.WriteRune(0x1d)
		case '6':
			buf.WriteRune(0x1e)
		case '7':
			buf.WriteRune(0x1f)
		case '8':
			buf.WriteRune(0x7f)
		case '9':
		default:
			buf.WriteRune(key.Keycode - 0x40)
		}
		return buf.String()
	}
	if xtermMods&vaxis.ModShift != 0 {
		if key.ShiftedCode > 0 {
			buf.WriteRune(key.ShiftedCode)
		} else {
			buf.WriteRune(key.Keycode)
		}
		return buf.String()
	}

	buf.WriteRune(key.Keycode)
	return buf.String()
}

func encodeTUITerminalMouse(msg vaxis.Mouse, sgr bool, drag bool, motion bool) string {
	if !motion && msg.EventType == vaxis.EventMotion && msg.Button == vaxis.MouseNoButton {
		return ""
	}
	if !drag && msg.EventType == vaxis.EventMotion {
		return ""
	}

	if sgr {
		switch msg.EventType {
		case vaxis.EventMotion:
			return fmt.Sprintf("\x1b[<%d;%d;%dM", msg.Button+32, msg.Col+1, msg.Row+1)
		case vaxis.EventPress:
			return fmt.Sprintf("\x1b[<%d;%d;%dM", msg.Button, msg.Col+1, msg.Row+1)
		case vaxis.EventRelease:
			return fmt.Sprintf("\x1b[<%d;%d;%dm", msg.Button, msg.Col+1, msg.Row+1)
		default:
			return ""
		}
	}

	encodedCol := 32 + msg.Col + 1
	encodedRow := 32 + msg.Row + 1
	return fmt.Sprintf("\x1b[M%c%c%c", msg.Button+32, encodedCol, encodedRow)
}

type tuiTerminalKeycode struct {
	number int
	final  rune
}

var tuiTerminalXtermKeymap = map[rune]tuiTerminalKeycode{
	vaxis.KeyUp:     {1, 'A'},
	vaxis.KeyDown:   {1, 'B'},
	vaxis.KeyRight:  {1, 'C'},
	vaxis.KeyLeft:   {1, 'D'},
	vaxis.KeyEnd:    {1, 'F'},
	vaxis.KeyHome:   {1, 'H'},
	vaxis.KeyInsert: {2, '~'},
	vaxis.KeyDelete: {3, '~'},
	vaxis.KeyPgUp:   {5, '~'},
	vaxis.KeyPgDown: {6, '~'},
	vaxis.KeyF01:    {1, 'P'},
	vaxis.KeyF02:    {1, 'Q'},
	vaxis.KeyF03:    {1, 'R'},
	vaxis.KeyF04:    {1, 'S'},
	vaxis.KeyF05:    {15, '~'},
	vaxis.KeyF06:    {17, '~'},
	vaxis.KeyF07:    {18, '~'},
	vaxis.KeyF08:    {19, '~'},
	vaxis.KeyF09:    {20, '~'},
	vaxis.KeyF10:    {21, '~'},
	vaxis.KeyF11:    {23, '~'},
	vaxis.KeyF12:    {24, '~'},
}

var tuiCursorKeysApplicationMode = map[rune]string{
	vaxis.KeyUp:    "\x1BOA",
	vaxis.KeyDown:  "\x1BOB",
	vaxis.KeyRight: "\x1BOC",
	vaxis.KeyLeft:  "\x1BOD",
	vaxis.KeyEnd:   "\x1BOF",
	vaxis.KeyHome:  "\x1BOH",
}

var tuiCursorKeysNormalMode = map[rune]string{
	vaxis.KeyUp:    "\x1B[A",
	vaxis.KeyDown:  "\x1B[B",
	vaxis.KeyRight: "\x1B[C",
	vaxis.KeyLeft:  "\x1B[D",
	vaxis.KeyEnd:   "\x1B[F",
	vaxis.KeyHome:  "\x1B[H",
}

var tuiNumericKeymap = map[rune]string{
	vaxis.KeyInsert: "\x1B[2~",
	vaxis.KeyDelete: "\x1B[3~",
	vaxis.KeyPgUp:   "\x1B[5~",
	vaxis.KeyPgDown: "\x1B[6~",
}

var tuiApplicationKeymap = map[rune]string{
	vaxis.KeyInsert: "\x1B[2~",
	vaxis.KeyDelete: "\x1B[3~",
	vaxis.KeyPgUp:   "\x1B[5~",
	vaxis.KeyPgDown: "\x1B[6~",
}

var tuiTerminalKeymap = map[rune]string{
	vaxis.KeyF01: "\x1BOP",
	vaxis.KeyF02: "\x1BOQ",
	vaxis.KeyF03: "\x1BOR",
	vaxis.KeyF04: "\x1BOS",
	vaxis.KeyF05: "\x1B[15~",
	vaxis.KeyF06: "\x1B[17~",
	vaxis.KeyF07: "\x1B[18~",
	vaxis.KeyF08: "\x1B[19~",
	vaxis.KeyF09: "\x1B[20~",
	vaxis.KeyF10: "\x1B[21~",
	vaxis.KeyF11: "\x1B[23~",
	vaxis.KeyF12: "\x1B[24~",
	vaxis.KeyF13: "\x1B[1;2P",
	vaxis.KeyF14: "\x1B[1;2Q",
	vaxis.KeyF15: "\x1B[1;2R",
	vaxis.KeyF16: "\x1B[1;2S",
	vaxis.KeyF17: "\x1B[15;2~",
	vaxis.KeyF18: "\x1B[17;2~",
	vaxis.KeyF19: "\x1B[18;2~",
	vaxis.KeyF20: "\x1B[19;2~",
	vaxis.KeyF21: "\x1B[20;2~",
	vaxis.KeyF22: "\x1B[21;2~",
	vaxis.KeyF23: "\x1B[23;2~",
	vaxis.KeyF24: "\x1B[24;2~",
	vaxis.KeyF25: "\x1B[1;5P",
	vaxis.KeyF26: "\x1B[1;5Q",
	vaxis.KeyF27: "\x1B[1;5R",
	vaxis.KeyF28: "\x1B[1;5S",
	vaxis.KeyF29: "\x1B[15;5~",
	vaxis.KeyF30: "\x1B[17;5~",
	vaxis.KeyF31: "\x1B[18;5~",
	vaxis.KeyF32: "\x1B[19;5~",
	vaxis.KeyF33: "\x1B[20;5~",
	vaxis.KeyF34: "\x1B[21;5~",
	vaxis.KeyF35: "\x1B[23;5~",
	vaxis.KeyF36: "\x1B[24;5~",
	vaxis.KeyF37: "\x1B[1;6P",
	vaxis.KeyF38: "\x1B[1;6Q",
	vaxis.KeyF39: "\x1B[1;6R",
	vaxis.KeyF40: "\x1B[1;6S",
}
