package codelima

import (
	"github.com/brianrackle/codelima/internal/terminalgraphics"
	"go.rockorager.dev/vaxis"
)

// RendererTerminal is the worker-owned native engine contract. The control
// server only handles owned Go values; it cannot reach native pointers, actor
// queues, or native state mutexes. The executable injects the native factory.
type RendererTerminal interface {
	Output(uint64, []byte)
	UpdateEvent(uint64, vaxis.Event)
	Resize(int, int)
	ResizePixels(int, int, int, int) error
	Focus()
	Blur()
	Scroll(int)
	ReadVisible(ReadFormat) ReadResult
	ReadRecent(ReadFormat) ReadResult
	Publish() (SnapshotResult, ReadResult)
	PublishGraphics() (SnapshotResult, ReadResult, terminalgraphics.Frame, error)
	EncodePaste(string) ([]byte, error)
	Checkpoint() (terminalCheckpointState, error)
	RestoreCheckpoint(terminalCheckpointState) error
	Interact(TerminalInteractionRequest) (TerminalInteractionResult, error)
	Health() bool
	Close()
}

type RendererTerminalFactory func(
	terminalID string,
	postEvent func(vaxis.Event),
	postPTYWrite func(eventID uint64, ordinal uint32, data []byte),
) (RendererTerminal, error)
