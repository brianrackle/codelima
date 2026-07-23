package codelima

import (
	"os/exec"

	"git.sr.ht/~rockorager/vaxis"
)

type tuiTerminal interface {
	Start(*exec.Cmd) error
	Resize(width, height int)
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

// tuiTerminalClosedEvent reports that one terminal tab (session) closed;
// SessionKey is the tab's session key ("<target>#<n>"), not a target key.
type tuiTerminalClosedEvent struct {
	SessionKey string
	Err        error
}

type tuiTerminalErrorEvent struct {
	TargetKey string
	Err       error
}

// tuiDaemonDisconnectedEvent reports that the TUI's daemon session cannot
// service any more requests. The UI latches this state so routine focus events
// do not keep writing ownership requests to a known-dead socket or overwrite
// the recovery guidance with transport errors.
type tuiDaemonDisconnectedEvent struct {
	Err error
}

// tuiDaemonTerminalDirtyEvent crosses from the daemon event-reader goroutine
// to the single-owner TUI event loop before it touches the terminal registry.
type tuiDaemonTerminalDirtyEvent struct {
	TerminalID string
}
