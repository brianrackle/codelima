#include "ghostty_bridge_compat.h"

#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CODELIMA_GHOSTTY_SCROLLBACK_LINES 10000

struct ghostty_bridge_terminal {
	GhosttyTerminal terminal;
	GhosttyRenderState render_state;
	GhosttyRenderStateRowIterator row_iter;
	GhosttyRenderStateRowCells row_cells;
	uint8_t* response_buf;
	size_t response_len;
	size_t response_cap;
};

typedef struct {
	void* handle;
	GhosttyResult (*terminal_new)(const GhosttyAllocator*, GhosttyTerminal*, GhosttyTerminalOptions);
	void (*terminal_free)(GhosttyTerminal);
	GhosttyResult (*terminal_resize)(GhosttyTerminal, uint16_t, uint16_t, uint32_t, uint32_t);
	GhosttyResult (*terminal_set)(GhosttyTerminal, GhosttyTerminalOption, const void*);
	void (*terminal_vt_write)(GhosttyTerminal, const uint8_t*, size_t);
	void (*terminal_scroll_viewport)(GhosttyTerminal, GhosttyTerminalScrollViewport);
	GhosttyResult (*terminal_mode_get)(GhosttyTerminal, GhosttyMode, bool*);
	GhosttyResult (*terminal_get)(GhosttyTerminal, GhosttyTerminalData, void*);
	GhosttyResult (*terminal_grid_ref)(GhosttyTerminal, GhosttyPoint, GhosttyGridRef*);
	int (*terminal_get_hyperlink_uri)(GhosttyTerminal, int, int, uint8_t*, size_t);
	int (*terminal_get_scrollback_hyperlink_uri)(GhosttyTerminal, int, int, uint8_t*, size_t);
	GhosttyResult (*render_state_new)(const GhosttyAllocator*, GhosttyRenderState*);
	void (*render_state_free)(GhosttyRenderState);
	GhosttyResult (*render_state_update)(GhosttyRenderState, GhosttyTerminal);
	GhosttyResult (*render_state_get)(GhosttyRenderState, GhosttyRenderStateData, void*);
	GhosttyResult (*render_state_set)(GhosttyRenderState, GhosttyRenderStateOption, const void*);
	GhosttyResult (*render_state_colors_get)(GhosttyRenderState, GhosttyRenderStateColors*);
	GhosttyResult (*render_state_row_iterator_new)(const GhosttyAllocator*, GhosttyRenderStateRowIterator*);
	void (*render_state_row_iterator_free)(GhosttyRenderStateRowIterator);
	bool (*render_state_row_iterator_next)(GhosttyRenderStateRowIterator);
	GhosttyResult (*render_state_row_get)(GhosttyRenderStateRowIterator, GhosttyRenderStateRowData, void*);
	GhosttyResult (*render_state_row_set)(GhosttyRenderStateRowIterator, GhosttyRenderStateRowOption, const void*);
	GhosttyResult (*render_state_row_cells_new)(const GhosttyAllocator*, GhosttyRenderStateRowCells*);
	void (*render_state_row_cells_free)(GhosttyRenderStateRowCells);
	GhosttyResult (*render_state_row_cells_select)(GhosttyRenderStateRowCells, uint16_t);
	GhosttyResult (*render_state_row_cells_get)(GhosttyRenderStateRowCells, GhosttyRenderStateRowCellsData, void*);
	GhosttyResult (*cell_get)(GhosttyCell, GhosttyCellData, void*);
	GhosttyResult (*row_get)(GhosttyRow, GhosttyRowData, void*);
	GhosttyResult (*grid_ref_cell)(const GhosttyGridRef*, GhosttyCell*);
	GhosttyResult (*grid_ref_row)(const GhosttyGridRef*, GhosttyRow*);
	GhosttyResult (*grid_ref_graphemes)(const GhosttyGridRef*, uint32_t*, size_t, size_t*);
	GhosttyResult (*grid_ref_style)(const GhosttyGridRef*, GhosttyStyle*);
	GhosttyResult (*key_encoder_new)(const GhosttyAllocator*, GhosttyKeyEncoder*);
	void (*key_encoder_free)(GhosttyKeyEncoder);
	void (*key_encoder_setopt)(GhosttyKeyEncoder, GhosttyKeyEncoderOption, const void*);
	void (*key_encoder_setopt_from_terminal)(GhosttyKeyEncoder, GhosttyTerminal);
	GhosttyResult (*key_encoder_encode)(GhosttyKeyEncoder, GhosttyKeyEvent, char*, size_t, size_t*);
	GhosttyResult (*key_event_new)(const GhosttyAllocator*, GhosttyKeyEvent*);
	void (*key_event_free)(GhosttyKeyEvent);
	void (*key_event_set_action)(GhosttyKeyEvent, GhosttyKeyAction);
	void (*key_event_set_key)(GhosttyKeyEvent, GhosttyKey);
	void (*key_event_set_mods)(GhosttyKeyEvent, GhosttyMods);
	void (*key_event_set_consumed_mods)(GhosttyKeyEvent, GhosttyMods);
	void (*key_event_set_utf8)(GhosttyKeyEvent, const char*, size_t);
	void (*key_event_set_unshifted_codepoint)(GhosttyKeyEvent, uint32_t);
	GhosttyResult (*mouse_encoder_new)(const GhosttyAllocator*, GhosttyMouseEncoder*);
	void (*mouse_encoder_free)(GhosttyMouseEncoder);
	void (*mouse_encoder_setopt)(GhosttyMouseEncoder, GhosttyMouseEncoderOption, const void*);
	void (*mouse_encoder_setopt_from_terminal)(GhosttyMouseEncoder, GhosttyTerminal);
	void (*mouse_encoder_reset)(GhosttyMouseEncoder);
	GhosttyResult (*mouse_encoder_encode)(GhosttyMouseEncoder, GhosttyMouseEvent, char*, size_t, size_t*);
	GhosttyResult (*mouse_event_new)(const GhosttyAllocator*, GhosttyMouseEvent*);
	void (*mouse_event_free)(GhosttyMouseEvent);
	void (*mouse_event_set_action)(GhosttyMouseEvent, GhosttyMouseAction);
	void (*mouse_event_set_button)(GhosttyMouseEvent, GhosttyMouseButton);
	void (*mouse_event_clear_button)(GhosttyMouseEvent);
	void (*mouse_event_set_mods)(GhosttyMouseEvent, GhosttyMods);
	void (*mouse_event_set_position)(GhosttyMouseEvent, GhosttyMousePosition);
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

static int ghostty_bridge_set_result_error(const char* action, GhosttyResult result) {
	snprintf(ghostty_last_error, sizeof(ghostty_last_error), "%s failed: %d", action, (int)result);
	return 0;
}

static int ghostty_bridge_result_ok(GhosttyResult result) {
	return result == GHOSTTY_SUCCESS;
}

static int ghostty_bridge_response_reserve(struct ghostty_bridge_terminal* bridge, size_t extra) {
	if (bridge == NULL || extra == 0) {
		return 1;
	}
	size_t needed = bridge->response_len + extra;
	if (needed <= bridge->response_cap) {
		return 1;
	}
	size_t cap = bridge->response_cap == 0 ? 256 : bridge->response_cap;
	while (cap < needed) {
		cap *= 2;
	}
	uint8_t* buf = (uint8_t*)realloc(bridge->response_buf, cap);
	if (buf == NULL) {
		return 0;
	}
	bridge->response_buf = buf;
	bridge->response_cap = cap;
	return 1;
}

static void ghostty_bridge_write_pty_cb(GhosttyTerminal terminal, void* userdata, const uint8_t* data, size_t len) {
	(void)terminal;
	if (userdata == NULL || data == NULL || len == 0) {
		return;
	}
	struct ghostty_bridge_terminal* bridge = (struct ghostty_bridge_terminal*)userdata;
	if (!ghostty_bridge_response_reserve(bridge, len)) {
		return;
	}
	memcpy(bridge->response_buf + bridge->response_len, data, len);
	bridge->response_len += len;
}

int ghostty_bridge_load(const char* path) {
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

	LOAD_GHOSTTY_SYMBOL(terminal_new, "ghostty_terminal_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyTerminal*, GhosttyTerminalOptions));
	LOAD_GHOSTTY_SYMBOL(terminal_free, "ghostty_terminal_free", void (*)(GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(terminal_resize, "ghostty_terminal_resize", GhosttyResult (*)(GhosttyTerminal, uint16_t, uint16_t, uint32_t, uint32_t));
	LOAD_GHOSTTY_SYMBOL(terminal_set, "ghostty_terminal_set", GhosttyResult (*)(GhosttyTerminal, GhosttyTerminalOption, const void*));
	LOAD_GHOSTTY_SYMBOL(terminal_vt_write, "ghostty_terminal_vt_write", void (*)(GhosttyTerminal, const uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_scroll_viewport, "ghostty_terminal_scroll_viewport", void (*)(GhosttyTerminal, GhosttyTerminalScrollViewport));
	LOAD_GHOSTTY_SYMBOL(terminal_mode_get, "ghostty_terminal_mode_get", GhosttyResult (*)(GhosttyTerminal, GhosttyMode, bool*));
	LOAD_GHOSTTY_SYMBOL(terminal_get, "ghostty_terminal_get", GhosttyResult (*)(GhosttyTerminal, GhosttyTerminalData, void*));
	LOAD_GHOSTTY_SYMBOL(terminal_grid_ref, "ghostty_terminal_grid_ref", GhosttyResult (*)(GhosttyTerminal, GhosttyPoint, GhosttyGridRef*));
	LOAD_GHOSTTY_SYMBOL(terminal_get_hyperlink_uri, "ghostty_terminal_get_hyperlink_uri", int (*)(GhosttyTerminal, int, int, uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(terminal_get_scrollback_hyperlink_uri, "ghostty_terminal_get_scrollback_hyperlink_uri", int (*)(GhosttyTerminal, int, int, uint8_t*, size_t));
	LOAD_GHOSTTY_SYMBOL(render_state_new, "ghostty_render_state_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyRenderState*));
	LOAD_GHOSTTY_SYMBOL(render_state_free, "ghostty_render_state_free", void (*)(GhosttyRenderState));
	LOAD_GHOSTTY_SYMBOL(render_state_update, "ghostty_render_state_update", GhosttyResult (*)(GhosttyRenderState, GhosttyTerminal));
	LOAD_GHOSTTY_SYMBOL(render_state_get, "ghostty_render_state_get", GhosttyResult (*)(GhosttyRenderState, GhosttyRenderStateData, void*));
	LOAD_GHOSTTY_SYMBOL(render_state_set, "ghostty_render_state_set", GhosttyResult (*)(GhosttyRenderState, GhosttyRenderStateOption, const void*));
	LOAD_GHOSTTY_SYMBOL(render_state_colors_get, "ghostty_render_state_colors_get", GhosttyResult (*)(GhosttyRenderState, GhosttyRenderStateColors*));
	LOAD_GHOSTTY_SYMBOL(render_state_row_iterator_new, "ghostty_render_state_row_iterator_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyRenderStateRowIterator*));
	LOAD_GHOSTTY_SYMBOL(render_state_row_iterator_free, "ghostty_render_state_row_iterator_free", void (*)(GhosttyRenderStateRowIterator));
	LOAD_GHOSTTY_SYMBOL(render_state_row_iterator_next, "ghostty_render_state_row_iterator_next", bool (*)(GhosttyRenderStateRowIterator));
	LOAD_GHOSTTY_SYMBOL(render_state_row_get, "ghostty_render_state_row_get", GhosttyResult (*)(GhosttyRenderStateRowIterator, GhosttyRenderStateRowData, void*));
	LOAD_GHOSTTY_SYMBOL(render_state_row_set, "ghostty_render_state_row_set", GhosttyResult (*)(GhosttyRenderStateRowIterator, GhosttyRenderStateRowOption, const void*));
	LOAD_GHOSTTY_SYMBOL(render_state_row_cells_new, "ghostty_render_state_row_cells_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyRenderStateRowCells*));
	LOAD_GHOSTTY_SYMBOL(render_state_row_cells_free, "ghostty_render_state_row_cells_free", void (*)(GhosttyRenderStateRowCells));
	LOAD_GHOSTTY_SYMBOL(render_state_row_cells_select, "ghostty_render_state_row_cells_select", GhosttyResult (*)(GhosttyRenderStateRowCells, uint16_t));
	LOAD_GHOSTTY_SYMBOL(render_state_row_cells_get, "ghostty_render_state_row_cells_get", GhosttyResult (*)(GhosttyRenderStateRowCells, GhosttyRenderStateRowCellsData, void*));
	LOAD_GHOSTTY_SYMBOL(cell_get, "ghostty_cell_get", GhosttyResult (*)(GhosttyCell, GhosttyCellData, void*));
	LOAD_GHOSTTY_SYMBOL(row_get, "ghostty_row_get", GhosttyResult (*)(GhosttyRow, GhosttyRowData, void*));
	LOAD_GHOSTTY_SYMBOL(grid_ref_cell, "ghostty_grid_ref_cell", GhosttyResult (*)(const GhosttyGridRef*, GhosttyCell*));
	LOAD_GHOSTTY_SYMBOL(grid_ref_row, "ghostty_grid_ref_row", GhosttyResult (*)(const GhosttyGridRef*, GhosttyRow*));
	LOAD_GHOSTTY_SYMBOL(grid_ref_graphemes, "ghostty_grid_ref_graphemes", GhosttyResult (*)(const GhosttyGridRef*, uint32_t*, size_t, size_t*));
	LOAD_GHOSTTY_SYMBOL(grid_ref_style, "ghostty_grid_ref_style", GhosttyResult (*)(const GhosttyGridRef*, GhosttyStyle*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_new, "ghostty_key_encoder_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyKeyEncoder*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_free, "ghostty_key_encoder_free", void (*)(GhosttyKeyEncoder));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_setopt, "ghostty_key_encoder_setopt", void (*)(GhosttyKeyEncoder, GhosttyKeyEncoderOption, const void*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_setopt_from_terminal, "ghostty_key_encoder_setopt_from_terminal", void (*)(GhosttyKeyEncoder, GhosttyTerminal));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_encoder_encode, "ghostty_key_encoder_encode", GhosttyResult (*)(GhosttyKeyEncoder, GhosttyKeyEvent, char*, size_t, size_t*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_new, "ghostty_key_event_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyKeyEvent*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_free, "ghostty_key_event_free", void (*)(GhosttyKeyEvent));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_action, "ghostty_key_event_set_action", void (*)(GhosttyKeyEvent, GhosttyKeyAction));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_key, "ghostty_key_event_set_key", void (*)(GhosttyKeyEvent, GhosttyKey));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_mods, "ghostty_key_event_set_mods", void (*)(GhosttyKeyEvent, GhosttyMods));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_consumed_mods, "ghostty_key_event_set_consumed_mods", void (*)(GhosttyKeyEvent, GhosttyMods));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_utf8, "ghostty_key_event_set_utf8", void (*)(GhosttyKeyEvent, const char*, size_t));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(key_event_set_unshifted_codepoint, "ghostty_key_event_set_unshifted_codepoint", void (*)(GhosttyKeyEvent, uint32_t));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_new, "ghostty_mouse_encoder_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyMouseEncoder*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_free, "ghostty_mouse_encoder_free", void (*)(GhosttyMouseEncoder));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_setopt, "ghostty_mouse_encoder_setopt", void (*)(GhosttyMouseEncoder, GhosttyMouseEncoderOption, const void*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_setopt_from_terminal, "ghostty_mouse_encoder_setopt_from_terminal", void (*)(GhosttyMouseEncoder, GhosttyTerminal));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_reset, "ghostty_mouse_encoder_reset", void (*)(GhosttyMouseEncoder));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_encoder_encode, "ghostty_mouse_encoder_encode", GhosttyResult (*)(GhosttyMouseEncoder, GhosttyMouseEvent, char*, size_t, size_t*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_new, "ghostty_mouse_event_new", GhosttyResult (*)(const GhosttyAllocator*, GhosttyMouseEvent*));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_free, "ghostty_mouse_event_free", void (*)(GhosttyMouseEvent));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_set_action, "ghostty_mouse_event_set_action", void (*)(GhosttyMouseEvent, GhosttyMouseAction));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_set_button, "ghostty_mouse_event_set_button", void (*)(GhosttyMouseEvent, GhosttyMouseButton));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_clear_button, "ghostty_mouse_event_clear_button", void (*)(GhosttyMouseEvent));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_set_mods, "ghostty_mouse_event_set_mods", void (*)(GhosttyMouseEvent, GhosttyMods));
	LOAD_GHOSTTY_OPTIONAL_SYMBOL(mouse_event_set_position, "ghostty_mouse_event_set_position", void (*)(GhosttyMouseEvent, GhosttyMousePosition));

	#undef LOAD_GHOSTTY_OPTIONAL_SYMBOL
	#undef LOAD_GHOSTTY_SYMBOL

	ghostty_last_error[0] = '\0';
	return 1;
}

const char* ghostty_bridge_last_error(void) {
	return ghostty_last_error;
}

static uint32_t ghostty_bridge_pack_rgb(GhosttyColorRgb rgb) {
	return ((uint32_t)rgb.r << 16) | ((uint32_t)rgb.g << 8) | (uint32_t)rgb.b;
}

static GhosttyPoint ghostty_bridge_make_point(GhosttyPointTag tag, int row, int col) {
	GhosttyPoint point;
	memset(&point, 0, sizeof(point));
	point.tag = tag;
	point.value.coordinate.x = (uint16_t)col;
	point.value.coordinate.y = (uint32_t)row;
	return point;
}

static GhosttyColorRgb ghostty_bridge_palette_color(const GhosttyRenderStateColors* colors, uint8_t index) {
	if (colors == NULL) {
		GhosttyColorRgb zero = {0};
		return zero;
	}
	return colors->palette[index];
}

static GhosttyColorRgb ghostty_bridge_resolve_style_color(const GhosttyStyleColor* color, const GhosttyRenderStateColors* colors, GhosttyColorRgb fallback) {
	if (color == NULL) {
		return fallback;
	}
	switch (color->tag) {
	case GHOSTTY_STYLE_COLOR_RGB:
		return color->value.rgb;
	case GHOSTTY_STYLE_COLOR_PALETTE:
		return ghostty_bridge_palette_color(colors, color->value.palette);
	case GHOSTTY_STYLE_COLOR_NONE:
	default:
		return fallback;
	}
}

static bool ghostty_bridge_style_color_is_default(const GhosttyStyleColor* color) {
	return color == NULL || color->tag == GHOSTTY_STYLE_COLOR_NONE;
}

static GhosttyColorRgb ghostty_bridge_resolve_fg_from_style(const GhosttyStyle* style, const GhosttyRenderStateColors* colors) {
	return ghostty_bridge_resolve_style_color(style == NULL ? NULL : &style->fg_color, colors, colors->foreground);
}

static bool ghostty_bridge_bg_is_default(GhosttyCell raw, const GhosttyStyle* style) {
	GhosttyCellContentTag tag = GHOSTTY_CELL_CONTENT_CODEPOINT;
	if (!ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_CONTENT_TAG, &tag))) {
		return true;
	}
	switch (tag) {
	case GHOSTTY_CELL_CONTENT_BG_COLOR_PALETTE:
	case GHOSTTY_CELL_CONTENT_BG_COLOR_RGB:
		return false;
	default:
		return ghostty_bridge_style_color_is_default(style == NULL ? NULL : &style->bg_color);
	}
}

static GhosttyColorRgb ghostty_bridge_resolve_bg_from_cell(GhosttyCell raw, const GhosttyStyle* style, const GhosttyRenderStateColors* colors) {
	GhosttyCellContentTag tag = GHOSTTY_CELL_CONTENT_CODEPOINT;
	if (!ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_CONTENT_TAG, &tag))) {
		return colors->background;
	}
	switch (tag) {
	case GHOSTTY_CELL_CONTENT_BG_COLOR_PALETTE: {
		GhosttyColorPaletteIndex index = 0;
		if (ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_COLOR_PALETTE, &index))) {
			return ghostty_bridge_palette_color(colors, index);
		}
		return colors->background;
	}
	case GHOSTTY_CELL_CONTENT_BG_COLOR_RGB: {
		GhosttyColorRgb rgb = {0};
		if (ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_COLOR_RGB, &rgb))) {
			return rgb;
		}
		return colors->background;
	}
	default:
		return ghostty_bridge_resolve_style_color(style == NULL ? NULL : &style->bg_color, colors, colors->background);
	}
}

