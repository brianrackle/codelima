//go:build cgo && (darwin || linux)

package ghostty

/*
#cgo pkg-config: libghostty-vt-static
#include <stdlib.h>
#include "ghostty_bridge_compat.h"

static void codelima_test_ghostty_hang_forever(void) {
	for (;;) {}
}
*/
import "C"

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/brianrackle/codelima/internal/rendererbuild"
	"github.com/brianrackle/codelima/internal/terminalio"
	"github.com/brianrackle/codelima/internal/terminalstate"

	"github.com/creack/pty"
	"go.rockorager.dev/vaxis"
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

var ghosttyInit struct {
	once sync.Once
	err  error
}

// Native logs are drained from the library's bounded callback buffer. No
// terminal changes the process's stderr descriptor or starts a log goroutine.
func drainGhosttyLogs() {
	var buffer [4096]byte
	for range 16 {
		n := int(C.ghostty_bridge_read_log((*C.uint8_t)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer))))
		if n <= 0 {
			return
		}
		packageLog().Debug(strings.TrimSpace(string(buffer[:n])), "source", "libghostty")
	}
}

func newGhosttyTUITerminal(targetKey string, postEvent func(vaxis.Event)) (tuiTerminal, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}

	term := C.ghostty_bridge_terminal_new(80, 24)
	if term == nil {
		return nil, fmt.Errorf("create ghostty terminal: %s", C.GoString(C.ghostty_bridge_last_error()))
	}

	keyEncoder, err := newGhosttyKeyEncoder()
	if err != nil {
		C.ghostty_bridge_terminal_free(term)
		return nil, err
	}
	mouseEncoder, err := newGhosttyMouseEncoder()
	if err != nil {
		keyEncoder.Close()
		C.ghostty_bridge_terminal_free(term)
		return nil, err
	}

	impl := &ghosttyTUITerminal{
		targetKey:    targetKey,
		postEvent:    postEvent,
		term:         term,
		keyEncoder:   keyEncoder,
		mouseEncoder: mouseEncoder,
		cols:         80,
		rows:         24,
		state:        runtimeStateRunning,
		commands:     make(chan actorEnvelope),
		readCh:       make(chan []byte, 64),
		readErrCh:    make(chan error, 1),
		quit:         make(chan struct{}),
		actorDone:    make(chan struct{}),
	}

	// The actor runs from construction (not from Start) so the synchronous
	// interface methods work even before a child is spawned — several tests
	// Resize/Focus/Blur a terminal that never calls Start.
	go impl.runActor()

	return impl, nil
}

func loadGhosttyVT() error {
	ghosttyInit.once.Do(func() {
		if result := C.ghostty_bridge_init(); result != C.GHOSTTY_SUCCESS {
			ghosttyInit.err = fmt.Errorf("initialize statically linked Ghostty: %d", int(result))
			return
		}
		if actual := C.GoString(C.ghostty_bridge_build_identity()); actual != rendererbuild.Identity() {
			ghosttyInit.err = fmt.Errorf("native build mismatch: archive %s, Go adapter %s; run make init", actual, rendererbuild.Identity())
		}
	})
	return ghosttyInit.err
}

