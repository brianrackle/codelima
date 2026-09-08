package codelima

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/brianrackle/codelima/internal/terminalstate"
	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// daemonTUITerminal is a client-side view of a daemon-owned terminal. The
// daemon owns the PTY and emulator; this type pulls immutable cell snapshots
// and translates TUI input back into semantic events.
type daemonRPCCaller interface {
	Call(context.Context, string, any, any) error
}

const daemonTerminalInputQueueBytes = 1024 * 1024
const daemonTerminalInputQueueEvents = 1024

// daemonRPCTimeout bounds every fire-and-forget daemon terminal RPC issued by
// this client-side view (resize, input, snapshot, focus, close).
const daemonRPCTimeout = 2 * time.Second

// Keep the established 20 FPS ceiling while terminal output is active, but
// do not wake at all while the terminal is idle. Dirty notifications coalesce
// behind this interval instead of every open tab owning a permanent ticker.
const daemonTerminalSnapshotMinInterval = 50 * time.Millisecond

// daemonTerminalResizeRetryInterval paces the resize reassert loop after a
// failed attempt. A degraded daemon must not be retried once per redraw.
const daemonTerminalResizeRetryInterval = 250 * time.Millisecond

type daemonTUITerminal struct {
	client              daemonRPCCaller
	id                  string
	postEvent           func(vaxis.Event)
	mu                  sync.RWMutex
	inputMu             sync.Mutex
	inputOnce           sync.Once
	inputQueue          []daemonTerminalInputRequest
	inputBytes          int
	inputWake           chan struct{}
	inputDone           chan struct{}
	pasting             bool
	pasteRejected       bool
	paste               strings.Builder
	resizeMu            sync.Mutex
	resizeOnce          sync.Once
	resizeWake          chan struct{}
	desiredCols         int
	desiredRows         int
	desiredCellWidth    int
	desiredCellHeight   int
	resizeCols          int
	resizeRows          int
	resizeCellWidth     int
	resizeCellHeight    int
	snapshot            daemon.Snapshot
	text                string
	focused             bool
	focusVersion        uint64
	closed              bool
	stop                chan struct{}
	stopOnce            sync.Once
	closeOnce           sync.Once
	generation          uint64
	snapshotWake        chan struct{}
	snapshotVersion     uint64
	snapshotReadVersion uint64
}

type daemonTerminalInputRequest struct {
	params map[string]any
	bytes  int
}

func newDaemonTUITerminal(client daemonRPCCaller, id string, postEvent func(vaxis.Event)) *daemonTUITerminal {
	t := &daemonTUITerminal{
		client:          client,
		id:              id,
		postEvent:       postEvent,
		stop:            make(chan struct{}),
		snapshotWake:    make(chan struct{}, 1),
		snapshotVersion: 1,
	}
	go t.snapshotLoop()
	return t
}

func (t *daemonTUITerminal) Start(*exec.Cmd) error { return nil }

// Resize records the window's latest geometry. Draw calls it every frame, so it
// must never touch the socket: the size is replaceable latest-value state, and
// a background reassert loop drives it to the daemon and retries on failure.
func (t *daemonTUITerminal) Resize(width, height int) {
	t.resizePixels(width, height, 0, 0)
}

func (t *daemonTUITerminal) resizePixels(width, height, cellWidth, cellHeight int) {
	if width <= 0 || height <= 0 || t.isClosed() {
		return
	}
	t.resizeMu.Lock()
	if cellWidth == 0 || cellHeight == 0 {
		cellWidth, cellHeight = t.desiredCellWidth, t.desiredCellHeight
	}
	if width == t.desiredCols && height == t.desiredRows && cellWidth == t.desiredCellWidth && cellHeight == t.desiredCellHeight {
		t.resizeMu.Unlock()
		return
	}
	t.desiredCols, t.desiredRows = width, height
	t.desiredCellWidth, t.desiredCellHeight = cellWidth, cellHeight
	t.resizeOnce.Do(func() {
		t.resizeWake = make(chan struct{}, 1)
		go t.reassertResize()
	})
	wake := t.resizeWake
	t.resizeMu.Unlock()

	select {
	case wake <- struct{}{}:
	default:
	}
}

