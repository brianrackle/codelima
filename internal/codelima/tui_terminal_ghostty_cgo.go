//go:build cgo && (darwin || linux)

package codelima

/*
#cgo CFLAGS: -I${SRCDIR}/../../.tooling/ghostty-vt/current/include
#cgo linux LDFLAGS: -ldl
#include <stdlib.h>
#include "ghostty_bridge_compat.h"
*/
import "C"

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const (
	ghosttyModeCursorKeys        = 1
	ghosttyModeApplicationKeypad = 66
	ghosttyModeMouseButtons      = 1000
	ghosttyModeMouseDrag         = 1002
	ghosttyModeMouseMotion       = 1003
	ghosttyModeFocusEvents       = 1004
	ghosttyModeMouseSGR          = 1006
	ghosttyModeAltScroll         = 1007
	ghosttyModeBracketedPaste    = 2004
	ghosttyModeColorScheme       = 2031
)

type ghosttyAPILoadState struct {
	once sync.Once
	err  error
}

var ghosttyAPI ghosttyAPILoadState

type ghosttyStderrState struct {
	once sync.Once
	mu   sync.Mutex
	file *os.File // write end of the capture pipe, dup2'd over fd 2 around bridge calls
	err  error
}

var ghosttyStderr ghosttyStderrState

// withGhosttyStderrSuppressed redirects process stderr to an internal pipe for
// the duration of fn so libghostty's parser warnings never spill into the TUI
// chrome. A background reader drains the pipe and forwards each non-empty line to
// the package logger at debug level, tagged source=libghostty, so the warnings
// stay inspectable in _logs/codelima.log while staying invisible on screen
// (work item 0.5, ADR 59). This replaces the former /dev/null sink, which threw
// the warnings away entirely.
//
// CAVEAT: the dup2 over fd 2 is process-global and not concurrency-safe. The
// mutex serializes every bridge call, so two terminals initializing at once can
// no longer interleave the redirect (which also closes that prior data race).
// The pipe and its reader goroutine are created once via sync.Once and live for
// the life of the process; the reader must keep draining or a full pipe buffer
// would block libghostty's writes to fd 2 and hang the bridge call.
func withGhosttyStderrSuppressed[T any](fn func() T) T {
	ghosttyStderr.mu.Lock()
	defer ghosttyStderr.mu.Unlock()

	ghosttyStderr.once.Do(func() {
		reader, writer, err := os.Pipe()
		if err != nil {
			ghosttyStderr.err = err
			return
		}
		ghosttyStderr.file = writer
		go drainGhosttyStderr(reader)
	})
	if ghosttyStderr.err != nil || ghosttyStderr.file == nil {
		return fn()
	}

	stderrFD := int(os.Stderr.Fd())
	savedFD, err := unix.Dup(stderrFD)
	if err != nil {
		return fn()
	}
	if err := unix.Dup2(int(ghosttyStderr.file.Fd()), stderrFD); err != nil {
		_ = unix.Close(savedFD)
		return fn()
	}
	defer func() {
		_ = unix.Dup2(savedFD, stderrFD)
		_ = unix.Close(savedFD)
	}()

	return fn()
}

// drainGhosttyStderr forwards captured libghostty stderr lines to the package
// logger. It runs for the process lifetime; the pipe's write end stays open
// (dup2 keeps targeting it), so the scanner only returns at process teardown.
func drainGhosttyStderr(reader *os.File) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		packageLog().Debug(line, "source", "libghostty")
	}
}

func newGhosttyTUITerminal(targetKey string, postEvent func(vaxis.Event)) (tuiTerminal, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}

	term := withGhosttyStderrSuppressed(func() C.GhosttyBridgeTerminal {
		return C.ghostty_bridge_terminal_new(80, 24)
	})
	if term == nil {
		return nil, fmt.Errorf("create ghostty terminal: %s", C.GoString(C.ghostty_bridge_last_error()))
	}

	impl := &ghosttyTUITerminal{
		targetKey: targetKey,
		postEvent: postEvent,
		term:      term,
		keyEncoder: func() *ghosttyKeyEncoder {
			encoder, err := newGhosttyKeyEncoder()
			if err != nil {
				return nil
			}
			return encoder
		}(),
		mouseEncoder: func() *ghosttyMouseEncoder {
			encoder, err := newGhosttyMouseEncoder()
			if err != nil {
				return nil
			}
			return encoder
		}(),
		clipboardScanner: newOSC52ClipboardScanner(func(text string) {
			if postEvent != nil {
				postEvent(tuiClipboardEvent{TargetKey: targetKey, Text: text})
			}
		}),
		cols:      80,
		rows:      24,
		state:     runtimeStateRunning,
		commands:  make(chan actorEnvelope),
		readCh:    make(chan []byte, 64),
		readErrCh: make(chan error, 1),
		quit:      make(chan struct{}),
		actorDone: make(chan struct{}),
	}

	// The actor runs from construction (not from Start) so the synchronous
	// interface methods work even before a child is spawned — several tests
	// Resize/Focus/Blur a terminal that never calls Start.
	go impl.runActor()

	return impl, nil
}

func loadGhosttyVT() error {
	ghosttyAPI.once.Do(func() {
		var loadErr error = errors.New("ghostty library not found")
		for _, candidate := range ghosttyVTCandidates() {
			if _, err := os.Stat(candidate); err != nil {
				continue
			}

			candidateC := C.CString(candidate)
			ok := C.ghostty_bridge_load(candidateC)
			C.free(unsafe.Pointer(candidateC))
			if ok == 1 {
				loadErr = nil
				return
			}

			loadErr = fmt.Errorf("load ghostty library %s: %s", candidate, C.GoString(C.ghostty_bridge_last_error()))
		}
		ghosttyAPI.err = loadErr
	})
	return ghosttyAPI.err
}

func ghosttyVTCandidates() []string {
	filename := "libghostty-vt.so"
	if runtime.GOOS == "darwin" {
		filename = "libghostty-vt.dylib"
	}

	var candidates []string
	appendCandidate := func(path string) {
		if strings.TrimSpace(path) == "" {
			return
		}
		for _, existing := range candidates {
			if existing == path {
				return
			}
		}
		candidates = append(candidates, path)
	}
	appendAncestorCandidates := func(base string) {
		base = filepath.Clean(base)
		for {
			appendCandidate(filepath.Join(base, ".tooling", "ghostty-vt", "current", "lib", filename))
			parent := filepath.Dir(base)
			if parent == base {
				return
			}
			base = parent
		}
	}

	if path := strings.TrimSpace(os.Getenv("CODELIMA_GHOSTTY_VT_LIB")); path != "" {
		appendCandidate(path)
	}

	if cwd, err := os.Getwd(); err == nil {
		appendAncestorCandidates(cwd)
	}

	if executable, err := os.Executable(); err == nil {
		appendAncestorCandidates(filepath.Dir(executable))
	}

	return candidates
}

type ghosttyTUITerminal struct {
	targetKey    string
	postEvent    func(vaxis.Event)
	term         C.GhosttyBridgeTerminal
	keyEncoder   *ghosttyKeyEncoder
	mouseEncoder *ghosttyMouseEncoder

	mu               sync.Mutex
	cmd              *exec.Cmd
	pty              *os.File
	ptyWriter        *ghosttyPTYWriter
	clipboardScanner *osc52ClipboardScanner
	cols             int
	rows             int
	focused          bool
	mouseButtonsDown int
	snapshot         string
	closed           bool
	suppressEvent    bool
	redrawPending    bool
	generation       uint64
	waitOnce         sync.Once
	waitErr          error
	waitDone         chan struct{}
	closeOnce        sync.Once

	// Runtime-actor plumbing (work item 2.1, ADR 63). The actor goroutine
	// (runActor) is the sole live mutator of pty+emulator; interface methods
	// enqueue onto commands, the read pump feeds readCh/readErrCh, quit stops
	// the pump during teardown, and actorDone closes when the actor exits.
	state        runtimeState
	commands     chan actorEnvelope
	readCh       chan []byte
	readErrCh    chan error
	quit         chan struct{}
	quitOnce     sync.Once
	actorDone    chan struct{}
	childPID     int
	replay       []byte
	handoffPTY   *os.File
	readPumpDone chan struct{}
}

type ghosttyKeyEncoder struct {
	encoder C.GhosttyKeyEncoder
}

type ghosttyMouseEncoder struct {
	encoder C.GhosttyMouseEncoder
}

type ghosttyScrollbarState struct {
	total  int
	offset int
	length int
}

type ghosttyPTYWriteTarget interface {
	Write([]byte) (int, error)
	Close() error
	Fd() uintptr
}

type ghosttyPTYWriter struct {
	target       ghosttyPTYWriteTarget
	waitWritable func(fd int) error
	onError      func(error)

	mu     sync.Mutex
	cond   *sync.Cond
	queue  bytes.Buffer
	closed bool
	active bool
	done   chan struct{}
}

func newGhosttyKeyEncoder() (*ghosttyKeyEncoder, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}
	if !bool(C.ghostty_bridge_has_key_encoder_api()) {
		return nil, errors.New("ghostty key encoder API unavailable")
	}

	var encoder C.GhosttyKeyEncoder
	//nolint:gocritic // dupSubExpr false positive: gocritic misreads the cgo-typed nil comparison
	if result := C.ghostty_bridge_key_encoder_new(&encoder); result != C.GHOSTTY_SUCCESS || encoder == nil {
		return nil, fmt.Errorf("create ghostty key encoder: %d", int(result))
	}

	return &ghosttyKeyEncoder{encoder: encoder}, nil
}

func (e *ghosttyKeyEncoder) Close() {
	if e == nil || e.encoder == nil {
		return
	}
	C.ghostty_bridge_key_encoder_free(e.encoder)
	e.encoder = nil
}

func newGhosttyMouseEncoder() (*ghosttyMouseEncoder, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}
	if !bool(C.ghostty_bridge_has_mouse_encoder_api()) {
		return nil, errors.New("ghostty mouse encoder API unavailable")
	}

	var encoder C.GhosttyMouseEncoder
	//nolint:gocritic // dupSubExpr false positive: gocritic misreads the cgo-typed nil comparison
	if result := C.ghostty_bridge_mouse_encoder_new(&encoder); result != C.GHOSTTY_SUCCESS || encoder == nil {
		return nil, fmt.Errorf("create ghostty mouse encoder: %d", int(result))
	}

	return &ghosttyMouseEncoder{encoder: encoder}, nil
}