static uint8_t ghostty_bridge_style_flags(const GhosttyStyle* style) {
	if (style == NULL) {
		return 0;
	}
	uint8_t flags = 0;
	if (style->bold) flags |= GHOSTTY_CELL_BOLD;
	if (style->italic) flags |= GHOSTTY_CELL_ITALIC;
	if (style->underline != 0) flags |= GHOSTTY_CELL_UNDERLINE;
	if (style->strikethrough) flags |= GHOSTTY_CELL_STRIKETHROUGH;
	if (style->inverse) flags |= GHOSTTY_CELL_INVERSE;
	if (style->invisible) flags |= GHOSTTY_CELL_INVISIBLE;
	if (style->blink) flags |= GHOSTTY_CELL_BLINK;
	if (style->faint) flags |= GHOSTTY_CELL_FAINT;
	return flags;
}

static uint8_t ghostty_bridge_cell_width(GhosttyCell raw) {
	GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
	if (!ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide))) {
		return 1;
	}
	switch (wide) {
	case GHOSTTY_CELL_WIDE_WIDE:
		return 2;
	case GHOSTTY_CELL_WIDE_SPACER_TAIL:
	case GHOSTTY_CELL_WIDE_SPACER_HEAD:
		return 0;
	case GHOSTTY_CELL_WIDE_NARROW:
	default:
		return 1;
	}
}

