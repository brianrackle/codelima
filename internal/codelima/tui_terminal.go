package codelima

import (
	"os/exec"

	"git.sr.ht/~rockorager/vaxis"
)

type tuiTerminal interface {
	Start(*exec.Cmd) error
	Update(vaxis.Event)
	Draw(vaxis.Window)
	Close()
	Focus()
	Blur()
	String() string
	TermEnv() string
	HyperlinkAt(col, row int) (string, bool)
	CapturesMouse() bool
}

type tuiTerminalClosedEvent struct {
	NodeID string
	Err    error
}

type tuiTerminalErrorEvent struct {
	NodeID string
	Err    error
}
