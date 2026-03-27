#ifndef CODELIMA_GHOSTTY_BRIDGE_COMPAT_H
#define CODELIMA_GHOSTTY_BRIDGE_COMPAT_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>
#include <ghostty/vt.h>

typedef struct ghostty_bridge_terminal* GhosttyBridgeTerminal;

typedef struct {
	uint32_t codepoint;
	uint8_t fg_r, fg_g, fg_b;
	uint8_t bg_r, bg_g, bg_b;
	uint8_t flags;
	uint8_t width;
	uint16_t hyperlink_id;
	uint8_t grapheme_len;
	uint8_t _pad;
} GhosttyResolvedCell;

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

int ghostty_bridge_load(const char* path);
const char* ghostty_bridge_last_error(void);

GhosttyBridgeTerminal ghostty_bridge_terminal_new(int cols, int rows);
void ghostty_bridge_terminal_free(GhosttyBridgeTerminal term);
void ghostty_bridge_terminal_resize(GhosttyBridgeTerminal term, int cols, int rows);
void ghostty_bridge_terminal_write(GhosttyBridgeTerminal term, const uint8_t* data, size_t len);

GhosttyDirty ghostty_bridge_render_state_update(GhosttyBridgeTerminal term);
uint32_t ghostty_bridge_render_state_get_bg_color(GhosttyBridgeTerminal term);
bool ghostty_bridge_render_state_get_cursor_visible(GhosttyBridgeTerminal term);
int ghostty_bridge_render_state_get_cursor_x(GhosttyBridgeTerminal term);
int ghostty_bridge_render_state_get_cursor_y(GhosttyBridgeTerminal term);
void ghostty_bridge_render_state_mark_clean(GhosttyBridgeTerminal term);
int ghostty_bridge_render_state_get_viewport(GhosttyBridgeTerminal term, GhosttyResolvedCell* out_buffer, size_t buffer_size);
int ghostty_bridge_render_state_get_grapheme(GhosttyBridgeTerminal term, int row, int col, uint32_t* out_buffer, size_t buffer_size);

bool ghostty_bridge_terminal_is_alternate_screen(GhosttyBridgeTerminal term);
bool ghostty_bridge_terminal_has_mouse_tracking(GhosttyBridgeTerminal term);
bool ghostty_bridge_terminal_get_mode(GhosttyBridgeTerminal term, int mode, bool is_ansi);
int ghostty_bridge_terminal_get_scrollback_length(GhosttyBridgeTerminal term);
int ghostty_bridge_terminal_get_scrollback_line(GhosttyBridgeTerminal term, int offset, GhosttyResolvedCell* out_buffer, size_t buffer_size);
int ghostty_bridge_terminal_get_scrollback_grapheme(GhosttyBridgeTerminal term, int offset, int col, uint32_t* out_buffer, size_t buffer_size);
bool ghostty_bridge_terminal_is_row_wrapped(GhosttyBridgeTerminal term, int row);
int ghostty_bridge_terminal_get_hyperlink_uri(GhosttyBridgeTerminal term, int row, int col, uint8_t* out_buffer, size_t buffer_size);
int ghostty_bridge_terminal_get_scrollback_hyperlink_uri(GhosttyBridgeTerminal term, int offset, int col, uint8_t* out_buffer, size_t buffer_size);
bool ghostty_bridge_terminal_has_response(GhosttyBridgeTerminal term);
int ghostty_bridge_terminal_read_response(GhosttyBridgeTerminal term, uint8_t* out_buffer, size_t buffer_size);

bool ghostty_bridge_has_key_encoder_api(void);
bool ghostty_bridge_has_mouse_encoder_api(void);
bool ghostty_bridge_has_scroll_viewport_api(void);
bool ghostty_bridge_has_terminal_effects_api(void);
bool ghostty_bridge_has_render_row_cells_api(void);

GhosttyResult ghostty_bridge_key_encoder_new(GhosttyKeyEncoder* encoder);
void ghostty_bridge_key_encoder_free(GhosttyKeyEncoder encoder);
void ghostty_bridge_key_encoder_setopt_bool(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, bool value);
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

#endif