static uint8_t ghostty_bridge_cell_hyperlink_id(GhosttyCell raw) {
	bool has_hyperlink = false;
	if (!ghostty_bridge_result_ok(ghostty.cell_get(raw, GHOSTTY_CELL_DATA_HAS_HYPERLINK, &has_hyperlink))) {
		return 0;
	}
	return has_hyperlink ? 1 : 0;
}

static uint8_t ghostty_bridge_grapheme_extra_len_from_ref(const GhosttyGridRef* ref) {
	size_t grapheme_len = 0;
	GhosttyResult result = ghostty.grid_ref_graphemes(ref, NULL, 0, &grapheme_len);
	if (result != GHOSTTY_SUCCESS && result != GHOSTTY_OUT_OF_SPACE) {
		return 0;
	}
	return grapheme_len > 0 ? (uint8_t)(grapheme_len - 1) : 0;
}

static void ghostty_bridge_resolved_cell_init(GhosttyResolvedCell* out) {
	memset(out, 0, sizeof(*out));
	out->width = 1;
	out->color_flags = GHOSTTY_CELL_BG_DEFAULT | GHOSTTY_CELL_FG_DEFAULT;
}

static void ghostty_bridge_fill_cell_base(GhosttyCell raw, const GhosttyStyle* style, GhosttyResolvedCell* out) {
	uint32_t codepoint = 0;
	ghostty_bridge_resolved_cell_init(out);
	(void)ghostty.cell_get(raw, GHOSTTY_CELL_DATA_CODEPOINT, &codepoint);
	out->codepoint = codepoint;
	out->flags = ghostty_bridge_style_flags(style);
	out->width = ghostty_bridge_cell_width(raw);
	out->hyperlink_id = ghostty_bridge_cell_hyperlink_id(raw);
}