func newGhosttyPTYWriter(target ghosttyPTYWriteTarget, waitWritable func(fd int) error, onError func(error)) *ghosttyPTYWriter {
	writer := &ghosttyPTYWriter{
		target:       target,
		waitWritable: waitWritable,
		onError:      onError,
		done:         make(chan struct{}),
	}
	writer.cond = sync.NewCond(&writer.mu)
	go writer.loop()
	return writer
}

func (w *ghosttyPTYWriter) Enqueue(data []byte) bool {
	if w == nil || len(data) == 0 {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	_, _ = w.queue.Write(data)
	w.cond.Signal()
	return true
}

func (w *ghosttyPTYWriter) Close() {
	if w == nil {
		return
	}

	w.mu.Lock()
	alreadyClosed := w.closed
	w.closed = true
	w.queue.Reset()
	w.cond.Broadcast()
	target := w.target
	done := w.done
	w.mu.Unlock()

	if !alreadyClosed && target != nil {
		_ = target.Close()
	}
	<-done
}

func (w *ghosttyPTYWriter) loop() {
	defer close(w.done)

	for {
		chunk, ok := w.nextChunk()
		if !ok {
			return
		}
		err := ghosttyWriteAllToPTY(w.target, chunk, w.waitWritable)
		w.mu.Lock()
		w.active = false
		w.cond.Broadcast()
		w.mu.Unlock()
		if err != nil {
			if w.isClosed() || isGhosttyPTYClosedError(err) {
				return
			}
			if w.onError != nil {
				w.onError(err)
			}
			return
		}
	}
}

func (w *ghosttyPTYWriter) nextChunk() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for w.queue.Len() == 0 && !w.closed {
		w.cond.Wait()
	}
	if w.queue.Len() == 0 {
		return nil, false
	}

	chunk := append([]byte(nil), w.queue.Bytes()...)
	w.queue.Reset()
	w.active = true
	return chunk, true
}

func (w *ghosttyPTYWriter) Drain(timeout time.Duration) error {
	if w == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		empty := w.queue.Len() == 0 && !w.active
		closed := w.closed
		w.mu.Unlock()
		if empty || closed {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out draining terminal writes")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *ghosttyPTYWriter) isClosed() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func ghosttyWriteAllToPTY(target ghosttyPTYWriteTarget, data []byte, waitWritable func(fd int) error) error {
	if target == nil || len(data) == 0 {
		return nil
	}

	for len(data) > 0 {
		n, err := target.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if len(data) == 0 && err == nil {
			return nil
		}
		if err == nil {
			if n == 0 {
				if waitWritable == nil {
					return io.ErrShortWrite
				}
				if err := waitWritable(int(target.Fd())); err != nil {
					return err
				}
			}
			continue
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if isGhosttyPTYWouldBlockError(err) {
			if waitWritable == nil {
				continue
			}
			if err := waitWritable(int(target.Fd())); err != nil {
				return err
			}
			continue
		}
		return err
	}

	return nil
}

func waitGhosttyPTYWritable(fd int) error {
	if fd < 0 {
		return unix.EBADF
	}
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT | unix.POLLHUP | unix.POLLERR}}
		// Keep this wait bounded. Handoff PTYs are nonblocking, and closing an
		// fd concurrently with poll is not guaranteed to wake on every Unix;
		// returning after one beat lets the write loop observe EBADF/closed.
		_, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

func isGhosttyPTYWouldBlockError(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)
}

func isGhosttyPTYClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, unix.EBADF) ||
		errors.Is(err, unix.EIO)
}

func (e *ghosttyMouseEncoder) Close() {
	if e == nil || e.encoder == nil {
		return
	}
	C.ghostty_bridge_mouse_encoder_free(e.encoder)
	e.encoder = nil
}

func (e *ghosttyMouseEncoder) Reset() {
	if e == nil || e.encoder == nil {
		return
	}
	C.ghostty_bridge_mouse_encoder_reset(e.encoder)
}

func encodeTUITerminalKeyWithGhostty(
	key vaxis.Key,
	encoder *ghosttyKeyEncoder,
	term C.GhosttyBridgeTerminal,
	applicationKeypad bool,
	cursorKeysApplication bool,
) string {
	if key.EventType == vaxis.EventPaste {
		return encodeTUITerminalPasteKey(key)
	}
	if encoder != nil {
		if encoded, handled := encoder.Encode(key, term, applicationKeypad, cursorKeysApplication); handled {
			return encoded
		}
	}

	return encodeTUITerminalKey(key, cursorKeysApplication)
}

func encodeTUITerminalMouseWithGhostty(
	mouse vaxis.Mouse,
	encoder *ghosttyMouseEncoder,
	term C.GhosttyBridgeTerminal,
	cols int,
	rows int,
	mouseButtonsDown int,
	sgr bool,
	drag bool,
	motion bool,
) string {
	if encoder != nil {
		if encoded, handled := encoder.Encode(mouse, term, cols, rows, mouseButtonsDown); handled {
			return encoded
		}
	}

	return encodeTUITerminalMouse(mouse, sgr, drag, motion)
}

func encodeTUITerminalFocusWithGhostty(focused bool) string {
	event := C.GhosttyFocusEvent(C.GHOSTTY_FOCUS_LOST)
	fallback := "\x1b[O"
	if focused {
		event = C.GhosttyFocusEvent(C.GHOSTTY_FOCUS_GAINED)
		fallback = "\x1b[I"
	}

	if bool(C.ghostty_bridge_has_focus_encoder_api()) {
		buffer := make([]byte, 16)
		var outLen C.size_t
		result := C.ghostty_bridge_focus_encode(
			event,
			(*C.char)(unsafe.Pointer(&buffer[0])),
			C.size_t(len(buffer)),
			&outLen,
		)
		if result == C.GHOSTTY_OUT_OF_SPACE {
			buffer = make([]byte, int(outLen))
			var bufferPtr *C.char
			if len(buffer) > 0 {
				bufferPtr = (*C.char)(unsafe.Pointer(&buffer[0]))
			}
			result = C.ghostty_bridge_focus_encode(
				event,
				bufferPtr,
				C.size_t(len(buffer)),
				&outLen,
			)
		}
		if result == C.GHOSTTY_SUCCESS {
			return string(buffer[:int(outLen)])
		}
	}

	return fallback
}

func (e *ghosttyKeyEncoder) Encode(
	key vaxis.Key,
	term C.GhosttyBridgeTerminal,
	applicationKeypad bool,
	cursorKeysApplication bool,
) (string, bool) {
	if e == nil || e.encoder == nil {
		return "", false
	}

	if key.EventType == vaxis.EventPaste {
		return encodeTUITerminalPasteKey(key), true
	}

	physicalKey, ok := ghosttyKeyForVaxis(key)
	if !ok {
		if key.EventType == vaxis.EventRelease {
			return "", true
		}
		return "", false
	}

	if !bool(C.ghostty_bridge_key_encoder_setopt_from_terminal(e.encoder, term)) {
		cursor := C.bool(cursorKeysApplication)
		C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_CURSOR_KEY_APPLICATION, cursor)
		keypad := C.bool(applicationKeypad)
		C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_KEYPAD_KEY_APPLICATION, keypad)
		altEscPrefix := C.bool(true)
		C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_ALT_ESC_PREFIX, altEscPrefix)
		modifyOtherKeysState2 := C.bool(false)
		C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_MODIFY_OTHER_KEYS_STATE_2, modifyOtherKeysState2)
	}

	text := ghosttyTextForVaxis(key)
	var textPtr *C.char
	if text != "" {
		textPtr = (*C.char)(unsafe.Pointer(unsafe.StringData(text)))
	}

	action := ghosttyKeyActionForVaxis(key.EventType)
	mods := ghosttyModsForVaxis(key.Modifiers)
	unshiftedCodepoint := C.uint32_t(ghosttyUnshiftedCodepoint(key))

	buffer := make([]byte, 64)
	var outLen C.size_t
	result := C.ghostty_bridge_key_encoder_encode_event(
		e.encoder,
		action,
		physicalKey,
		mods,
		textPtr,
		C.size_t(len(text)),
		unshiftedCodepoint,
		(*C.char)(unsafe.Pointer(&buffer[0])),
		C.size_t(len(buffer)),
		&outLen,
	)
	if result == C.GHOSTTY_OUT_OF_MEMORY {
		buffer = make([]byte, int(outLen))
		var bufferPtr *C.char
		if len(buffer) > 0 {
			bufferPtr = (*C.char)(unsafe.Pointer(&buffer[0]))
		}
		result = C.ghostty_bridge_key_encoder_encode_event(
			e.encoder,
			action,
			physicalKey,
			mods,
			textPtr,
			C.size_t(len(text)),
			unshiftedCodepoint,
			bufferPtr,
			C.size_t(len(buffer)),
			&outLen,
		)
	}
	if result != C.GHOSTTY_SUCCESS {
		return "", false
	}

	return string(buffer[:int(outLen)]), true
}

func (e *ghosttyMouseEncoder) Encode(
	mouse vaxis.Mouse,
	term C.GhosttyBridgeTerminal,
	cols int,
	rows int,
	mouseButtonsDown int,
) (string, bool) {
	if e == nil || e.encoder == nil || term == nil || cols <= 0 || rows <= 0 {
		return "", false
	}

	action := ghosttyMouseActionForVaxis(mouse.EventType)
	hasButton, button, ok := ghosttyMouseButtonForVaxis(mouse.Button)
	if !ok {
		return "", false
	}

	size := ghosttyMouseEncoderSizeForTerminal(cols, rows)
	position := ghosttyMousePositionForVaxis(mouse)
	mods := ghosttyModsForVaxis(mouse.Modifiers)
	anyButtonPressed := ghosttyMouseAnyButtonPressed(mouse, mouseButtonsDown)
	trackLastCell := C.bool(true)

	buffer := make([]byte, 64)
	var outLen C.size_t
	result := C.ghostty_bridge_mouse_encoder_encode_event(
		e.encoder,
		term,
		action,
		C.bool(hasButton),
		button,
		mods,
		position,
		&size,
		C.bool(anyButtonPressed),
		trackLastCell,
		(*C.char)(unsafe.Pointer(&buffer[0])),
		C.size_t(len(buffer)),
		&outLen,
	)
	if result == C.GHOSTTY_OUT_OF_SPACE {
		buffer = make([]byte, int(outLen))
		var bufferPtr *C.char
		if len(buffer) > 0 {
			bufferPtr = (*C.char)(unsafe.Pointer(&buffer[0]))
		}
		result = C.ghostty_bridge_mouse_encoder_encode_event(
			e.encoder,
			term,
			action,
			C.bool(hasButton),
			button,
			mods,
			position,
			&size,
			C.bool(anyButtonPressed),
			trackLastCell,
			bufferPtr,
			C.size_t(len(buffer)),
			&outLen,
		)
	}
	if result != C.GHOSTTY_SUCCESS {
		return "", false
	}

	return string(buffer[:int(outLen)]), true
}

