#ifndef CODELIMA_GHOSTTY_BRIDGE_COMPAT_H
#define CODELIMA_GHOSTTY_BRIDGE_COMPAT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>

typedef struct ghostty_bridge_terminal* GhosttyBridgeTerminal;

/* Bridge policy failures are separate from upstream result values. */
#define GHOSTTY_BRIDGE_UNSUPPORTED ((GhosttyResult)-1000)

typedef struct {
	uint32_t codepoint;
	uint8_t fg_r, fg_g, fg_b;
	uint8_t bg_r, bg_g, bg_b;
	uint8_t flags;
	uint8_t width;
	uint16_t hyperlink_id;
	uint8_t grapheme_len;
	uint8_t color_flags;
	uint8_t underline_style;
	bool overline;
} GhosttyResolvedCell;

typedef struct {
	GhosttyResolvedCell cell;
	size_t grapheme_offset;
	size_t grapheme_len;
	size_t hyperlink_offset;
	size_t hyperlink_len;
} GhosttyBridgeFrameCell;

typedef struct {
	bool active;
	uint16_t start_x;
	uint16_t end_x;
} GhosttyBridgeSelectionRow;

/* The frame retains every pointer, including UTF-8 and hyperlink bytes in text.
 * All returned buffers are immutable; the adapter may share them with its cache.
 * Native terminal mutation does not invalidate it. No string is NUL-terminated.
 * Frame publication, then clean, is the caller's responsibility. */
typedef struct {
	uint16_t cols;
	uint16_t rows;
	size_t count;
	GhosttyBridgeFrameCell* cells;
	uint8_t* text;
	size_t text_len;
	/* Read-only extraction counters for this frame; copies are not decodes. */
	size_t rows_decoded;
	size_t rows_reused;
	/* Private reference-counted ownership; release only with frame_free. */
	void* storage;
	uint8_t* dirty_rows;
	GhosttyBridgeSelectionRow* selection_rows;
	int dirty;
	GhosttyRenderStateCursor cursor;
	GhosttyRenderStateColors colors;
	GhosttyTerminalScrollbar scrollbar;
	bool captures_mouse;
	bool cursor_at_prompt;
} GhosttyBridgeFrame;

GhosttyResult ghostty_bridge_terminal_frame(GhosttyBridgeTerminal term, size_t max_bytes, GhosttyBridgeFrame* frame);
void ghostty_bridge_frame_free(GhosttyBridgeFrame* frame);

/* Read-only restored-native allocation accounting (NO_VALUE before restore).
 * Counts actual requested/rounded live heap and page allocations, not RSS. */
GhosttyResult ghostty_bridge_terminal_restore_memory(GhosttyBridgeTerminal term,
	size_t* used, size_t* peak, size_t* limit);

typedef enum {
	GHOSTTY_BRIDGE_SELECTION_PRESS = 0,
	GHOSTTY_BRIDGE_SELECTION_DRAG = 1,
	GHOSTTY_BRIDGE_SELECTION_RELEASE = 2,
	GHOSTTY_BRIDGE_SELECTION_CANCEL = 3
} GhosttyBridgeSelectionAction;

/* Coordinates must be in 0..65535; drag/release beyond the viewport clamps.
 * Behavior chooses cell/word/line/output using upstream's semantic engine. */
GhosttyResult ghostty_bridge_terminal_selection_event(GhosttyBridgeTerminal term,
	GhosttyBridgeSelectionAction action, int col, int row,
	GhosttySelectionGestureBehavior behavior, bool rectangle);
GhosttyResult ghostty_bridge_terminal_selection_format(GhosttyBridgeTerminal term,
	size_t max_bytes, uint8_t** out, size_t* out_len);

typedef struct {
	size_t total_matches;
	size_t selected_index;
	bool has_selected;
	bool caught_up;
} GhosttyBridgeSearchStatus;

/* Upstream matching is literal, ASCII case-insensitive; no regex or full
 * Unicode case folding. Empty query clears. Queries are limited to 4096 bytes.
 * Each tick performs at most 32 bounded upstream feed/tick steps. */
GhosttyResult ghostty_bridge_terminal_search_start(GhosttyBridgeTerminal term,
	const uint8_t* query, size_t len);
GhosttyResult ghostty_bridge_terminal_search_tick(GhosttyBridgeTerminal term,
	unsigned int max_steps, GhosttyBridgeSearchStatus* status);
GhosttyResult ghostty_bridge_terminal_search_move(GhosttyBridgeTerminal term,
	bool next, GhosttyBridgeSearchStatus* status);