static int ghostty_bridge_get_render_colors(struct ghostty_bridge_terminal* bridge, GhosttyRenderStateColors* out_colors) {
	if (bridge == NULL || out_colors == NULL) {
		return 0;
	}
	*out_colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
	return ghostty_bridge_result_ok(ghostty.render_state_colors_get(bridge->render_state, out_colors));
}

GhosttyBridgeTerminal ghostty_bridge_terminal_new(int cols, int rows) {
	if (cols <= 0 || rows <= 0) {
		ghostty_bridge_set_error("terminal dimensions must be positive");
		return NULL;
	}

	struct ghostty_bridge_terminal* bridge = (struct ghostty_bridge_terminal*)calloc(1, sizeof(*bridge));
	if (bridge == NULL) {
		ghostty_bridge_set_error("allocate ghostty terminal bridge");
		return NULL;
	}

	GhosttyTerminalOptions opts = {
		.cols = (uint16_t)cols,
		.rows = (uint16_t)rows,
		.max_scrollback = CODELIMA_GHOSTTY_SCROLLBACK_LINES,
	};
	GhosttyResult result = ghostty.terminal_new(NULL, &bridge->terminal, opts);
	if (!ghostty_bridge_result_ok(result) || bridge->terminal == NULL) {
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty terminal", result);
		return NULL;
	}

	result = ghostty.render_state_new(NULL, &bridge->render_state);
	if (!ghostty_bridge_result_ok(result) || bridge->render_state == NULL) {
		ghostty.terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty render state", result);
		return NULL;
	}

	result = ghostty.render_state_row_iterator_new(NULL, &bridge->row_iter);
	if (!ghostty_bridge_result_ok(result) || bridge->row_iter == NULL) {
		ghostty.render_state_free(bridge->render_state);
		ghostty.terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty row iterator", result);
		return NULL;
	}

	result = ghostty.render_state_row_cells_new(NULL, &bridge->row_cells);
	if (!ghostty_bridge_result_ok(result) || bridge->row_cells == NULL) {
		ghostty.render_state_row_iterator_free(bridge->row_iter);
		ghostty.render_state_free(bridge->render_state);
		ghostty.terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty row cells", result);
		return NULL;
	}

	result = ghostty.terminal_set(bridge->terminal, GHOSTTY_TERMINAL_OPT_USERDATA, bridge);
	if (!ghostty_bridge_result_ok(result)) {
		ghostty_bridge_terminal_free(bridge);
		ghostty_bridge_set_result_error("set ghostty userdata callback", result);
		return NULL;
	}

	result = ghostty.terminal_set(bridge->terminal, GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void*)&ghostty_bridge_write_pty_cb);
	if (!ghostty_bridge_result_ok(result)) {
		ghostty_bridge_terminal_free(bridge);
		ghostty_bridge_set_result_error("set ghostty write_pty callback", result);
		return NULL;
	}

	ghostty_last_error[0] = '\0';
	return bridge;
}