func ghosttyKeyActionForVaxis(eventType vaxis.EventType) C.GhosttyKeyAction {
	switch eventType {
	case vaxis.EventRelease:
		return C.GHOSTTY_KEY_ACTION_RELEASE
	case vaxis.EventRepeat:
		return C.GHOSTTY_KEY_ACTION_REPEAT
	default:
		return C.GHOSTTY_KEY_ACTION_PRESS
	}
}

func ghosttyMouseActionForVaxis(eventType vaxis.EventType) C.GhosttyMouseAction {
	switch eventType {
	case vaxis.EventRelease:
		return C.GHOSTTY_MOUSE_ACTION_RELEASE
	case vaxis.EventMotion:
		return C.GHOSTTY_MOUSE_ACTION_MOTION
	default:
		return C.GHOSTTY_MOUSE_ACTION_PRESS
	}
}

func ghosttyMouseButtonForVaxis(button vaxis.MouseButton) (bool, C.GhosttyMouseButton, bool) {
	switch button {
	case vaxis.MouseNoButton:
		return false, C.GHOSTTY_MOUSE_BUTTON_UNKNOWN, true
	case vaxis.MouseLeftButton:
		return true, C.GHOSTTY_MOUSE_BUTTON_LEFT, true
	case vaxis.MouseMiddleButton:
		return true, C.GHOSTTY_MOUSE_BUTTON_MIDDLE, true
	case vaxis.MouseRightButton:
		return true, C.GHOSTTY_MOUSE_BUTTON_RIGHT, true
	case vaxis.MouseWheelUp:
		return true, C.GHOSTTY_MOUSE_BUTTON_FOUR, true
	case vaxis.MouseWheelDown:
		return true, C.GHOSTTY_MOUSE_BUTTON_FIVE, true
	case vaxis.MouseButton8:
		return true, C.GHOSTTY_MOUSE_BUTTON_EIGHT, true
	case vaxis.MouseButton9:
		return true, C.GHOSTTY_MOUSE_BUTTON_NINE, true
	case vaxis.MouseButton10:
		return true, C.GHOSTTY_MOUSE_BUTTON_TEN, true
	case vaxis.MouseButton11:
		return true, C.GHOSTTY_MOUSE_BUTTON_ELEVEN, true
	default:
		return false, C.GHOSTTY_MOUSE_BUTTON_UNKNOWN, false
	}
}

func ghosttyMouseEncoderSizeForTerminal(cols int, rows int) C.GhosttyMouseEncoderSize {
	return C.GhosttyMouseEncoderSize{
		size:          C.size_t(C.sizeof_GhosttyMouseEncoderSize),
		screen_width:  C.uint32_t(cols),
		screen_height: C.uint32_t(rows),
		cell_width:    1,
		cell_height:   1,
	}
}

func ghosttyMousePositionForVaxis(mouse vaxis.Mouse) C.GhosttyMousePosition {
	return C.GhosttyMousePosition{
		x: C.float(float32(mouse.Col) + 0.5),
		y: C.float(float32(mouse.Row) + 0.5),
	}
}

func ghosttyMouseAnyButtonPressed(mouse vaxis.Mouse, mouseButtonsDown int) bool {
	if mouseButtonsDown > 0 {
		return true
	}
	switch mouse.EventType {
	case vaxis.EventPress:
		return ghosttyTrackedMouseButton(mouse.Button)
	case vaxis.EventMotion:
		return ghosttyTrackedMouseButton(mouse.Button)
	default:
		return false
	}
}

func ghosttyTrackedMouseButton(button vaxis.MouseButton) bool {
	switch button {
	case vaxis.MouseNoButton, vaxis.MouseWheelUp, vaxis.MouseWheelDown:
		return false
	default:
		return true
	}
}

func ghosttyModsForVaxis(modifiers vaxis.ModifierMask) C.GhosttyMods {
	var mods C.GhosttyMods
	if modifiers&vaxis.ModShift != 0 {
		mods |= C.GHOSTTY_MODS_SHIFT
	}
	if modifiers&vaxis.ModCtrl != 0 {
		mods |= C.GHOSTTY_MODS_CTRL
	}
	if modifiers&vaxis.ModAlt != 0 {
		mods |= C.GHOSTTY_MODS_ALT
	}
	if modifiers&vaxis.ModSuper != 0 || modifiers&vaxis.ModMeta != 0 {
		mods |= C.GHOSTTY_MODS_SUPER
	}
	if modifiers&vaxis.ModCapsLock != 0 {
		mods |= C.GHOSTTY_MODS_CAPS_LOCK
	}
	if modifiers&vaxis.ModNumLock != 0 {
		mods |= C.GHOSTTY_MODS_NUM_LOCK
	}
	return mods
}

func ghosttyTextForVaxis(key vaxis.Key) string {
	if key.Text != "" {
		return key.Text
	}
	if key.Keycode >= unicode.MaxRune {
		return ""
	}
	if key.Keycode < 0x20 || key.Keycode == 0x7f {
		return ""
	}
	if key.Modifiers&(vaxis.ModCtrl|vaxis.ModAlt|vaxis.ModSuper|vaxis.ModMeta) != 0 {
		return ""
	}
	if key.Modifiers&vaxis.ModShift != 0 && key.ShiftedCode > 0 {
		return string(key.ShiftedCode)
	}
	return string(key.Keycode)
}

func ghosttyUnshiftedCodepoint(key vaxis.Key) uint32 {
	codepoint := key.BaseLayoutCode
	if codepoint == 0 || codepoint >= unicode.MaxRune {
		codepoint = key.Keycode
	}
	if codepoint == 0 || codepoint >= unicode.MaxRune {
		return 0
	}
	if codepoint < 0x20 || codepoint == 0x7f {
		return 0
	}
	if unicode.IsLetter(codepoint) {
		codepoint = unicode.ToLower(codepoint)
	}
	return uint32(codepoint)
}