type ghosttyTUITerminal struct {
	targetKey     string
	postEvent     func(vaxis.Event)
	term          C.GhosttyBridgeTerminal
	keyEncoder    *ghosttyKeyEncoder
	mouseEncoder  *ghosttyMouseEncoder
	metadata      TerminalMetadata
	nativeErr     error
	compression   idleCompressionState
	defaultColors *TerminalColors
	// Private immutable rows: callers receive separate cell arrays, while
	// unchanged rows retain already-owned strings and resolved cell styles.
	cachedFrameRows [][]SnapshotCell

	mu               sync.Mutex
	cmd              *exec.Cmd
	pty              *os.File
	ptyWriter        *ghosttyPTYWriter
	rendererOutput   func(eventID uint64, ordinal uint32, data []byte)
	responseEventID  uint64
	responseOrdinal  uint32
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

func newGhosttyKeyEncoder() (*ghosttyKeyEncoder, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
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
	var encoder C.GhosttyMouseEncoder
	//nolint:gocritic // dupSubExpr false positive: gocritic misreads the cgo-typed nil comparison
	if result := C.ghostty_bridge_mouse_encoder_new(&encoder); result != C.GHOSTTY_SUCCESS || encoder == nil {
		return nil, fmt.Errorf("create ghostty mouse encoder: %d", int(result))
	}

	return &ghosttyMouseEncoder{encoder: encoder}, nil
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

	packageLog().Warn("Ghostty could not encode terminal key", "keycode", key.Keycode)
	return ""
}

func encodeTUITerminalMouseWithGhostty(
	mouse vaxis.Mouse,
	encoder *ghosttyMouseEncoder,
	term C.GhosttyBridgeTerminal,
	cols int,
	rows int,
	mouseButtonsDown int,
	_ bool,
	_ bool,
	_ bool,
) string {
	if encoder != nil {
		if encoded, handled := encoder.Encode(mouse, term, cols, rows, mouseButtonsDown); handled {
			return encoded
		}
	}

	packageLog().Warn("Ghostty could not encode terminal mouse event", "button", mouse.Button)
	return ""
}

func encodeTUITerminalFocusWithGhostty(focused bool) string {
	event := C.GhosttyFocusEvent(C.GHOSTTY_FOCUS_LOST)
	if focused {
		event = C.GhosttyFocusEvent(C.GHOSTTY_FOCUS_GAINED)
	}
	var buffer [16]byte
	var outLen C.size_t
	if result := C.ghostty_bridge_focus_encode(event, (*C.char)(unsafe.Pointer(&buffer[0])), C.size_t(len(buffer)), &outLen); result != C.GHOSTTY_SUCCESS {
		packageLog().Error("encode Ghostty focus", "result", int(result))
		return ""
	}
	return string(buffer[:int(outLen)])
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
	// Vaxis represents legacy control-function-key aliases above Ghostty's
	// physical F25. Translate the event, then let the native encoder choose VT.
	if key.Keycode >= vaxis.KeyF26 && key.Keycode <= vaxis.KeyF35 {
		key.Keycode = vaxis.KeyF02 + key.Keycode - vaxis.KeyF26
		key.Modifiers |= vaxis.ModCtrl
	}

	physicalKey, ok := ghosttyKeyForVaxis(key)
	if !ok {
		if key.EventType == vaxis.EventRelease {
			return "", true
		}
		return "", false
	}

	if term != nil && !bool(C.ghostty_bridge_key_encoder_setopt_from_terminal(e.encoder, term)) {
		return "", false
	}
	if term == nil {
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
	// ghostty/vt/key/encoder.h: "@return GHOSTTY_SUCCESS on success,
	// GHOSTTY_OUT_OF_SPACE if buffer too small ... out_len will contain the
	// required buffer size." This retry checked OUT_OF_MEMORY, which the encoder
	// never returns for a short buffer, so the regrow was dead code and any key
	// whose encoding exceeded 64 bytes was silently dropped instead of being
	// re-encoded. The focus and mouse encoders next to it already check
	// OUT_OF_SPACE.
	if result == C.GHOSTTY_OUT_OF_SPACE {
		if uint64(outLen) > terminalMaxPasteBytes {
			return "", false
		}
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
		// Legacy input supplies text without a physical/base-layout key.
		// Preserve that text and codepoint through Ghostty's native encoder
		// instead of guessing a US keyboard position or dropping the event.
		if unicode.IsPrint(key.Keycode) {
			return C.GHOSTTY_KEY_UNIDENTIFIED, true
		}
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
	ptyFD, err := ghosttyPTYFileDescriptor(ptyFile)
	if err != nil {
		_ = ptyFile.Close()
		return fmt.Errorf("resolve terminal pty descriptor: %w", err)
	}
	if err := unix.SetNonblock(ptyFD, true); err != nil {
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
			data := slices.Clone(buffer[:n])
			if t.handoffInProgress() {
				t.readCh <- data
			} else {
				select {
				case <-quit:
					return
				default:
				}
				select {
				case t.readCh <- data:
				case <-quit:
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
				case <-quit:
					return
				default:
				}
				_ = waitGhosttyPTYReadable(ptyFD, 50*time.Millisecond)
				continue
			}
			if t.handoffInProgress() {
				return
			}
			select {
			case <-quit:
				return
			default:
			}
			select {
			case t.readErrCh <- err:
			case <-quit:
			}
			return
		}
	}
}

func (t *ghosttyTUITerminal) handoffInProgress() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state == runtimeStateQuiescing || t.state == runtimeStateQuiesced
}

func (t *ghosttyTUITerminal) ingestPTY(data []byte) {
	t.ingestPTYEvent(data, 0)
}

func (t *ghosttyTUITerminal) ingestPTYEvent(data []byte, eventID uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || len(data) == 0 {
		return
	}
	t.replay = append(t.replay, data...)
	if len(t.replay) > 8*1024 {
		t.replay = slices.Clone(t.replay[len(t.replay)-8*1024:])
	}

	t.responseEventID = eventID
	t.responseOrdinal = 0
	if os.Getenv("CODELIMA_TEST_GHOSTTY_HANG_OPERATION") == "output" {
		C.codelima_test_ghostty_hang_forever()
	}
	C.ghostty_bridge_terminal_write(
		t.term,
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
	)
	t.drainEffectsLockedRaw()
	// Output is asynchronous; retain the typed error for Read/Publish/status.
	_ = t.checkNativeErrorLockedRaw("write")
	drainGhosttyLogs()
	t.drainResponsesLockedRaw()
	// A generation identifies a readable render revision, including output,
	// geometry and viewport changes, not merely an output-byte sequence.
	t.generation++
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) drainResponsesLockedRaw() {
	if (t.ptyWriter == nil && t.rendererOutput == nil) || t.term == nil {
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

func (t *ghosttyTUITerminal) checkNativeErrorLockedRaw(operation string) error {
	if result := C.ghostty_bridge_terminal_error(t.term); result != C.GHOSTTY_SUCCESS {
		err := fmt.Errorf("Ghostty %s failed: native result %d", operation, int(result))
		if t.nativeErr == nil {
			t.nativeErr = err
			if t.postEvent != nil {
				t.postEvent(tuiTerminalErrorEvent{Err: err})
			}
		}
	}
	return t.nativeErr
}

func (t *ghosttyTUITerminal) drainEffectsLockedRaw() {
	var effect C.GhosttyBridgeEffect
	for C.ghostty_bridge_terminal_next_effect(t.term, &effect) { //nolint:gocritic // cgo-generated pointer-safety expression.
		// The bridge transferred ownership; copy before releasing. Native queue
		// bounds limit both the count and bytes processed in this actor turn.
		text := C.GoStringN((*C.char)(unsafe.Pointer(effect.data)), C.int(effect.len))
		detail := C.GoStringN((*C.char)(unsafe.Pointer(effect.detail)), C.int(effect.detail_len))
		switch effect.kind {
		case C.GHOSTTY_BRIDGE_EFFECT_CLIPBOARD:
			if len(text) <= terminalMaxPasteBytes && utf8.ValidString(text) && t.postEvent != nil {
				t.postEvent(tuiClipboardEvent{TargetKey: t.targetKey, Text: text})
			}
		case C.GHOSTTY_BRIDGE_EFFECT_TITLE:
			t.metadata.Title = text
		case C.GHOSTTY_BRIDGE_EFFECT_PWD:
			t.metadata.WorkingDirectory = text
		case C.GHOSTTY_BRIDGE_EFFECT_BELL:
			t.metadata.BellCount++
		case C.GHOSTTY_BRIDGE_EFFECT_NOTIFICATION:
			t.metadata.NotificationTitle, t.metadata.NotificationBody = text, detail
		case C.GHOSTTY_BRIDGE_EFFECT_PROGRESS:
			t.metadata.ProgressState, t.metadata.Progress = int(effect.location), int(effect.value)
		}
		C.ghostty_bridge_effect_free(&effect) //nolint:gocritic // cgo-generated pointer-safety expression.
	}
}

func (t *ghosttyTUITerminal) readPendingResponses() string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.term == nil {
		return ""
	}

	return t.readPendingResponsesLockedRaw()
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
	if t.nativeErr != nil {
		return ReadResult{Err: t.nativeErr}
	}
	text, err := t.formatLockedRaw(source, format)
	return ReadResult{Text: text, Generation: t.generation, Err: err}
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
	return t.snapshotLockedRaw()
}

func (t *ghosttyTUITerminal) servePublication() terminalPublication {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return terminalPublication{Snapshot: SnapshotResult{Err: errTerminalClosed}, Visible: ReadResult{Err: errTerminalClosed}}
	}
	snapshot := t.captureSnapshotLockedRaw()
	if snapshot.Err != nil {
		return terminalPublication{Snapshot: snapshot, Visible: ReadResult{Err: snapshot.Err}}
	}
	text, err := t.formatLockedRaw(ReadVisible, ReadText)
	if err == nil {
		C.ghostty_bridge_render_state_mark_clean(t.term)
		err = t.checkNativeErrorLockedRaw("publish terminal")
	}
	return terminalPublication{
		Snapshot: snapshot,
		Visible:  ReadResult{Text: text, Generation: t.generation, Err: err},
	}
}

func (t *ghosttyTUITerminal) Checkpoint() (terminalCheckpointState, error) {
	reply := make(chan checkpointResult, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdCheckpoint{Reply: reply}}:
	case <-t.actorDone:
		return terminalCheckpointState{}, errTerminalClosed
	}
	select {
	case result := <-reply:
		return result.State, result.Err
	case <-t.actorDone:
		return terminalCheckpointState{}, errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) RestoreCheckpoint(state terminalCheckpointState) error {
	reply := make(chan error, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdRestoreCheckpoint{State: state, Reply: reply}}:
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

func (t *ghosttyTUITerminal) checkpointLocked() (terminalCheckpointState, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return terminalCheckpointState{}, errTerminalClosed
	}
	if t.nativeErr != nil {
		return terminalCheckpointState{}, t.nativeErr
	}
	var data *C.uint8_t
	var length C.size_t
	result := C.ghostty_bridge_terminal_checkpoint(t.term, C.size_t(rendererCheckpointMaxBytes), &data, &length) //nolint:gocritic // cgo-generated pointer-safety expression.
	if data != nil {
		defer C.ghostty_bridge_free(unsafe.Pointer(data))
	}
	if result == C.GHOSTTY_BRIDGE_UNSUPPORTED {
		return terminalCheckpointState{}, terminalstate.ErrCheckpointGraphics
	}
	if result != C.GHOSTTY_SUCCESS {
		return terminalCheckpointState{}, fmt.Errorf("checkpoint terminal: native result %d", int(result))
	}
	if length == 0 || uint64(length) > rendererCheckpointMaxBytes || data == nil {
		return terminalCheckpointState{}, errors.New("checkpoint terminal: invalid native output")
	}
	scrollbar, ok := t.scrollbarLockedRaw()
	if !ok {
		return terminalCheckpointState{}, errors.New("checkpoint terminal: viewport unavailable")
	}
	var cellWidth, cellHeight C.uint32_t
	if result := C.ghostty_bridge_terminal_cell_size(t.term, &cellWidth, &cellHeight); result != C.GHOSTTY_SUCCESS {
		return terminalCheckpointState{}, fmt.Errorf("checkpoint terminal: pixel geometry unavailable: %d", int(result))
	}
	return terminalCheckpointState{
		Data: C.GoBytes(unsafe.Pointer(data), C.int(length)), Generation: t.generation,
		Cols: t.cols, Rows: t.rows, ViewportOffset: scrollbar.offset, Focused: t.focused, Metadata: t.metadata,
		Colors:    terminalstate.CloneColors(t.defaultColors),
		CellWidth: int(cellWidth), CellHeight: int(cellHeight),
	}, nil
}

func (t *ghosttyTUITerminal) restoreCheckpointLocked(state terminalCheckpointState) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return errTerminalClosed
	}
	if len(state.Data) == 0 || len(state.Data) > rendererCheckpointMaxBytes || state.Cols <= 0 || state.Rows <= 0 || state.ViewportOffset < 0 {
		return errors.New("restore terminal checkpoint: invalid bounds")
	}
	if err := terminalstate.ValidateColors(state.Colors); err != nil {
		return err
	}
	if state.CellWidth < 0 || state.CellHeight < 0 || state.CellWidth > 4096 || state.CellHeight > 4096 || (state.CellWidth == 0) != (state.CellHeight == 0) {
		return errors.New("restore terminal checkpoint: invalid pixel geometry")
	}
	result := C.ghostty_bridge_terminal_restore(t.term, (*C.uint8_t)(unsafe.Pointer(&state.Data[0])), C.size_t(len(state.Data)))
	if result != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("restore terminal checkpoint: native result %d", int(result))
	}
	t.cols, t.rows = state.Cols, state.Rows
	t.generation, t.focused, t.metadata = state.Generation, state.Focused, state.Metadata
	t.nativeErr = nil
	t.cachedFrameRows = nil
	colors := state.Colors
	if colors == nil {
		colors = &TerminalColors{}
	}
	if err := t.applyColorsPolicyLocked(colors, false); err != nil {
		return err
	}
	t.defaultColors = terminalstate.CloneColors(state.Colors)
	if state.CellWidth > 0 {
		result := C.ghostty_bridge_terminal_resize_pixels(t.term, C.int(state.Cols), C.int(state.Rows), C.uint32_t(state.CellWidth), C.uint32_t(state.CellHeight))
		if result != C.GHOSTTY_SUCCESS {
			return fmt.Errorf("restore terminal pixel geometry: native result %d", int(result))
		}
	}
	t.drainEffectsLockedRaw()
	C.ghostty_bridge_terminal_scroll_viewport_top(t.term)
	C.ghostty_bridge_terminal_scroll_viewport_delta(t.term, C.intptr_t(state.ViewportOffset))
	t.mouseEncoder.Reset()
	return t.checkNativeErrorLockedRaw("restore checkpoint")
}