void ghostty_bridge_terminal_free(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL) {
		return;
	}
	free(bridge->response_buf);
	if (bridge->row_cells != NULL) {
		ghostty.render_state_row_cells_free(bridge->row_cells);
	}
	if (bridge->row_iter != NULL) {
		ghostty.render_state_row_iterator_free(bridge->row_iter);
	}
	if (bridge->render_state != NULL) {
		ghostty.render_state_free(bridge->render_state);
	}
	if (bridge->terminal != NULL) {
		ghostty.terminal_free(bridge->terminal);
	}
	free(bridge);
}

void ghostty_bridge_terminal_resize(GhosttyBridgeTerminal term, int cols, int rows) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || cols <= 0 || rows <= 0) {
		return;
	}
	(void)ghostty.terminal_resize(bridge->terminal, (uint16_t)cols, (uint16_t)rows, 0, 0);
}

void ghostty_bridge_terminal_write(GhosttyBridgeTerminal term, const uint8_t* data, size_t len) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || data == NULL || len == 0) {
		return;
	}
	ghostty.terminal_vt_write(bridge->terminal, data, len);
}

GhosttyDirty ghostty_bridge_render_state_update(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || bridge->render_state == NULL) {
		return GHOSTTY_DIRTY_NONE;
	}
	if (!ghostty_bridge_result_ok(ghostty.render_state_update(bridge->render_state, bridge->terminal))) {
		return GHOSTTY_DIRTY_NONE;
	}
	GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty))) {
		return GHOSTTY_DIRTY_NONE;
	}
	switch (dirty) {
	case GHOSTTY_RENDER_STATE_DIRTY_FULL:
		return GHOSTTY_DIRTY_FULL;
	case GHOSTTY_RENDER_STATE_DIRTY_PARTIAL:
		return GHOSTTY_DIRTY_PARTIAL;
	case GHOSTTY_RENDER_STATE_DIRTY_FALSE:
	default:
		return GHOSTTY_DIRTY_NONE;
	}
}

uint32_t ghostty_bridge_render_state_get_bg_color(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	GhosttyRenderStateColors colors;
	if (bridge == NULL || !ghostty_bridge_get_render_colors(bridge, &colors)) {
		return 0;
	}
	return ghostty_bridge_pack_rgb(colors.background);
}

bool ghostty_bridge_render_state_get_cursor_visible(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->render_state == NULL) {
		return false;
	}
	bool visible = false;
	return ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VISIBLE, &visible)) && visible;
}

