//go:build darwin || linux

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

	"github.com/brianrackle/codelima/internal/terminalio"
	"github.com/creack/pty"
	"go.rockorager.dev/vaxis"
	"golang.org/x/sys/unix"
)

const rendererResponseDedupeLimit = 4096

type isolatedTerminalCache struct {
	state   rendererPublishedState
	partial bool
	// epoch names this published screen. Read variants memoized against it stay
	// valid until the renderer publishes a new one; re-stamping staleness onto
	// the same screen keeps the epoch, because the content did not change.
	epoch uint64
}

// isolatedReadVariant selects one of the on-demand read renderings.
type isolatedReadVariant struct {
	source ReadSource
	format ReadFormat
}

// isolatedReadVariants memoizes read variants the renderer computes on demand.
// Only the plain visible text rides along with every published screen; the ANSI
// viewport render and both scrollback renders each walk the whole grid or the
// retained scrollback, so they are fetched the first time a terminal.read wants
// one and reused for the rest of that screen's epoch.
type isolatedReadVariants struct {
	mu       sync.Mutex
	epoch    uint64
	values   map[isolatedReadVariant]ReadResultDTO
	inflight map[isolatedReadVariant]*isolatedReadFlight
	// last retains the newest successful rendering of each variant regardless of
	// epoch. A renderer that is restarting or degraded cannot answer, and
	// serving its last text preserves the behaviour of the era when every
	// variant was pushed with the snapshot: stale, but present.
	last map[isolatedReadVariant]ReadResultDTO
}

