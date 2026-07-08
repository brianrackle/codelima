package terminal

import (
	"fmt"
	"time"
)

// TerminalRuntime owns a single live terminal backend, paired with the opaque
// TerminalID it is registered under. Backend is the concrete backend type the
// owner supplies (the TUI's live terminal today); keeping it a type parameter
// lets this package stay a leaf — it never imports the parent package or any UI
// library. It is a deliberate migration shim: in later tracks the backend
// becomes a runtime actor, but the identity and ownership model stays the same.
type TerminalRuntime[B any] struct {
	ID      TerminalID
	Backend B
}

// TerminalRuntimeRegistry is the single owner of live terminal runtimes: a
// plain map from opaque TerminalID to runtime. It holds NO lock and is not safe
// for concurrent use — it is meant to be owned by one goroutine (the TUI event
// loop today, the daemon later). That lockless, single-owner invariant is what
// keeps the later actor refactor tractable, so a mutex must not be added here.
type TerminalRuntimeRegistry[B any] struct {
	runtimes map[TerminalID]*TerminalRuntime[B]
	counter  uint64
}

// NewTerminalRuntimeRegistry returns an empty registry.
func NewTerminalRuntimeRegistry[B any]() *TerminalRuntimeRegistry[B] {
	return &TerminalRuntimeRegistry[B]{runtimes: map[TerminalID]*TerminalRuntime[B]{}}
}

// Allocate mints a fresh opaque TerminalID, registers backend under it, and
// returns the runtime.
func (r *TerminalRuntimeRegistry[B]) Allocate(backend B) *TerminalRuntime[B] {
	id := r.nextID()
	runtime := &TerminalRuntime[B]{ID: id, Backend: backend}
	r.runtimes[id] = runtime
	return runtime
}

// nextID mints an opaque identity of the form
// "term_<unix-nano-hex>_<counter-hex>". It is deliberately NOT derived from any
// tab index, pane, layout position, or TargetKey: identity must survive any
// change in how terminals are arranged, displayed, or owned (ADR 61). The
// monotonic counter guarantees uniqueness even for allocations within one
// nanosecond.
func (r *TerminalRuntimeRegistry[B]) nextID() TerminalID {
	r.counter++
	return TerminalID(fmt.Sprintf("term_%x_%x", time.Now().UnixNano(), r.counter))
}

// Lookup returns the runtime registered under id, if any.
func (r *TerminalRuntimeRegistry[B]) Lookup(id TerminalID) (*TerminalRuntime[B], bool) {
	runtime, ok := r.runtimes[id]
	return runtime, ok
}

// Remove deregisters and returns the runtime for id, if it was present. The
// caller owns any teardown the backend needs; the registry only forgets it.
func (r *TerminalRuntimeRegistry[B]) Remove(id TerminalID) (*TerminalRuntime[B], bool) {
	runtime, ok := r.runtimes[id]
	if !ok {
		return nil, false
	}
	delete(r.runtimes, id)
	return runtime, true
}

// Len reports how many runtimes are registered.
func (r *TerminalRuntimeRegistry[B]) Len() int {
	return len(r.runtimes)
}
