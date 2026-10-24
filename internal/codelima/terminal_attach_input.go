package codelima

import (
	"bytes"
	"strings"
)

// rawAttachInput recognizes only the detach key and paste boundaries. Every
// other byte is forwarded unchanged, including terminal query replies, mouse
// reports, Option/Alt keys and application keyboard protocols.
type rawAttachInput struct {
	pending []byte
	paste   bool
}

func (f *rawAttachInput) feed(data []byte) (output []byte, detach bool) {
	for _, b := range data {
		if len(f.pending) == 0 {
			switch {
			case b == 0x1d && !f.paste:
				return output, true
			case b == 0x1b:
				f.pending = append(f.pending, b)
			default:
				output = append(output, b)
			}
			continue
		}
		f.pending = append(f.pending, b)
		if len(f.pending) == 2 && b != '[' {
			output = append(output, f.flush()...)
			continue
		}
		if len(f.pending) > 2 && b >= 0x40 && b <= 0x7e {
			switch {
			case bytes.Equal(f.pending, []byte("\x1b[200~")):
				f.paste = true
			case bytes.Equal(f.pending, []byte("\x1b[201~")):
				f.paste = false
			case !f.paste && rawKittyDetach(f.pending):
				f.pending = nil
				return output, true
			}
			output = append(output, f.flush()...)
		} else if len(f.pending) >= 128 {
			output = append(output, f.flush()...)
		}
	}
	return output, false
}

func (f *rawAttachInput) flush() []byte {
	data := f.pending
	f.pending = nil
	return data
}

func rawKittyDetach(data []byte) bool {
	if len(data) < 7 || data[len(data)-1] != 'u' {
		return false
	}
	parts := strings.Split(string(data[2:len(data)-1]), ";")
	if len(parts) < 2 || parts[0] != "93" {
		return false
	}
	modifier := strings.Split(parts[1], ":")
	return modifier[0] == "5" && (len(modifier) == 1 || modifier[1] == "1" || modifier[1] == "2")
}