func ghosttyKeyForVaxis(key vaxis.Key) (C.GhosttyKey, bool) {
	switch key.Keycode {
	case vaxis.KeyEnter:
		return C.GHOSTTY_KEY_ENTER, true
	case vaxis.KeyTab:
		return C.GHOSTTY_KEY_TAB, true
	case vaxis.KeyEsc:
		return C.GHOSTTY_KEY_ESCAPE, true
	case vaxis.KeySpace:
		return C.GHOSTTY_KEY_SPACE, true
	case vaxis.KeyBackspace:
		return C.GHOSTTY_KEY_BACKSPACE, true
	case vaxis.KeyInsert:
		return C.GHOSTTY_KEY_INSERT, true
	case vaxis.KeyDelete:
		return C.GHOSTTY_KEY_DELETE, true
	case vaxis.KeyHome:
		return C.GHOSTTY_KEY_HOME, true
	case vaxis.KeyEnd:
		return C.GHOSTTY_KEY_END, true
	case vaxis.KeyPgUp:
		return C.GHOSTTY_KEY_PAGE_UP, true
	case vaxis.KeyPgDown:
		return C.GHOSTTY_KEY_PAGE_DOWN, true
	case vaxis.KeyUp:
		return C.GHOSTTY_KEY_ARROW_UP, true
	case vaxis.KeyDown:
		return C.GHOSTTY_KEY_ARROW_DOWN, true
	case vaxis.KeyLeft:
		return C.GHOSTTY_KEY_ARROW_LEFT, true
	case vaxis.KeyRight:
		return C.GHOSTTY_KEY_ARROW_RIGHT, true
	case vaxis.KeyCapsLock:
		return C.GHOSTTY_KEY_CAPS_LOCK, true
	case vaxis.KeyScrollLock:
		return C.GHOSTTY_KEY_SCROLL_LOCK, true
	case vaxis.KeyNumlock:
		return C.GHOSTTY_KEY_NUM_LOCK, true
	case vaxis.KeyPrintScreen:
		return C.GHOSTTY_KEY_PRINT_SCREEN, true
	case vaxis.KeyPause:
		return C.GHOSTTY_KEY_PAUSE, true
	case vaxis.KeyMenu:
		return C.GHOSTTY_KEY_CONTEXT_MENU, true
	case vaxis.KeyLeftShift:
		return C.GHOSTTY_KEY_SHIFT_LEFT, true
	case vaxis.KeyRightShift:
		return C.GHOSTTY_KEY_SHIFT_RIGHT, true
	case vaxis.KeyLeftControl:
		return C.GHOSTTY_KEY_CONTROL_LEFT, true
	case vaxis.KeyRightControl:
		return C.GHOSTTY_KEY_CONTROL_RIGHT, true
	case vaxis.KeyLeftAlt:
		return C.GHOSTTY_KEY_ALT_LEFT, true
	case vaxis.KeyRightAlt:
		return C.GHOSTTY_KEY_ALT_RIGHT, true
	case vaxis.KeyLeftSuper, vaxis.KeyLeftMeta:
		return C.GHOSTTY_KEY_META_LEFT, true
	case vaxis.KeyRightSuper, vaxis.KeyRightMeta:
		return C.GHOSTTY_KEY_META_RIGHT, true
	case vaxis.KeyCopy:
		return C.GHOSTTY_KEY_COPY, true
	case vaxis.KeyMediaPlayPause:
		return C.GHOSTTY_KEY_MEDIA_PLAY_PAUSE, true
	case vaxis.KeyMediaStop:
		return C.GHOSTTY_KEY_MEDIA_STOP, true
	case vaxis.KeyMediaNext:
		return C.GHOSTTY_KEY_MEDIA_TRACK_NEXT, true
	case vaxis.KeyMediaPrev:
		return C.GHOSTTY_KEY_MEDIA_TRACK_PREVIOUS, true
	case vaxis.KeyMediaVolDown:
		return C.GHOSTTY_KEY_AUDIO_VOLUME_DOWN, true
	case vaxis.KeyMediaVolUp:
		return C.GHOSTTY_KEY_AUDIO_VOLUME_UP, true
	case vaxis.KeyMediaMute:
		return C.GHOSTTY_KEY_AUDIO_VOLUME_MUTE, true
	case vaxis.KeyKeyPad0:
		return C.GHOSTTY_KEY_NUMPAD_0, true
	case vaxis.KeyKeyPad1:
		return C.GHOSTTY_KEY_NUMPAD_1, true
	case vaxis.KeyKeyPad2:
		return C.GHOSTTY_KEY_NUMPAD_2, true
	case vaxis.KeyKeyPad3:
		return C.GHOSTTY_KEY_NUMPAD_3, true
	case vaxis.KeyKeyPad4:
		return C.GHOSTTY_KEY_NUMPAD_4, true
	case vaxis.KeyKeyPad5:
		return C.GHOSTTY_KEY_NUMPAD_5, true
	case vaxis.KeyKeyPad6:
		return C.GHOSTTY_KEY_NUMPAD_6, true
	case vaxis.KeyKeyPad7:
		return C.GHOSTTY_KEY_NUMPAD_7, true
	case vaxis.KeyKeyPad8:
		return C.GHOSTTY_KEY_NUMPAD_8, true
	case vaxis.KeyKeyPad9:
		return C.GHOSTTY_KEY_NUMPAD_9, true
	case vaxis.KeyKeyPadDecimal:
		return C.GHOSTTY_KEY_NUMPAD_DECIMAL, true
	case vaxis.KeyKeyPadDivide:
		return C.GHOSTTY_KEY_NUMPAD_DIVIDE, true
	case vaxis.KeyKeyPadMultiply:
		return C.GHOSTTY_KEY_NUMPAD_MULTIPLY, true
	case vaxis.KeyKeyPadSubtract:
		return C.GHOSTTY_KEY_NUMPAD_SUBTRACT, true
	case vaxis.KeyKeyPadAdd:
		return C.GHOSTTY_KEY_NUMPAD_ADD, true
	case vaxis.KeyKeyPadEnter:
		return C.GHOSTTY_KEY_NUMPAD_ENTER, true
	case vaxis.KeyKeyPadEqual:
		return C.GHOSTTY_KEY_NUMPAD_EQUAL, true
	case vaxis.KeyKeyPadSeparator:
		return C.GHOSTTY_KEY_NUMPAD_SEPARATOR, true
	case vaxis.KeyKeyPadLeft:
		return C.GHOSTTY_KEY_NUMPAD_LEFT, true
	case vaxis.KeyKeyPadRight:
		return C.GHOSTTY_KEY_NUMPAD_RIGHT, true
	case vaxis.KeyKeyPadUp:
		return C.GHOSTTY_KEY_NUMPAD_UP, true
	case vaxis.KeyKeyPadDown:
		return C.GHOSTTY_KEY_NUMPAD_DOWN, true
	case vaxis.KeyKeyPadPageUp:
		return C.GHOSTTY_KEY_NUMPAD_PAGE_UP, true
	case vaxis.KeyKeyPadPageDown:
		return C.GHOSTTY_KEY_NUMPAD_PAGE_DOWN, true
	case vaxis.KeyKeyPadHome:
		return C.GHOSTTY_KEY_NUMPAD_HOME, true
	case vaxis.KeyKeyPadEnd:
		return C.GHOSTTY_KEY_NUMPAD_END, true
	case vaxis.KeyKeyPadInsert:
		return C.GHOSTTY_KEY_NUMPAD_INSERT, true
	case vaxis.KeyKeyPadDelete:
		return C.GHOSTTY_KEY_NUMPAD_DELETE, true
	case vaxis.KeyKeyPadBegin:
		return C.GHOSTTY_KEY_NUMPAD_BEGIN, true
	case vaxis.KeyF01:
		return C.GHOSTTY_KEY_F1, true
	case vaxis.KeyF02:
		return C.GHOSTTY_KEY_F2, true
	case vaxis.KeyF03:
		return C.GHOSTTY_KEY_F3, true
	case vaxis.KeyF04:
		return C.GHOSTTY_KEY_F4, true
	case vaxis.KeyF05:
		return C.GHOSTTY_KEY_F5, true
	case vaxis.KeyF06:
		return C.GHOSTTY_KEY_F6, true
	case vaxis.KeyF07:
		return C.GHOSTTY_KEY_F7, true
	case vaxis.KeyF08:
		return C.GHOSTTY_KEY_F8, true
	case vaxis.KeyF09:
		return C.GHOSTTY_KEY_F9, true
	case vaxis.KeyF10:
		return C.GHOSTTY_KEY_F10, true
	case vaxis.KeyF11:
		return C.GHOSTTY_KEY_F11, true
	case vaxis.KeyF12:
		return C.GHOSTTY_KEY_F12, true
	case vaxis.KeyF13:
		return C.GHOSTTY_KEY_F13, true
	case vaxis.KeyF14:
		return C.GHOSTTY_KEY_F14, true
	case vaxis.KeyF15:
		return C.GHOSTTY_KEY_F15, true
	case vaxis.KeyF16:
		return C.GHOSTTY_KEY_F16, true
	case vaxis.KeyF17:
		return C.GHOSTTY_KEY_F17, true
	case vaxis.KeyF18:
		return C.GHOSTTY_KEY_F18, true
	case vaxis.KeyF19:
		return C.GHOSTTY_KEY_F19, true
	case vaxis.KeyF20:
		return C.GHOSTTY_KEY_F20, true
	case vaxis.KeyF21:
		return C.GHOSTTY_KEY_F21, true
	case vaxis.KeyF22:
		return C.GHOSTTY_KEY_F22, true
	case vaxis.KeyF23:
		return C.GHOSTTY_KEY_F23, true
	case vaxis.KeyF24:
		return C.GHOSTTY_KEY_F24, true
	case vaxis.KeyF25:
		return C.GHOSTTY_KEY_F25, true
	}

	layoutKey := key.BaseLayoutCode
	if layoutKey == 0 || layoutKey >= unicode.MaxRune {
		layoutKey = key.Keycode
	}
	if layoutKey == 0 || layoutKey >= unicode.MaxRune {
		return 0, false
	}

	switch unicode.ToLower(layoutKey) {
	case '`':
		return C.GHOSTTY_KEY_BACKQUOTE, true
	case '\\':
		return C.GHOSTTY_KEY_BACKSLASH, true
	case '[':
		return C.GHOSTTY_KEY_BRACKET_LEFT, true
	case ']':
		return C.GHOSTTY_KEY_BRACKET_RIGHT, true
	case ',':
		return C.GHOSTTY_KEY_COMMA, true
	case '0':
		return C.GHOSTTY_KEY_DIGIT_0, true
	case '1':
		return C.GHOSTTY_KEY_DIGIT_1, true
	case '2':
		return C.GHOSTTY_KEY_DIGIT_2, true
	case '3':
		return C.GHOSTTY_KEY_DIGIT_3, true
	case '4':
		return C.GHOSTTY_KEY_DIGIT_4, true
	case '5':
		return C.GHOSTTY_KEY_DIGIT_5, true
	case '6':
		return C.GHOSTTY_KEY_DIGIT_6, true
	case '7':
		return C.GHOSTTY_KEY_DIGIT_7, true
	case '8':
		return C.GHOSTTY_KEY_DIGIT_8, true
	case '9':
		return C.GHOSTTY_KEY_DIGIT_9, true
	case '=':
		return C.GHOSTTY_KEY_EQUAL, true
	case 'a':
		return C.GHOSTTY_KEY_A, true
	case 'b':
		return C.GHOSTTY_KEY_B, true
	case 'c':
		return C.GHOSTTY_KEY_C, true
	case 'd':
		return C.GHOSTTY_KEY_D, true
	case 'e':
		return C.GHOSTTY_KEY_E, true
	case 'f':
		return C.GHOSTTY_KEY_F, true
	case 'g':
		return C.GHOSTTY_KEY_G, true
	case 'h':
		return C.GHOSTTY_KEY_H, true
	case 'i':
		return C.GHOSTTY_KEY_I, true
	case 'j':
		return C.GHOSTTY_KEY_J, true
	case 'k':
		return C.GHOSTTY_KEY_K, true
	case 'l':
		return C.GHOSTTY_KEY_L, true
	case 'm':
		return C.GHOSTTY_KEY_M, true
	case 'n':
		return C.GHOSTTY_KEY_N, true
	case 'o':
		return C.GHOSTTY_KEY_O, true
	case 'p':
		return C.GHOSTTY_KEY_P, true
	case 'q':
		return C.GHOSTTY_KEY_Q, true
	case 'r':
		return C.GHOSTTY_KEY_R, true
	case 's':
		return C.GHOSTTY_KEY_S, true
	case 't':
		return C.GHOSTTY_KEY_T, true
	case 'u':
		return C.GHOSTTY_KEY_U, true
	case 'v':
		return C.GHOSTTY_KEY_V, true
	case 'w':
		return C.GHOSTTY_KEY_W, true
	case 'x':
		return C.GHOSTTY_KEY_X, true
	case 'y':
		return C.GHOSTTY_KEY_Y, true
	case 'z':
		return C.GHOSTTY_KEY_Z, true
	case '-':
		return C.GHOSTTY_KEY_MINUS, true
	case '.':
		return C.GHOSTTY_KEY_PERIOD, true
	case '\'':
		return C.GHOSTTY_KEY_QUOTE, true
	case ';':
		return C.GHOSTTY_KEY_SEMICOLON, true
	case '/':
		return C.GHOSTTY_KEY_SLASH, true
	default:
		return 0, false
	}
}

func (t *ghosttyTUITerminal) Start(cmd *exec.Cmd) error {
	if cmd == nil {
		return fmt.Errorf("no command to run")
	}
	if t.term == nil {
		return fmt.Errorf("ghostty terminal unavailable")
	}

	env := os.Environ()
	if cmd.Env != nil {
		env = cmd.Env
	}
	env = append(env, "TERM="+tuiEmbeddedTermEnv)
	cmd.Env = env

	winsize := pty.Winsize{
		Cols: uint16(t.cols),
		Rows: uint16(t.rows),
	}
	ptyFile, err := pty.StartWithAttrs(
		cmd,
		&winsize,
		&syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    1,
		},
	)
	if err != nil {
		return err
	}
	if err := unix.SetNonblock(int(ptyFile.Fd()), true); err != nil {
		_ = ptyFile.Close()
		return fmt.Errorf("set terminal pty nonblocking: %w", err)
	}

	// Wire the real POLLOUT waiter so an EAGAIN on the PTY master parks on
	// poll() instead of busy-spinning (work item 2.1 busy-spin fix, ADR 63).
	// Production previously passed waitWritable=nil, which spun hot on a
	// would-block write.
	ptyWriter := newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, func(err error) {
		if t.postEvent != nil {
			t.postEvent(tuiTerminalErrorEvent{
				TargetKey: t.targetKey,
				Err:       fmt.Errorf("write embedded terminal pty: %w", err),
			})
		}
	})

	waitDone := make(chan struct{})
	t.mu.Lock()
	t.cmd = cmd
	t.childPID = cmd.Process.Pid
	t.pty = ptyFile
	t.ptyWriter = ptyWriter
	t.waitDone = waitDone
	t.readPumpDone = make(chan struct{})
	t.mu.Unlock()

	// The read pump only reads the fd and forwards bytes to the actor, which
	// owns ingest; keeping it a dumb reader (not an emulator mutator) preserves
	// the single-owner invariant while blocking reads stay simple.
	go t.readPump()
	// Single reaper: cmd.Wait may only be called once, so it runs solely
	// inside the waitOnce-guarded wait(); waitDone lets teardown observe the
	// reap without racing a second Wait.
	go func() {
		_ = t.wait()
		close(waitDone)
	}()
	return nil
}