typedef struct {
	uint32_t image_id;
	uint64_t generation;
	uint32_t width;
	uint32_t height;
	size_t rgba_offset;
	size_t rgba_len;
} GhosttyBridgeGraphicsAsset;

typedef struct {
	uint32_t image_id;
	uint32_t placement_id;
	size_t asset_index;
	int32_t z;
	uint32_t offset_x;
	uint32_t offset_y;
	GhosttyKittyGraphicsPlacementRenderInfo geometry;
} GhosttyBridgeGraphicsPlacement;

/* Static in-band images only; invisible and virtual placements are excluded.
 * Owned RGBA bytes are unpremultiplied. Generation invalidates image caches,
 * while placement geometry must be refreshed after scrolling/resizing too.
 * Bounds: 16MiB aggregate, 256 visible assets, 1024 visible placements. */
typedef struct {
	uint64_t generation;
	GhosttyBridgeGraphicsAsset* assets;
	size_t assets_len;
	GhosttyBridgeGraphicsPlacement* placements;
	size_t placements_len;
	uint8_t* rgba;
	size_t rgba_len;
} GhosttyBridgeGraphicsFrame;

GhosttyResult ghostty_bridge_terminal_graphics(GhosttyBridgeTerminal term,
	size_t max_bytes, GhosttyBridgeGraphicsFrame* frame);
void ghostty_bridge_graphics_frame_free(GhosttyBridgeGraphicsFrame* frame);
GhosttyResult ghostty_bridge_terminal_resize_pixels(GhosttyBridgeTerminal term,
	int cols, int rows, uint32_t cell_width, uint32_t cell_height);
GhosttyResult ghostty_bridge_terminal_cell_size(GhosttyBridgeTerminal term,
	uint32_t* cell_width, uint32_t* cell_height);
GhosttyResult ghostty_bridge_png_alloc(const GhosttyAllocator* allocator,
	uint32_t width, uint32_t height, GhosttySysImage* out);
void ghostty_bridge_png_free(const GhosttyAllocator* allocator, GhosttySysImage* image);

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

#define GHOSTTY_CELL_BG_DEFAULT (1 << 0)
#define GHOSTTY_CELL_FG_DEFAULT (1 << 1)

GhosttyResult ghostty_bridge_init(void);
const char* ghostty_bridge_build_identity(void);
const char* ghostty_bridge_last_error(void);
GhosttyResult ghostty_bridge_terminal_error(GhosttyBridgeTerminal term);
void ghostty_bridge_free(void* value);

enum {
	GHOSTTY_BRIDGE_EFFECT_CLIPBOARD = 1,
	GHOSTTY_BRIDGE_EFFECT_TITLE = 2,
	GHOSTTY_BRIDGE_EFFECT_PWD = 3,
	GHOSTTY_BRIDGE_EFFECT_BELL = 4,
	GHOSTTY_BRIDGE_EFFECT_NOTIFICATION = 5,
	GHOSTTY_BRIDGE_EFFECT_PROGRESS = 6
};

/* An effect owns its byte arrays after next_effect succeeds. Release both
 * arrays with effect_free; no native borrowed pointer escapes the callback. */
typedef struct {
	int kind;
	int location;
	uint8_t* data;
	size_t len;
	uint8_t* detail;
	size_t detail_len;
	int value;
} GhosttyBridgeEffect;

bool ghostty_bridge_terminal_next_effect(GhosttyBridgeTerminal term, GhosttyBridgeEffect* effect);
void ghostty_bridge_effect_free(GhosttyBridgeEffect* effect);
int ghostty_bridge_read_log(uint8_t* out, size_t capacity);
void ghostty_bridge_test_log(const uint8_t* message, size_t len);

/* Returned bytes are owned by the caller and released with bridge_free.
 * recent includes at most 2000 history rows plus the current visible area. */
GhosttyResult ghostty_bridge_terminal_format(GhosttyBridgeTerminal term, bool recent, bool ansi,
	size_t max_bytes, uint8_t** out, size_t* out_len);
GhosttyResult ghostty_bridge_terminal_paste(GhosttyBridgeTerminal term, const uint8_t* data,
	size_t len, bool allow_unsafe);
GhosttyResult ghostty_bridge_terminal_set_default_colors(GhosttyBridgeTerminal term,
	const GhosttyColorRgb* foreground, const GhosttyColorRgb* background,
	const GhosttyColorRgb* cursor, const GhosttyColorRgb* palette);