static int ghostty_bridge_render_state_cursor_has_value(struct ghostty_bridge_terminal* bridge) {
	if (bridge == NULL || bridge->render_state == NULL) {
		return 0;
	}
	bool has_value = false;
	return ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_HAS_VALUE, &has_value)) && has_value;
}

int ghostty_bridge_render_state_get_cursor_x(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (!ghostty_bridge_render_state_cursor_has_value(bridge)) {
		return -1;
	}
	uint16_t x = 0;
	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_X, &x))) {
		return -1;
	}
	return (int)x;
}

int ghostty_bridge_render_state_get_cursor_y(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (!ghostty_bridge_render_state_cursor_has_value(bridge)) {
		return -1;
	}
	uint16_t y = 0;
	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_CURSOR_VIEWPORT_Y, &y))) {
		return -1;
	}
	return (int)y;
}

void ghostty_bridge_render_state_mark_clean(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->render_state == NULL) {
		return;
	}
	GhosttyRenderStateDirty clean = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
	(void)ghostty.render_state_set(bridge->render_state, GHOSTTY_RENDER_STATE_OPTION_DIRTY, &clean);
	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &bridge->row_iter))) {
		return;
	}
	while (ghostty.render_state_row_iterator_next(bridge->row_iter)) {
		bool dirty = false;
		(void)ghostty.render_state_row_set(bridge->row_iter, GHOSTTY_RENDER_STATE_ROW_OPTION_DIRTY, &dirty);
	}
}

static int ghostty_bridge_fill_viewport_cell(struct ghostty_bridge_terminal* bridge, const GhosttyRenderStateColors* colors, GhosttyResolvedCell* out) {
	GhosttyCell raw = 0;
	GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
	GhosttyColorRgb fg = colors->foreground;
	GhosttyColorRgb bg = colors->background;
	uint32_t grapheme_len = 0;
	bool fg_default = true;
	bool bg_default = true;

	if (!ghostty_bridge_result_ok(ghostty.render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw))) {
		return 0;
	}
	(void)ghostty.render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
	fg_default = !ghostty_bridge_result_ok(ghostty.render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fg));
	bg_default = !ghostty_bridge_result_ok(ghostty.render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg));
	(void)ghostty.render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &grapheme_len);

	ghostty_bridge_fill_cell_base(raw, &style, out);
	out->color_flags = 0;
	if (fg_default) out->color_flags |= GHOSTTY_CELL_FG_DEFAULT;
	if (bg_default) out->color_flags |= GHOSTTY_CELL_BG_DEFAULT;
	out->fg_r = fg.r;
	out->fg_g = fg.g;
	out->fg_b = fg.b;
	out->bg_r = bg.r;
	out->bg_g = bg.g;
	out->bg_b = bg.b;
	out->grapheme_len = grapheme_len > 0 ? (uint8_t)(grapheme_len - 1) : 0;
	return 1;
}

int ghostty_bridge_render_state_get_viewport(GhosttyBridgeTerminal term, GhosttyResolvedCell* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->render_state == NULL || out_buffer == NULL) {
		return -1;
	}

	uint16_t cols = 0;
	uint16_t rows = 0;
	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_COLS, &cols)) ||
		!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_ROWS, &rows))) {
		return -1;
	}
	if (buffer_size < (size_t)cols * (size_t)rows) {
		return -1;
	}

	GhosttyRenderStateColors colors;
	if (!ghostty_bridge_get_render_colors(bridge, &colors)) {
		return -1;
	}

	if (!ghostty_bridge_result_ok(ghostty.render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &bridge->row_iter))) {
		return -1;
	}

	size_t count = 0;
	while (ghostty.render_state_row_iterator_next(bridge->row_iter)) {
		if (!ghostty_bridge_result_ok(ghostty.render_state_row_get(bridge->row_iter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &bridge->row_cells))) {
			return -1;
		}
		for (uint16_t x = 0; x < cols; x++) {
			if (!ghostty_bridge_result_ok(ghostty.render_state_row_cells_select(bridge->row_cells, x))) {
				return -1;
			}
			if (!ghostty_bridge_fill_viewport_cell(bridge, &colors, &out_buffer[count])) {
				return -1;
			}
			count++;
		}
	}

	return (int)count;
}

static int ghostty_bridge_grid_ref_grapheme(const GhosttyGridRef* ref, uint32_t* out_buffer, size_t buffer_size) {
	size_t out_len = 0;
	GhosttyResult result = ghostty.grid_ref_graphemes(ref, out_buffer, buffer_size, &out_len);
	if (result != GHOSTTY_SUCCESS) {
		return -1;
	}
	return (int)out_len;
}

int ghostty_bridge_render_state_get_grapheme(GhosttyBridgeTerminal term, int row, int col, uint32_t* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || out_buffer == NULL) {
		return -1;
	}
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	if (!ghostty_bridge_result_ok(ghostty.terminal_grid_ref(bridge->terminal, ghostty_bridge_make_point(GHOSTTY_POINT_TAG_VIEWPORT, row, col), &ref))) {
		return -1;
	}
	return ghostty_bridge_grid_ref_grapheme(&ref, out_buffer, buffer_size);
}

bool ghostty_bridge_terminal_is_alternate_screen(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL) {
		return false;
	}
	GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	return ghostty_bridge_result_ok(ghostty.terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen)) &&
		screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

bool ghostty_bridge_terminal_has_mouse_tracking(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL) {
		return false;
	}
	bool tracking = false;
	return ghostty_bridge_result_ok(ghostty.terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &tracking)) && tracking;
}

bool ghostty_bridge_terminal_get_mode(GhosttyBridgeTerminal term, int mode, bool is_ansi) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || mode < 0) {
		return false;
	}
	bool value = false;
	GhosttyMode packed = ghostty_mode_new((uint16_t)mode, is_ansi);
	if (!ghostty_bridge_result_ok(ghostty.terminal_mode_get(bridge->terminal, packed, &value))) {
		return false;
	}
	return value;
}