// Resize enqueues a resize command and waits for the actor to apply it, so the
// emulator/PTY are resized before the call returns (ordering the daemon and the
// render loop rely on).
func (t *ghosttyTUITerminal) Resize(width, height int) {
	t.sendSync(cmdResize{Cols: width, Rows: height})
}

func (t *ghosttyTUITerminal) applyResize(width, height int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.resizeLocked(width, height)
	t.invalidateLocked()
}

// readPump does blocking reads on the PTY master and forwards raw bytes to the
// actor (which owns ingest). On any read error it reports once and exits; the
// quit channel unblocks it if the actor tore the terminal down first.
func (t *ghosttyTUITerminal) readPump() {
	t.mu.Lock()
	done := t.readPumpDone
	t.mu.Unlock()
	if done != nil {
		defer close(done)
	}
	buffer := make([]byte, 32*1024)
	for {
		n, err := t.readPTY(buffer)
		if n > 0 {
			data := append([]byte(nil), buffer[:n]...)
			if t.handoffInProgress() {
				t.readCh <- data
			} else {
				select {
				case <-t.quit:
					return
				default:
				}
				select {
				case t.readCh <- data:
				case <-t.quit:
					if t.handoffInProgress() {
						t.readCh <- data
					}
					return
				}
			}
		}
		if err != nil {
			if isGhosttyPTYWouldBlockError(err) {
				select {
				case <-t.quit:
					return
				default:
				}
				_ = waitGhosttyPTYReadable(int(t.currentPTYFD()), 50*time.Millisecond)
				continue
			}
			if t.handoffInProgress() {
				return
			}
			select {
			case <-t.quit:
				return
			default:
			}
			select {
			case t.readErrCh <- err:
			case <-t.quit:
			}
			return
		}
	}
}

func (t *ghosttyTUITerminal) currentPTYFD() uintptr {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pty == nil {
		return ^uintptr(0)
	}
	return t.pty.Fd()
}

func waitGhosttyPTYReadable(fd int, timeout time.Duration) error {
	if fd < 0 {
		return os.ErrClosed
	}
	timeoutMS := int(timeout / time.Millisecond)
	for {
		pollFDs := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		_, err := unix.Poll(pollFDs, timeoutMS)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

func (t *ghosttyTUITerminal) handoffInProgress() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == runtimeStateQuiescing || t.state == runtimeStateQuiesced
}

// isGhosttyPTYClosedReadError reports whether a read error means the PTY was
// closed under us (no exit status to report), matching the pre-actor readLoop's
// os.ErrClosed branch.
func isGhosttyPTYClosedReadError(err error) bool {
	return errors.Is(err, os.ErrClosed)
}

func (t *ghosttyTUITerminal) readPTY(buffer []byte) (int, error) {
	t.mu.Lock()
	ptyFile := t.pty
	t.mu.Unlock()
	if ptyFile == nil {
		return 0, io.EOF
	}
	return ptyFile.Read(buffer)
}

func (t *ghosttyTUITerminal) ingestPTY(data []byte) {
	if t.clipboardScanner != nil {
		t.clipboardScanner.Write(data)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || len(data) == 0 {
		return
	}
	t.replay = append(t.replay, data...)
	if len(t.replay) > 8*1024 {
		t.replay = append([]byte(nil), t.replay[len(t.replay)-8*1024:]...)
	}

	withGhosttyStderrSuppressed(func() struct{} {
		C.ghostty_bridge_terminal_write(
			t.term,
			(*C.uint8_t)(unsafe.Pointer(&data[0])),
			C.size_t(len(data)),
		)
		t.drainResponsesLockedRaw()
		return struct{}{}
	})
	// Advance the output generation so cmdSnapshot/cmdRead consumers can detect
	// new child output and discard stale pulls (ADR 63).
	t.generation++
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) drainResponsesLockedRaw() {
	if t.ptyWriter == nil || t.term == nil {
		return
	}

	buffer := make([]byte, 4096)
	for C.ghostty_bridge_terminal_has_response(t.term) {
		n := int(C.ghostty_bridge_terminal_read_response(
			t.term,
			(*C.uint8_t)(unsafe.Pointer(&buffer[0])),
			C.size_t(len(buffer)),
		))
		if n <= 0 {
			return
		}
		t.writePTYBytesLocked(buffer[:n])
	}
}

func (t *ghosttyTUITerminal) readPendingResponses() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.term == nil {
		return ""
	}

	return withGhosttyStderrSuppressed(func() string {
		return t.readPendingResponsesLockedRaw()
	})
}

func (t *ghosttyTUITerminal) readPendingResponsesLockedRaw() string {
	buffer := make([]byte, 4096)
	var output bytes.Buffer
	for C.ghostty_bridge_terminal_has_response(t.term) {
		n := int(C.ghostty_bridge_terminal_read_response(
			t.term,
			(*C.uint8_t)(unsafe.Pointer(&buffer[0])),
			C.size_t(len(buffer)),
		))
		if n <= 0 {
			break
		}
		output.Write(buffer[:n])
	}
	return output.String()
}

// serveRead answers a cmdRead query on the actor goroutine. It renders the
// viewport (and, for ReadRecent, recent scrollback) on demand — reusing the same
// bridge queries Draw uses — so terminal content is readable with no TUI
// attached. This is the daemon (Track 3) / agent-detection (Track 5) seam.
func (t *ghosttyTUITerminal) serveRead(source ReadSource, format ReadFormat) ReadResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil {
		return ReadResult{Err: errTerminalClosed}
	}
	return withGhosttyStderrSuppressed(func() ReadResult {
		var text string
		switch source {
		case ReadRecent:
			text = t.recentTextLockedRaw(format)
		default:
			text = t.visibleTextLockedRaw(format)
		}
		return ReadResult{Text: text, Generation: t.generation}
	})
}

// serveSnapshot answers a cmdSnapshot query: the whole cell grid + cursor +
// output generation, assembled under t.mu so it is internally consistent even
// while child output streams.
func (t *ghosttyTUITerminal) serveSnapshot() SnapshotResult {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil {
		return SnapshotResult{Err: errTerminalClosed}
	}
	return withGhosttyStderrSuppressed(func() SnapshotResult {
		return t.snapshotLockedRaw()
	})
}

func (t *ghosttyTUITerminal) snapshotLockedRaw() SnapshotResult {
	cols, rows := t.cols, t.rows
	if cols <= 0 || rows <= 0 {
		return SnapshotResult{Err: errSnapshotFailed}
	}

	C.ghostty_bridge_render_state_update(t.term)

	viewport := make([]C.GhosttyResolvedCell, cols*rows)
	count := int(C.ghostty_bridge_render_state_get_viewport(
		t.term,
		(*C.GhosttyResolvedCell)(unsafe.Pointer(&viewport[0])),
		C.size_t(len(viewport)),
	))
	if count < 0 {
		return SnapshotResult{Err: errSnapshotFailed}
	}

	snap := TerminalSnapshot{
		Cols:       cols,
		Rows:       rows,
		Generation: t.generation,
		Cells:      make([]SnapshotCell, 0, cols*rows),
	}
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			cell := viewport[row*cols+col]
			snap.Cells = append(snap.Cells, t.snapshotCellLocked(row, col, cell))
		}
	}
	snap.CursorVisible = bool(C.ghostty_bridge_render_state_get_cursor_visible(t.term))
	snap.CursorX = int(C.ghostty_bridge_render_state_get_cursor_x(t.term))
	snap.CursorY = int(C.ghostty_bridge_render_state_get_cursor_y(t.term))
	snap.CapturesMouse = bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term))
	return SnapshotResult{Snapshot: snap}
}

func (t *ghosttyTUITerminal) snapshotCellLocked(row, col int, cell C.GhosttyResolvedCell) SnapshotCell {
	grapheme := t.graphemeLocked(row, col, cell)
	if grapheme == "" {
		grapheme = " "
	}
	snapshot := SnapshotCell{
		Grapheme:      grapheme,
		Width:         int(cell.width),
		FG:            ghosttyRGB(uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)),
		BG:            ghosttyRGB(uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)),
		FGDefault:     cell.color_flags&C.GHOSTTY_CELL_FG_DEFAULT != 0,
		BGDefault:     cell.color_flags&C.GHOSTTY_CELL_BG_DEFAULT != 0,
		Bold:          cell.flags&C.GHOSTTY_CELL_BOLD != 0,
		Faint:         cell.flags&C.GHOSTTY_CELL_FAINT != 0,
		Italic:        cell.flags&C.GHOSTTY_CELL_ITALIC != 0,
		Underline:     cell.flags&C.GHOSTTY_CELL_UNDERLINE != 0,
		Strikethrough: cell.flags&C.GHOSTTY_CELL_STRIKETHROUGH != 0,
		Inverse:       cell.flags&C.GHOSTTY_CELL_INVERSE != 0,
		Invisible:     cell.flags&C.GHOSTTY_CELL_INVISIBLE != 0,
		Blink:         cell.flags&C.GHOSTTY_CELL_BLINK != 0,
	}
	if cell.hyperlink_id != 0 {
		if target, ok := t.hyperlinkAtLocked(row, col); ok {
			snapshot.Hyperlink = target
		}
	}
	return snapshot
}

