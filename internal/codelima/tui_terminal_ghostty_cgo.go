//go:build cgo && (darwin || linux)

package codelima

/*
#cgo linux LDFLAGS: -ldl
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include <stdio.h>
#include <string.h>
#include <stdlib.h>
#include <dlfcn.h>

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

	LOAD_GHOSTTY_SYMBOL(terminal_new, "ghostty_terminal_new", GhosttyTerminal (*)(int, int));
	LOAD_GHOSTTY_SYMBOL(terminal_free, "ghostty_terminal_free", void (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_resize, "ghostty_terminal_resize", void (*)(GhosttyTerminal, int, int));
	LOAD_GHOSTTY_SYMBOL(terminal_write, "ghostty_terminal_write", void (*)(GhosttyTerminal, const uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(render_state_update, "ghostty_render_state_update", GhosttyDirty (*)(GhosttyTerminal));
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
	"unicode/utf8"
	"unsafe"

	"git.sr.ht/~rockorager/vaxis"
	"github.com/creack/pty"
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

func newGhosttyTUITerminal(nodeID string, postEvent func(vaxis.Event)) (tuiTerminal, error) {
	if err := loadGhosttyVT(); err != nil {
		return nil, err
	}

	term := C.ghostty_bridge_terminal_new(80, 24)
	if term == nil {
		return nil, fmt.Errorf("create ghostty terminal: %s", C.GoString(C.ghostty_bridge_last_error()))
	}

	return &ghosttyTUITerminal{
		nodeID:    nodeID,
		postEvent: postEvent,
		term:      term,
		cols:      80,
		rows:      24,
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
	nodeID    string
	postEvent func(vaxis.Event)
	term      C.GhosttyTerminal

	mu            sync.Mutex
	cmd           *exec.Cmd
	pty           *os.File
	cols          int
	rows          int
	focused       bool
	scrollOffset  int
	snapshot      string
	drawRows      []ghosttyRowSource
	closed        bool
	suppressEvent bool
	redrawPending bool
	waitOnce      sync.Once
	waitErr       error
	closeOnce     sync.Once
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

	C.ghostty_bridge_terminal_write(
		t.term,
		(*C.uint8_t)(unsafe.Pointer(&data[0])),
		C.size_t(len(data)),
	)
	t.drainResponsesLocked()

	if C.ghostty_bridge_terminal_is_alternate_screen(t.term) {
		t.scrollOffset = 0
	} else {
		t.clampScrollLocked()
	}
	t.invalidateLocked()
}

func (t *ghosttyTUITerminal) drainResponsesLocked() {
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
		t.writePTYLocked(encodeTUITerminalKey(
			event,
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

	if !bool(C.ghostty_bridge_terminal_has_mouse_tracking(t.term)) {
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

	maxOffset := int(C.ghostty_bridge_terminal_get_scrollback_length(t.term))
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
	if width != t.cols || height != t.rows {
		t.cols = width
		t.rows = height
		C.ghostty_bridge_terminal_resize(t.term, C.int(width), C.int(height))
		if t.pty != nil {
			_ = pty.Setsize(t.pty, &pty.Winsize{Cols: uint16(width), Rows: uint16(height)})
		}
		t.clampScrollLocked()
	}

	C.ghostty_bridge_render_state_update(t.term)

	viewport := make([]C.GhosttyCell, width*height)
	if len(viewport) > 0 {
		count := int(C.ghostty_bridge_render_state_get_viewport(
			t.term,
			(*C.GhosttyCell)(unsafe.Pointer(&viewport[0])),
			C.size_t(len(viewport)),
		))
		if count < 0 {
			return
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
}

func (t *ghosttyTUITerminal) drawCellsLocked(win vaxis.Window, row int, rowSource ghosttyRowSource, cells []C.GhosttyCell) string {
	var line strings.Builder
	for col := 0; col < t.cols; col++ {
		cell := cells[col]
		style := ghosttyCellStyle(cell)
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

func ghosttyCellStyle(cell C.GhosttyCell) vaxis.Style {
	style := vaxis.Style{
		Foreground: vaxis.RGBColor(uint8(cell.fg_r), uint8(cell.fg_g), uint8(cell.fg_b)),
		Background: vaxis.RGBColor(uint8(cell.bg_r), uint8(cell.bg_g), uint8(cell.bg_b)),
	}
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
		t.pty = nil
		t.term = nil
		t.mu.Unlock()

		if ptyFile != nil {
			_ = ptyFile.Close()
		}
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = t.wait()
		if term != nil {
			C.ghostty_bridge_terminal_free(term)
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
	return t.hyperlinkAtLocked(t.drawRows[row], col)
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
	postEvent := !t.suppressEvent
	t.term = nil
	t.pty = nil
	t.mu.Unlock()

	if term != nil {
		C.ghostty_bridge_terminal_free(term)
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
	return bool(C.ghostty_bridge_terminal_get_mode(t.term, C.int(mode), C.bool(isANSI)))
}

func (t *ghosttyTUITerminal) clampScrollLocked() {
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
