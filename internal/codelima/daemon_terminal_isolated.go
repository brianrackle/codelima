//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const rendererResponseDedupeLimit = 4096

type isolatedTerminalCache struct {
	state   rendererPublishedState
	partial bool
}

type rendererResponseKey struct {
	Generation uint64
	EventID    uint64
	Ordinal    uint32
}

// isolatedDaemonTerminal is the daemon-side pure-Go session owner. It owns the
// shell, PTY, ordered output journal, and immutable renderer cache. Every
// Ghostty call happens in a renderer worker process reached through
// rendererSupervisor, so a stuck native call cannot stop the PTY drain, daemon,
// another terminal, or another client.
type isolatedDaemonTerminal struct {
	targetKey string
	postEvent func(vaxis.Event)
	options   rendererProcessOptions

	mu           sync.Mutex
	cmd          *exec.Cmd
	pty          *os.File
	ptyWriter    *ghosttyPTYWriter
	childPID     int
	cols         int
	rows         int
	focused      bool
	closed       bool
	state        runtimeState
	waitOnce     sync.Once
	waitErr      error
	waitDone     chan struct{}
	readPumpDone chan struct{}
	quit         chan struct{}
	quitOnce     sync.Once
	closeOnce    sync.Once
	handoffPTY   *os.File

	journal    *rendererJournal
	renderer   *rendererSupervisor
	cache      atomic.Pointer[isolatedTerminalCache]
	responseMu sync.Mutex
	responses  map[rendererResponseKey]struct{}
	order      []rendererResponseKey
}

func newIsolatedDaemonTerminal(targetKey string, postEvent func(vaxis.Event)) tuiTerminal {
	return newIsolatedDaemonTerminalWithOptions(targetKey, postEvent, defaultRendererProcessOptions())
}

func newIsolatedDaemonTerminalWithOptions(
	targetKey string,
	postEvent func(vaxis.Event),
	options rendererProcessOptions,
) *isolatedDaemonTerminal {
	terminal := &isolatedDaemonTerminal{
		targetKey: targetKey,
		postEvent: postEvent,
		options:   options,
		cols:      80,
		rows:      24,
		state:     runtimeStateRunning,
		quit:      make(chan struct{}),
		journal:   newRendererJournal(defaultRendererJournalBytes),
		responses: make(map[rendererResponseKey]struct{}),
	}
	terminal.journal.AppendResize(terminal.cols, terminal.rows)
	return terminal
}

func (t *isolatedDaemonTerminal) newRenderer() *rendererSupervisor {
	return newRendererSupervisor(
		t.targetKey,
		t.journal,
		t.options,
		t.installRendererSnapshot,
		t.applyRendererPTYWrite,
		t.handleRendererEvent,
		t.markRendererStale,
	)
}

func (t *isolatedDaemonTerminal) Start(command *exec.Cmd) error {
	if command == nil || len(command.Args) == 0 {
		return errors.New("no command to run")
	}

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return errTerminalClosed
	}
	cols, rows := t.cols, t.rows
	renderer := t.newRenderer()
	t.renderer = renderer
	t.mu.Unlock()
	if err := renderer.Start(context.Background(), cols, rows); err != nil {
		renderer.Close()
		return fmt.Errorf("start isolated terminal renderer: %w", err)
	}

	env := os.Environ()
	if command.Env != nil {
		env = command.Env
	}
	command.Env = append(slices.Clone(env), "TERM="+tuiEmbeddedTermEnv)
	ptyFile, err := pty.StartWithAttrs(
		command,
		&pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)},
		&syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1},
	)
	if err != nil {
		renderer.Close()
		return err
	}
	if err := unix.SetNonblock(int(ptyFile.Fd()), true); err != nil {
		_ = ptyFile.Close()
		renderer.Close()
		return fmt.Errorf("set terminal pty nonblocking: %w", err)
	}

	waitDone := make(chan struct{})
	t.mu.Lock()
	t.cmd = command
	t.pty = ptyFile
	t.childPID = command.Process.Pid
	t.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, t.postPTYError)
	t.waitDone = waitDone
	t.readPumpDone = make(chan struct{})
	t.mu.Unlock()
	go t.readPump()
	go func() {
		_ = t.wait()
		close(waitDone)
	}()
	return nil
}