bool ghostty_bridge_terminal_get_scrollbar(GhosttyBridgeTerminal term, GhosttyTerminalScrollbar* out_scrollbar) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || out_scrollbar == NULL) {
		return false;
	}
	memset(out_scrollbar, 0, sizeof(*out_scrollbar));
	return ghostty_bridge_result_ok(
		ghostty.terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, out_scrollbar)
	);
}

static void ghostty_bridge_terminal_scroll_viewport(struct ghostty_bridge_terminal* bridge, GhosttyTerminalScrollViewportTag tag, intptr_t delta) {
	if (bridge == NULL || bridge->terminal == NULL || ghostty.terminal_scroll_viewport == NULL) {
		return;
	}
	GhosttyTerminalScrollViewport behavior;
	memset(&behavior, 0, sizeof(behavior));
	behavior.tag = tag;
	if (tag == GHOSTTY_SCROLL_VIEWPORT_DELTA) {
		behavior.value.delta = delta;
	}
	ghostty.terminal_scroll_viewport(bridge->terminal, behavior);
}

void ghostty_bridge_terminal_scroll_viewport_top(GhosttyBridgeTerminal term) {
	ghostty_bridge_terminal_scroll_viewport(term, GHOSTTY_SCROLL_VIEWPORT_TOP, 0);
}

void ghostty_bridge_terminal_scroll_viewport_bottom(GhosttyBridgeTerminal term) {
	ghostty_bridge_terminal_scroll_viewport(term, GHOSTTY_SCROLL_VIEWPORT_BOTTOM, 0);
}

void ghostty_bridge_terminal_scroll_viewport_delta(GhosttyBridgeTerminal term, intptr_t delta) {
	ghostty_bridge_terminal_scroll_viewport(term, GHOSTTY_SCROLL_VIEWPORT_DELTA, delta);
}

int ghostty_bridge_terminal_get_scrollback_length(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL) {
		return 0;
	}
	size_t rows = 0;
	if (!ghostty_bridge_result_ok(ghostty.terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBACK_ROWS, &rows))) {
		return 0;
	}
	return (int)rows;
}

static int ghostty_bridge_fill_scrollback_cell(struct ghostty_bridge_terminal* bridge, const GhosttyRenderStateColors* colors, const GhosttyGridRef* ref, GhosttyResolvedCell* out) {
	GhosttyCell raw = 0;
	GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
	if (!ghostty_bridge_result_ok(ghostty.grid_ref_cell(ref, &raw))) {
		return 0;
	}
	if (!ghostty_bridge_result_ok(ghostty.grid_ref_style(ref, &style))) {
		return 0;
	}
	ghostty_bridge_fill_cell_base(raw, &style, out);
	out->color_flags = 0;
	if (ghostty_bridge_style_color_is_default(&style.fg_color)) out->color_flags |= GHOSTTY_CELL_FG_DEFAULT;
	if (ghostty_bridge_bg_is_default(raw, &style)) out->color_flags |= GHOSTTY_CELL_BG_DEFAULT;
	GhosttyColorRgb fg = ghostty_bridge_resolve_fg_from_style(&style, colors);
	GhosttyColorRgb bg = ghostty_bridge_resolve_bg_from_cell(raw, &style, colors);
	out->fg_r = fg.r;
	out->fg_g = fg.g;
	out->fg_b = fg.b;
	out->bg_r = bg.r;
	out->bg_g = bg.g;
	out->bg_b = bg.b;
	out->grapheme_len = ghostty_bridge_grapheme_extra_len_from_ref(ref);
	return 1;
}

int ghostty_bridge_terminal_get_scrollback_line(GhosttyBridgeTerminal term, int offset, GhosttyResolvedCell* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || out_buffer == NULL || offset < 0) {
		return -1;
	}

	uint16_t cols_raw = 0;
	if (!ghostty_bridge_result_ok(ghostty.terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_COLS, &cols_raw))) {
		return -1;
	}
	size_t cols = (size_t)cols_raw;
	if (buffer_size < cols) {
		return -1;
	}

	GhosttyRenderStateColors colors;
	if (!ghostty_bridge_get_render_colors(bridge, &colors)) {
		return -1;
	}

	for (size_t x = 0; x < cols; x++) {
		GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
		if (!ghostty_bridge_result_ok(ghostty.terminal_grid_ref(bridge->terminal, ghostty_bridge_make_point(GHOSTTY_POINT_TAG_HISTORY, offset, (int)x), &ref))) {
			return -1;
		}
		if (!ghostty_bridge_fill_scrollback_cell(bridge, &colors, &ref, &out_buffer[x])) {
			return -1;
		}
	}

	return (int)cols;
}

int ghostty_bridge_terminal_get_scrollback_grapheme(GhosttyBridgeTerminal term, int offset, int col, uint32_t* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || out_buffer == NULL || offset < 0 || col < 0) {
		return -1;
	}
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	if (!ghostty_bridge_result_ok(ghostty.terminal_grid_ref(bridge->terminal, ghostty_bridge_make_point(GHOSTTY_POINT_TAG_HISTORY, offset, col), &ref))) {
		return -1;
	}
	return ghostty_bridge_grid_ref_grapheme(&ref, out_buffer, buffer_size);
}

bool ghostty_bridge_terminal_is_row_wrapped(GhosttyBridgeTerminal term, int row) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || row < 0) {
		return false;
	}
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	if (!ghostty_bridge_result_ok(ghostty.terminal_grid_ref(bridge->terminal, ghostty_bridge_make_point(GHOSTTY_POINT_TAG_ACTIVE, row, 0), &ref))) {
		return false;
	}
	GhosttyRow raw_row = 0;
	if (!ghostty_bridge_result_ok(ghostty.grid_ref_row(&ref, &raw_row))) {
		return false;
	}
	bool wrapped = false;
	return ghostty_bridge_result_ok(ghostty.row_get(raw_row, GHOSTTY_ROW_DATA_WRAP_CONTINUATION, &wrapped)) && wrapped;
}

