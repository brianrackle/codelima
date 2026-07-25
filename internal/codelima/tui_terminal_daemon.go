package codelima

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// daemonTUITerminal is a client-side view of a daemon-owned terminal. The
// daemon owns the PTY and emulator; this type pulls immutable cell snapshots
// and translates TUI input back into semantic events.
type daemonRPCCaller interface {
	Call(context.Context, string, any, any) error
}

// Keep worst-case JSON escaping (six bytes for one control byte), request
// metadata, and the newline delimiter comfortably below daemon.MaxMessageSize.
const daemonTerminalPasteChunkBytes = daemon.MaxMessageSize / 16

// daemonRPCTimeout bounds every fire-and-forget daemon terminal RPC issued by
// this client-side view (resize, input, snapshot, focus, close).
const daemonRPCTimeout = 2 * time.Second

// Keep the established 20 FPS ceiling while terminal output is active, but
// do not wake at all while the terminal is idle. Dirty notifications coalesce
// behind this interval instead of every open tab owning a permanent ticker.
const daemonTerminalSnapshotMinInterval = 50 * time.Millisecond

type daemonTUITerminal struct {
	client              daemonRPCCaller
	id                  string
	postEvent           func(vaxis.Event)
	mu                  sync.RWMutex
	inputMu             sync.Mutex
	inputOnce           sync.Once
	inputQueue          []daemonTerminalInputRequest
	inputWake           chan struct{}
	inputDone           chan struct{}
	pasting             bool
	paste               strings.Builder
	resizeMu            sync.Mutex
	resizeCols          int
	resizeRows          int
	snapshot            daemon.Snapshot
	text                string
	focused             bool
	closed              bool
	stop                chan struct{}
	stopOnce            sync.Once
	generation          uint64
	snapshotWake        chan struct{}
	snapshotVersion     uint64
	snapshotReadVersion uint64
}

type daemonTerminalInputRequest struct {
	params map[string]any
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

func (t *daemonTUITerminal) Resize(width, height int) {
	if width <= 0 || height <= 0 || t.isClosed() {
		return
	}
	t.resizeMu.Lock()
	defer t.resizeMu.Unlock()
	if width == t.resizeCols && height == t.resizeRows {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	if err := t.client.Call(ctx, "terminal.resize", map[string]any{"terminal_id": t.id, "cols": width, "rows": height}, nil); err == nil {
		t.resizeCols, t.resizeRows = width, height
	}
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
		t.paste.Reset()
		return
	case vaxis.PasteEndEvent:
		t.finishPasteLocked()
		return
	case vaxis.Key:
		if t.pasting && value.EventType == vaxis.EventPaste {
			t.paste.WriteString(encodeTUITerminalPasteKey(value))
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

	for text != "" {
		end := min(len(text), daemonTerminalPasteChunkBytes)
		if end < len(text) {
			for end > 0 && !utf8.RuneStart(text[end]) {
				end--
			}
		}
		chunk := text[:end]
		text = text[end:]
		t.enqueueInputLocked(map[string]any{
			"terminal_id": t.id,
			"type":        "paste",
			"text":        chunk,
		})
	}
}

func (t *daemonTUITerminal) enqueueInputLocked(params map[string]any) {
	t.inputOnce.Do(func() {
		t.inputWake = make(chan struct{}, 1)
		t.inputDone = make(chan struct{})
		go t.deliverInput()
	})
	t.inputQueue = append(t.inputQueue, daemonTerminalInputRequest{params: params})
	select {
	case t.inputWake <- struct{}{}:
	default:
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

func daemonCellStyle(cell daemon.SnapshotCell) vaxis.Style {
	style := vaxis.Style{}
	if !cell.FGDefault {
		style.Foreground = vaxis.HexColor(cell.FG)
	}
	if !cell.BGDefault {
		style.Background = vaxis.HexColor(cell.BG)
	}
	if cell.Bold {
		style.Attribute |= vaxis.AttrBold
	}
	if cell.Faint {
		style.Attribute |= vaxis.AttrDim
	}
	if cell.Italic {
		style.Attribute |= vaxis.AttrItalic
	}
	if cell.Strikethrough {
		style.Attribute |= vaxis.AttrStrikethrough
	}
	if cell.Inverse {
		style.Attribute |= vaxis.AttrReverse
	}
	if cell.Invisible {
		style.Attribute |= vaxis.AttrInvisible
	}
	if cell.Blink {
		style.Attribute |= vaxis.AttrBlink
	}
	if cell.Underline || cell.Hyperlink != "" {
		style.UnderlineStyle = vaxis.UnderlineSingle
	}
	style.Hyperlink = cell.Hyperlink
	return style
}

func (t *daemonTUITerminal) Close() {
	t.detach()
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	_ = t.client.Call(ctx, "terminal.close", map[string]string{"terminal_id": t.id}, nil)
}

func (t *daemonTUITerminal) Detach() { t.detach() }

func (t *daemonTUITerminal) detach() {
	t.stopOnce.Do(func() {
		t.inputMu.Lock()
		t.mu.Lock()
		t.closed = true
		t.mu.Unlock()
		close(t.stop)
		inputDone := t.inputDone
		t.inputMu.Unlock()
		if inputDone != nil {
			<-inputDone
		}
	})
}

func (t *daemonTUITerminal) Focus() {
	t.mu.Lock()
	t.focused = true
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	_ = t.client.Call(ctx, "terminal.focus", map[string]string{"terminal_id": t.id}, nil)
}

func (t *daemonTUITerminal) Blur() {
	t.mu.Lock()
	t.focused = false
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
		changed := snapshot.Generation != t.generation || snapshot.Cols != t.snapshot.Cols || snapshot.Rows != t.snapshot.Rows
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
	lines := make([]string, 0, snapshot.Rows)
	for row := 0; row < snapshot.Rows; row++ {
		var line strings.Builder
		for col := 0; col < snapshot.Cols; col++ {
			index := row*snapshot.Cols + col
			if index >= len(snapshot.Cells) {
				break
			}
			cell := snapshot.Cells[index]
			if cell.Width == 0 {
				continue
			}
			grapheme := cell.Grapheme
			if grapheme == "" {
				grapheme = " "
			}
			line.WriteString(grapheme)
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}