func (t *isolatedDaemonTerminal) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 {
		return
	}
	t.mu.Lock()
	if t.closed || (t.cols == cols && t.rows == rows) {
		t.mu.Unlock()
		return
	}
	oldCols := t.cols
	t.cols, t.rows = cols, rows
	ptyFile := t.pty
	childPID := t.childPID
	renderer := t.renderer
	t.mu.Unlock()

	event := t.journal.AppendResize(cols, rows)
	if ptyFile != nil {
		_ = pty.Setsize(ptyFile, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
		if cols != oldCols && childPID > 0 {
			_ = syscall.Kill(-childPID, syscall.SIGWINCH)
		}
	}
	if renderer != nil {
		if err := renderer.TryResize(event); err != nil {
			t.postTerminalError(fmt.Errorf("resize renderer: %w", err))
		}
	}
}

func (t *isolatedDaemonTerminal) Update(event vaxis.Event) {
	encoded, ok := encodeRendererInputEvent(event)
	if !ok {
		return
	}
	t.mu.Lock()
	renderer := t.renderer
	closed := t.closed
	t.mu.Unlock()
	if closed || renderer == nil {
		return
	}
	if err := renderer.TryUpdate(encoded); err != nil {
		t.postTerminalError(fmt.Errorf("send renderer input: %w", err))
	}
}

func encodeRendererInputEvent(event vaxis.Event) (rendererInputEvent, bool) {
	switch value := event.(type) {
	case vaxis.Key:
		return rendererInputEvent{
			Type:      "key",
			Keycode:   value.Keycode,
			Shifted:   value.ShiftedCode,
			Text:      value.Text,
			Modifiers: uint8(value.Modifiers),
			EventType: uint8(value.EventType),
		}, true
	case vaxis.Mouse:
		return rendererInputEvent{
			Type:      "mouse",
			Col:       value.Col,
			Row:       value.Row,
			Button:    int(value.Button),
			EventType: uint8(value.EventType),
		}, true
	case vaxis.PasteStartEvent:
		return rendererInputEvent{Type: "paste_start"}, true
	case vaxis.PasteEndEvent:
		return rendererInputEvent{Type: "paste_end"}, true
	default:
		return rendererInputEvent{}, false
	}
}

func (t *isolatedDaemonTerminal) Draw(vaxis.Window) {}

func (t *isolatedDaemonTerminal) Close() {
	t.closeOnce.Do(func() {
		t.closeRuntime(true, false, nil)
	})
}

func (t *isolatedDaemonTerminal) closeRuntime(kill, post bool, closeErr error) {
	t.quitOnce.Do(func() { close(t.quit) })
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	childPID := t.childPID
	ptyFile := t.pty
	writer := t.ptyWriter
	waitDone := t.waitDone
	renderer := t.renderer
	handoffPTY := t.handoffPTY
	t.pty = nil
	t.ptyWriter = nil
	t.renderer = nil
	t.handoffPTY = nil
	if kill {
		t.childPID = 0
	}
	t.mu.Unlock()

	if renderer != nil {
		renderer.Close()
	}
	if handoffPTY != nil {
		_ = handoffPTY.Close()
	}
	closeIO := func() {
		if writer != nil {
			writer.Close()
		}
		if ptyFile != nil {
			_ = ptyFile.Close()
		}
	}
	if kill && childPID > 0 {
		_ = shutdownTerminalProcess(childPID, closeIO, waitDone)
	} else {
		closeIO()
	}
	if kill {
		_ = t.wait()
	}
	if post && t.postEvent != nil {
		t.postEvent(tuiTerminalClosedEvent{SessionKey: t.targetKey, Err: closeErr})
	}
}

func (t *isolatedDaemonTerminal) Focus() {
	t.setFocus(true)
}

func (t *isolatedDaemonTerminal) Blur() {
	t.setFocus(false)
}

func (t *isolatedDaemonTerminal) setFocus(focused bool) {
	t.mu.Lock()
	t.focused = focused
	renderer := t.renderer
	t.mu.Unlock()
	if renderer != nil {
		if err := renderer.TryFocus(focused); err != nil {
			t.postTerminalError(fmt.Errorf("focus renderer: %w", err))
		}
	}
}

func (t *isolatedDaemonTerminal) String() string {
	if cache := t.cache.Load(); cache != nil {
		return cache.state.VisibleText.Text
	}
	return ""
}

func (t *isolatedDaemonTerminal) TermEnv() string {
	return tuiEmbeddedTermEnv
}

func (t *isolatedDaemonTerminal) HyperlinkAt(col, row int) (string, bool) {
	cache := t.cache.Load()
	if cache == nil || col < 0 || row < 0 || col >= cache.state.Snapshot.Cols || row >= cache.state.Snapshot.Rows {
		return "", false
	}
	index := row*cache.state.Snapshot.Cols + col
	if index < 0 || index >= len(cache.state.Snapshot.Cells) {
		return "", false
	}
	target := cache.state.Snapshot.Cells[index].Hyperlink
	return target, target != ""
}