const maxTerminalFrameBytes = 16 * 1024 * 1024

func (t *ghosttyTUITerminal) snapshotLockedRaw() SnapshotResult {
	result := t.captureSnapshotLockedRaw()
	if result.Err == nil {
		C.ghostty_bridge_render_state_mark_clean(t.term)
		result.Err = t.checkNativeErrorLockedRaw("clean terminal frame")
	}
	return result
}

func (t *ghosttyTUITerminal) captureSnapshotLockedRaw() SnapshotResult {
	if t.nativeErr != nil {
		return SnapshotResult{Err: t.nativeErr}
	}
	var frame C.GhosttyBridgeFrame
	result := C.ghostty_bridge_terminal_frame(t.term, C.size_t(maxTerminalFrameBytes), &frame) //nolint:gocritic // cgo-generated pointer-safety expression.
	defer C.ghostty_bridge_frame_free(&frame)                                                  //nolint:gocritic // cgo-generated pointer-safety expression.
	if result != C.GHOSTTY_SUCCESS {
		return SnapshotResult{Err: fmt.Errorf("capture terminal frame: native result %d", int(result))}
	}
	cols, rows := int(frame.cols), int(frame.rows)
	if cols <= 0 || rows <= 0 || uint64(frame.count) != uint64(cols)*uint64(rows) ||
		uint64(frame.count) > maxTerminalFrameBytes/uint64(C.sizeof_GhosttyBridgeFrameCell) || frame.cells == nil ||
		uint64(frame.text_len) > maxTerminalFrameBytes || (frame.text_len > 0 && frame.text == nil) {
		return SnapshotResult{Err: errors.New("capture terminal frame: invalid native bounds")}
	}
	nativeCells := unsafe.Slice(frame.cells, int(frame.count))
	if frame.selection_rows == nil || frame.dirty_rows == nil {
		return SnapshotResult{Err: errors.New("capture terminal frame: missing selection rows")}
	}
	selectionRows := unsafe.Slice(frame.selection_rows, rows)
	dirtyRows := unsafe.Slice(frame.dirty_rows, rows)
	nativeText := unsafe.Slice(frame.text, int(frame.text_len))
	snap := TerminalSnapshot{
		Metadata: t.metadata, Cols: cols, Rows: rows, Generation: t.generation,
		Cells:         make([]SnapshotCell, len(nativeCells)),
		CursorVisible: bool(frame.cursor.visible) && bool(frame.cursor.viewport_has_value),
		CursorX:       int(frame.cursor.viewport_x), CursorY: int(frame.cursor.viewport_y),
		CapturesMouse: bool(frame.captures_mouse),
	}
	snap.Metadata.CursorAtPrompt = bool(frame.cursor_at_prompt)
	nextRows := make([][]SnapshotCell, rows)
	for row := range rows {
		if len(t.cachedFrameRows) == rows && len(t.cachedFrameRows[row]) == cols && dirtyRows[row] == 0 && frame.dirty != C.GHOSTTY_RENDER_STATE_DIRTY_FULL {
			nextRows[row] = t.cachedFrameRows[row]
		} else {
			// Each cached row owns only its own text, so changing one row per
			// frame cannot retain a complete obsolete frame for every row.
			start, end := uint64(nativeCells[row*cols].grapheme_offset), uint64(len(nativeText))
			if row+1 < rows {
				end = uint64(nativeCells[(row+1)*cols].grapheme_offset)
			}
			if start > end || end > uint64(len(nativeText)) {
				return SnapshotResult{Err: errors.New("capture terminal frame: invalid row text bounds")}
			}
			text := string(nativeText[start:end])
			cells := make([]SnapshotCell, cols)
			for col, item := range nativeCells[row*cols : (row+1)*cols] {
				grapheme, ok := ghosttyFrameText(text, uint64(item.grapheme_offset)-start, uint64(item.grapheme_len))
				if !ok {
					return SnapshotResult{Err: errors.New("capture terminal frame: invalid grapheme span")}
				}
				link := ""
				if item.hyperlink_len != 0 {
					link, ok = ghosttyFrameText(text, uint64(item.hyperlink_offset)-start, uint64(item.hyperlink_len))
					if !ok {
						return SnapshotResult{Err: errors.New("capture terminal frame: invalid hyperlink span")}
					}
				}
				cells[col] = ghosttySnapshotCell(item.cell, grapheme, link)
			}
			nextRows[row] = cells
		}
		copy(snap.Cells[row*cols:(row+1)*cols], nextRows[row])
		selection := selectionRows[row]
		for col := range cols {
			snap.Cells[row*cols+col].Selected = bool(selection.active) && col >= int(selection.start_x) && col <= int(selection.end_x)
		}
	}
	if err := t.checkNativeErrorLockedRaw("snapshot"); err != nil {
		return SnapshotResult{Err: err}
	}
	t.cachedFrameRows = nextRows
	return SnapshotResult{Snapshot: snap}
}

