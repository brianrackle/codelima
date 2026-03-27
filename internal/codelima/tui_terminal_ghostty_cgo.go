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
	file *os.File
	err  error
}

var ghosttyStderr ghosttyStderrState

// Ghostty currently logs parser warnings to process stderr, so contain them
// around backend calls instead of letting them spill into the TUI chrome.
func withGhosttyStderrSuppressed[T any](fn func() T) T {
	ghosttyStderr.mu.Lock()
	defer ghosttyStderr.mu.Unlock()

	ghosttyStderr.once.Do(func() {
		ghosttyStderr.file, ghosttyStderr.err = os.OpenFile(os.DevNull, os.O_WRONLY, 0)
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

func newGhosttyTUITerminal(nodeID string, postEvent func(vaxis.Event)) (tuiTerminal, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}

	term := withGhosttyStderrSuppressed(func() C.GhosttyBridgeTerminal {
		return C.ghostty_bridge_terminal_new(80, 24)
	})
	if term == nil {
		return nil, fmt.Errorf("create ghostty terminal: %s", C.GoString(C.ghostty_bridge_last_error()))
	}

	return &ghosttyTUITerminal{
		nodeID:    nodeID,
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
		cols: 80,
		rows: 24,
	}, nil
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
	nodeID       string
	postEvent    func(vaxis.Event)
	term         C.GhosttyBridgeTerminal
	keyEncoder   *ghosttyKeyEncoder
	mouseEncoder *ghosttyMouseEncoder

	mu               sync.Mutex
	cmd              *exec.Cmd
	pty              *os.File
	ptyWriter        *ghosttyPTYWriter
	cols             int
	rows             int
	focused          bool
	mouseButtonsDown int
	snapshot         string
	closed           bool
	suppressEvent    bool
	redrawPending    bool
	waitOnce         sync.Once
	waitErr          error
	closeOnce        sync.Once
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
		if err := ghosttyWriteAllToPTY(w.target, chunk, w.waitWritable); err != nil {
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
	return chunk, true
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
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	for {
		_, err := unix.Poll(fds, -1)
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

func encodeTUITerminalKeyWithGhostty(key vaxis.Key, encoder *ghosttyKeyEncoder, applicationKeypad bool, cursorKeysApplication bool) string {
	if key.EventType == vaxis.EventPaste {
		return key.Text
	}
	if encoder != nil {
		if encoded, handled := encoder.Encode(key, applicationKeypad, cursorKeysApplication); handled {
			return encoded
		}
	}

	return encodeTUITerminalKey(key, applicationKeypad, cursorKeysApplication)
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

func (e *ghosttyKeyEncoder) Encode(key vaxis.Key, applicationKeypad bool, cursorKeysApplication bool) (string, bool) {
	if e == nil || e.encoder == nil {
		return "", false
	}

	if key.EventType == vaxis.EventPaste {
		if key.Text != "" {
			return key.Text, true
		}
		return "", true
	}

	physicalKey, ok := ghosttyKeyForVaxis(key)
	if !ok {
		if key.EventType == vaxis.EventRelease {
			return "", true
		}
		return "", false
	}

	cursor := C.bool(cursorKeysApplication)
	C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_CURSOR_KEY_APPLICATION, cursor)
	keypad := C.bool(applicationKeypad)
	C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_KEYPAD_KEY_APPLICATION, keypad)
	altEscPrefix := C.bool(true)
	C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_ALT_ESC_PREFIX, altEscPrefix)
	modifyOtherKeysState2 := C.bool(false)
	C.ghostty_bridge_key_encoder_setopt_bool(e.encoder, C.GHOSTTY_KEY_ENCODER_OPT_MODIFY_OTHER_KEYS_STATE_2, modifyOtherKeysState2)

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
	cmd.Env = append(env, "TERM="+tuiEmbeddedTermEnv)

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

	ptyWriter := newGhosttyPTYWriter(ptyFile, nil, func(err error) {
		if t.postEvent != nil {
			t.postEvent(tuiTerminalErrorEvent{
				NodeID: t.nodeID,
				Err:    fmt.Errorf("write embedded terminal pty: %w", err),
			})
		}
	})

	t.mu.Lock()
	t.cmd = cmd
	t.pty = ptyFile
	t.ptyWriter = ptyWriter
	t.mu.Unlock()

	go t.readLoop()
	return nil
}

func (t *ghosttyTUITerminal) Resize(width, height int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.resizeLocked(width, height)
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) readLoop() {
	buffer := make([]byte, 32*1024)
	for {
		n, err := t.readPTY(buffer)
		if n > 0 {
			t.ingestPTY(buffer[:n])
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				t.finish(nil)
				return
			}
			if errors.Is(err, io.EOF) {
				t.finish(t.wait())
				return
			}
			t.finish(t.wait())
			return
		}
	}
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
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || len(data) == 0 {
		return
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
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) drainResponsesLocked() {
	withGhosttyStderrSuppressed(func() struct{} {
		t.drainResponsesLockedRaw()
		return struct{}{}
	})
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

func (t *ghosttyTUITerminal) Update(event vaxis.Event) {
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

	t.cols = width
	t.rows = height
	C.ghostty_bridge_terminal_resize(t.term, C.int(width), C.int(height))
	if t.mouseEncoder != nil {
		t.mouseEncoder.Reset()
	}
	if t.pty != nil {
		_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
	}
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

func (t *ghosttyTUITerminal) Close() {
	t.closeOnce.Do(func() {
		t.mu.Lock()
		t.suppressEvent = true
		t.closed = true
		ptyFile := t.pty
		ptyWriter := t.ptyWriter
		cmd := t.cmd
		term := t.term
		keyEncoder := t.keyEncoder
		mouseEncoder := t.mouseEncoder
		t.pty = nil
		t.ptyWriter = nil
		t.term = nil
		t.keyEncoder = nil
		t.mouseEncoder = nil
		t.mu.Unlock()

		if ptyWriter != nil {
			ptyWriter.Close()
		}
		if ptyFile != nil {
			_ = ptyFile.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = t.wait()
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
	})
}

func (t *ghosttyTUITerminal) Focus() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused = true
	if t.term != nil && t.getModeLocked(ghosttyModeFocusEvents, false) {
		t.writePTYLocked("\x1b[I")
	}
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) Blur() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.focused = false
	if t.term != nil && t.getModeLocked(ghosttyModeFocusEvents, false) {
		t.writePTYLocked("\x1b[O")
	}
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

func (t *ghosttyTUITerminal) finish(err error) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	term := t.term
	ptyFile := t.pty
	ptyWriter := t.ptyWriter
	keyEncoder := t.keyEncoder
	mouseEncoder := t.mouseEncoder
	postEvent := !t.suppressEvent
	t.term = nil
	t.pty = nil
	t.ptyWriter = nil
	t.keyEncoder = nil
	t.mouseEncoder = nil
	t.mu.Unlock()

	if ptyWriter != nil {
		ptyWriter.Close()
	}
	if ptyFile != nil {
		_ = ptyFile.Close()
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
		t.postEvent(tuiTerminalClosedEvent{NodeID: t.nodeID, Err: err})
	}
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