func (t *isolatedDaemonTerminal) CapturesMouse() bool {
	cache := t.cache.Load()
	return cache != nil && cache.state.Snapshot.CapturesMouse
}

func (t *isolatedDaemonTerminal) ReadVisible(format ReadFormat) ReadResult {
	cache := t.cache.Load()
	if cache == nil {
		return ReadResult{Err: errSnapshotFailed}
	}
	if format == ReadANSI {
		return cache.state.VisibleANSI.readResult()
	}
	return cache.state.VisibleText.readResult()
}

func (t *isolatedDaemonTerminal) ReadRecent(format ReadFormat) ReadResult {
	cache := t.cache.Load()
	if cache == nil {
		return ReadResult{Err: errSnapshotFailed}
	}
	if format == ReadANSI {
		return cache.state.RecentANSI.readResult()
	}
	return cache.state.RecentText.readResult()
}

func (t *isolatedDaemonTerminal) Snapshot() SnapshotResult {
	cache := t.cache.Load()
	if cache == nil {
		t.mu.Lock()
		renderer := t.renderer
		t.mu.Unlock()
		if renderer != nil {
			_ = renderer.RequestSnapshot()
		}
		return SnapshotResult{Err: errSnapshotFailed}
	}
	return SnapshotResult{Snapshot: cloneTerminalSnapshot(cache.state.Snapshot)}
}

func cloneTerminalSnapshot(snapshot TerminalSnapshot) TerminalSnapshot {
	snapshot.Cells = slices.Clone(snapshot.Cells)
	return snapshot
}

func (t *isolatedDaemonTerminal) Scroll(delta int) {
	t.mu.Lock()
	renderer := t.renderer
	t.mu.Unlock()
	if renderer != nil {
		if err := renderer.TryScroll(delta); err != nil {
			t.postTerminalError(fmt.Errorf("scroll renderer: %w", err))
		}
	}
}

func (t *isolatedDaemonTerminal) SendInput(data []byte) {
	if len(data) == 0 {
		return
	}
	t.mu.Lock()
	writer := t.ptyWriter
	t.mu.Unlock()
	if writer != nil && !writer.Enqueue(slices.Clone(data)) {
		t.postTerminalError(errors.New("terminal PTY writer is closed"))
	}
}

func (t *isolatedDaemonTerminal) readPump() {
	t.mu.Lock()
	done := t.readPumpDone
	t.mu.Unlock()
	if done != nil {
		defer close(done)
	}
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-t.quit:
			return
		default:
		}
		n, err := t.readPTY(buffer)
		if n > 0 {
			event := t.journal.AppendOutput(buffer[:n])
			t.mu.Lock()
			renderer := t.renderer
			t.mu.Unlock()
			if renderer != nil {
				// Apply bounded backpressure when a producer outruns its
				// renderer. The terminal-local PTY may pause, but daemon
				// control, other terminals, and high-priority renderer health
				// traffic remain independent. A renderer replacement replays
				// this journaled event instead of dropping it.
				if sendErr := renderer.SendOutput(event); sendErr != nil &&
					!errors.Is(sendErr, errTerminalClosed) {
					t.postTerminalError(fmt.Errorf("queue renderer output: %w", sendErr))
				}
			}
		}
		if err == nil {
			continue
		}
		if isGhosttyPTYWouldBlockError(err) {
			select {
			case <-t.quit:
				return
			default:
			}
			_ = waitGhosttyPTYReadable(int(t.currentPTYFD()), 50*time.Millisecond)
			continue
		}
		select {
		case <-t.quit:
			return
		default:
		}
		var exitErr error
		if !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
			exitErr = t.wait()
		}
		t.closeRuntime(false, true, exitErr)
		return
	}
}

func (t *isolatedDaemonTerminal) readPTY(buffer []byte) (int, error) {
	t.mu.Lock()
	ptyFile := t.pty
	t.mu.Unlock()
	if ptyFile == nil {
		return 0, io.EOF
	}
	return ptyFile.Read(buffer)
}

func (t *isolatedDaemonTerminal) currentPTYFD() uintptr {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pty == nil {
		return ^uintptr(0)
	}
	return t.pty.Fd()
}

func (t *isolatedDaemonTerminal) installRendererSnapshot(
	generation uint64,
	state rendererPublishedState,
	partial bool,
) {
	t.mu.Lock()
	renderer := t.renderer
	t.mu.Unlock()
	if renderer == nil {
		return
	}
	_ = generation
	state.Snapshot = cloneTerminalSnapshot(state.Snapshot)
	t.cache.Store(&isolatedTerminalCache{state: state, partial: partial})
	if t.postEvent != nil {
		t.postEvent(vaxis.Redraw{})
	}
}