func ghosttyFrameText(text string, offset, length uint64) (string, bool) {
	if offset > uint64(len(text)) || length > uint64(len(text))-offset {
		return "", false
	}
	return text[offset : offset+length], true
}

func ghosttySnapshotCell(cell C.GhosttyResolvedCell, grapheme, link string) SnapshotCell {
	if grapheme == "" {
		grapheme = " "
	}
	return SnapshotCell{
		Grapheme: grapheme, Hyperlink: link, Width: int(cell.width),
		FG:             ghosttyRGB(uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)),
		BG:             ghosttyRGB(uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)),
		FGDefault:      cell.color_flags&C.GHOSTTY_CELL_FG_DEFAULT != 0,
		BGDefault:      cell.color_flags&C.GHOSTTY_CELL_BG_DEFAULT != 0,
		Bold:           cell.flags&C.GHOSTTY_CELL_BOLD != 0,
		Faint:          cell.flags&C.GHOSTTY_CELL_FAINT != 0,
		Italic:         cell.flags&C.GHOSTTY_CELL_ITALIC != 0,
		Underline:      cell.flags&C.GHOSTTY_CELL_UNDERLINE != 0,
		UnderlineStyle: uint8(cell.underline_style),
		Overline:       bool(cell.overline),
		Strikethrough:  cell.flags&C.GHOSTTY_CELL_STRIKETHROUGH != 0,
		Inverse:        cell.flags&C.GHOSTTY_CELL_INVERSE != 0,
		Invisible:      cell.flags&C.GHOSTTY_CELL_INVISIBLE != 0,
		Blink:          cell.flags&C.GHOSTTY_CELL_BLINK != 0,
	}
}