func (t *ghosttyTUITerminal) visibleTextLockedRaw(format ReadFormat) string {
	cols, rows := t.cols, t.rows
	if cols <= 0 || rows <= 0 {
		return ""
	}

	C.ghostty_bridge_render_state_update(t.term)

	viewport := make([]C.GhosttyResolvedCell, cols*rows)
	count := int(C.ghostty_bridge_render_state_get_viewport(
		t.term,
		(*C.GhosttyResolvedCell)(unsafe.Pointer(&viewport[0])),
		C.size_t(len(viewport)),
	))
	if count < 0 {
		return ""
	}

	lines := make([]string, 0, rows)
	for row := 0; row < rows; row++ {
		cells := viewport[row*cols : row*cols+cols]
		if format == ReadANSI {
			lines = append(lines, t.viewportRowANSILocked(row, cells))
		} else {
			lines = append(lines, t.viewportRowTextLocked(row, cells))
		}
	}

	if format == ReadANSI {
		return strings.Join(lines, "\n")
	}
	return joinTrimmedTerminalLines(lines)
}

func (t *ghosttyTUITerminal) recentTextLockedRaw(format ReadFormat) string {
	visible := t.visibleTextLockedRaw(format)

	cols := t.cols
	length := int(C.ghostty_bridge_terminal_get_scrollback_length(t.term))
	if length <= 0 || cols <= 0 {
		return visible
	}

	const maxRecentRows = 2000
	start := 0
	if length > maxRecentRows {
		start = length - maxRecentRows
	}

	cellBuf := make([]C.GhosttyResolvedCell, cols)
	lines := make([]string, 0, length-start)
	for offset := start; offset < length; offset++ {
		n := int(C.ghostty_bridge_terminal_get_scrollback_line(
			t.term,
			C.int(offset),
			(*C.GhosttyResolvedCell)(unsafe.Pointer(&cellBuf[0])),
			C.size_t(len(cellBuf)),
		))
		if n < 0 {
			continue
		}
		if n > cols {
			n = cols
		}
		lines = append(lines, t.scrollbackRowTextLocked(offset, cellBuf[:n]))
	}

	recent := joinTrimmedTerminalLines(lines)
	switch {
	case recent == "":
		return visible
	case visible == "":
		return recent
	default:
		return recent + "\n" + visible
	}
}

func (t *ghosttyTUITerminal) viewportRowTextLocked(row int, cells []C.GhosttyResolvedCell) string {
	var b strings.Builder
	for col := 0; col < len(cells); col++ {
		cell := cells[col]
		if cell.width == 0 {
			// Zero-width cell: a wide-glyph spacer or an empty continuation;
			// the preceding wide grapheme already carries the full width.
			continue
		}
		grapheme := t.graphemeLocked(row, col, cell)
		if grapheme == "" {
			grapheme = " "
		}
		b.WriteString(grapheme)
	}
	return b.String()
}

func (t *ghosttyTUITerminal) viewportRowANSILocked(row int, cells []C.GhosttyResolvedCell) string {
	var b strings.Builder
	current := ""
	for col := 0; col < len(cells); col++ {
		cell := cells[col]
		if cell.width == 0 {
			continue
		}
		sgr := ghosttyANSISGRForCell(cell)
		if sgr != current {
			b.WriteString("\x1b[0m")
			b.WriteString(sgr)
			current = sgr
		}
		grapheme := t.graphemeLocked(row, col, cell)
		if grapheme == "" {
			grapheme = " "
		}
		b.WriteString(grapheme)
	}
	if current != "" {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

func (t *ghosttyTUITerminal) scrollbackRowTextLocked(offset int, cells []C.GhosttyResolvedCell) string {
	var b strings.Builder
	for col := 0; col < len(cells); col++ {
		cell := cells[col]
		if cell.width == 0 {
			continue
		}
		grapheme := t.scrollbackGraphemeLocked(offset, col, cell)
		if grapheme == "" {
			grapheme = " "
		}
		b.WriteString(grapheme)
	}
	return b.String()
}

func (t *ghosttyTUITerminal) scrollbackGraphemeLocked(offset int, col int, cell C.GhosttyResolvedCell) string {
	if cell.grapheme_len == 0 {
		if cell.codepoint == 0 {
			return " "
		}
		return string(rune(cell.codepoint))
	}

	codepoints := make([]C.uint32_t, int(cell.grapheme_len)+1)
	count := int(C.ghostty_bridge_terminal_get_scrollback_grapheme(
		t.term,
		C.int(offset),
		C.int(col),
		(*C.uint32_t)(unsafe.Pointer(&codepoints[0])),
		C.size_t(len(codepoints)),
	))
	if count <= 0 {
		if cell.codepoint == 0 {
			return " "
		}
		return string(rune(cell.codepoint))
	}

	runes := make([]rune, 0, count)
	for index := 0; index < count; index++ {
		runes = append(runes, rune(codepoints[index]))
	}
	return string(runes)
}

func ghosttyANSISGRForCell(cell C.GhosttyResolvedCell) string {
	codes := make([]string, 0, 8)
	if cell.flags&C.GHOSTTY_CELL_BOLD != 0 {
		codes = append(codes, "1")
	}
	if cell.flags&C.GHOSTTY_CELL_FAINT != 0 {
		codes = append(codes, "2")
	}
	if cell.flags&C.GHOSTTY_CELL_ITALIC != 0 {
		codes = append(codes, "3")
	}
	if cell.flags&C.GHOSTTY_CELL_UNDERLINE != 0 {
		codes = append(codes, "4")
	}
	if cell.flags&C.GHOSTTY_CELL_BLINK != 0 {
		codes = append(codes, "5")
	}
	if cell.flags&C.GHOSTTY_CELL_INVERSE != 0 {
		codes = append(codes, "7")
	}
	if cell.flags&C.GHOSTTY_CELL_INVISIBLE != 0 {
		codes = append(codes, "8")
	}
	if cell.flags&C.GHOSTTY_CELL_STRIKETHROUGH != 0 {
		codes = append(codes, "9")
	}
	if cell.color_flags&C.GHOSTTY_CELL_FG_DEFAULT == 0 {
		codes = append(codes, fmt.Sprintf("38;2;%d;%d;%d", uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)))
	}
	if cell.color_flags&C.GHOSTTY_CELL_BG_DEFAULT == 0 {
		codes = append(codes, fmt.Sprintf("48;2;%d;%d;%d", uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)))
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// joinTrimmedTerminalLines joins rendered rows with newlines, trimming trailing
// spaces from each row and dropping trailing blank rows, so plain-text reads are
// stable enough to assert against.
func joinTrimmedTerminalLines(lines []string) string {
	trimmed := make([]string, len(lines))
	for i, line := range lines {
		trimmed[i] = strings.TrimRight(line, " ")
	}
	end := len(trimmed)
	for end > 0 && trimmed[end-1] == "" {
		end--
	}
	return strings.Join(trimmed[:end], "\n")
}

// Update enqueues a TUI input event and waits for the actor to apply it. The
// wait preserves the pre-actor synchronous contract: e.g. a color-theme update
// must have queued its VT response by the time Update returns.
func (t *ghosttyTUITerminal) Update(event vaxis.Event) {
	t.sendSync(cmdUpdate{Event: event})
}

// applyInput writes raw daemon-supplied bytes to the child (cmdInput). Like a
// keystroke it snaps the viewport to the bottom first.
func (t *ghosttyTUITerminal) applyInput(data []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || t.ptyWriter == nil || len(data) == 0 {
		return
	}
	t.scrollViewportBottomLocked()
	t.writePTYBytesLocked(data)
}

func (t *ghosttyTUITerminal) applyUpdate(event vaxis.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || t.pty == nil {
		return
	}

	switch event := event.(type) {
	case vaxis.Key:
		t.scrollViewportBottomLocked()
		t.writePTYLocked(encodeTUITerminalKeyWithGhostty(
			event,
			t.keyEncoder,
			t.term,
			t.getModeLocked(ghosttyModeApplicationKeypad, false),
			t.getModeLocked(ghosttyModeCursorKeys, false),
		))
	case vaxis.PasteStartEvent:
		if t.getModeLocked(ghosttyModeBracketedPaste, false) {
			t.scrollViewportBottomLocked()
			t.writePTYLocked("\x1B[200~")
		}
	case vaxis.PasteEndEvent:
		if t.getModeLocked(ghosttyModeBracketedPaste, false) {
			t.scrollViewportBottomLocked()
			t.writePTYLocked("\x1B[201~")
		}
	case vaxis.Mouse:
		t.handleMouseLocked(event)
	case vaxis.ColorThemeUpdate:
		t.setColorThemeModeLocked(event.Mode)
		if t.getModeLocked(ghosttyModeColorScheme, false) {
			t.reportColorThemeModeLocked()
		}
	}
}

func (t *ghosttyTUITerminal) handleMouseLocked(event vaxis.Mouse) {
	if event.Button == vaxis.MouseWheelUp || event.Button == vaxis.MouseWheelDown {
		if t.handleWheelLocked(event.Button) {
			return
		}
	}

	hasMouseTracking := withGhosttyStderrSuppressed(func() bool {
		return bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term))
	})
	if !hasMouseTracking {
		return
	}

	encoded := encodeTUITerminalMouseWithGhostty(
		event,
		t.mouseEncoder,
		t.term,
		t.cols,
		t.rows,
		t.mouseButtonsDown,
		t.getModeLocked(ghosttyModeMouseSGR, false),
		t.getModeLocked(ghosttyModeMouseDrag, false) || t.getModeLocked(ghosttyModeMouseMotion, false),
		t.getModeLocked(ghosttyModeMouseMotion, false),
	)
	t.updateMouseButtonsDownLocked(event)
	t.writePTYLocked(encoded)
}

func (t *ghosttyTUITerminal) updateMouseButtonsDownLocked(event vaxis.Mouse) {
	if !ghosttyTrackedMouseButton(event.Button) {
		if event.EventType == vaxis.EventRelease && event.Button == vaxis.MouseNoButton {
			t.mouseButtonsDown = 0
		}
		return
	}

	switch event.EventType {
	case vaxis.EventPress:
		t.mouseButtonsDown++
	case vaxis.EventRelease:
		if t.mouseButtonsDown > 0 {
			t.mouseButtonsDown--
		}
	}
}

func (t *ghosttyTUITerminal) handleWheelLocked(button vaxis.MouseButton) bool {
	if t.term == nil {
		return false
	}
	if withGhosttyStderrSuppressed(func() bool {
		return bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term))
	}) {
		return false
	}
	if withGhosttyStderrSuppressed(func() bool {
		return bool(C.ghostty_bridge_terminal_is_alternate_screen(t.term))
	}) {
		if t.getModeLocked(ghosttyModeAltScroll, false) {
			switch button {
			case vaxis.MouseWheelUp:
				t.writePTYLocked("\x1bOA\x1bOA\x1bOA")
				return true
			case vaxis.MouseWheelDown:
				t.writePTYLocked("\x1bOB\x1bOB\x1bOB")
				return true
			}
		}
		return false
	}

	scrollbar, ok := t.scrollbarLocked()
	if !ok || scrollbar.total <= scrollbar.length {
		return false
	}

	const scrollStep = 3
	delta := 0
	switch button {
	case vaxis.MouseWheelUp:
		if scrollbar.offset <= 0 {
			return false
		}
		delta = -scrollStep
	case vaxis.MouseWheelDown:
		if scrollbar.offset+scrollbar.length >= scrollbar.total {
			return false
		}
		delta = scrollStep
	default:
		return false
	}

	t.scrollViewportDeltaLocked(delta)
	t.invalidateLocked()
	return true
}