func (t *isolatedDaemonTerminal) markRendererStale() {
	if current := t.cache.Load(); current != nil && !current.state.Snapshot.Stale {
		next := *current
		next.state.Snapshot = cloneTerminalSnapshot(current.state.Snapshot)
		next.state.Snapshot.Stale = true
		t.cache.Store(&next)
	}
	if t.postEvent != nil {
		t.postEvent(vaxis.Redraw{})
	}
}

func (t *isolatedDaemonTerminal) applyRendererPTYWrite(
	generation uint64,
	eventID uint64,
	ordinal uint32,
	data []byte,
) {
	if len(data) == 0 || eventID == 0 || ordinal == 0 {
		return
	}
	key := rendererResponseKey{EventID: eventID, Ordinal: ordinal}
	if eventID&rendererInputEventBit != 0 {
		key.Generation = generation
	}
	t.responseMu.Lock()
	if _, duplicate := t.responses[key]; duplicate {
		t.responseMu.Unlock()
		return
	}
	t.responses[key] = struct{}{}
	t.order = append(t.order, key)
	if len(t.order) > rendererResponseDedupeLimit {
		oldest := t.order[0]
		t.order = t.order[1:]
		delete(t.responses, oldest)
	}
	t.responseMu.Unlock()
	t.SendInput(data)
}

func (t *isolatedDaemonTerminal) handleRendererEvent(_ uint64, frame rendererWorkerFrame) {
	switch frame.Event {
	case "clipboard":
		var text string
		if json.Unmarshal(frame.Result, &text) == nil && t.postEvent != nil {
			t.postEvent(tuiClipboardEvent{TargetKey: t.targetKey, Text: text})
		}
	case "error":
		t.postTerminalError(errors.New(frame.Error))
	}
}

func (t *isolatedDaemonTerminal) postPTYError(err error) {
	if err != nil {
		t.postTerminalError(fmt.Errorf("write isolated terminal PTY: %w", err))
	}
}

func (t *isolatedDaemonTerminal) postTerminalError(err error) {
	if err != nil && t.postEvent != nil {
		t.postEvent(tuiTerminalErrorEvent{TargetKey: t.targetKey, Err: err})
	}
}

func (t *isolatedDaemonTerminal) wait() error {
	t.waitOnce.Do(func() {
		t.mu.Lock()
		command := t.cmd
		t.mu.Unlock()
		if command != nil {
			t.waitErr = command.Wait()
		}
	})
	return t.waitErr
}