int ghostty_bridge_terminal_get_hyperlink_uri(GhosttyBridgeTerminal term, int row, int col, uint8_t* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || ghostty.terminal_get_hyperlink_uri == NULL) {
		return -1;
	}
	return ghostty.terminal_get_hyperlink_uri(bridge->terminal, row, col, out_buffer, buffer_size);
}

int ghostty_bridge_terminal_get_scrollback_hyperlink_uri(GhosttyBridgeTerminal term, int offset, int col, uint8_t* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || ghostty.terminal_get_scrollback_hyperlink_uri == NULL) {
		return -1;
	}
	return ghostty.terminal_get_scrollback_hyperlink_uri(bridge->terminal, offset, col, out_buffer, buffer_size);
}

bool ghostty_bridge_terminal_has_response(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	return bridge != NULL && bridge->response_len > 0;
}

int ghostty_bridge_terminal_read_response(GhosttyBridgeTerminal term, uint8_t* out_buffer, size_t buffer_size) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || out_buffer == NULL) {
		return -1;
	}
	size_t len = bridge->response_len < buffer_size ? bridge->response_len : buffer_size;
	if (len == 0) {
		return 0;
	}
	memcpy(out_buffer, bridge->response_buf, len);
	if (len < bridge->response_len) {
		memmove(bridge->response_buf, bridge->response_buf + len, bridge->response_len - len);
	}
	bridge->response_len -= len;
	return (int)len;
}

bool ghostty_bridge_has_key_encoder_api(void) {
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

bool ghostty_bridge_has_mouse_encoder_api(void) {
	return ghostty.mouse_encoder_new != NULL &&
		ghostty.mouse_encoder_free != NULL &&
		ghostty.mouse_encoder_setopt != NULL &&
		ghostty.mouse_encoder_setopt_from_terminal != NULL &&
		ghostty.mouse_encoder_reset != NULL &&
		ghostty.mouse_encoder_encode != NULL &&
		ghostty.mouse_event_new != NULL &&
		ghostty.mouse_event_free != NULL &&
		ghostty.mouse_event_set_action != NULL &&
		ghostty.mouse_event_set_button != NULL &&
		ghostty.mouse_event_clear_button != NULL &&
		ghostty.mouse_event_set_mods != NULL &&
		ghostty.mouse_event_set_position != NULL;
}

bool ghostty_bridge_has_scroll_viewport_api(void) {
	return ghostty.terminal_scroll_viewport != NULL;
}

bool ghostty_bridge_has_terminal_effects_api(void) {
	return ghostty.terminal_set != NULL;
}

bool ghostty_bridge_has_render_row_cells_api(void) {
	return ghostty.render_state_new != NULL &&
		ghostty.render_state_colors_get != NULL &&
		ghostty.render_state_row_iterator_new != NULL &&
		ghostty.render_state_row_get != NULL &&
		ghostty.render_state_row_cells_new != NULL &&
		ghostty.render_state_row_cells_select != NULL &&
		ghostty.render_state_row_cells_get != NULL;
}

GhosttyResult ghostty_bridge_key_encoder_new(GhosttyKeyEncoder* encoder) {
	if (!ghostty_bridge_has_key_encoder_api()) {
		return GHOSTTY_INVALID_VALUE;
	}
	return ghostty.key_encoder_new(NULL, encoder);
}

void ghostty_bridge_key_encoder_free(GhosttyKeyEncoder encoder) {
	if (ghostty.key_encoder_free != NULL) {
		ghostty.key_encoder_free(encoder);
	}
}

void ghostty_bridge_key_encoder_setopt_bool(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, bool value) {
	if (ghostty.key_encoder_setopt == NULL) {
		return;
	}
	ghostty.key_encoder_setopt(encoder, option, &value);
}

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

GhosttyResult ghostty_bridge_mouse_encoder_new(GhosttyMouseEncoder* encoder) {
	if (!ghostty_bridge_has_mouse_encoder_api()) {
		return GHOSTTY_INVALID_VALUE;
	}
	return ghostty.mouse_encoder_new(NULL, encoder);
}

void ghostty_bridge_mouse_encoder_free(GhosttyMouseEncoder encoder) {
	if (ghostty.mouse_encoder_free != NULL) {
		ghostty.mouse_encoder_free(encoder);
	}
}

void ghostty_bridge_mouse_encoder_reset(GhosttyMouseEncoder encoder) {
	if (ghostty.mouse_encoder_reset != NULL) {
		ghostty.mouse_encoder_reset(encoder);
	}
}

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
) {
	if (!ghostty_bridge_has_mouse_encoder_api() || encoder == NULL || term == NULL) {
		return GHOSTTY_INVALID_VALUE;
	}

	ghostty.mouse_encoder_setopt_from_terminal(encoder, term->terminal);
	if (size != NULL) {
		ghostty.mouse_encoder_setopt(encoder, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, size);
	}
	ghostty.mouse_encoder_setopt(
		encoder,
		GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED,
		&any_button_pressed
	);
	ghostty.mouse_encoder_setopt(
		encoder,
		GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL,
		&track_last_cell
	);

	GhosttyMouseEvent event = NULL;
	GhosttyResult result = ghostty.mouse_event_new(NULL, &event);
	if (result != GHOSTTY_SUCCESS || event == NULL) {
		return result;
	}

	ghostty.mouse_event_set_action(event, action);
	if (has_button) {
		ghostty.mouse_event_set_button(event, button);
	} else {
		ghostty.mouse_event_clear_button(event);
	}
	ghostty.mouse_event_set_mods(event, mods);
	ghostty.mouse_event_set_position(event, position);

	result = ghostty.mouse_encoder_encode(encoder, event, out_buffer, out_buffer_size, out_len);
	ghostty.mouse_event_free(event);
	return result;
}