type isolatedReadFlight struct {
	epoch  uint64
	done   chan struct{}
	result ReadResult
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

	mu                    sync.Mutex
	resizeMu              sync.Mutex
	cmd                   *exec.Cmd
	pty                   *os.File
	ptyWriter             *ghosttyPTYWriter
	childPID              int
	cols                  int
	rows                  int
	cellWidth, cellHeight int
	focused               bool
	closed                bool
	state                 runtimeState
	waitOnce              sync.Once
	waitErr               error
	waitDone              chan struct{}
	readPumpDone          chan struct{}
	quit                  chan struct{}
	quitOnce              sync.Once
	closeOnce             sync.Once
	handoffPTY            *os.File
	handoffRecovery       []byte

	journal    *rendererJournal
	renderer   *rendererSupervisor
	cache      atomic.Pointer[isolatedTerminalCache]
	cacheEpoch atomic.Uint64
	variants   isolatedReadVariants
	// readFetches counts on-demand variant round-trips to the renderer. It is
	// test observability for the "nothing is rendered until a read asks" rule.
	readFetches atomic.Uint64
	responseMu  sync.Mutex
	responses   map[rendererResponseKey]struct{}
	order       []rendererResponseKey
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
	var renderer *rendererSupervisor
	renderer = newRendererSupervisor(
		t.targetKey,
		t.journal,
		t.options,
		func(generation uint64, state rendererPublishedState, partial bool) {
			t.installRendererSnapshot(renderer, generation, state, partial)
		},
		t.applyRendererPTYWrite,
		t.handleRendererEvent,
		func() { t.markRendererStaleFrom(renderer) },
	)
	return renderer
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
	ptyFD, err := ghosttyPTYFileDescriptor(ptyFile)
	if err != nil {
		_ = ptyFile.Close()
		renderer.Close()
		return fmt.Errorf("resolve terminal pty descriptor: %w", err)
	}
	if err := unix.SetNonblock(ptyFD, true); err != nil {
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
	if err := t.resizePixels(cols, rows, 0, 0); err != nil {
		t.postTerminalError(err)
	}
}

func (t *isolatedDaemonTerminal) ResizePixels(cols, rows, cellWidth, cellHeight int) error {
	if cellWidth <= 0 || cellHeight <= 0 {
		return errors.New("terminal pixel geometry must be positive")
	}
	return t.resizePixels(cols, rows, cellWidth, cellHeight)
}

func (t *isolatedDaemonTerminal) resizePixels(cols, rows, cellWidth, cellHeight int) error {
	if !rendererValidGeometry(cols, rows) || !rendererValidPixels(cellWidth, cellHeight) {
		return errors.New("terminal geometry exceeds renderer bounds")
	}
	t.resizeMu.Lock()
	defer t.resizeMu.Unlock()
	t.mu.Lock()
	if t.closed || t.state != runtimeStateRunning {
		t.mu.Unlock()
		return errTerminalClosed
	}
	if cellWidth == 0 {
		cellWidth, cellHeight = t.cellWidth, t.cellHeight
	}
	if t.cols == cols && t.rows == rows && t.cellWidth == cellWidth && t.cellHeight == cellHeight {
		t.mu.Unlock()
		return nil
	}
	oldCols := t.cols
	t.cols, t.rows = cols, rows
	t.cellWidth, t.cellHeight = cellWidth, cellHeight
	ptyFile := t.pty
	childPID := t.childPID
	renderer := t.renderer
	t.mu.Unlock()

	event := t.journal.AppendResizePixels(cols, rows, cellWidth, cellHeight)
	if ptyFile != nil {
		if err := terminalio.Resize(ptyFile, &unix.Winsize{Col: uint16(cols), Row: uint16(rows), Xpixel: uint16(min(cols*cellWidth, 65535)), Ypixel: uint16(min(rows*cellHeight, 65535))}); err != nil {
			return err
		}
		if cols != oldCols && childPID > 0 {
			_ = syscall.Kill(-childPID, syscall.SIGWINCH)
		}
	}
	if renderer != nil {
		if err := renderer.TryResize(event); err != nil {
			return fmt.Errorf("resize renderer: %w", err)
		}
	}
	return nil
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
	t.handoffRecovery = nil
	rendererHandoffRecoveryQuota.release(t)
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
	return t.readVariant(ReadVisible, format)
}

func (t *isolatedDaemonTerminal) ReadRecent(format ReadFormat) ReadResult {
	return t.readVariant(ReadRecent, format)
}

// readVariant answers one read variant. The plain visible text arrives with
// every published screen; everything else is rendered by the renderer the first
// time it is asked for and memoized for the rest of that screen's epoch, so a
// terminal nobody reads never pays for a scrollback walk.
func (t *isolatedDaemonTerminal) readVariant(source ReadSource, format ReadFormat) ReadResult {
	key := isolatedReadVariant{source: source, format: format}
	for {
		cache := t.cache.Load()
		if cache == nil {
			return ReadResult{Err: errSnapshotFailed}
		}
		if source == ReadVisible && format == ReadText {
			return cache.state.VisibleText.readResult()
		}
		t.variants.mu.Lock()
		if t.variants.epoch == cache.epoch {
			if value, ok := t.variants.values[key]; ok {
				t.variants.mu.Unlock()
				return value.readResult()
			}
		}
		if flight := t.variants.inflight[key]; flight != nil {
			t.variants.mu.Unlock()
			// At most one renderer read per variant is active, even if output
			// publishes many new epochs while the first reader is still busy.
			<-flight.done
			if flight.epoch == cache.epoch {
				return flight.result
			}
			continue
		}
		flight := &isolatedReadFlight{epoch: cache.epoch, done: make(chan struct{})}
		if t.variants.inflight == nil {
			t.variants.inflight = map[isolatedReadVariant]*isolatedReadFlight{}
		}
		t.variants.inflight[key] = flight
		t.variants.mu.Unlock()

		t.mu.Lock()
		renderer := t.renderer
		t.mu.Unlock()
		var value ReadResultDTO
		err := errRendererUnavailable
		if renderer != nil {
			t.readFetches.Add(1)
			value, err = renderer.Read(source, format)
		}
		result := value.readResult()
		if err != nil {
			result = t.lastReadVariant(key)
		}
		t.variants.mu.Lock()
		if err == nil && result.Err == nil {
			t.rememberReadVariantLocked(key, value)
			// Reads execute against live state. A result from another output
			// generation or an epoch replaced during the RPC cannot populate
			// this published screen's memo.
			current := t.cache.Load()
			if current != nil && current.epoch == cache.epoch && value.Generation == cache.state.Snapshot.Generation {
				if t.variants.epoch != cache.epoch || t.variants.values == nil {
					t.variants.epoch = cache.epoch
					t.variants.values = map[isolatedReadVariant]ReadResultDTO{}
				}
				t.variants.values[key] = value
			}
		}
		flight.result = result
		delete(t.variants.inflight, key)
		close(flight.done)
		t.variants.mu.Unlock()
		return result
	}
}

func (t *isolatedDaemonTerminal) rememberReadVariantLocked(key isolatedReadVariant, value ReadResultDTO) {
	if t.variants.last == nil {
		t.variants.last = map[isolatedReadVariant]ReadResultDTO{}
	}
	t.variants.last[key] = value
}

// lastReadVariant serves the newest text this variant ever produced. When there
// is none, it returns an empty result rather than an error: an empty variant is
// the daemon's existing signal to fall back to the visible screen, and a
// renderer that is briefly unavailable should not turn terminal.read into a
// failure.
func (t *isolatedDaemonTerminal) lastReadVariant(key isolatedReadVariant) ReadResult {
	t.variants.mu.Lock()
	defer t.variants.mu.Unlock()
	if value, ok := t.variants.last[key]; ok {
		return value.readResult()
	}
	return ReadResult{}
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
	snapshot.Graphics = cloneGraphicsMetadata(snapshot.Graphics)
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
	running := t.state == runtimeStateRunning
	t.mu.Unlock()
	if running && writer != nil && !writer.Enqueue(slices.Clone(data)) {
		t.postTerminalError(errors.New("terminal PTY writer is closed"))
	}
}

func (t *isolatedDaemonTerminal) readPump() {
	t.mu.Lock()
	done := t.readPumpDone
	quit := t.quit
	ptyFile := t.pty
	ptyFD := -1
	var descriptorErr error
	if ptyFile != nil {
		ptyFD, descriptorErr = ghosttyPTYFileDescriptor(ptyFile)
	}
	t.mu.Unlock()
	if done != nil {
		defer close(done)
	}
	if ptyFile == nil || descriptorErr != nil {
		return
	}
	buffer := make([]byte, 32*1024)
	for {
		select {
		case <-quit:
			return
		default:
		}
		n, err := ghosttyReadPTY(ptyFD, buffer)
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
			case <-quit:
				return
			default:
			}
			_ = waitGhosttyPTYReadable(ptyFD, 50*time.Millisecond)
			continue
		}
		select {
		case <-quit:
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

func (t *isolatedDaemonTerminal) installRendererSnapshot(
	source *rendererSupervisor,
	generation uint64,
	state rendererPublishedState,
	partial bool,
) {
	state.Snapshot = cloneTerminalSnapshot(state.Snapshot)
	t.mu.Lock()
	if source == nil || t.renderer != source || t.closed {
		t.mu.Unlock()
		return
	}
	source.mu.Lock()
	if source.generation != generation || !source.acceptFrames || source.closed {
		source.mu.Unlock()
		t.mu.Unlock()
		return
	}
	t.cache.Store(&isolatedTerminalCache{state: state, partial: partial, epoch: t.cacheEpoch.Add(1)})
	source.mu.Unlock()
	t.mu.Unlock()
	if t.postEvent != nil {
		t.postEvent(vaxis.Redraw{})
	}
}

func (t *isolatedDaemonTerminal) markRendererStale() {
	t.markRendererStaleFrom(nil)
}

func (t *isolatedDaemonTerminal) markRendererStaleFrom(source *rendererSupervisor) {
	t.mu.Lock()
	if source != nil && t.renderer != source {
		t.mu.Unlock()
		return
	}
	if current := t.cache.Load(); current != nil && !current.state.Snapshot.Stale {
		next := *current
		next.state.Snapshot = cloneTerminalSnapshot(current.state.Snapshot)
		next.state.Snapshot.Stale = true
		t.cache.Store(&next)
	}
	t.mu.Unlock()
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
	writer := t.ptyWriter
	ptyFile := t.pty
	ptyFD, descriptorErr := ghosttyPTYFileDescriptor(ptyFile)
	if descriptorErr != nil {
		t.mu.Unlock()
		return handoffTerminalState{Err: descriptorErr}
	}
	t.state = runtimeStateQuiescing
	childPID, cols, rows := t.childPID, t.cols, t.rows
	renderer := t.renderer
	readDone := t.readPumpDone
	t.mu.Unlock()
	if err := writer.Drain(2 * time.Second); err != nil {
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	rollbackFD, err := unix.Dup(ptyFD)
	if err != nil {
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	transferFD, err := unix.Dup(ptyFD)
	if err != nil {
		_ = unix.Close(rollbackFD)
		t.mu.Lock()
		t.state = runtimeStateRunning
		t.mu.Unlock()
		return handoffTerminalState{Err: err}
	}
	t.quitOnce.Do(func() { close(t.quit) })
	writerClosed := false
	if readDone != nil {
		select {
		case <-readDone:
		case <-time.After(250 * time.Millisecond):
			// The writer is drained and input admission stopped. Close the
			// original wrapper to force a tardy pump out, then still observe its
			// generation acknowledgement before transfer proceeds. The pump's
			// immutable numeric descriptor avoids the former Fd/Close race.
			writer.Close()
			writerClosed = true
			<-readDone
		}
	}
	if !writerClosed {
		writer.Close()
	}
	journalSnapshot := t.journal.Snapshot()
	var recovery []byte
	if renderer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), rendererReadDeadlineFactor*t.options.CommandTimeout)
		_ = renderer.captureCheckpoint(ctx)
		cancel()
		recovery = renderer.handoffRecovery(journalSnapshot)
		if len(recovery) > 0 && !rendererHandoffRecoveryQuota.reserve(t, len(recovery)) {
			recovery = renderer.handoffRecovery(journalSnapshot, false)
			if len(recovery) > 0 && !rendererHandoffRecoveryQuota.reserve(t, len(recovery)) {
				recovery = nil
			}
		}
		renderer.Close()
	}
	t.mu.Lock()
	t.pty = nil
	t.ptyWriter = nil
	t.renderer = nil
	t.handoffPTY = os.NewFile(uintptr(rollbackFD), "codelima-isolated-handoff-rollback")
	t.handoffRecovery = recovery
	t.state = runtimeStateQuiesced
	replay := rendererJournalReplay(journalSnapshot)
	t.mu.Unlock()
	return handoffTerminalState{
		PTY:           os.NewFile(uintptr(transferFD), "codelima-isolated-handoff-transfer"),
		ChildPID:      childPID,
		Cols:          cols,
		Rows:          rows,
		Replay:        replay,
		ReplayPartial: journalSnapshot.Partial,
		Recovery:      recovery,
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
	t.handoffRecovery = nil
	rendererHandoffRecoveryQuota.release(t)
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
	recovery := t.handoffRecovery
	t.handoffRecovery = nil
	rendererHandoffRecoveryQuota.release(t)
	t.handoffPTY = nil
	t.quit = make(chan struct{})
	t.quitOnce = sync.Once{}
	t.pty = ptyFile
	t.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, t.postPTYError)
	t.readPumpDone = make(chan struct{})
	t.state = runtimeStateRunning
	renderer := t.newRenderer()
	if len(recovery) > 0 {
		if bundle, err := decodeRendererRecovery(t.targetKey, recovery); err == nil {
			renderer.restoreCheckpoint(bundle.Checkpoint)
			renderer.restoreColors(bundle.Colors)
		}
	}
	t.renderer = renderer
	cols, rows := t.cols, t.rows
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), maxRendererInitDeadline)
	defer cancel()
	if err := renderer.Start(ctx, cols, rows); err != nil {
		return err
	}
	go t.readPump()
	return nil
}