func (t *ghosttyTUITerminal) writePTYLocked(value string) {
	if value == "" {
		return
	}
	t.writePTYBytesLocked([]byte(value))
}

func (t *ghosttyTUITerminal) writePTYBytesLocked(data []byte) {
	if len(data) == 0 || t.ptyWriter == nil {
		return
	}
	t.ptyWriter.Enqueue(data)
}

func (t *ghosttyTUITerminal) Draw(win vaxis.Window) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil {
		return
	}

	width, height := win.Size()
	if width <= 0 || height <= 0 {
		return
	}
	if !withGhosttyStderrSuppressed(func() bool {
		t.resizeLocked(width, height)

		C.ghostty_bridge_render_state_update(t.term)

		viewport := make([]C.GhosttyResolvedCell, width*height)
		if len(viewport) > 0 {
			count := int(C.ghostty_bridge_render_state_get_viewport(
				t.term,
				(*C.GhosttyResolvedCell)(unsafe.Pointer(&viewport[0])),
				C.size_t(len(viewport)),
			))
			if count < 0 {
				return false
			}
		}

		lineTexts := make([]string, 0, height)
		for visibleRow := 0; visibleRow < height; visibleRow++ {
			offset := visibleRow * width
			if offset+width > len(viewport) {
				break
			}
			lineTexts = append(lineTexts, t.drawCellsLocked(win, visibleRow, viewport[offset:offset+width]))
		}

		t.snapshot = strings.Join(lineTexts, "\n")

		if t.focused && t.viewportAtBottomLockedRaw() && bool(C.ghostty_bridge_render_state_get_cursor_visible(t.term)) {
			cursorRow := int(C.ghostty_bridge_render_state_get_cursor_y(t.term))
			cursorCol := int(C.ghostty_bridge_render_state_get_cursor_x(t.term))
			if cursorRow >= 0 && cursorRow < height && cursorCol >= 0 && cursorCol < width {
				win.ShowCursor(cursorCol, cursorRow, vaxis.CursorBlock)
			}
		}

		C.ghostty_bridge_render_state_mark_clean(t.term)
		t.redrawPending = false
		return true
	}) {
		return
	}
}

func (t *ghosttyTUITerminal) resizeLocked(width, height int) {
	if t.closed || t.term == nil || width <= 0 || height <= 0 {
		return
	}
	if width == t.cols && height == t.rows {
		return
	}

	oldWidth := t.cols
	t.cols = width
	t.rows = height
	C.ghostty_bridge_terminal_resize(t.term, C.int(width), C.int(height))
	if t.mouseEncoder != nil {
		t.mouseEncoder.Reset()
	}
	if t.pty != nil {
		_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
		if t.shouldRequestPrimaryScreenRedrawLocked(oldWidth, width) {
			// Bash/readline prompt redraws can leave stale wrapped fragments after
			// width growth, so ask shell-like primary-screen sessions to repaint.
			t.writePTYLocked("\x0c")
		}
	}
}

func (t *ghosttyTUITerminal) shouldRequestPrimaryScreenRedrawLocked(previousWidth, width int) bool {
	if t.term == nil || t.pty == nil || width <= previousWidth {
		return false
	}
	if bool(C.ghostty_bridge_terminal_is_alternate_screen(t.term)) {
		return false
	}
	if bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term)) {
		return false
	}
	return t.viewportAtBottomLockedRaw()
}

func (t *ghosttyTUITerminal) drawCellsLocked(win vaxis.Window, row int, cells []C.GhosttyResolvedCell) string {
	var line strings.Builder
	for col := 0; col < t.cols; col++ {
		cell := cells[col]
		style := ghosttyCellStyle(cell)
		if cell.hyperlink_id != 0 {
			if target, ok := t.hyperlinkAtLocked(row, col); ok {
				style.Hyperlink = target
				if style.UnderlineStyle == 0 {
					style.UnderlineStyle = vaxis.UnderlineSingle
				}
			}
		}

		grapheme := t.graphemeLocked(row, col, cell)
		if grapheme == "" {
			grapheme = " "
		}
		if cell.width == 0 {
			line.WriteRune(' ')
			continue
		}

		win.SetCell(col, row, vaxis.Cell{
			Character: vaxis.Character{
				Grapheme: grapheme,
				Width:    int(cell.width),
			},
			Style: style,
		})

		r, _ := utf8.DecodeRuneInString(grapheme)
		if r == utf8.RuneError && len(grapheme) > 1 {
			line.WriteString(grapheme)
			if int(cell.width) > 1 {
				line.WriteString(strings.Repeat(" ", int(cell.width)-1))
			}
			continue
		}
		line.WriteString(grapheme)
		if int(cell.width) > 1 {
			line.WriteString(strings.Repeat(" ", int(cell.width)-1))
		}
	}

	return line.String()
}

func ghosttyCellStyle(cell C.GhosttyResolvedCell) vaxis.Style {
	style := ghosttyStyleForColors(
		ghosttyRGB(uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)),
		ghosttyRGB(uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)),
		cell.color_flags&C.GHOSTTY_CELL_FG_DEFAULT == 0,
		cell.color_flags&C.GHOSTTY_CELL_BG_DEFAULT == 0,
	)
	if cell.flags&C.GHOSTTY_CELL_BOLD != 0 {
		style.Attribute |= vaxis.AttrBold
	}
	if cell.flags&C.GHOSTTY_CELL_FAINT != 0 {
		style.Attribute |= vaxis.AttrDim
	}
	if cell.flags&C.GHOSTTY_CELL_ITALIC != 0 {
		style.Attribute |= vaxis.AttrItalic
	}
	if cell.flags&C.GHOSTTY_CELL_UNDERLINE != 0 {
		style.UnderlineStyle = vaxis.UnderlineSingle
	}
	if cell.flags&C.GHOSTTY_CELL_STRIKETHROUGH != 0 {
		style.Attribute |= vaxis.AttrStrikethrough
	}
	if cell.flags&C.GHOSTTY_CELL_INVERSE != 0 {
		style.Attribute |= vaxis.AttrReverse
	}
	if cell.flags&C.GHOSTTY_CELL_INVISIBLE != 0 {
		style.Attribute |= vaxis.AttrInvisible
	}
	if cell.flags&C.GHOSTTY_CELL_BLINK != 0 {
		style.Attribute |= vaxis.AttrBlink
	}
	return style
}

func ghosttyStyleForColors(foregroundRGB, backgroundRGB uint32, explicitForeground bool, explicitBackground bool) vaxis.Style {
	style := vaxis.Style{}
	if explicitForeground {
		style.Foreground = vaxis.HexColor(foregroundRGB)
	}
	if explicitBackground {
		style.Background = vaxis.HexColor(backgroundRGB)
	}
	return style
}

func ghosttyRGB(r, g, b uint8) uint32 {
	return uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

func (t *ghosttyTUITerminal) graphemeLocked(row int, col int, cell C.GhosttyResolvedCell) string {
	if cell.grapheme_len == 0 {
		if cell.codepoint == 0 {
			return " "
		}
		return string(rune(cell.codepoint))
	}

	codepoints := make([]C.uint32_t, int(cell.grapheme_len)+1)
	count := int(C.ghostty_bridge_render_state_get_grapheme(
		t.term,
		C.int(row),
		C.int(col),
		(*C.uint32_t)(unsafe.Pointer(&codepoints[0])),
		C.size_t(len(codepoints)),
	))
	if count <= 0 {
		if cell.codepoint == 0 {
			return " "
		}
		return string(rune(cell.codepoint))
	}

	runes := make([]rune, 0, count)
	for index := 0; index < count; index++ {
		runes = append(runes, rune(codepoints[index]))
	}
	return string(runes)
}

// Close asks the actor to tear the terminal down (group-killing the child via
// the Track 0.1 helper) and blocks until teardown + reap complete, so callers
// can observe a reaped child immediately. If the actor already exited (the
// child ended on its own and the read-error path tore down), Close still runs
// the kill=true teardown directly: resources are already nil'd, so it only
// escalates on the child's process group — preserving the pre-actor guarantee
// that Close after self-exit still reaps surviving grandchildren. teardown was
// built for exactly this Close-vs-finish interleave. Repeat Closes just wait on
// actorDone.
func (t *ghosttyTUITerminal) Close() {
	t.closeOnce.Do(func() {
		select {
		case t.commands <- actorEnvelope{cmd: cmdClose{}}:
		case <-t.actorDone:
			t.teardown(true, false, nil)
		}
	})
	<-t.actorDone
}

func (t *ghosttyTUITerminal) Focus() {
	t.sendSync(cmdFocus{Focused: true})
}

func (t *ghosttyTUITerminal) Blur() {
	t.sendSync(cmdFocus{Focused: false})
}

func (t *ghosttyTUITerminal) applyFocus(focused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused = focused
	if t.term != nil && t.getModeLocked(ghosttyModeFocusEvents, false) {
		t.writePTYLocked(encodeTUITerminalFocusWithGhostty(focused))
	}
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) applyScroll(delta int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil || delta == 0 {
		return
	}
	t.scrollViewportDeltaLocked(delta)
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshot
}

func (t *ghosttyTUITerminal) TermEnv() string {
	return tuiEmbeddedTermEnv
}

func (t *ghosttyTUITerminal) HyperlinkAt(col, row int) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if row < 0 || row >= t.rows || col < 0 || col >= t.cols || t.term == nil {
		return "", false
	}
	return withGhosttyStderrSuppressed(func() hyperlinkResult {
		target, ok := t.hyperlinkAtLocked(row, col)
		return hyperlinkResult{target: target, ok: ok}
	}).unpack()
}

func (t *ghosttyTUITerminal) CapturesMouse() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil {
		return false
	}
	return withGhosttyStderrSuppressed(func() bool {
		return bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term))
	})
}

