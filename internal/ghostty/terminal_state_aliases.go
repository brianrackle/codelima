package ghostty

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
)

type tuiTerminal = terminalstate.Terminal
type TerminalMetadata = terminalstate.Metadata
type TerminalInteractionRequest = terminalstate.InteractionRequest
type TerminalInteractionResult = terminalstate.InteractionResult
type TerminalSearchStatus = terminalstate.SearchStatus
type TerminalColors = terminalstate.Colors

const tuiEmbeddedTermEnv = terminalstate.TermEnv
const terminalMaxPasteBytes = terminalstate.MaxPasteBytes

var validateTerminalPaste = terminalstate.ValidatePaste
var validateTerminalInteraction = terminalstate.ValidateInteraction
var errUnsafeTerminalPaste = terminalstate.ErrUnsafePaste
var encodeTUITerminalPasteKey = terminalstate.EncodePasteKey
var daemonCellStyle = terminalstate.CellStyle

type daemonTerminal interface {
	tuiTerminal
	ReadVisible(ReadFormat) ReadResult
	ReadRecent(ReadFormat) ReadResult
	Snapshot() SnapshotResult
	Scroll(int)
	SendInput([]byte)
}
