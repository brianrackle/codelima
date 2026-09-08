package codelima

import "github.com/brianrackle/codelima/internal/terminalstate"

type ReadSource = terminalstate.ReadSource
type ReadFormat = terminalstate.ReadFormat
type ReadResult = terminalstate.ReadResult
type SnapshotCell = terminalstate.SnapshotCell
type TerminalSnapshot = terminalstate.Snapshot
type SnapshotResult = terminalstate.SnapshotResult
type terminalCheckpointState = terminalstate.CheckpointState
type runtimeState = terminalstate.RuntimeState
type handoffTerminalState = terminalstate.HandoffState
type tuiTerminalClosedEvent = terminalstate.ClosedEvent
type tuiTerminalErrorEvent = terminalstate.ErrorEvent
type tuiClipboardEvent = terminalstate.ClipboardEvent

const (
	ReadVisible                = terminalstate.ReadVisible
	ReadRecent                 = terminalstate.ReadRecent
	ReadText                   = terminalstate.ReadText
	ReadANSI                   = terminalstate.ReadANSI
	rendererCheckpointMaxBytes = terminalstate.CheckpointMaxBytes
	runtimeStateRunning        = terminalstate.Running
	runtimeStateQuiescing      = terminalstate.Quiescing
	runtimeStateQuiesced       = terminalstate.Quiesced
	runtimeStateReleased       = terminalstate.Released
)

var (
	errTerminalClosed = terminalstate.ErrClosed
	errSnapshotFailed = terminalstate.ErrSnapshotFailed
)
