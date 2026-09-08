//go:build cgo && (darwin || linux)

package ghostty

import (
	"time"

	"go.rockorager.dev/vaxis"
)

// This file holds the runtime-actor orchestration for the Ghostty embedded
// terminal (work item 2.1, ADR 63). The cgo-touching engine methods it drives
// (ingestPTY, applyResize, serveRead, serveSnapshot, teardown, ...) live in
// tui_terminal_ghostty_cgo.go; nothing here imports "C".
//
// The model: each live terminal owns ONE actor goroutine (runActor) that is the
// sole mutator of the PTY + libghostty-vt emulator on the live path. Callers
// interact only by sending commands over t.commands (and, for the read pump, over
// t.readCh/t.readErrCh). Draw and the other read-only interface methods
// (String/HyperlinkAt/CapturesMouse) stay synchronous and share t.mu, which the
// actor's handlers also take — the "narrowly scoped lock the actor honors"
// consistency model (ADR 63, Draw-consistency decision (b)).

// runtimeCommand is the marker interface for every message the actor accepts.
// The public field shapes match the daemon protocol (plan Part F §2.1 / §3.3);
// synchronous delivery is layered on top via actorEnvelope.ack rather than by
// widening these structs, so the command set stays byte-shaped for Track 3.
type runtimeCommand interface{}

// cmdInput carries raw input bytes to write to the child (daemon
// terminal.send_input path). The TUI's own key/mouse events arrive as cmdUpdate
// instead, because they need mode-aware encoding against the live emulator.
type cmdInput struct{ Data []byte }

type cmdPaste struct {
	EventID uint64
	Text    string
	Reply   chan error
}

type cmdEncodePaste struct {
	Text  string
	Reply chan encodedPasteResult
}

type cmdInteract struct {
	Request TerminalInteractionRequest
	Reply   chan interactionOutcome
}
type interactionOutcome struct {
	Result TerminalInteractionResult
	Err    error
}
type encodedPasteResult struct {
	Data []byte
	Err  error
}

// cmdRendererOutput applies one journaled PTY-output event in an isolated
// renderer. EventID fences renderer-generated terminal responses across replay.
type cmdRendererOutput struct {
	EventID uint64
	Data    []byte
}

// cmdRendererHealth crosses the same actor queue as native rendering work. A
// worker-level socket ping would not prove that the Ghostty-owning actor can
// make progress.
type cmdRendererHealth struct{}

// cmdResize changes the emulator + PTY window size.
type cmdResize struct{ Cols, Rows int }

// cmdFocus applies focus/blur (and emits a focus report when DECSET 1004 is on).
type cmdFocus struct{ Focused bool }

// cmdScroll scrolls the viewport by Delta rows (negative = up/back).
type cmdScroll struct{ Delta int }

// cmdRead answers a visible-text / recent-scrollback query. Reply must be
// buffered (cap 1) so the actor never blocks delivering the result.
type cmdRead struct {
	Source ReadSource
	Format ReadFormat
	Reply  chan ReadResult
}

// cmdSnapshot answers a full cell-grid query. Reply must be buffered (cap 1).
type cmdSnapshot struct {
	Reply chan SnapshotResult
}

// cmdPublish captures the screen and its text in one actor turn. Independent
// snapshot/read commands can observe different output generations.
type cmdPublish struct {
	Reply chan terminalPublication
}

type terminalPublication struct {
	Snapshot SnapshotResult
	Visible  ReadResult
}

type cmdCheckpoint struct{ Reply chan checkpointResult }
type cmdRestoreCheckpoint struct {
	State terminalCheckpointState
	Reply chan error
}
type checkpointResult struct {
	State terminalCheckpointState
	Err   error
}

// cmdClose tears the terminal down: it routes through the Track 0.1
// shutdownTerminalProcess helper (group-kill preserved) and reaps the child.
type cmdClose struct{ Reason string }

// The handoff commands are DECLARED here but intentionally NOT implemented in
// this work item; Track 4 (live update) fills them in. Declaring them now means
// Track 4 extends the dispatch switch rather than reshaping the loop. They drive
// the actor through Running -> Quiescing -> Quiesced -> Released (see
// runtimeState); in Track 2 only Running and the closed terminal matter.
type cmdBeginHandoff struct{ Reply chan handoffTerminalState }
type cmdReleaseAfterHandoff struct{}
type cmdRollbackHandoff struct{ Reply chan error }

// cmdUpdate is the internal TUI-facing input command: a vaxis event that the
// actor encodes against the live emulator modes (application keypad, cursor
// keys, modifyOtherKeys, ...) before writing. It is deliberately not part of the
// daemon command set.
type cmdUpdate struct{ Event vaxis.Event }

type cmdRendererUpdate struct {
	EventID uint64
	Event   vaxis.Event
}

// actorEnvelope wraps a command with an optional ack channel. When ack is
// non-nil the actor closes it after the command is fully applied, which is how
// the synchronous interface methods (Resize/Update/Focus/Blur/Scroll) preserve
// their pre-actor "applied before the call returns" semantics without widening
// the documented command structs.
type actorEnvelope struct {
	cmd runtimeCommand
	ack chan struct{}
}