func (t *ghosttyTUITerminal) hyperlinkAtLocked(row int, col int) (string, bool) {
	buffer := make([]byte, 2048)
	n := int(C.ghostty_bridge_terminal_get_hyperlink_uri(
		t.term,
		C.int(row),
		C.int(col),
		(*C.uint8_t)(unsafe.Pointer(&buffer[0])),
		C.size_t(len(buffer)),
	))
	if n <= 0 {
		return "", false
	}
	return string(buffer[:n]), true
}

// teardown is the single resource-release path shared by Close (kill=true:
// the user closed the tab, so the child's whole process group is terminated
// and reaped before returning) and finish (kill=false: the child already
// exited on its own and a closed event may be posted). Resources are captured
// and nil'd under the mutex, so the cgo handles are freed exactly once no
// matter how Close and finish interleave.
func (t *ghosttyTUITerminal) teardown(kill bool, post bool, err error) {
	// Stop the read pump before closing the PTY so it never blocks trying to
	// hand bytes to an actor that is exiting.
	t.quitOnce.Do(func() { close(t.quit) })

	t.mu.Lock()
	if !post {
		t.suppressEvent = true
	}
	if t.closed && post {
		t.mu.Unlock()
		return
	}
	t.closed = true
	childPID := t.childPID
	ptyFile := t.pty
	ptyWriter := t.ptyWriter
	term := t.term
	keyEncoder := t.keyEncoder
	mouseEncoder := t.mouseEncoder
	waitDone := t.waitDone
	postEvent := post && !t.suppressEvent
	t.pty = nil
	t.ptyWriter = nil
	t.term = nil
	t.keyEncoder = nil
	t.mouseEncoder = nil
	if kill {
		t.childPID = 0
	}
	handoffPTY := t.handoffPTY
	t.handoffPTY = nil
	t.mu.Unlock()
	if handoffPTY != nil {
		_ = handoffPTY.Close()
	}

	closeIO := func() {
		// Writer first (it drains and closes its PTY target), then the PTY
		// master: closing the master hangs up the line, delivering SIGHUP to
		// the child's foreground process group.
		if ptyWriter != nil {
			ptyWriter.Close()
		}
		if ptyFile != nil {
			_ = ptyFile.Close()
		}
	}
	if kill && childPID > 0 {
		// The child is a session leader (Setsid in Start), so its process
		// group id equals its pid and the helper can group-kill descendants.
		_ = shutdownTerminalProcess(childPID, closeIO, waitDone)
	} else {
		closeIO()
	}
	if kill {
		// Block until the waitOnce reaper has collected the direct child so
		// Close never returns while a zombie remains.
		_ = t.wait()
	}
	if term != nil {
		withGhosttyStderrSuppressed(func() struct{} {
			C.ghostty_bridge_terminal_free(term)
			return struct{}{}
		})
	}
	if keyEncoder != nil {
		keyEncoder.Close()
	}
	if mouseEncoder != nil {
		mouseEncoder.Close()
	}
	if postEvent && t.postEvent != nil {
		t.postEvent(tuiTerminalClosedEvent{SessionKey: t.targetKey, Err: err})
	}
}

func (t *ghosttyTUITerminal) beginHandoff(timeout time.Duration) handoffTerminalState {
	t.mu.Lock()
	if t.closed || t.pty == nil || t.state != runtimeStateRunning {
		t.mu.Unlock()
		return handoffTerminalState{Err: errTerminalClosed}
	}
	t.state = runtimeStateQuiescing
	writer := t.ptyWriter
	ptyFile := t.pty
	childPID, cols, rows := t.childPID, t.cols, t.rows
	t.mu.Unlock()
	if err := writer.Drain(timeout); err != nil {
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
	t.mu.Lock()
	readPumpDone := t.readPumpDone
	t.mu.Unlock()
	if readPumpDone != nil {
		select {
		case <-readPumpDone:
		case <-time.After(250 * time.Millisecond):
			// A blocking PTY read with no pending child output may outlive close
			// on some kernels. The actor is already gated and the old process
			// exits immediately after commit; drain any delivered boundary bytes
			// below without turning an idle terminal into a failed handoff.
		}
	}
	for {
		select {
		case data := <-t.readCh:
			t.ingestPTY(data)
		default:
			goto readsDrained
		}
	}
readsDrained:
	t.mu.Lock()
	t.pty = nil
	t.ptyWriter = nil
	t.handoffPTY = os.NewFile(uintptr(rollbackFD), "codelima-handoff-rollback")
	t.state = runtimeStateQuiesced
	replay := append([]byte(nil), t.replay...)
	t.mu.Unlock()
	return handoffTerminalState{PTY: os.NewFile(uintptr(transferFD), "codelima-handoff-transfer"), ChildPID: childPID, Cols: cols, Rows: rows, Replay: replay}
}

func (t *ghosttyTUITerminal) rollbackHandoff() error {
	t.mu.Lock()
	if t.state != runtimeStateQuiesced || t.handoffPTY == nil {
		t.mu.Unlock()
		return fmt.Errorf("terminal is not quiesced")
	}
	ptyFile := t.handoffPTY
	t.handoffPTY = nil
	t.quit = make(chan struct{})
	t.quitOnce = sync.Once{}
	writer := newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, nil)
	t.pty = ptyFile
	t.ptyWriter = writer
	t.readPumpDone = make(chan struct{})
	t.state = runtimeStateRunning
	t.mu.Unlock()
	go t.readPump()
	return nil
}

func adoptGhosttyTUITerminal(targetKey string, postEvent func(vaxis.Event), ptyFile *os.File, childPID, cols, rows int, replay []byte) (daemonTerminal, error) {
	base, err := newGhosttyTUITerminal(targetKey, postEvent)
	if err != nil {
		return nil, err
	}
	term := base.(*ghosttyTUITerminal)
	term.mu.Lock()
	term.cols, term.rows = cols, rows
	term.pty = ptyFile
	term.childPID = childPID
	term.ptyWriter = newGhosttyPTYWriter(ptyFile, waitGhosttyPTYWritable, nil)
	term.mu.Unlock()
	if len(replay) > 0 {
		term.ingestPTY(replay)
	}
	return term, nil
}

func (t *ghosttyTUITerminal) ActivateAfterHandoff() {
	t.mu.Lock()
	t.readPumpDone = make(chan struct{})
	t.mu.Unlock()
	go t.readPump()
}

func (t *ghosttyTUITerminal) wait() error {
	t.waitOnce.Do(func() {
		if t.cmd == nil {
			return
		}
		t.waitErr = t.cmd.Wait()
	})
	return t.waitErr
}

func (t *ghosttyTUITerminal) getModeLocked(mode int, isANSI bool) bool {
	if t.term == nil {
		return false
	}
	return withGhosttyStderrSuppressed(func() bool {
		return bool(C.ghostty_bridge_terminal_get_mode(t.term, C.int(mode), C.bool(isANSI)))
	})
}

func (t *ghosttyTUITerminal) scrollbarLocked() (ghosttyScrollbarState, bool) {
	if t.term == nil {
		return ghosttyScrollbarState{}, false
	}
	var state ghosttyScrollbarState
	ok := withGhosttyStderrSuppressed(func() bool {
		var rawOK bool
		state, rawOK = t.scrollbarLockedRaw()
		return rawOK
	})
	return state, ok
}

func (t *ghosttyTUITerminal) viewportAtBottomLocked() bool {
	return withGhosttyStderrSuppressed(func() bool {
		return t.viewportAtBottomLockedRaw()
	})
}

func (t *ghosttyTUITerminal) defaultBackgroundRGBLocked() uint32 {
	if t.term == nil {
		return 0
	}
	return withGhosttyStderrSuppressed(func() uint32 {
		C.ghostty_bridge_render_state_update(t.term)
		return uint32(C.ghostty_bridge_render_state_get_bg_color(t.term))
	})
}

func (t *ghosttyTUITerminal) setColorThemeModeLocked(mode vaxis.ColorThemeMode) {
	if t.term == nil {
		return
	}
	withGhosttyStderrSuppressed(func() struct{} {
		C.ghostty_bridge_terminal_set_color_theme_mode(t.term, C.int(mode))
		return struct{}{}
	})
}

func (t *ghosttyTUITerminal) reportColorThemeModeLocked() {
	if t.term == nil {
		return
	}
	withGhosttyStderrSuppressed(func() struct{} {
		C.ghostty_bridge_terminal_report_color_theme_mode(t.term)
		return struct{}{}
	})
}

func (t *ghosttyTUITerminal) scrollbarLockedRaw() (ghosttyScrollbarState, bool) {
	if t.term == nil {
		return ghosttyScrollbarState{}, false
	}
	var scrollbar C.GhosttyTerminalScrollbar
	if !bool(C.ghostty_bridge_terminal_get_scrollbar(t.term, &scrollbar)) {
		return ghosttyScrollbarState{}, false
	}
	return ghosttyScrollbarState{
		total:  int(scrollbar.total),
		offset: int(scrollbar.offset),
		length: int(scrollbar.len),
	}, true
}

func (t *ghosttyTUITerminal) viewportAtBottomLockedRaw() bool {
	if t.term == nil {
		return true
	}
	if bool(C.ghostty_bridge_terminal_is_alternate_screen(t.term)) {
		return true
	}
	scrollbar, ok := t.scrollbarLockedRaw()
	if !ok || scrollbar.total <= scrollbar.length {
		return true
	}
	return scrollbar.offset+scrollbar.length >= scrollbar.total
}

func (t *ghosttyTUITerminal) scrollViewportBottomLocked() {
	if t.term == nil {
		return
	}
	withGhosttyStderrSuppressed(func() struct{} {
		C.ghostty_bridge_terminal_scroll_viewport_bottom(t.term)
		return struct{}{}
	})
}

func (t *ghosttyTUITerminal) scrollViewportDeltaLocked(delta int) {
	if t.term == nil || delta == 0 {
		return
	}
	withGhosttyStderrSuppressed(func() struct{} {
		C.ghostty_bridge_terminal_scroll_viewport_delta(t.term, C.intptr_t(delta))
		return struct{}{}
	})
}

type hyperlinkResult struct {
	target string
	ok     bool
}

func (r hyperlinkResult) unpack() (string, bool) {
	return r.target, r.ok
}

func (t *ghosttyTUITerminal) invalidateLocked() {
	if t.redrawPending || t.postEvent == nil {
		return
	}
	t.redrawPending = true
	time.AfterFunc(8*time.Millisecond, func() {
		t.mu.Lock()
		if t.closed {
			t.redrawPending = false
			t.mu.Unlock()
			return
		}
		t.redrawPending = false
		t.mu.Unlock()
		t.postEvent(vaxis.Redraw{})
	})
}