// reassertResize drives the daemon to the latest requested geometry. Only the
// newest size matters, so a superseded one is simply dropped, and a failed
// attempt retries on a bounded interval instead of once per redraw.
func (t *daemonTUITerminal) reassertResize() {
	for {
		select {
		case <-t.resizeWake:
		case <-t.stop:
			return
		}

		for {
			t.resizeMu.Lock()
			cols, rows := t.desiredCols, t.desiredRows
			cellWidth, cellHeight := t.desiredCellWidth, t.desiredCellHeight
			settled := cols == t.resizeCols && rows == t.resizeRows && cellWidth == t.resizeCellWidth && cellHeight == t.resizeCellHeight
			t.resizeMu.Unlock()
			if settled || t.isClosed() {
				break
			}

			ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
			err := t.client.Call(ctx, "terminal.resize", map[string]any{"terminal_id": t.id, "cols": cols, "rows": rows, "cell_width": cellWidth, "cell_height": cellHeight}, nil)
			cancel()
			if err != nil {
				if isSeatRejection(err) {
					// Another window holds the seat, so this geometry is an
					// observer's view, not an error to retry: park until the
					// next local size change or a seat acquisition pokes the
					// loop (ADR 130).
					break
				}
				select {
				case <-t.stop:
					return
				case <-time.After(daemonTerminalResizeRetryInterval):
				}
				continue
			}
			t.resizeMu.Lock()
			t.resizeCols, t.resizeRows = cols, rows
			t.resizeCellWidth, t.resizeCellHeight = cellWidth, cellHeight
			t.resizeMu.Unlock()
		}

		select {
		case <-t.stop:
			return
		default:
		}
	}
}