// runActor is the single owner of live emulator/PTY mutation. It reorganizes the
// pre-existing read loop + queued writer + VT-response writeback into one command
// pump: read-pump bytes are ingested here, control commands are applied here, and
// read/snapshot queries are served here. Draw (and the read-only interface
// methods) run on their own goroutine and share t.mu.
func (t *ghosttyTUITerminal) runActor() {
	defer close(t.actorDone)
	t.initializeCompression()
	var idleTimer *time.Timer
	var idle <-chan time.Time
	var scheduled time.Time
	defer func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
	}()
	for {
		deadline, pending := t.compressionDeadline(time.Now())
		if !pending {
			if idleTimer != nil && idle != nil {
				idleTimer.Stop()
			}
			idle, scheduled = nil, time.Time{}
		} else if deadline != scheduled {
			if idleTimer == nil {
				idleTimer = time.NewTimer(time.Until(deadline))
			} else {
				if !idleTimer.Stop() {
					select {
					case <-idleTimer.C:
					default:
					}
				}
				idleTimer.Reset(time.Until(deadline))
			}
			idle, scheduled = idleTimer.C, deadline
		}
		select {
		case <-idle:
			idle, scheduled = nil, time.Time{}
			t.compressIdle(time.Now())
		case data := <-t.readCh:
			t.ingestPTY(data)
		case err := <-t.readErrCh:
			t.handleReadError(err)
			return
		case env := <-t.commands:
			if t.dispatch(env) {
				return
			}
		}
	}
}

// dispatch applies one command. It returns true when the actor must exit (the
// terminal was closed). The ack channel, when present, is closed after the
// command is fully applied so synchronous callers unblock.
func (t *ghosttyTUITerminal) dispatch(env actorEnvelope) (exit bool) {
	if env.ack != nil {
		defer close(env.ack)
	}

	switch c := env.cmd.(type) {
	case cmdUpdate:
		t.applyUpdate(c.Event)
	case cmdRendererUpdate:
		t.mu.Lock()
		t.responseEventID = c.EventID
		t.responseOrdinal = 0
		t.mu.Unlock()
		t.applyUpdate(c.Event)
	case cmdRendererOutput:
		t.ingestPTYEvent(c.Data, c.EventID)
	case cmdRendererHealth:
		// Reaching this case and closing the envelope acknowledgement is the
		// health result.
	case cmdInput:
		t.applyInput(c.Data)
	case cmdPaste:
		c.Reply <- t.applyPaste(c.EventID, c.Text)
	case cmdEncodePaste:
		data, err := t.encodePaste(c.Text)
		c.Reply <- encodedPasteResult{Data: data, Err: err}
	case cmdInteract:
		result, err := t.applyInteraction(c.Request)
		c.Reply <- interactionOutcome{Result: result, Err: err}
	case cmdResize:
		t.applyResize(c.Cols, c.Rows)
	case cmdFocus:
		t.applyFocus(c.Focused)
	case cmdScroll:
		t.applyScroll(c.Delta)
	case cmdRead:
		if c.Reply != nil {
			c.Reply <- t.serveRead(c.Source, c.Format)
		}
	case cmdSnapshot:
		if c.Reply != nil {
			c.Reply <- t.serveSnapshot()
		}
	case cmdPublish:
		if c.Reply != nil {
			c.Reply <- t.servePublication()
		}
	case cmdCheckpoint:
		state, err := t.checkpointLocked()
		c.Reply <- checkpointResult{State: state, Err: err}
	case cmdGraphics:
		frame, err := t.graphicsLocked()
		c.Reply <- graphicsOutcome{Frame: frame, Err: err}
	case cmdPublishGraphics:
		c.Reply <- t.publishGraphicsLocked()
	case cmdCompression:
		c.Reply <- t.configureCompression(c)
	case cmdResizePixels:
		c.Reply <- t.resizePixels(c)
	case cmdRestoreCheckpoint:
		c.Reply <- t.restoreCheckpointLocked(c.State)
	case cmdClose:
		t.teardown(true, false, nil)
		return true
	case cmdBeginHandoff:
		state := t.beginHandoff(2 * time.Second)
		if c.Reply != nil {
			c.Reply <- state
		}
	case cmdReleaseAfterHandoff:
		t.state = runtimeStateReleased
		t.teardown(false, false, nil)
		return true
	case cmdRollbackHandoff:
		err := t.rollbackHandoff()
		if c.Reply != nil {
			c.Reply <- err
		}
	}
	return false
}

// handleReadError mirrors the pre-actor readLoop teardown: a closed PTY posts no
// exit error, an EOF/other error reaps the child and reports its status.
func (t *ghosttyTUITerminal) handleReadError(err error) {
	var postErr error
	if !isGhosttyPTYClosedReadError(err) {
		postErr = t.wait()
	}
	t.teardown(false, true, postErr)
}

