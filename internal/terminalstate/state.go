// Package terminalstate contains portable terminal data shared by the session
// owner and the native worker. It has no native renderer dependency.
package terminalstate

import (
	"errors"
	"os"
	"os/exec"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/terminalgraphics"
	"go.rockorager.dev/vaxis"
)

type ReadSource int

const (
	ReadVisible ReadSource = iota
	ReadRecent
)

type ReadFormat int

const (
	ReadText ReadFormat = iota
	ReadANSI
)

type ReadResult struct {
	Text       string
	Generation uint64
	Err        error
}

type SnapshotCell = daemon.SnapshotCell
type Metadata = daemon.TerminalMetadata

type Snapshot struct {
	Graphics      terminalgraphics.Frame
	Metadata      Metadata
	Cols          int
	Rows          int
	Cells         []SnapshotCell
	CursorX       int
	CursorY       int
	CursorVisible bool
	Generation    uint64
	CapturesMouse bool
	Stale         bool
}

type SnapshotResult struct {
	Snapshot Snapshot
	Err      error
}

// CheckpointState combines the native FINISH stream with application-owned
// observations that the native format intentionally does not preserve.
type CheckpointState struct {
	CellWidth      int      `json:"cell_width,omitempty"`
	CellHeight     int      `json:"cell_height,omitempty"`
	Data           []byte   `json:"data"`
	Generation     uint64   `json:"generation"`
	Cols           int      `json:"cols"`
	Rows           int      `json:"rows"`
	ViewportOffset int      `json:"viewport_offset"`
	Focused        bool     `json:"focused"`
	Metadata       Metadata `json:"metadata"`
	Colors         *Colors  `json:"colors,omitempty"`
}

const CheckpointMaxBytes = 16 << 20
const TermEnv = "xterm-256color"

var (
	ErrClosed         = errors.New("terminal runtime closed")
	ErrSnapshotFailed = errors.New("terminal snapshot unavailable")
)

type RuntimeState int

const (
	Running RuntimeState = iota
	Quiescing
	Quiesced
	Released
)

type HandoffState struct {
	PTY           *os.File
	ChildPID      int
	Cols          int
	Rows          int
	Replay        []byte
	ReplayPartial bool
	Recovery      []byte
	Err           error
}

type ClosedEvent struct {
	SessionKey string
	Err        error
}
type ErrorEvent struct {
	TargetKey string
	Err       error
}
type ClipboardEvent struct {
	TargetKey string
	Text      string
}

// Terminal is the presentation/session contract, independent of its engine.
// The native worker uses only its engine methods; this interface also keeps
// the characterized standalone terminal harness usable outside the main app.
type Terminal interface {
	Start(*exec.Cmd) error
	Resize(int, int)
	Update(vaxis.Event)
	Draw(vaxis.Window)
	Close()
	Focus()
	Blur()
	String() string
	TermEnv() string
	HyperlinkAt(int, int) (string, bool)
	CapturesMouse() bool
}