// maxTerminalReadBytes bounds native formatter allocation and the worker reply.
const maxTerminalReadBytes = 4 * 1024 * 1024

func (t *ghosttyTUITerminal) formatLockedRaw(source ReadSource, format ReadFormat) (string, error) {
	var data *C.uint8_t
	var length C.size_t
	//nolint:gocritic // cgo-generated pointer-safety expression.
	result := C.ghostty_bridge_terminal_format(t.term, C.bool(source == ReadRecent), C.bool(format == ReadANSI),
		C.size_t(maxTerminalReadBytes), &data, &length)
	if data != nil {
		defer C.ghostty_bridge_free(unsafe.Pointer(data))
	}
	if result != C.GHOSTTY_SUCCESS {
		return "", fmt.Errorf("format terminal: native result %d", int(result))
	}
	if uint64(length) > maxTerminalReadBytes || (length > 0 && data == nil) {
		return "", errors.New("format terminal: invalid native output")
	}
	text := C.GoStringN((*C.char)(unsafe.Pointer(data)), C.int(length))
	if format == ReadText {
		text = joinTrimmedTerminalLines(strings.Split(text, "\n"))
	}
	return text, nil
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

func (t *ghosttyTUITerminal) Paste(text string) error {
	return t.PasteEvent(0, text)
}

func (t *ghosttyTUITerminal) EncodePaste(text string) ([]byte, error) {
	if err := validateTerminalPaste(text); err != nil {
		return nil, err
	}
	reply := make(chan encodedPasteResult, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdEncodePaste{Text: text, Reply: reply}}:
	case <-t.actorDone:
		return nil, errTerminalClosed
	}
	select {
	case result := <-reply:
		return result.Data, result.Err
	case <-t.actorDone:
		return nil, errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) encodePaste(text string) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return nil, errTerminalClosed
	}
	if text == "" {
		return nil, nil
	}
	// Flush any earlier response before capturing exactly this paste's writes.
	t.drainResponsesLockedRaw()
	if C.ghostty_bridge_terminal_has_response(t.term) {
		return nil, errors.New("terminal has undelivered protocol responses")
	}
	result := C.ghostty_bridge_terminal_paste(t.term, (*C.uint8_t)(unsafe.Pointer(unsafe.StringData(text))), C.size_t(len(text)), false)
	if result == C.GHOSTTY_REJECTED {
		return nil, errUnsafeTerminalPaste
	}
	if result != C.GHOSTTY_SUCCESS {
		return nil, fmt.Errorf("paste terminal: native result %d", int(result))
	}
	data := []byte(t.readPendingResponsesLockedRaw())
	if err := t.checkNativeErrorLockedRaw("paste"); err != nil {
		return nil, err
	}
	t.scrollViewportBottomLocked()
	return data, nil
}