bool ghostty_bridge_terminal_cursor_at_prompt(GhosttyBridgeTerminal term);
GhosttyResult ghostty_bridge_terminal_compression_activity(GhosttyBridgeTerminal term, uint64_t* activity);
GhosttyResult ghostty_bridge_terminal_compress(GhosttyBridgeTerminal term, GhosttyTerminalCompressionResult* result);
GhosttyResult ghostty_bridge_terminal_checkpoint(GhosttyBridgeTerminal term, size_t max_bytes,
	uint8_t** out, size_t* out_len);
/* Restore validates a complete snapshot and atomically replaces core state.
 * Failure leaves the current terminal usable. Caller restores viewport/focus.
 * Existing encoder objects remain valid; set options from terminal again. */
GhosttyResult ghostty_bridge_terminal_restore(GhosttyBridgeTerminal term, const uint8_t* data, size_t len);

GhosttyBridgeTerminal ghostty_bridge_terminal_new(int cols, int rows);
void ghostty_bridge_terminal_free(GhosttyBridgeTerminal term);
void ghostty_bridge_terminal_resize(GhosttyBridgeTerminal term, int cols, int rows);
void ghostty_bridge_terminal_write(GhosttyBridgeTerminal term, const uint8_t* data, size_t len);

GhosttyDirty ghostty_bridge_render_state_update(GhosttyBridgeTerminal term);
uint32_t ghostty_bridge_render_state_get_bg_color(GhosttyBridgeTerminal term);
void ghostty_bridge_render_state_mark_clean(GhosttyBridgeTerminal term);

bool ghostty_bridge_terminal_is_alternate_screen(GhosttyBridgeTerminal term);
bool ghostty_bridge_terminal_has_mouse_tracking(GhosttyBridgeTerminal term);
bool ghostty_bridge_terminal_get_mode(GhosttyBridgeTerminal term, int mode, bool is_ansi);
void ghostty_bridge_terminal_set_color_theme_mode(GhosttyBridgeTerminal term, int mode);
void ghostty_bridge_terminal_report_color_theme_mode(GhosttyBridgeTerminal term);
bool ghostty_bridge_terminal_get_scrollbar(GhosttyBridgeTerminal term, GhosttyTerminalScrollbar* out_scrollbar);
void ghostty_bridge_terminal_scroll_viewport_top(GhosttyBridgeTerminal term);
void ghostty_bridge_terminal_scroll_viewport_bottom(GhosttyBridgeTerminal term);
void ghostty_bridge_terminal_scroll_viewport_delta(GhosttyBridgeTerminal term, intptr_t delta);
int ghostty_bridge_terminal_get_hyperlink_uri(GhosttyBridgeTerminal term, int row, int col, uint8_t* out_buffer, size_t buffer_size);
bool ghostty_bridge_terminal_has_response(GhosttyBridgeTerminal term);
int ghostty_bridge_terminal_read_response(GhosttyBridgeTerminal term, uint8_t* out_buffer, size_t buffer_size);

GhosttyResult ghostty_bridge_focus_encode(GhosttyFocusEvent event, char* out_buffer, size_t out_buffer_size, size_t* out_len);

GhosttyResult ghostty_bridge_key_encoder_new(GhosttyKeyEncoder* encoder);
void ghostty_bridge_key_encoder_free(GhosttyKeyEncoder encoder);
void ghostty_bridge_key_encoder_setopt_bool(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, bool value);
bool ghostty_bridge_key_encoder_setopt_from_terminal(GhosttyKeyEncoder encoder, GhosttyBridgeTerminal term);
GhosttyResult ghostty_bridge_key_encoder_encode_event(
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
);

GhosttyResult ghostty_bridge_mouse_encoder_new(GhosttyMouseEncoder* encoder);
void ghostty_bridge_mouse_encoder_free(GhosttyMouseEncoder encoder);
void ghostty_bridge_mouse_encoder_reset(GhosttyMouseEncoder encoder);
GhosttyResult ghostty_bridge_mouse_encoder_encode_event(
	GhosttyMouseEncoder encoder,
	GhosttyBridgeTerminal term,
	GhosttyMouseAction action,
	bool has_button,
	GhosttyMouseButton button,
	GhosttyMods mods,
	GhosttyMousePosition position,
	const GhosttyMouseEncoderSize* size,
	bool any_button_pressed,
	bool track_last_cell,
	char* out_buffer,
	size_t out_buffer_size,
	size_t* out_len
);

#endif