// pokeResize re-drives the reassert loop after this client acquires the seat,
// so a geometry parked on a seat rejection is re-presented without waiting
// for a local size change.
func (t *daemonTUITerminal) pokeResize() {
	t.resizeMu.Lock()
	wake := t.resizeWake
	t.resizeMu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

// isSeatRejection reports whether err is the daemon's seat gate refusing
// replaceable state from a client that does not hold the input lease. The
// gate rejects before handler dispatch, so a seat rejection provably applied
// nothing.
func isSeatRejection(err error) bool {
	var rpcErr *daemon.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == daemon.CodePreconditionFailed
}

func (t *daemonTUITerminal) Update(event vaxis.Event) {
	t.inputMu.Lock()
	defer t.inputMu.Unlock()

	if t.isClosed() {
		return
	}
	params := map[string]any{"terminal_id": t.id}
	switch value := event.(type) {
	case vaxis.PasteStartEvent:
		t.pasting = true
		t.pasteRejected = false
		t.paste.Reset()
		return
	case vaxis.PasteEndEvent:
		t.finishPasteLocked()
		return
	case vaxis.Key:
		if t.pasting && value.EventType == vaxis.EventPaste {
			text := encodeTUITerminalPasteKey(value)
			if !t.pasteRejected && len(text) <= terminalMaxPasteBytes-t.paste.Len() {
				t.paste.WriteString(text)
			} else if !t.pasteRejected {
				t.pasteRejected = true
				t.paste.Reset()
				t.reportInputErrorLocked(fmt.Errorf("paste exceeds the %d-byte limit; nothing was sent", terminalMaxPasteBytes))
			}
			return
		}
		if t.pasting {
			// Be defensive if a terminal fails to emit PasteEnd: preserve input
			// ordering by committing the buffered paste before the next key.
			t.finishPasteLocked()
		}
		params["type"] = "key"
		params["keycode"] = value.Keycode
		params["shifted_code"] = value.ShiftedCode
		params["text"] = value.Text
		params["modifiers"] = value.Modifiers
		params["event_type"] = value.EventType
	case vaxis.Mouse:
		params["type"] = "mouse"
		params["col"], params["row"] = value.Col, value.Row
		params["button"], params["event_type"] = value.Button, value.EventType
	default:
		return
	}
	t.enqueueInputLocked(params)
}

func (t *daemonTUITerminal) finishPasteLocked() {
	if !t.pasting {
		return
	}
	text := normalizeTUITerminalPasteText(t.paste.String())
	t.pasting = false
	t.paste.Reset()
	if t.pasteRejected || text == "" {
		return
	}
	t.enqueueInputLocked(map[string]any{
		"terminal_id": t.id,
		"type":        "paste",
		"text":        text,
	})
}

func (t *daemonTUITerminal) enqueueInputLocked(params map[string]any) {
	text, _ := params["text"].(string)
	// Include fixed overhead so zero-text key and mouse floods are bounded too.
	cost := len(text) + 128
	if len(t.inputQueue) >= daemonTerminalInputQueueEvents || cost > daemonTerminalInputQueueBytes-t.inputBytes {
		t.reportInputErrorLocked(errors.New("terminal input queue is full; input was not admitted"))
		return
	}
	t.inputOnce.Do(func() {
		t.inputWake = make(chan struct{}, 1)
		t.inputDone = make(chan struct{})
		go t.deliverInput()
	})
	t.inputQueue = append(t.inputQueue, daemonTerminalInputRequest{params: params, bytes: cost})
	t.inputBytes += cost
	select {
	case t.inputWake <- struct{}{}:
	default:
	}
}

func (t *daemonTUITerminal) reportInputErrorLocked(err error) {
	if t.postEvent != nil {
		t.postEvent(tuiTerminalErrorEvent{Err: err})
	}
}

func (t *daemonTUITerminal) deliverInput() {
	defer close(t.inputDone)
	inputFailed := false
	for {
		select {
		case <-t.inputWake:
		case <-t.stop:
		}

		for {
			t.inputMu.Lock()
			if len(t.inputQueue) == 0 {
				t.inputMu.Unlock()
				break
			}
			request := t.inputQueue[0]
			t.inputBytes -= request.bytes
			t.inputQueue[0] = daemonTerminalInputRequest{}
			t.inputQueue = t.inputQueue[1:]
			t.inputMu.Unlock()

			ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
			err := t.client.Call(ctx, "terminal.send_event", request.params, nil)
			cancel()
			if err != nil {
				if !inputFailed && t.postEvent != nil {
					t.postEvent(tuiTerminalErrorEvent{Err: fmt.Errorf("send terminal input: %w", err)})
				}
				inputFailed = true
			} else {
				inputFailed = false
			}
		}

		select {
		case <-t.stop:
			return
		default:
		}
	}
}

func (t *daemonTUITerminal) Draw(win vaxis.Window) {
	width, height := win.Size()
	t.Resize(width, height)
	t.requestSnapshot()
	t.mu.RLock()
	snapshot := t.snapshot
	focused := t.focused
	t.mu.RUnlock()
	rows := min(height, snapshot.Rows)
	cols := min(width, snapshot.Cols)
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			index := row*snapshot.Cols + col
			if index < 0 || index >= len(snapshot.Cells) {
				continue
			}
			cell := snapshot.Cells[index]
			if cell.Width == 0 {
				continue
			}
			grapheme := cell.Grapheme
			if grapheme == "" {
				grapheme = " "
			}
			style := daemonCellStyle(cell)
			win.SetCell(col, row, vaxis.Cell{Character: vaxis.Character{Grapheme: grapheme, Width: cell.Width}, Style: style})
		}
	}
	if focused && snapshot.CursorVisible && snapshot.CursorX >= 0 && snapshot.CursorX < width && snapshot.CursorY >= 0 && snapshot.CursorY < height {
		win.ShowCursor(snapshot.CursorX, snapshot.CursorY, vaxis.CursorBlock)
	}
}

func daemonCellStyle(cell daemon.SnapshotCell) vaxis.Style { return terminalstate.CellStyle(cell) }

func (t *daemonTUITerminal) Close() {
	// CloseSession runs on the Vaxis event loop. Stop local input admission
	// before returning, but let already accepted input and the bounded daemon
	// close request finish without holding that UI actor.
	inputDone := t.beginDetach()
	t.closeOnce.Do(func() {
		go func() {
			if inputDone != nil {
				<-inputDone
			}
			ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
			defer cancel()
			_ = t.client.Call(ctx, "terminal.close", map[string]string{"terminal_id": t.id}, nil)
		}()
	})
}

func (t *daemonTUITerminal) Detach() { t.detach() }

func (t *daemonTUITerminal) detach() {
	inputDone := t.beginDetach()
	if inputDone != nil {
		<-inputDone
	}
}

func (t *daemonTUITerminal) beginDetach() <-chan struct{} {
	t.stopOnce.Do(func() {
		t.inputMu.Lock()
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.stop)
		t.inputMu.Unlock()
	})
	t.inputMu.Lock()
	defer t.inputMu.Unlock()
	return t.inputDone
}