func (t *ghosttyTUITerminal) PasteEvent(eventID uint64, text string) error {
	if err := validateTerminalPaste(text); err != nil {
		return err
	}
	reply := make(chan error, 1)
	select {
	case t.commands <- actorEnvelope{cmd: cmdPaste{EventID: eventID, Text: text, Reply: reply}}:
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

func (t *ghosttyTUITerminal) applyPaste(eventID uint64, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.term == nil {
		return errTerminalClosed
	}
	if text == "" {
		return nil
	}
	t.responseEventID, t.responseOrdinal = eventID, 0
	result := C.ghostty_bridge_terminal_paste(t.term, (*C.uint8_t)(unsafe.Pointer(unsafe.StringData(text))), C.size_t(len(text)), false)
	if result == C.GHOSTTY_REJECTED {
		return errUnsafeTerminalPaste
	}
	if result != C.GHOSTTY_SUCCESS {
		return fmt.Errorf("paste terminal: native result %d", int(result))
	}
	t.scrollViewportBottomLocked()
	t.drainResponsesLockedRaw()
	return nil
}

func (t *ghosttyTUITerminal) applyUpdate(event vaxis.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil || (t.pty == nil && t.rendererOutput == nil) {
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

	if !bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term)) {
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
	if bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term)) {
		return false
	}
	if bool(C.ghostty_bridge_terminal_is_alternate_screen(t.term)) {
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
	if len(data) == 0 {
		return
	}
	if t.rendererOutput != nil {
		t.responseOrdinal++
		t.rendererOutput(t.responseEventID, t.responseOrdinal, slices.Clone(data))
		return
	}
	if t.ptyWriter != nil {
		t.ptyWriter.Enqueue(data)
	}
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
	t.resizeLocked(width, height)
	result := t.snapshotLockedRaw()
	if result.Err != nil {
		return
	}
	snapshot := result.Snapshot
	for row := range min(height, snapshot.Rows) {
		for col := range min(width, snapshot.Cols) {
			cell := snapshot.Cells[row*snapshot.Cols+col]
			if cell.Width == 0 {
				continue
			}
			win.SetCell(col, row, vaxis.Cell{Character: vaxis.Character{Grapheme: cell.Grapheme, Width: cell.Width}, Style: daemonCellStyle(cell)})
		}
	}
	if text, err := t.formatLockedRaw(ReadVisible, ReadText); err == nil {
		t.snapshot = text
	}
	if t.focused && snapshot.CursorVisible && snapshot.CursorX >= 0 && snapshot.CursorX < width && snapshot.CursorY >= 0 && snapshot.CursorY < height {
		win.ShowCursor(snapshot.CursorX, snapshot.CursorY, vaxis.CursorBlock)
	}
	t.redrawPending = false
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
	t.generation++
	t.drainResponsesLockedRaw()
	t.invalidateLocked()
	if t.mouseEncoder != nil {
		t.mouseEncoder.Reset()
	}
	if t.pty != nil {
		_ = terminalio.Resize(t.pty, &unix.Winsize{Col: uint16(width), Row: uint16(height)})
		if t.shouldRequestPrimaryScreenRedrawLocked(oldWidth, width) {
			// Bash/readline prompt redraws can leave stale wrapped fragments after
			// width growth. Request another window-change redraw from the terminal
			// process group (the child is the session leader created by pty.Start)
			// without injecting Ctrl+L into application input.
			_ = syscall.Kill(-t.childPID, syscall.SIGWINCH)
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
	return t.hyperlinkAtLocked(row, col)
}

func (t *ghosttyTUITerminal) CapturesMouse() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed || t.term == nil {
		return false
	}
	return bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term))
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
	t.cachedFrameRows = nil
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
		C.ghostty_bridge_terminal_free(term)
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
	writer := t.ptyWriter
	ptyFile := t.pty
	ptyFD, descriptorErr := ghosttyPTYFileDescriptor(ptyFile)
	if descriptorErr != nil {
		t.mu.Unlock()
		return handoffTerminalState{Err: descriptorErr}
	}
	t.state = runtimeStateQuiescing
	childPID, cols, rows := t.childPID, t.cols, t.rows
	readPumpDone := t.readPumpDone
	t.mu.Unlock()
	if err := writer.Drain(timeout); err != nil {
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
	if readPumpDone != nil {
		timer := time.NewTimer(250 * time.Millisecond)
		timeoutC := timer.C
		waiting := true
		for waiting {
			select {
			case data := <-t.readCh:
				t.ingestPTY(data)
			case <-readPumpDone:
				waiting = false
			case <-timeoutC:
				// The writer is drained and the actor is quiescing. Closing the
				// original wrapper forces a tardy pump out without invalidating
				// the duplicated transfer/rollback descriptors; keep draining
				// until that exact pump generation acknowledges completion.
				writer.Close()
				writerClosed = true
				timeoutC = nil
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}
	if !writerClosed {
		writer.Close()
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
	replay := slices.Clone(t.replay)
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
	if cols <= 0 || rows <= 0 {
		term.Close()
		return nil, fmt.Errorf("adopt terminal with invalid geometry %dx%d", cols, rows)
	}
	// Construction initializes both the Go wrapper and Ghostty at 80x24. Resize
	// the emulator itself before replaying output; changing only the wrapper
	// fields makes snapshot rows use a different stride from Ghostty's cells.
	term.Resize(cols, rows)
	term.mu.Lock()
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
	return bool(C.ghostty_bridge_terminal_get_mode(t.term, C.int(mode), C.bool(isANSI)))
}

func (t *ghosttyTUITerminal) scrollbarLocked() (ghosttyScrollbarState, bool) {
	if t.term == nil {
		return ghosttyScrollbarState{}, false
	}
	return t.scrollbarLockedRaw()
}

func (t *ghosttyTUITerminal) viewportAtBottomLocked() bool {
	return t.viewportAtBottomLockedRaw()
}

func (t *ghosttyTUITerminal) defaultBackgroundRGBLocked() uint32 {
	if t.term == nil {
		return 0
	}
	C.ghostty_bridge_render_state_update(t.term)
	return uint32(C.ghostty_bridge_render_state_get_bg_color(t.term))
}

func (t *ghosttyTUITerminal) setColorThemeModeLocked(mode vaxis.ColorThemeMode) {
	if t.term == nil {
		return
	}
	C.ghostty_bridge_terminal_set_color_theme_mode(t.term, C.int(mode))
}

func (t *ghosttyTUITerminal) reportColorThemeModeLocked() {
	if t.term == nil {
		return
	}
	C.ghostty_bridge_terminal_report_color_theme_mode(t.term)
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
	if t.viewportAtBottomLockedRaw() {
		return
	}
	C.ghostty_bridge_terminal_scroll_viewport_bottom(t.term)
	t.generation++
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) scrollViewportDeltaLocked(delta int) {
	if t.term == nil || delta == 0 {
		return
	}
	C.ghostty_bridge_terminal_scroll_viewport_delta(t.term, C.intptr_t(delta))
	t.generation++
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