// rendererStartContext bounds a renderer startup with the same journal-scaled
// budget the supervisor uses for its own restarts. A renderer has to replay the
// retained journal before it can answer init, so a fixed timeout turns a large
// journal into a guaranteed adoption or rollback failure.
func rendererStartContext(timeout time.Duration, journal *rendererJournal) (context.Context, context.CancelFunc) {
	journalBytes := 0
	if journal != nil {
		journalBytes = journal.Stats().Bytes
	}
	return context.WithTimeout(context.Background(), rendererInitDeadline(timeout, journalBytes))
}

// adoptIsolatedDaemonTerminal rebuilds a terminal in the replacement daemon
// from what a live update hands over.
//
// A renderer worker never survives a live update, so this path cannot inherit
// one that predates the running binary and needs no separate version check
// beyond the handshake every spawn already performs. The evidence, all in this
// file plus the handoff protocol:
//
//   - BeginHandoff closes the outgoing daemon's supervisor (renderer.Close())
//     before the PTY descriptor is transferred, which kills that worker process.
//   - handoffTerminalState carries a PTY, a child PID, geometry and the journal
//     replay bytes -- no renderer process, no control socket, no descriptor that
//     could reach one. daemon.HandoffRuntime, the serialized form, has the same
//     fields and no more.
//   - This function always constructs a fresh supervisor (newRenderer) and
//     starts it, and startRendererLink resolves the worker beside
//     os.Executable() -- which in the importing process is the NEW binary.
//
// So the only skew that can reach a renderer is an out-of-date worker file on
// disk beside the new binary, which is exactly what the init-reply version
// handshake rejects, and the journal replay assembled below is already the safe
// restart path a rejected worker falls back to.
func adoptIsolatedDaemonTerminal(
	targetKey string,
	postEvent func(vaxis.Event),
	ptyFile *os.File,
	childPID, cols, rows int,
	replay []byte,
	replayPartial bool,
	recovery []byte,
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
	var checkpoint *rendererCheckpoint
	var colors *TerminalColors
	if len(recovery) > 0 {
		bundle, err := decodeRendererRecovery(targetKey, recovery)
		if err != nil {
			return nil, fmt.Errorf("adopt renderer recovery: %w", err)
		}
		terminal.journal = journalFromRendererRecovery(bundle.Journal)
		terminal.cellWidth, terminal.cellHeight = bundle.Journal.CellWidth, bundle.Journal.CellHeight
		checkpoint = bundle.Checkpoint
		colors = bundle.Colors
	}
	terminal.pty = ptyFile
	terminal.childPID = childPID
	terminal.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, terminal.postPTYError)
	terminal.readPumpDone = make(chan struct{})
	terminal.renderer = terminal.newRenderer()
	terminal.renderer.restoreCheckpoint(checkpoint)
	terminal.renderer.restoreColors(colors)
	ctx, cancel := context.WithTimeout(context.Background(), maxRendererInitDeadline)
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

// RestartRenderer replaces this terminal's renderer process immediately. The
// supervisor's own restart budget and degraded cooldown are deliberately
// bypassed: this is the operator escape hatch for a renderer that the automatic
// policy has already given up on, and it is the only way back from a permanent
// degraded state without closing the tab.
//
// The PTY, the child process and the output journal are untouched, so the
// replacement replays the retained journal and the shell never notices.
func (t *isolatedDaemonTerminal) RestartRenderer() error {
	t.mu.Lock()
	renderer := t.renderer
	closed := t.closed
	t.mu.Unlock()
	if closed || renderer == nil {
		return errTerminalClosed
	}
	renderer.Restart()
	return nil
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