// Focus marks this terminal focused and tells the daemon off the event loop.
// Focus transitions are driven from key, mouse, and tab handling, so the loop
// must not wait on the RPC; the daemon-side call is idempotent per terminal, and
// a failure rolls the local flag back when no newer transition superseded it.
func (t *daemonTUITerminal) Focus() {
	t.mu.Lock()
	if t.closed || t.focused {
		t.mu.Unlock()
		return
	}
	t.focused = true
	t.focusVersion++
	focusVersion := t.focusVersion
	t.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
		defer cancel()
		if err := t.client.Call(ctx, "terminal.focus", map[string]string{"terminal_id": t.id}, nil); err != nil {
			t.mu.Lock()
			if t.focused && t.focusVersion == focusVersion {
				t.focused = false
			}
			t.mu.Unlock()
		}
	}()
}

func (t *daemonTUITerminal) Blur() {
	t.mu.Lock()
	if !t.focused {
		t.mu.Unlock()
		return
	}
	t.focused = false
	t.focusVersion++
	t.mu.Unlock()
}

func (t *daemonTUITerminal) String() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.text
}

func (t *daemonTUITerminal) TermEnv() string { return tuiEmbeddedTermEnv }

func (t *daemonTUITerminal) HyperlinkAt(col, row int) (string, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if row < 0 || col < 0 || row >= t.snapshot.Rows || col >= t.snapshot.Cols {
		return "", false
	}
	index := row*t.snapshot.Cols + col
	if index >= len(t.snapshot.Cells) || t.snapshot.Cells[index].Hyperlink == "" {
		return "", false
	}
	return t.snapshot.Cells[index].Hyperlink, true
}

func (t *daemonTUITerminal) CapturesMouse() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshot.CapturesMouse
}

func (t *daemonTUITerminal) isClosed() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.closed
}

func (t *daemonTUITerminal) markSnapshotDirty() {
	t.mu.Lock()
	if !t.closed {
		t.snapshotVersion++
	}
	t.mu.Unlock()
}

func (t *daemonTUITerminal) installSnapshot(snapshot daemon.Snapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.snapshot = snapshot
	t.generation = snapshot.Generation
	t.text = daemonSnapshotText(snapshot)
	t.snapshotReadVersion = t.snapshotVersion
}

func (t *daemonTUITerminal) requestSnapshot() {
	t.mu.RLock()
	dirty := !t.closed && t.snapshotVersion != t.snapshotReadVersion
	wake := t.snapshotWake
	t.mu.RUnlock()
	if !dirty || wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (t *daemonTUITerminal) snapshotLoop() {
	var lastSnapshot time.Time
	for {
		select {
		case <-t.stop:
			return
		case <-t.snapshotWake:
		}

		if delay := time.Until(lastSnapshot.Add(daemonTerminalSnapshotMinInterval)); !lastSnapshot.IsZero() && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-t.stop:
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		t.mu.RLock()
		dirty := !t.closed && t.snapshotVersion != t.snapshotReadVersion
		requestedVersion := t.snapshotVersion
		t.mu.RUnlock()
		if !dirty {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
		var snapshot daemon.Snapshot
		err := t.client.Call(ctx, "terminal.snapshot", map[string]string{"terminal_id": t.id}, &snapshot)
		cancel()
		lastSnapshot = time.Now()
		if err != nil {
			continue
		}
		t.mu.Lock()
		changed := snapshot.SnapshotSequence != t.snapshot.SnapshotSequence ||
			snapshot.Generation != t.generation ||
			snapshot.Cols != t.snapshot.Cols ||
			snapshot.Rows != t.snapshot.Rows
		t.snapshot = snapshot
		t.generation = snapshot.Generation
		t.text = daemonSnapshotText(snapshot)
		if requestedVersion > t.snapshotReadVersion {
			t.snapshotReadVersion = requestedVersion
		}
		t.mu.Unlock()
		if changed && t.postEvent != nil {
			t.postEvent(vaxis.Redraw{})
		}
	}
}

func daemonSnapshotText(snapshot daemon.Snapshot) string {
	return terminalstate.SnapshotText(terminalstate.Snapshot{Rows: snapshot.Rows, Cols: snapshot.Cols, Cells: snapshot.Cells})
}
