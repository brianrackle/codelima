//go:build cgo && (darwin || linux)

// Package ghostty owns the native terminal engine. Only the renderer worker
// imports this package; the main executable supervises it over portable RPC.
package ghostty

import (
	"log/slog"

	"go.rockorager.dev/vaxis"
)

type Terminal = ghosttyTUITerminal

// New constructs an actor-owned engine with owned-value event/response sinks.
// It does not start a shell or acquire a PTY in the renderer worker.
func New(id string, postEvent func(vaxis.Event), postPTYWrite func(uint64, uint32, []byte)) (*Terminal, error) {
	base, err := newGhosttyTUITerminal(id, postEvent)
	if err != nil {
		return nil, err
	}
	terminal := base.(*ghosttyTUITerminal)
	terminal.mu.Lock()
	terminal.rendererOutput = postPTYWrite
	terminal.mu.Unlock()
	return terminal, nil
}

func (t *ghosttyTUITerminal) Output(id uint64, data []byte) {
	t.sendSync(cmdRendererOutput{EventID: id, Data: data})
}

func (t *ghosttyTUITerminal) UpdateEvent(id uint64, event vaxis.Event) {
	t.sendSync(cmdRendererUpdate{EventID: id, Event: event})
}

func (t *ghosttyTUITerminal) Health() bool { return t.rendererHealth() }

func packageLog() *slog.Logger { return slog.Default() }