func (t *isolatedDaemonTerminal) BeginHandoff() handoffTerminalState {
	t.mu.Lock()
	if t.closed || t.pty == nil || t.state != runtimeStateRunning {
		t.mu.Unlock()
		return handoffTerminalState{Err: errTerminalClosed}
	}
	t.state = runtimeStateQuiescing
	writer := t.ptyWriter
	ptyFile := t.pty
	childPID, cols, rows := t.childPID, t.cols, t.rows
	renderer := t.renderer
	t.mu.Unlock()
	if err := writer.Drain(2 * time.Second); err != nil {
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	rollbackFD, err := unix.Dup(int(ptyFile.Fd()))
	if err != nil {
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	transferFD, err := unix.Dup(int(ptyFile.Fd()))
	if err != nil {
		_ = unix.Close(rollbackFD)
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	t.quitOnce.Do(func() { close(t.quit) })
	writer.Close()
	if renderer != nil {
		renderer.Close()
	}
	t.mu.Lock()
	readDone := t.readPumpDone
	t.mu.Unlock()
	if readDone != nil {
		select {
		case <-readDone:
		case <-time.After(250 * time.Millisecond):
		}
	}
	t.mu.Lock()
	t.pty = nil
	t.ptyWriter = nil
	t.renderer = nil
	t.handoffPTY = os.NewFile(uintptr(rollbackFD), "codelima-isolated-handoff-rollback")
	t.state = runtimeStateQuiesced
	journalSnapshot := t.journal.Snapshot()
	replay := rendererJournalReplay(journalSnapshot)
	t.mu.Unlock()
	return handoffTerminalState{
		PTY:           os.NewFile(uintptr(transferFD), "codelima-isolated-handoff-transfer"),
		ChildPID:      childPID,
		Cols:          cols,
		Rows:          rows,
		Replay:        replay,
		ReplayPartial: journalSnapshot.Partial,
	}
}

func rendererJournalReplay(snapshot rendererJournalSnapshot) []byte {
	var replay []byte
	for _, event := range snapshot.Events {
		if event.Type == "output" {
			replay = append(replay, event.Data...)
		}
	}
	return replay
}

func (t *isolatedDaemonTerminal) ReleaseAfterHandoff() {
	t.mu.Lock()
	t.state = runtimeStateReleased
	t.closed = true
	handoffPTY := t.handoffPTY
	t.handoffPTY = nil
	t.mu.Unlock()
	if handoffPTY != nil {
		_ = handoffPTY.Close()
	}
}

func (t *isolatedDaemonTerminal) RollbackHandoff() error {
	t.mu.Lock()
	if t.state != runtimeStateQuiesced || t.handoffPTY == nil {
		t.mu.Unlock()
		return errors.New("terminal is not quiesced")
	}
	ptyFile := t.handoffPTY
	t.handoffPTY = nil
	t.quit = make(chan struct{})
	t.quitOnce = sync.Once{}
	t.pty = ptyFile
	t.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, t.postPTYError)
	t.readPumpDone = make(chan struct{})
	t.state = runtimeStateRunning
	renderer := t.newRenderer()
	t.renderer = renderer
	cols, rows := t.cols, t.rows
	t.mu.Unlock()
	ctx, cancel := contextWithRendererTimeout(t.options.CommandTimeout)
	defer cancel()
	if err := renderer.Start(ctx, cols, rows); err != nil {
		return err
	}
	go t.readPump()
	return nil
}

func contextWithRendererTimeout(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		timeout = defaultRendererCommandTimeout
	}
	return context.WithTimeout(context.Background(), timeout)
}

func adoptIsolatedDaemonTerminal(
	targetKey string,
	postEvent func(vaxis.Event),
	ptyFile *os.File,
	childPID, cols, rows int,
	replay []byte,
	replayPartial bool,
) (daemonTerminal, error) {
	if cols <= 0 || rows <= 0 || ptyFile == nil {
		return nil, fmt.Errorf("adopt isolated terminal with invalid runtime")
	}
	terminal := newIsolatedDaemonTerminalWithOptions(targetKey, postEvent, defaultRendererProcessOptions())
	terminal.cols, terminal.rows = cols, rows
	terminal.journal = newRendererJournal(defaultRendererJournalBytes)
	terminal.journal.AppendResize(cols, rows)
	if len(replay) > 0 {
		terminal.journal.AppendOutput(replay)
	}
	terminal.journal.mu.Lock()
	terminal.journal.partial = replayPartial
	terminal.journal.mu.Unlock()
	terminal.pty = ptyFile
	terminal.childPID = childPID
	terminal.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, terminal.postPTYError)
	terminal.readPumpDone = make(chan struct{})
	terminal.renderer = terminal.newRenderer()
	ctx, cancel := contextWithRendererTimeout(terminal.options.CommandTimeout)
	defer cancel()
	if err := terminal.renderer.Start(ctx, cols, rows); err != nil {
		terminal.renderer.Close()
		_ = ptyFile.Close()
		return nil, err
	}
	return terminal, nil
}

func (t *isolatedDaemonTerminal) ActivateAfterHandoff() {
	go t.readPump()
}

func (t *isolatedDaemonTerminal) RendererStatus() rendererSupervisorStatus {
	t.mu.Lock()
	renderer := t.renderer
	t.mu.Unlock()
	if renderer == nil {
		return rendererSupervisorStatus{State: "unavailable"}
	}
	return renderer.Status()
}

func (t *isolatedDaemonTerminal) RuntimeDiagnostics() map[string]any {
	status := t.RendererStatus()
	t.mu.Lock()
	childPID := t.childPID
	cols, rows := t.cols, t.rows
	t.mu.Unlock()
	return map[string]any{
		"architecture":               "go-session-isolated-renderer",
		"session_pid":                os.Getpid(),
		"shell_pid":                  childPID,
		"renderer_pid":               status.PID,
		"renderer_generation":        status.Generation,
		"renderer_state":             status.State,
		"renderer_last_progress_at":  status.LastProgressAt,
		"renderer_restart_count":     status.RestartCount,
		"renderer_last_error":        status.LastError,
		"renderer_outbound_depth":    status.OutboundDepth,
		"renderer_pending_requests":  status.PendingRequests,
		"renderer_oldest_pending":    status.OldestPending.String(),
		"renderer_current_operation": status.CurrentOperation,
		"journal_bytes":              status.JournalBytes,
		"partial_recovery":           status.PartialRecovery,
		"cols":                       cols,
		"rows":                       rows,
	}
}
