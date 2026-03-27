//go:build cgo && (darwin || linux)

package codelima

/*
#cgo CFLAGS: -I${SRCDIR}/../../.tooling/ghostty-vt/current/include
#cgo linux LDFLAGS: -ldl
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <dlfcn.h>
#include <ghostty/vt/key/encoder.h>
#include <ghostty/vt/key/event.h>

typedef void* GhosttyTerminal;

typedef struct {
	uint32_t codepoint;
	uint8_t fg_r, fg_g, fg_b;
	uint8_t bg_r, bg_g, bg_b;
	uint8_t flags;
	uint8_t width;
	uint16_t hyperlink_id;
	uint8_t grapheme_len;
	uint8_t _pad;
} GhosttyCell;

typedef enum {
	GHOSTTY_DIRTY_NONE = 0,
	GHOSTTY_DIRTY_PARTIAL = 1,
	GHOSTTY_DIRTY_FULL = 2
} GhosttyDirty;

#define GHOSTTY_CELL_BOLD          (1 << 0)
#define GHOSTTY_CELL_ITALIC        (1 << 1)
#define GHOSTTY_CELL_UNDERLINE     (1 << 2)
#define GHOSTTY_CELL_STRIKETHROUGH (1 << 3)
#define GHOSTTY_CELL_INVERSE       (1 << 4)
#define GHOSTTY_CELL_INVISIBLE     (1 << 5)
#define GHOSTTY_CELL_BLINK         (1 << 6)
#define GHOSTTY_CELL_FAINT         (1 << 7)

typedef struct {
	void* handle;
	GhosttyTerminal (*terminal_new)(int cols, int rows);
	void (*terminal_free)(GhosttyTerminal term);
	void (*terminal_resize)(GhosttyTerminal term, int cols, int rows);
	void (*terminal_write)(GhosttyTerminal term, const uint8_t* data, size_t len);
	GhosttyDirty (*render_state_update)(GhosttyTerminal term);
	uint32_t (*render_state_get_bg_color)(GhosttyTerminal term);
	bool (*render_state_get_cursor_visible)(GhosttyTerminal term);
	int (*render_state_get_cursor_x)(GhosttyTerminal term);
	int (*render_state_get_cursor_y)(GhosttyTerminal term);
	void (*render_state_mark_clean)(GhosttyTerminal term);
	int (*render_state_get_viewport)(GhosttyTerminal term, GhosttyCell* out_buffer, size_t buffer_size);
	int (*render_state_get_grapheme)(GhosttyTerminal term, int row, int col, uint32_t* out_buffer, size_t buffer_size);
	bool (*terminal_is_alternate_screen)(GhosttyTerminal term);
	bool (*terminal_has_mouse_tracking)(GhosttyTerminal term);
	bool (*terminal_get_mode)(GhosttyTerminal term, int mode, bool is_ansi);
	int (*terminal_get_scrollback_length)(GhosttyTerminal term);
	int (*terminal_get_scrollback_line)(GhosttyTerminal term, int offset, GhosttyCell* out_buffer, size_t buffer_size);
	int (*terminal_get_scrollback_grapheme)(GhosttyTerminal term, int offset, int col, uint32_t* out_buffer, size_t buffer_size);
	bool (*terminal_is_row_wrapped)(GhosttyTerminal term, int row);
	int (*terminal_get_hyperlink_uri)(GhosttyTerminal term, int row, int col, uint8_t* out_buffer, size_t buffer_size);
	int (*terminal_get_scrollback_hyperlink_uri)(GhosttyTerminal term, int offset, int col, uint8_t* out_buffer, size_t buffer_size);
	bool (*terminal_has_response)(GhosttyTerminal term);
	int (*terminal_read_response)(GhosttyTerminal term, uint8_t* out_buffer, size_t buffer_size);
	GhosttyResult (*key_encoder_new)(const GhosttyAllocator* allocator, GhosttyKeyEncoder* encoder);
	void (*key_encoder_free)(GhosttyKeyEncoder encoder);
	void (*key_encoder_setopt)(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, const void* value);
	GhosttyResult (*key_encoder_encode)(GhosttyKeyEncoder encoder, GhosttyKeyEvent event, char* out_buffer, size_t out_buffer_size, size_t* out_len);
	GhosttyResult (*key_event_new)(const GhosttyAllocator* allocator, GhosttyKeyEvent* event);
	void (*key_event_free)(GhosttyKeyEvent event);
	void (*key_event_set_action)(GhosttyKeyEvent event, GhosttyKeyAction action);
	void (*key_event_set_key)(GhosttyKeyEvent event, GhosttyKey key);
	void (*key_event_set_mods)(GhosttyKeyEvent event, GhosttyMods mods);
	void (*key_event_set_consumed_mods)(GhosttyKeyEvent event, GhosttyMods consumed_mods);
	void (*key_event_set_utf8)(GhosttyKeyEvent event, const char* utf8, size_t len);
	void (*key_event_set_unshifted_codepoint)(GhosttyKeyEvent event, uint32_t codepoint);
} ghostty_api;

static ghostty_api ghostty;
static char ghostty_last_error[512];

static int ghostty_bridge_set_error(const char* message) {
	if (message == NULL) {
		message = "unknown ghostty error";
	}
	snprintf(ghostty_last_error, sizeof(ghostty_last_error), "%s", message);
	return 0;
}

static int ghostty_bridge_set_symbol_error(const char* symbol) {
	const char* err = dlerror();
	if (err == NULL) {
		err = "unknown symbol lookup error";
	}
	snprintf(ghostty_last_error, sizeof(ghostty_last_error), "load ghostty symbol %s: %s", symbol, err);
	return 0;
}

static int ghostty_bridge_load(const char* path) {
	if (ghostty.handle != NULL) {
		return 1;
	}

	void* handle = dlopen(path, RTLD_NOW | RTLD_LOCAL);
	if (handle == NULL) {
		return ghostty_bridge_set_error(dlerror());
	}

	memset(&ghostty, 0, sizeof(ghostty));
	ghostty.handle = handle;

	#define LOAD_GHOSTTY_SYMBOL(field, symbol, type) \
		do { \
			dlerror(); \
			ghostty.field = (type)dlsym(handle, symbol); \
			if (ghostty.field == NULL) { \
				dlclose(handle); \
				memset(&ghostty, 0, sizeof(ghostty)); \
				return ghostty_bridge_set_symbol_error(symbol); \
			} \
		} while (0)

	#define LOAD_GHOSTTY_OPTIONAL_SYMBOL(field, symbol, type) \
		do { \
			dlerror(); \
			ghostty.field = (type)dlsym(handle, symbol); \
			dlerror(); \
		} while (0)

	LOAD_GHOSTTY_SYMBOL(terminal_new, "ghostty_terminal_new", GhosttyTerminal (*)(int, int));
	LOAD_GHOSTTY_SYMBOL(terminal_free, "ghostty_terminal_free", void (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_resize, "ghostty_terminal_resize", void (*)(GhosttyTerminal, int, int));
	LOAD_GHOSTTY_SYMBOL(terminal_write, "ghostty_terminal_write", void (*)(GhosttyTerminal, const uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(render_state_update, "ghostty_render_state_update", GhosttyDirty (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get_bg_color, "ghostty_render_state_get_bg_color", uint32_t (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get_cursor_visible, "ghostty_render_state_get_cursor_visible", bool (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get_cursor_x, "ghostty_render_state_get_cursor_x", int (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get_cursor_y, "ghostty_render_state_get_cursor_y", int (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_mark_clean, "ghostty_render_state_mark_clean", void (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get_viewport, "ghostty_render_state_get_viewport", int (*)(GhosttyTerminal, GhosttyCell*, size_t));
	LOAD_GHOSTTY_SYMBOL(render_state_get_grapheme, "ghostty_render_state_get_grapheme", int (*)(GhosttyTerminal, int, int, uint32_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_is_alternate_screen, "ghostty_terminal_is_alternate_screen", bool (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_has_mouse_tracking, "ghostty_terminal_has_mouse_tracking", bool (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_get_mode, "ghostty_terminal_get_mode", bool (*)(GhosttyTerminal, int, bool));
	LOAD_GHOSTTY_SYMBOL(terminal_get_scrollback_length, "ghostty_terminal_get_scrollback_length", int (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_get_scrollback_line, "ghostty_terminal_get_scrollback_line", int (*)(GhosttyTerminal, int, GhosttyCell*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_get_scrollback_grapheme, "ghostty_terminal_get_scrollback_grapheme", int (*)(GhosttyTerminal, int, int, uint32_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_is_row_wrapped, "ghostty_terminal_is_row_wrapped", bool (*)(GhosttyTerminal, int));
	LOAD_GHOSTTY_SYMBOL(terminal_get_hyperlink_uri, "ghostty_terminal_get_hyperlink_uri", int (*)(GhosttyTerminal, int, int, uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_get_scrollback_hyperlink_uri, "ghostty_terminal_get_scrollback_hyperlink_uri", int (*)(GhosttyTerminal, int, int, uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_has_response, "ghostty_terminal_has_response", bool (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_read_response, "ghostty_terminal_read_response", int (*)(GhosttyTerminal, uint8_t*, size_t));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_new, "ghostty_key_encoder_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyKeyEncoder*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_free, "ghostty_key_encoder_free", void (*)(GhosttyKeyEncoder));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_setopt, "ghostty_key_encoder_setopt", void (*)(GhosttyKeyEncoder, GhosttyKeyEncoderOption, const void*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_encode, "ghostty_key_encoder_encode", GhosttyResult (*)(GhosttyKeyEncoder, GhosttyKeyEvent, char*, size_t, size_t*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_new, "ghostty_key_event_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyKeyEvent*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_free, "ghostty_key_event_free", void (*)(GhosttyKeyEvent));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_action, "ghostty_key_event_set_action", void (*)(GhosttyKeyEvent, GhosttyKeyAction));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_key, "ghostty_key_event_set_key", void (*)(GhosttyKeyEvent, GhosttyKey));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_mods, "ghostty_key_event_set_mods", void (*)(GhosttyKeyEvent, GhosttyMods));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_consumed_mods, "ghostty_key_event_set_consumed_mods", void (*)(GhosttyKeyEvent, GhosttyMods));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_utf8, "ghostty_key_event_set_utf8", void (*)(GhosttyKeyEvent, const char*, size_t));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_unshifted_codepoint, "ghostty_key_event_set_unshifted_codepoint", void (*)(GhosttyKeyEvent, uint32_t));

	#undef LOAD_GHOSTTY_OPTIONAL_SYMBOL
	#undef LOAD_GHOSTTY_SYMBOL
	ghostty_last_error[0] = '\0';
	return 1;
}

static const char* ghostty_bridge_last_error(void) {
	return ghostty_last_error;
}

static GhosttyTerminal ghostty_bridge_terminal_new(int cols, int rows) {
	return ghostty.terminal_new(cols, rows);
}

static void ghostty_bridge_terminal_free(GhosttyTerminal term) {
	ghostty.terminal_free(term);
}

static void ghostty_bridge_terminal_resize(GhosttyTerminal term, int cols, int rows) {
	ghostty.terminal_resize(term, cols, rows);
}

static void ghostty_bridge_terminal_write(GhosttyTerminal term, const uint8_t* data, size_t len) {
	ghostty.terminal_write(term, data, len);
}

static GhosttyDirty ghostty_bridge_render_state_update(GhosttyTerminal term) {
	return ghostty.render_state_update(term);
}

static uint32_t ghostty_bridge_render_state_get_bg_color(GhosttyTerminal term) {
	return ghostty.render_state_get_bg_color(term);
}

static bool ghostty_bridge_render_state_get_cursor_visible(GhosttyTerminal term) {
	return ghostty.render_state_get_cursor_visible(term);
}

static int ghostty_bridge_render_state_get_cursor_x(GhosttyTerminal term) {
	return ghostty.render_state_get_cursor_x(term);
}

static int ghostty_bridge_render_state_get_cursor_y(GhosttyTerminal term) {
	return ghostty.render_state_get_cursor_y(term);
}

static void ghostty_bridge_render_state_mark_clean(GhosttyTerminal term) {
	ghostty.render_state_mark_clean(term);
}

static int ghostty_bridge_render_state_get_viewport(GhosttyTerminal term, GhosttyCell* out_buffer, size_t buffer_size) {
	return ghostty.render_state_get_viewport(term, out_buffer, buffer_size);
}

static int ghostty_bridge_render_state_get_grapheme(GhosttyTerminal term, int row, int col, uint32_t* out_buffer, size_t buffer_size) {
	return ghostty.render_state_get_grapheme(term, row, col, out_buffer, buffer_size);
}

static bool ghostty_bridge_terminal_is_alternate_screen(GhosttyTerminal term) {
	return ghostty.terminal_is_alternate_screen(term);
}

static bool ghostty_bridge_terminal_has_mouse_tracking(GhosttyTerminal term) {
	return ghostty.terminal_has_mouse_tracking(term);
}

static bool ghostty_bridge_terminal_get_mode(GhosttyTerminal term, int mode, bool is_ansi) {
	return ghostty.terminal_get_mode(term, mode, is_ansi);
}

static int ghostty_bridge_terminal_get_scrollback_length(GhosttyTerminal term) {
	return ghostty.terminal_get_scrollback_length(term);
}

static int ghostty_bridge_terminal_get_scrollback_line(GhosttyTerminal term, int offset, GhosttyCell* out_buffer, size_t buffer_size) {
	return ghostty.terminal_get_scrollback_line(term, offset, out_buffer, buffer_size);
}

static int ghostty_bridge_terminal_get_scrollback_grapheme(GhosttyTerminal term, int offset, int col, uint32_t* out_buffer, size_t buffer_size) {
	return ghostty.terminal_get_scrollback_grapheme(term, offset, col, out_buffer, buffer_size);
}

static bool ghostty_bridge_terminal_is_row_wrapped(GhosttyTerminal term, int row) {
	return ghostty.terminal_is_row_wrapped(term, row);
}

static int ghostty_bridge_terminal_get_hyperlink_uri(GhosttyTerminal term, int row, int col, uint8_t* out_buffer, size_t buffer_size) {
	return ghostty.terminal_get_hyperlink_uri(term, row, col, out_buffer, buffer_size);
}

static int ghostty_bridge_terminal_get_scrollback_hyperlink_uri(GhosttyTerminal term, int offset, int col, uint8_t* out_buffer, size_t buffer_size) {
	return ghostty.terminal_get_scrollback_hyperlink_uri(term, offset, col, out_buffer, buffer_size);
}

static bool ghostty_bridge_terminal_has_response(GhosttyTerminal term) {
	return ghostty.terminal_has_response(term);
}

static int ghostty_bridge_terminal_read_response(GhosttyTerminal term, uint8_t* out_buffer, size_t buffer_size) {
	return ghostty.terminal_read_response(term, out_buffer, buffer_size);
}

static bool ghostty_bridge_has_key_encoder_api(void) {
	return ghostty.key_encoder_new != NULL &&
		ghostty.key_encoder_free != NULL &&
		ghostty.key_encoder_setopt != NULL &&
		ghostty.key_encoder_encode != NULL &&
		ghostty.key_event_new != NULL &&
		ghostty.key_event_free != NULL &&
		ghostty.key_event_set_action != NULL &&
		ghostty.key_event_set_key != NULL &&
		ghostty.key_event_set_mods != NULL &&
		ghostty.key_event_set_consumed_mods != NULL &&
		ghostty.key_event_set_utf8 != NULL &&
		ghostty.key_event_set_unshifted_codepoint != NULL;
}

static GhosttyResult ghostty_bridge_key_encoder_new(GhosttyKeyEncoder* encoder) {
	if (!ghostty_bridge_has_key_encoder_api()) {
		return GHOSTTY_INVALID_VALUE;
	}
	return ghostty.key_encoder_new(NULL, encoder);
}

static void ghostty_bridge_key_encoder_free(GhosttyKeyEncoder encoder) {
	if (ghostty.key_encoder_free != NULL) {
		ghostty.key_encoder_free(encoder);
	}
}

static void ghostty_bridge_key_encoder_setopt_bool(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, bool value) {
	if (ghostty.key_encoder_setopt == NULL) {
		return;
	}
	ghostty.key_encoder_setopt(encoder, option, &value);
}

static GhosttyResult ghostty_bridge_key_encoder_encode_event(
	GhosttyKeyEncoder encoder,
	GhosttyKeyAction action,
	GhosttyKey key,
	GhosttyMods mods,
	const char* utf8,
	size_t utf8_len,
	uint32_t unshifted_codepoint,
	char* out_buffer,
	size_t out_buffer_size,
	size_t* out_len
) {
	if (!ghostty_bridge_has_key_encoder_api()) {
		return GHOSTTY_INVALID_VALUE;
	}

	GhosttyKeyEvent event = NULL;
	GhosttyResult result = ghostty.key_event_new(NULL, &event);
	if (result != GHOSTTY_SUCCESS || event == NULL) {
		return result;
	}

	ghostty.key_event_set_action(event, action);
	ghostty.key_event_set_key(event, key);
	ghostty.key_event_set_mods(event, mods);
	ghostty.key_event_set_consumed_mods(event, 0);
	if (utf8 != NULL && utf8_len > 0) {
		ghostty.key_event_set_utf8(event, utf8, utf8_len);
	}
	if (unshifted_codepoint != 0) {
		ghostty.key_event_set_unshifted_codepoint(event, unshifted_codepoint);
	}

	result = ghostty.key_encoder_encode(encoder, event, out_buffer, out_buffer_size, out_len);
	ghostty.key_event_free(event);
	return result;
}
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

type ghosttyRowSource struct {
	scrollback bool
	index      int
}

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

	term := withGhosttyStderrSuppressed(func() C.GhosttyTerminal {
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
	nodeID     string
	postEvent  func(vaxis.Event)
	term       C.GhosttyTerminal
	keyEncoder *ghosttyKeyEncoder

	mu                   sync.Mutex
	cmd                  *exec.Cmd
	pty                  *os.File
	cols                 int
	rows                 int
	focused              bool
	scrollOffset         int
	snapshot             string
	drawRows             []ghosttyRowSource
	defaultBackgroundRGB uint32
	closed               bool
	suppressEvent        bool
	redrawPending        bool
	waitOnce             sync.Once
	waitErr              error
	closeOnce            sync.Once
}

type ghosttyKeyEncoder struct {
	encoder C.GhosttyKeyEncoder
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

	t.mu.Lock()
	t.cmd = cmd
	t.pty = ptyFile
	t.mu.Unlock()

	go t.readLoop()
	return nil
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

		if C.ghostty_bridge_terminal_is_alternate_screen(t.term) {
			t.scrollOffset = 0
		} else {
			t.clampScrollLockedRaw()
		}
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
	if t.pty == nil || t.term == nil {
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
		_, _ = t.pty.Write(buffer[:n])
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
		t.scrollOffset = 0
		t.writePTYLocked(encodeTUITerminalKeyWithGhostty(
			event,
			t.keyEncoder,
			t.getModeLocked(ghosttyModeApplicationKeypad, false),
			t.getModeLocked(ghosttyModeCursorKeys, false),
		))
	case vaxis.PasteStartEvent:
		if t.getModeLocked(ghosttyModeBracketedPaste, false) {
			t.scrollOffset = 0
			t.writePTYLocked("\x1B[200~")
		}
	case vaxis.PasteEndEvent:
		if t.getModeLocked(ghosttyModeBracketedPaste, false) {
			t.scrollOffset = 0
			t.writePTYLocked("\x1B[201~")
		}
	case vaxis.Mouse:
		t.handleMouseLocked(event)
	case vaxis.ColorThemeUpdate:
		if t.getModeLocked(ghosttyModeColorScheme, false) {
			t.writePTYLocked(fmt.Sprintf("\x1b[?997;%dn", event.Mode))
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

	encoded := encodeTUITerminalMouse(
		event,
		t.getModeLocked(ghosttyModeMouseSGR, false),
		t.getModeLocked(ghosttyModeMouseDrag, false) || t.getModeLocked(ghosttyModeMouseMotion, false),
		t.getModeLocked(ghosttyModeMouseMotion, false),
	)
	t.writePTYLocked(encoded)
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

	maxOffset := withGhosttyStderrSuppressed(func() int {
		return int(C.ghostty_bridge_terminal_get_scrollback_length(t.term))
	})
	if maxOffset <= 0 && t.scrollOffset == 0 {
		return false
	}

	const scrollStep = 3
	switch button {
	case vaxis.MouseWheelUp:
		if maxOffset == 0 {
			return false
		}
		t.scrollOffset += scrollStep
		if t.scrollOffset > maxOffset {
			t.scrollOffset = maxOffset
		}
	case vaxis.MouseWheelDown:
		t.scrollOffset -= scrollStep
		if t.scrollOffset < 0 {
			t.scrollOffset = 0
		}
	default:
		return false
	}

	t.invalidateLocked()
	return true
}

func (t *ghosttyTUITerminal) writePTYLocked(value string) {
	if value == "" || t.pty == nil {
		return
	}
	_, _ = io.WriteString(t.pty, value)
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
		if width != t.cols || height != t.rows {
			t.cols = width
			t.rows = height
			C.ghostty_bridge_terminal_resize(t.term, C.int(width), C.int(height))
			if t.pty != nil {
				_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
			}
			t.clampScrollLockedRaw()
		}

		C.ghostty_bridge_render_state_update(t.term)
		t.defaultBackgroundRGB = uint32(C.ghostty_bridge_render_state_get_bg_color(t.term))

		viewport := make([]C.GhosttyCell, width*height)
		if len(viewport) > 0 {
			count := int(C.ghostty_bridge_render_state_get_viewport(
				t.term,
				(*C.GhosttyCell)(unsafe.Pointer(&viewport[0])),
				C.size_t(len(viewport)),
			))
			if count < 0 {
				return false
			}
		}

		scrollbackLength := int(C.ghostty_bridge_terminal_get_scrollback_length(t.term))
		if bool(C.ghostty_bridge_terminal_is_alternate_screen(t.term)) {
			t.scrollOffset = 0
		} else if t.scrollOffset > scrollbackLength {
			t.scrollOffset = scrollbackLength
		}

		rows := make([]ghosttyRowSource, 0, height)
		totalLines := scrollbackLength + height
		start := totalLines - height - t.scrollOffset
		if start < 0 {
			start = 0
		}

		scrollbackRowBuffer := make([]C.GhosttyCell, width)
		lineTexts := make([]string, 0, height)
		for visibleRow := 0; visibleRow < height; visibleRow++ {
			virtualRow := start + visibleRow
			rowSource := ghosttyRowSource{}
			var cells []C.GhosttyCell
			if virtualRow < scrollbackLength {
				rowSource = ghosttyRowSource{scrollback: true, index: virtualRow}
				count := int(C.ghostty_bridge_terminal_get_scrollback_line(
					t.term,
					C.int(virtualRow),
					(*C.GhosttyCell)(unsafe.Pointer(&scrollbackRowBuffer[0])),
					C.size_t(len(scrollbackRowBuffer)),
				))
				if count < 0 {
					continue
				}
				cells = scrollbackRowBuffer
			} else {
				rowSource = ghosttyRowSource{index: virtualRow - scrollbackLength}
				offset := rowSource.index * width
				if offset+width > len(viewport) {
					continue
				}
				cells = viewport[offset : offset+width]
			}

			lineTexts = append(lineTexts, t.drawCellsLocked(win, visibleRow, rowSource, cells))
			rows = append(rows, rowSource)
		}

		t.drawRows = rows
		t.snapshot = strings.Join(lineTexts, "\n")

		if t.focused && t.scrollOffset == 0 && bool(C.ghostty_bridge_render_state_get_cursor_visible(t.term)) {
			cursorRow := int(C.ghostty_bridge_render_state_get_cursor_y(t.term))
			cursorCol := int(C.ghostty_bridge_render_state_get_cursor_x(t.term))
			for rowIndex, rowSource := range rows {
				if rowSource.scrollback || rowSource.index != cursorRow {
					continue
				}
				win.ShowCursor(cursorCol, rowIndex, vaxis.CursorBlock)
				break
			}
		}

		C.ghostty_bridge_render_state_mark_clean(t.term)
		t.redrawPending = false
		return true
	}) {
		return
	}
}

func (t *ghosttyTUITerminal) drawCellsLocked(win vaxis.Window, row int, rowSource ghosttyRowSource, cells []C.GhosttyCell) string {
	var line strings.Builder
	for col := 0; col < t.cols; col++ {
		cell := cells[col]
		style := ghosttyCellStyle(cell, t.defaultBackgroundRGB)
		if cell.hyperlink_id != 0 {
			if target, ok := t.hyperlinkAtLocked(rowSource, col); ok {
				style.Hyperlink = target
				if style.UnderlineStyle == 0 {
					style.UnderlineStyle = vaxis.UnderlineSingle
				}
			}
		}

		grapheme := t.graphemeLocked(rowSource, col, cell)
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

func ghosttyCellStyle(cell C.GhosttyCell, defaultBackgroundRGB uint32) vaxis.Style {
	style := ghosttyStyleForColors(
		ghosttyRGB(uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)),
		ghosttyRGB(uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)),
		defaultBackgroundRGB,
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

func ghosttyStyleForColors(foregroundRGB, backgroundRGB, defaultBackgroundRGB uint32) vaxis.Style {
	style := vaxis.Style{
		Foreground: vaxis.HexColor(foregroundRGB),
	}
	if backgroundRGB != defaultBackgroundRGB {
		style.Background = vaxis.HexColor(backgroundRGB)
	}
	return style
}

func ghosttyRGB(r, g, b uint8) uint32 {
	return uint32(r)<<16 | uint32(g)<<8 | uint32(b)
}

func (t *ghosttyTUITerminal) graphemeLocked(rowSource ghosttyRowSource, col int, cell C.GhosttyCell) string {
	if cell.grapheme_len == 0 {
		if cell.codepoint == 0 {
			return " "
		}
		return string(rune(cell.codepoint))
	}

	codepoints := make([]C.uint32_t, int(cell.grapheme_len)+1)
	var count int
	if rowSource.scrollback {
		count = int(C.ghostty_bridge_terminal_get_scrollback_grapheme(
			t.term,
			C.int(rowSource.index),
			C.int(col),
			(*C.uint32_t)(unsafe.Pointer(&codepoints[0])),
			C.size_t(len(codepoints)),
		))
	} else {
		count = int(C.ghostty_bridge_render_state_get_grapheme(
			t.term,
			C.int(rowSource.index),
			C.int(col),
			(*C.uint32_t)(unsafe.Pointer(&codepoints[0])),
			C.size_t(len(codepoints)),
		))
	}
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
		cmd := t.cmd
		term := t.term
		keyEncoder := t.keyEncoder
		t.pty = nil
		t.term = nil
		t.keyEncoder = nil
		t.mu.Unlock()

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

	if row < 0 || row >= len(t.drawRows) || col < 0 || col >= t.cols || t.term == nil {
		return "", false
	}
	return withGhosttyStderrSuppressed(func() hyperlinkResult {
		target, ok := t.hyperlinkAtLocked(t.drawRows[row], col)
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

func (t *ghosttyTUITerminal) hyperlinkAtLocked(rowSource ghosttyRowSource, col int) (string, bool) {
	buffer := make([]byte, 2048)
	var n int
	if rowSource.scrollback {
		n = int(C.ghostty_bridge_terminal_get_scrollback_hyperlink_uri(
			t.term,
			C.int(rowSource.index),
			C.int(col),
			(*C.uint8_t)(unsafe.Pointer(&buffer[0])),
			C.size_t(len(buffer)),
		))
	} else {
		n = int(C.ghostty_bridge_terminal_get_hyperlink_uri(
			t.term,
			C.int(rowSource.index),
			C.int(col),
			(*C.uint8_t)(unsafe.Pointer(&buffer[0])),
			C.size_t(len(buffer)),
		))
	}
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
	keyEncoder := t.keyEncoder
	postEvent := !t.suppressEvent
	t.term = nil
	t.pty = nil
	t.keyEncoder = nil
	t.mu.Unlock()

	if term != nil {
		withGhosttyStderrSuppressed(func() struct{} {
			C.ghostty_bridge_terminal_free(term)
			return struct{}{}
		})
	}
	if keyEncoder != nil {
		keyEncoder.Close()
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

func (t *ghosttyTUITerminal) clampScrollLocked() {
	withGhosttyStderrSuppressed(func() struct{} {
		t.clampScrollLockedRaw()
		return struct{}{}
	})
}

func (t *ghosttyTUITerminal) clampScrollLockedRaw() {
	if t.term == nil {
		t.scrollOffset = 0
		return
	}
	maxOffset := int(C.ghostty_bridge_terminal_get_scrollback_length(t.term))
	if t.scrollOffset > maxOffset {
		t.scrollOffset = maxOffset
	}
	if t.scrollOffset < 0 {
		t.scrollOffset = 0
	}
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
