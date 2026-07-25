package codelima

import (
	"os/exec"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
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

// tuiDaemonDisconnectedEvent reports that the current physical daemon
// connection failed. The logical TUI session remains alive and reconnects.
type tuiDaemonDisconnectedEvent struct {
	Err error
}

// tuiDaemonSynchronizedEvent installs one authoritative daemon state after a
// physical connection has authenticated and subscribed.
type tuiDaemonSynchronizedEvent struct {
	Snapshot daemon.SyncSnapshot
	applied  chan error
}

func (e tuiDaemonSynchronizedEvent) complete(err error) {
	if e.applied == nil {
		return
	}
	select {
	case e.applied <- err:
	default:
	}
}

// tuiDaemonTerminalDirtyEvent crosses from the daemon event-reader goroutine
// to the single-owner TUI event loop before it touches the terminal registry.
type tuiDaemonTerminalDirtyEvent struct {
	TerminalID string
}