// sendSync delivers a command and blocks until the actor has applied it, or
// until the actor has exited. It never blocks forever: a torn-down actor closes
// t.actorDone, which both selects observe.
func (t *ghosttyTUITerminal) sendSync(cmd runtimeCommand) {
	ack := make(chan struct{})
	select {
	case t.commands <- actorEnvelope{cmd: cmd, ack: ack}:
	case <-t.actorDone:
		return
	}
	select {
	case <-ack:
	case <-t.actorDone:
	}
}

func (t *ghosttyTUITerminal) rendererHealth() bool {
	ack := make(chan struct{})
	select {
	case t.commands <- actorEnvelope{cmd: cmdRendererHealth{}, ack: ack}:
	case <-t.actorDone:
		return false
	}
	select {
	case <-ack:
		return true
	case <-t.actorDone:
		return false
	}
}

// sendAsync delivers a fire-and-forget command, dropping it if the actor is gone.
func (t *ghosttyTUITerminal) sendAsync(cmd runtimeCommand) {
	select {
	case t.commands <- actorEnvelope{cmd: cmd}:
	case <-t.actorDone:
	}
}

// requestRead / requestSnapshot are the request-reply seam the daemon (Track 3)
// and agent detection (Track 5) consume; they make terminal content testable
// with no TUI attached.
func (t *ghosttyTUITerminal) requestRead(source ReadSource, format ReadFormat) ReadResult {
	reply := make(chan ReadResult, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdRead{Source: source, Format: format, Reply: reply}}:
	case <-t.actorDone:
		return ReadResult{Err: errTerminalClosed}
	}
	select {
	case r := <-reply:
		return r
	case <-t.actorDone:
		return ReadResult{Err: errTerminalClosed}
	}
}

func (t *ghosttyTUITerminal) requestSnapshot() SnapshotResult {
	reply := make(chan SnapshotResult, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdSnapshot{Reply: reply}}:
	case <-t.actorDone:
		return SnapshotResult{Err: errTerminalClosed}
	}
	select {
	case r := <-reply:
		return r
	case <-t.actorDone:
		return SnapshotResult{Err: errTerminalClosed}
	}
}

// ReadVisible returns the current on-screen text (daemon terminal.read
// source=visible). It works with no TUI: the actor renders the viewport on
// demand rather than reusing Draw's cached snapshot.
func (t *ghosttyTUITerminal) ReadVisible(format ReadFormat) ReadResult {
	return t.requestRead(ReadVisible, format)
}

// ReadRecent returns recent scrollback plus the visible viewport (daemon
// terminal.read source=recent).
func (t *ghosttyTUITerminal) ReadRecent(format ReadFormat) ReadResult {
	return t.requestRead(ReadRecent, format)
}

// Snapshot returns the full cell grid + cursor + generation (daemon
// terminal.snapshot).
func (t *ghosttyTUITerminal) Snapshot() SnapshotResult {
	return t.requestSnapshot()
}

// Publish returns one immutable observation of cells, cursor and visible text.
// The buffered reply lets shutdown complete even if a caller stops waiting.
func (t *ghosttyTUITerminal) Publish() (SnapshotResult, ReadResult) {
	reply := make(chan terminalPublication, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdPublish{Reply: reply}}:
	case <-t.actorDone:
		return SnapshotResult{Err: errTerminalClosed}, ReadResult{Err: errTerminalClosed}
	}
	select {
	case result := <-reply:
		return result.Snapshot, result.Visible
	case <-t.actorDone:
		return SnapshotResult{Err: errTerminalClosed}, ReadResult{Err: errTerminalClosed}
	}
}

// Scroll scrolls the viewport (daemon terminal.scroll). Fire-and-forget: the
// next Draw or Snapshot observes the new position.
func (t *ghosttyTUITerminal) Scroll(delta int) {
	t.sendAsync(cmdScroll{Delta: delta})
}

// SendInput writes raw bytes to the child (daemon terminal.send_input). It waits
// for the actor to enqueue the write so callers can rely on ordering against a
// subsequent read.
func (t *ghosttyTUITerminal) SendInput(data []byte) {
	if len(data) == 0 {
		return
	}
	t.sendSync(cmdInput{Data: append([]byte(nil), data...)})
}

func (t *ghosttyTUITerminal) BeginHandoff() handoffTerminalState {
	reply := make(chan handoffTerminalState, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdBeginHandoff{Reply: reply}}:
	case <-t.actorDone:
		return handoffTerminalState{Err: errTerminalClosed}
	}
	select {
	case state := <-reply:
		return state
	case <-t.actorDone:
		return handoffTerminalState{Err: errTerminalClosed}
	}
}

func (t *ghosttyTUITerminal) ReleaseAfterHandoff() {
	t.sendSync(cmdReleaseAfterHandoff{})
}

func (t *ghosttyTUITerminal) RollbackHandoff() error {
	reply := make(chan error, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdRollbackHandoff{Reply: reply}}:
	case <-t.actorDone:
		return errTerminalClosed
	}
	select {
	case err := <-reply:
		return err
	case <-t.actorDone:
		return errTerminalClosed
	}
}
