package terminalstate

// VTBoundary tracks whether a byte stream ends between complete UTF-8 and VT
// sequences. It retains no payload. ANSI state export can resume only at such a
// boundary: the exported screen does not contain an unfinished parser command.
// The small state is included in native checkpoints for the same reason.
type VTBoundary struct {
	State uint8 `json:"state,omitempty"`
	UTF8  uint8 `json:"utf8,omitempty"`
	OSC   bool  `json:"osc,omitempty"`
}

const (
	vtGround uint8 = iota
	vtEscape
	vtCSI
	vtString
	vtStringEscape
)

func (v VTBoundary) Ground() bool { return v.State == vtGround && v.UTF8 == 0 }

func (v *VTBoundary) Feed(data []byte) {
	for _, b := range data {
		if v.UTF8 > 0 {
			v.UTF8--
			if b&0xc0 == 0x80 {
				continue
			}
			v.UTF8 = 0
		}
		switch {
		case b >= 0xc2 && b <= 0xdf:
			v.UTF8 = 1
			continue
		case b >= 0xe0 && b <= 0xef:
			v.UTF8 = 2
			continue
		case b >= 0xf0 && b <= 0xf4:
			v.UTF8 = 3
			continue
		}
		if b == 0x18 || b == 0x1a || b == 0x9c {
			v.State = vtGround
			continue
		}
		if v.State == vtStringEscape && b != '\\' && b != 0x1b {
			// ESC cancels a control string and begins a new escape command.
			v.State = vtEscape
		}
		if v.State == vtString || v.State == vtStringEscape {
			switch {
			case b == 7 && v.OSC, b == '\\' && v.State == vtStringEscape:
				v.State = vtGround
			case b == 0x1b:
				v.State = vtStringEscape
			default:
				v.State = vtString
			}
			continue
		}
		switch b {
		case 0x1b:
			v.State = vtEscape
			continue
		case 0x9b:
			v.State = vtCSI
			continue
		case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
			v.State, v.OSC = vtString, b == 0x9d
			continue
		}
		switch v.State {
		case vtEscape:
			switch b {
			case '[':
				v.State = vtCSI
			case 'P', 'X', ']', '^', '_':
				v.State, v.OSC = vtString, b == ']'
			default:
				if b >= 0x30 && b <= 0x7e {
					v.State = vtGround
				}
			}
		case vtCSI:
			if b >= 0x40 && b <= 0x7e {
				v.State = vtGround
			}
		}
	}
}
