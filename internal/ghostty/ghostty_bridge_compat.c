#ifndef _DEFAULT_SOURCE
#define _DEFAULT_SOURCE
#endif
#include "ghostty_bridge_compat.h"
#include <ghostty/codelima_build.h>

#include <limits.h>
#include <pthread.h>
#include <stdio.h>
#include <stdlib.h>
#include <stdatomic.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#ifndef MAP_ANONYMOUS
#define MAP_ANONYMOUS MAP_ANON
#endif

#define CODELIMA_GHOSTTY_SCROLLBACK_LINES 10000
#define CODELIMA_GHOSTTY_SCROLLBACK_BYTES (64u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_RESPONSE_BYTES (8u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_EFFECT_BYTES (1024u * 1024u)
#define CODELIMA_GHOSTTY_CLIPBOARD_BYTES (64u * 1024u)
#define CODELIMA_GHOSTTY_EFFECT_COUNT 128u
#define CODELIMA_GHOSTTY_LOG_BYTES (64u * 1024u)
#define CODELIMA_GHOSTTY_SEARCH_BYTES (32u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_RESTORE_BYTES (128u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_GRAPHICS_BYTES (16u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_GRAPHICS_PIXELS (4u * 1024u * 1024u)
#define CODELIMA_GHOSTTY_GRAPHICS_ASSETS 256u
#define CODELIMA_GHOSTTY_GRAPHICS_PLACEMENTS 1024u

/* Defined by the Go PNG adapter. No borrowed pointer may escape this call. */
extern bool codelimaGhosttyDecodePNG(GhosttyAllocator*, uint8_t*, size_t, GhosttySysImage*);

struct ghostty_bridge_memory {
	size_t used;
	size_t peak;
	size_t limit;
	size_t page_size;
	bool limited;
	bool oom;
};

struct ghostty_bridge_owned_allocator {
	GhosttyAllocator allocator;
	struct ghostty_bridge_memory memory;
};

static const GhosttyAllocatorVtable ghostty_bridge_memory_vtable;

struct ghostty_bridge_effect_node {
	GhosttyBridgeEffect effect;
	struct ghostty_bridge_effect_node* next;
};

struct ghostty_bridge_terminal {
	GhosttyTerminal terminal;
	GhosttyRenderState render_state;
	GhosttyRenderStateRowIterator row_iter;
	GhosttyRenderStateRowCells row_cells;
	GhosttySelectionGesture selection_gesture;
	GhosttySearch search;
	bool search_selected;
	struct ghostty_bridge_memory search_memory;
	GhosttyAllocator search_allocator;
	struct ghostty_bridge_owned_allocator* restore_allocator;
	GhosttyBridgeFrame cached_frame;
	uint32_t cell_width;
	uint32_t cell_height;
	uint8_t* response_buf;
	size_t response_len;
	size_t response_cap;
	bool has_color_scheme;
	GhosttyColorScheme color_scheme;
	int color_theme_mode;
	GhosttyResult error;
	struct ghostty_bridge_effect_node* effect_head;
	struct ghostty_bridge_effect_node* effect_tail;
	size_t effect_bytes;
	size_t effect_count;
};

static _Thread_local char ghostty_last_error[512];
static pthread_once_t ghostty_init_once = PTHREAD_ONCE_INIT;
static GhosttyResult ghostty_init_result = GHOSTTY_SUCCESS;
static pthread_mutex_t ghostty_log_mutex = PTHREAD_MUTEX_INITIALIZER;
static uint8_t ghostty_log_buffer[CODELIMA_GHOSTTY_LOG_BYTES];
static size_t ghostty_log_length;

GhosttyResult ghostty_bridge_png_alloc(const GhosttyAllocator* allocator,
	uint32_t width, uint32_t height, GhosttySysImage* out) {
	if (out == NULL) return GHOSTTY_INVALID_VALUE;
	memset(out, 0, sizeof(*out));
	if (width == 0 || height == 0) return GHOSTTY_INVALID_VALUE;
	if ((uint64_t)width * height > CODELIMA_GHOSTTY_GRAPHICS_PIXELS) return GHOSTTY_LIMIT_EXCEEDED;
	size_t len = (size_t)width * height * 4;
	uint8_t* data = ghostty_alloc(allocator, len);
	if (data == NULL) return GHOSTTY_OUT_OF_MEMORY;
	*out = (GhosttySysImage){.width = width, .height = height, .data = data, .data_len = len};
	return GHOSTTY_SUCCESS;
}

void ghostty_bridge_png_free(const GhosttyAllocator* allocator, GhosttySysImage* image) {
	if (image == NULL) return;
	ghostty_free(allocator, image->data, image->data_len);
	memset(image, 0, sizeof(*image));
}

static bool ghostty_bridge_decode_png(void* userdata, const GhosttyAllocator* allocator,
	const uint8_t* data, size_t len, GhosttySysImage* out) {
	(void)userdata;
	if (out == NULL) return false;
	memset(out, 0, sizeof(*out));
	if (data == NULL || len == 0 || len > CODELIMA_GHOSTTY_GRAPHICS_BYTES) return false;
	bool ok = codelimaGhosttyDecodePNG((GhosttyAllocator*)allocator, (uint8_t*)data, len, out);
	if (!ok || out->data == NULL || out->width == 0 || out->height == 0 ||
		(uint64_t)out->width * out->height > CODELIMA_GHOSTTY_GRAPHICS_PIXELS ||
		out->data_len != (size_t)out->width * out->height * 4) {
		ghostty_bridge_png_free(allocator, out);
		return false;
	}
	return true;
}

static void ghostty_bridge_log_cb(void* userdata, GhosttySysLogLevel level,
	const uint8_t* scope, size_t scope_len, const uint8_t* message, size_t message_len) {
	(void)userdata;
	(void)level;
	pthread_mutex_lock(&ghostty_log_mutex);
	/* A diagnostic is best effort. Drop a whole record when its bounded sink
	 * is full; never stall terminal ingestion waiting for a logger consumer. */
	if (scope_len <= CODELIMA_GHOSTTY_LOG_BYTES - ghostty_log_length &&
		message_len <= CODELIMA_GHOSTTY_LOG_BYTES - ghostty_log_length - scope_len &&
		CODELIMA_GHOSTTY_LOG_BYTES - ghostty_log_length - scope_len - message_len >= 4) {
		ghostty_log_buffer[ghostty_log_length++] = '[';
		memcpy(ghostty_log_buffer + ghostty_log_length, scope, scope_len);
		ghostty_log_length += scope_len;
		ghostty_log_buffer[ghostty_log_length++] = ']';
		ghostty_log_buffer[ghostty_log_length++] = ' ';
		memcpy(ghostty_log_buffer + ghostty_log_length, message, message_len);
		ghostty_log_length += message_len;
		ghostty_log_buffer[ghostty_log_length++] = '\n';
	}
	pthread_mutex_unlock(&ghostty_log_mutex);
}

static void ghostty_bridge_initialize(void) {
	if (strcmp(CODELIMA_GHOSTTY_BUILD_ID, codelima_ghostty_archive_build_identity()) != 0) {
		ghostty_init_result = GHOSTTY_INVALID_VALUE;
		return;
	}
	ghostty_init_result = ghostty_sys_set(GHOSTTY_SYS_OPT_LOG, (const void*)&ghostty_bridge_log_cb);
	if (ghostty_init_result == GHOSTTY_SUCCESS)
		ghostty_init_result = ghostty_sys_set(GHOSTTY_SYS_OPT_DECODE_PNG, (const void*)&ghostty_bridge_decode_png);
}

const char* ghostty_bridge_build_identity(void) {
	return codelima_ghostty_archive_build_identity();
}

GhosttyResult ghostty_bridge_init(void) {
	if (pthread_once(&ghostty_init_once, ghostty_bridge_initialize) != 0) return GHOSTTY_IO_ERROR;
	return ghostty_init_result;
}

int ghostty_bridge_read_log(uint8_t* out, size_t capacity) {
	if (out == NULL || capacity == 0) return 0;
	pthread_mutex_lock(&ghostty_log_mutex);
	size_t n = ghostty_log_length < capacity ? ghostty_log_length : capacity;
	memcpy(out, ghostty_log_buffer, n);
	ghostty_log_length -= n;
	memmove(ghostty_log_buffer, ghostty_log_buffer + n, ghostty_log_length);
	pthread_mutex_unlock(&ghostty_log_mutex);
	return (int)n;
}

void ghostty_bridge_test_log(const uint8_t* message, size_t len) {
	if (message == NULL) return;
	ghostty_bridge_log_cb(NULL, GHOSTTY_SYS_LOG_LEVEL_INFO, (const uint8_t*)"test", 4, message, len);
}

void ghostty_bridge_free(void* value) { free(value); }

static void ghostty_bridge_record_error(GhosttyBridgeTerminal term, GhosttyResult result) {
	if (term != NULL && term->error == GHOSTTY_SUCCESS && result != GHOSTTY_SUCCESS)
		term->error = result;
}

GhosttyResult ghostty_bridge_terminal_error(GhosttyBridgeTerminal term) {
	if (term == NULL) return GHOSTTY_INVALID_VALUE;
	GhosttyResult result = term->error;
	term->error = GHOSTTY_SUCCESS;
	return result;
}

void ghostty_bridge_effect_free(GhosttyBridgeEffect* effect) {
	if (effect == NULL) return;
	free(effect->data);
	free(effect->detail);
	memset(effect, 0, sizeof(*effect));
}

static GhosttyResult ghostty_bridge_effect_push(GhosttyBridgeTerminal term, int kind, int location,
	GhosttyString data, GhosttyString detail, int value) {
	if (term == NULL || (data.len != 0 && data.ptr == NULL) || (detail.len != 0 && detail.ptr == NULL))
		return GHOSTTY_INVALID_VALUE;
	if (term->effect_count >= CODELIMA_GHOSTTY_EFFECT_COUNT ||
		data.len > CODELIMA_GHOSTTY_EFFECT_BYTES - term->effect_bytes ||
		detail.len > CODELIMA_GHOSTTY_EFFECT_BYTES - term->effect_bytes - data.len) {
		ghostty_bridge_record_error(term, GHOSTTY_LIMIT_EXCEEDED);
		return GHOSTTY_LIMIT_EXCEEDED;
	}
	struct ghostty_bridge_effect_node* node = calloc(1, sizeof(*node));
	if (node == NULL) goto oom;
	node->effect.kind = kind;
	node->effect.location = location;
	node->effect.value = value;
	if (data.len != 0) {
		node->effect.data = malloc(data.len);
		if (node->effect.data == NULL) goto oom;
		memcpy(node->effect.data, data.ptr, data.len);
		node->effect.len = data.len;
	}
	if (detail.len != 0) {
		node->effect.detail = malloc(detail.len);
		if (node->effect.detail == NULL) goto oom;
		memcpy(node->effect.detail, detail.ptr, detail.len);
		node->effect.detail_len = detail.len;
	}
	if (term->effect_tail != NULL) term->effect_tail->next = node;
	else term->effect_head = node;
	term->effect_tail = node;
	term->effect_bytes += data.len + detail.len;
	term->effect_count++;
	return GHOSTTY_SUCCESS;
oom:
	if (node != NULL) { ghostty_bridge_effect_free(&node->effect); free(node); }
	ghostty_bridge_record_error(term, GHOSTTY_OUT_OF_MEMORY);
	return GHOSTTY_OUT_OF_MEMORY;
}

bool ghostty_bridge_terminal_next_effect(GhosttyBridgeTerminal term, GhosttyBridgeEffect* effect) {
	if (term == NULL || effect == NULL || term->effect_head == NULL) return false;
	struct ghostty_bridge_effect_node* node = term->effect_head;
	term->effect_head = node->next;
	if (term->effect_head == NULL) term->effect_tail = NULL;
	*effect = node->effect;
	term->effect_bytes -= effect->len + effect->detail_len;
	term->effect_count--;
	free(node);
	return true;
}

static void ghostty_bridge_title_cb(GhosttyTerminal terminal, void* userdata) {
	GhosttyString title = {0};
	GhosttyResult result = ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_TITLE, &title);
	if (result == GHOSTTY_SUCCESS)
		(void)ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_TITLE, 0, title, (GhosttyString){0}, 0);
}

static void ghostty_bridge_pwd_cb(GhosttyTerminal terminal, void* userdata) {
	GhosttyString pwd = {0};
	GhosttyResult result = ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_PWD, &pwd);
	if (result == GHOSTTY_SUCCESS)
		(void)ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_PWD, 0, pwd, (GhosttyString){0}, 0);
}

static void ghostty_bridge_bell_cb(GhosttyTerminal terminal, void* userdata) {
	(void)terminal;
	(void)ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_BELL, 0, (GhosttyString){0}, (GhosttyString){0}, 0);
}

static void ghostty_bridge_notification_cb(GhosttyTerminal terminal, void* userdata,
	const GhosttyTerminalDesktopNotification* notification) {
	(void)terminal;
	if (notification == NULL || notification->size < sizeof(*notification)) return;
	(void)ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_NOTIFICATION, 0,
		notification->title, notification->body, 0);
}

static void ghostty_bridge_progress_cb(GhosttyTerminal terminal, void* userdata,
	const GhosttyTerminalProgressReport* report) {
	(void)terminal;
	if (report == NULL || report->size < sizeof(*report)) return;
	(void)ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_PROGRESS, (int)report->state,
		(GhosttyString){0}, (GhosttyString){0}, (int)report->progress);
}

static void ghostty_bridge_clipboard_cb(GhosttyTerminal terminal, void* userdata,
	const GhosttyClipboardWrite* write) {
	(void)terminal;
	if (write == NULL || write->size < sizeof(*write)) return;
	GhosttyClipboardWriteReply reply = GHOSTTY_INIT_SIZED(GhosttyClipboardWriteReply);
	reply.result = GHOSTTY_CLIPBOARD_WRITE_RESULT_UNSUPPORTED;
	if (write->ack_required) {
		write->reply(write, &reply);
		return;
	}
	if (write->location == GHOSTTY_CLIPBOARD_LOCATION_STANDARD && write->contents_len == 1) {
		const GhosttyClipboardContent* content = &write->contents[0];
		if (content->data.len <= CODELIMA_GHOSTTY_CLIPBOARD_BYTES &&
			content->mime.len == 10 && memcmp(content->mime.ptr, "text/plain", 10) == 0) {
			GhosttyResult result = ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_CLIPBOARD,
				(int)write->location, content->data, content->mime, 0);
			reply.result = result == GHOSTTY_SUCCESS ? GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS : GHOSTTY_CLIPBOARD_WRITE_RESULT_BUSY;
		}
	} else if (write->location == GHOSTTY_CLIPBOARD_LOCATION_STANDARD && write->contents_len == 0) {
		GhosttyResult result = ghostty_bridge_effect_push(userdata, GHOSTTY_BRIDGE_EFFECT_CLIPBOARD,
			(int)write->location, (GhosttyString){0}, (GhosttyString){.ptr = (const uint8_t*)"text/plain", .len = 10}, 0);
		reply.result = result == GHOSTTY_SUCCESS ? GHOSTTY_CLIPBOARD_WRITE_RESULT_SUCCESS : GHOSTTY_CLIPBOARD_WRITE_RESULT_BUSY;
	}
	write->reply(write, &reply);
}

static int ghostty_bridge_set_error(const char* message) {
	if (message == NULL) {
		message = "unknown ghostty error";
	}
	snprintf(ghostty_last_error, sizeof(ghostty_last_error), "%s", message);
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
	if (extra > CODELIMA_GHOSTTY_RESPONSE_BYTES - bridge->response_len) {
		ghostty_bridge_record_error(bridge, GHOSTTY_LIMIT_EXCEEDED);
		return 0;
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
		ghostty_bridge_record_error(bridge, GHOSTTY_OUT_OF_MEMORY);
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

static bool ghostty_bridge_color_scheme_for_mode(int mode, GhosttyColorScheme* out_scheme) {
	if (out_scheme == NULL) {
		return false;
	}
	switch (mode) {
	case 1:
		*out_scheme = GHOSTTY_COLOR_SCHEME_DARK;
		return true;
	case 2:
		*out_scheme = GHOSTTY_COLOR_SCHEME_LIGHT;
		return true;
	default:
		return false;
	}
}

static void ghostty_bridge_report_color_theme_mode(struct ghostty_bridge_terminal* bridge) {
	if (bridge == NULL || bridge->terminal == NULL || !bridge->has_color_scheme) {
		return;
	}
	char response[32];
	size_t written = 0;
	if (ghostty_color_scheme_report_encode(bridge->color_scheme, response, sizeof(response), &written) == GHOSTTY_SUCCESS)
		ghostty_bridge_write_pty_cb(bridge->terminal, bridge, (const uint8_t*)response, written);
}

static bool ghostty_bridge_color_scheme_cb(GhosttyTerminal terminal, void* userdata, GhosttyColorScheme* out_scheme) {
	(void)terminal;
	struct ghostty_bridge_terminal* bridge = (struct ghostty_bridge_terminal*)userdata;
	if (bridge == NULL || out_scheme == NULL || !bridge->has_color_scheme) {
		return false;
	}
	*out_scheme = bridge->color_scheme;
	return true;
}

static bool ghostty_bridge_device_attributes_cb(GhosttyTerminal terminal, void* userdata, GhosttyDeviceAttributes* out_attrs) {
	(void)terminal;
	(void)userdata;
	if (out_attrs == NULL) {
		return false;
	}
	memset(out_attrs, 0, sizeof(*out_attrs));
	out_attrs->primary.conformance_level = GHOSTTY_DA_CONFORMANCE_VT220;
	out_attrs->primary.features[0] = GHOSTTY_DA_FEATURE_WINDOWING;
	out_attrs->primary.features[1] = GHOSTTY_DA_FEATURE_ANSI_COLOR;
	out_attrs->primary.num_features = 2;
	out_attrs->secondary.device_type = GHOSTTY_DA_DEVICE_TYPE_VT220;
	return true;
}

static bool ghostty_bridge_size_cb(GhosttyTerminal terminal, void* userdata, GhosttySizeReportSize* out_size) {
	(void)userdata;
	if (terminal == NULL || out_size == NULL) {
		return false;
	}
	memset(out_size, 0, sizeof(*out_size));
	return ghostty_bridge_result_ok(ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_ROWS, &out_size->rows)) &&
		ghostty_bridge_result_ok(ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_COLS, &out_size->columns));
}

static GhosttyString ghostty_bridge_xtversion_cb(GhosttyTerminal terminal, void* userdata) {
	(void)terminal;
	(void)userdata;
	static const uint8_t version[] = "codelima";
	GhosttyString str = {
		.ptr = version,
		.len = sizeof(version) - 1,
	};
	return str;
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
	if (!ghostty_bridge_result_ok(ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide))) {
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
	if (!ghostty_bridge_result_ok(ghostty_cell_get(raw, GHOSTTY_CELL_DATA_HAS_HYPERLINK, &has_hyperlink))) {
		return 0;
	}
	return has_hyperlink ? 1 : 0;
}


static void ghostty_bridge_resolved_cell_init(GhosttyResolvedCell* out) {
	memset(out, 0, sizeof(*out));
	out->width = 1;
	out->color_flags = GHOSTTY_CELL_BG_DEFAULT | GHOSTTY_CELL_FG_DEFAULT;
}

static void ghostty_bridge_fill_cell_base(GhosttyCell raw, const GhosttyStyle* style, GhosttyResolvedCell* out) {
	uint32_t codepoint = 0;
	ghostty_bridge_resolved_cell_init(out);
	(void)ghostty_cell_get(raw, GHOSTTY_CELL_DATA_CODEPOINT, &codepoint);
	out->codepoint = codepoint;
	out->flags = ghostty_bridge_style_flags(style);
	out->width = ghostty_bridge_cell_width(raw);
	out->hyperlink_id = ghostty_bridge_cell_hyperlink_id(raw);
	if (style != NULL) {
		out->underline_style = (uint8_t)style->underline;
		out->overline = style->overline;
	}
}

static GhosttyResult ghostty_bridge_resolve_render_colors(GhosttyBridgeTerminal term, GhosttyRenderStateColors* colors) {
	/* Upstream render state refreshes fg/bg only when BOTH defaults exist.
	 * Optional host colors and an OSC override of only one channel are valid;
	 * resolve those channels independently using the effective public getters. */
	GhosttyColorRgb fg = {.r = 255, .g = 255, .b = 255}, bg = {0};
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_COLOR_FOREGROUND, &fg);
	if (result != GHOSTTY_SUCCESS && result != GHOSTTY_NO_VALUE) return result;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_COLOR_BACKGROUND, &bg);
	if (result != GHOSTTY_SUCCESS && result != GHOSTTY_NO_VALUE) return result;
	GhosttyTerminalModeConfig mode = {0};
	mode.mode = GHOSTTY_MODE_REVERSE_COLORS;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_MODE, &mode);
	if (result != GHOSTTY_SUCCESS) return result;
	colors->foreground = mode.value ? bg : fg;
	colors->background = mode.value ? fg : bg;
	return GHOSTTY_SUCCESS;
}

static int ghostty_bridge_get_render_colors(struct ghostty_bridge_terminal* bridge, GhosttyRenderStateColors* out_colors) {
	if (bridge == NULL || out_colors == NULL) {
		return 0;
	}
	*out_colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
	return ghostty_bridge_result_ok(ghostty_render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_COLORS, out_colors)) &&
		ghostty_bridge_result_ok(ghostty_bridge_resolve_render_colors(bridge, out_colors));
}

static GhosttyResult ghostty_bridge_bind_callbacks(GhosttyBridgeTerminal bridge) {
	const struct { GhosttyTerminalOption option; const void* value; } callbacks[] = {
		{GHOSTTY_TERMINAL_OPT_USERDATA, bridge},
		{GHOSTTY_TERMINAL_OPT_WRITE_PTY, (const void*)&ghostty_bridge_write_pty_cb},
		{GHOSTTY_TERMINAL_OPT_COLOR_SCHEME, (const void*)&ghostty_bridge_color_scheme_cb},
		{GHOSTTY_TERMINAL_OPT_DEVICE_ATTRIBUTES, (const void*)&ghostty_bridge_device_attributes_cb},
		{GHOSTTY_TERMINAL_OPT_SIZE, (const void*)&ghostty_bridge_size_cb},
		{GHOSTTY_TERMINAL_OPT_XTVERSION, (const void*)&ghostty_bridge_xtversion_cb},
		{GHOSTTY_TERMINAL_OPT_TITLE_CHANGED, (const void*)&ghostty_bridge_title_cb},
		{GHOSTTY_TERMINAL_OPT_PWD_CHANGED, (const void*)&ghostty_bridge_pwd_cb},
		{GHOSTTY_TERMINAL_OPT_BELL, (const void*)&ghostty_bridge_bell_cb},
		{GHOSTTY_TERMINAL_OPT_DESKTOP_NOTIFICATION, (const void*)&ghostty_bridge_notification_cb},
		{GHOSTTY_TERMINAL_OPT_PROGRESS_REPORT, (const void*)&ghostty_bridge_progress_cb},
		{GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE, (const void*)&ghostty_bridge_clipboard_cb},
		{GHOSTTY_TERMINAL_OPT_CLIPBOARD_READ, NULL},
	};
	for (size_t i = 0; i < sizeof(callbacks) / sizeof(callbacks[0]); i++) {
		GhosttyResult result = ghostty_terminal_set(bridge->terminal, callbacks[i].option, callbacks[i].value);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	return GHOSTTY_SUCCESS;
}

static GhosttyResult ghostty_bridge_graphics_policy(GhosttyTerminal terminal) {
	const uint64_t storage_limit = CODELIMA_GHOSTTY_GRAPHICS_BYTES;
	const size_t apc_limit = 4u * 1024u * 1024u;
	const bool disabled = false;
	const struct { GhosttyTerminalOption option; const void* value; } options[] = {
		{GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_STORAGE_LIMIT, &storage_limit},
		{GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_MEDIUM_FILE, &disabled},
		{GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_MEDIUM_TEMP_FILE, NULL},
		{GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_MEDIUM_SHARED_MEM, &disabled},
		{GHOSTTY_TERMINAL_OPT_KITTY_IMAGE_ANIMATION, &disabled},
		{GHOSTTY_TERMINAL_OPT_APC_MAX_BYTES, &apc_limit},
		{GHOSTTY_TERMINAL_OPT_APC_MAX_BYTES_KITTY, &apc_limit},
	};
	for (size_t i = 0; i < sizeof(options) / sizeof(options[0]); i++) {
		GhosttyResult result = ghostty_terminal_set(terminal, options[i].option, options[i].value);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	uint64_t applied_limit = 0;
	GhosttyResult result = ghostty_terminal_get(terminal, GHOSTTY_TERMINAL_DATA_KITTY_IMAGE_STORAGE_LIMIT, &applied_limit);
	if (result != GHOSTTY_SUCCESS) return result;
	return applied_limit == storage_limit ? GHOSTTY_SUCCESS : GHOSTTY_INVALID_VALUE;
}

GhosttyBridgeTerminal ghostty_bridge_terminal_new(int cols, int rows) {
	if (cols <= 0 || rows <= 0 || cols > UINT16_MAX || rows > UINT16_MAX) {
		ghostty_bridge_set_error("terminal dimensions must be positive");
		return NULL;
	}
	if (ghostty_bridge_init() != GHOSTTY_SUCCESS) return NULL;

	struct ghostty_bridge_terminal* bridge = (struct ghostty_bridge_terminal*)calloc(1, sizeof(*bridge));
	if (bridge == NULL) {
		ghostty_bridge_set_error("allocate ghostty terminal bridge");
		return NULL;
	}

	GhosttyResult result = ghostty_terminal_new(NULL, &bridge->terminal, (uint16_t)cols, (uint16_t)rows);
	if (!ghostty_bridge_result_ok(result) || bridge->terminal == NULL) {
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty terminal", result);
		return NULL;
	}

	result = ghostty_render_state_new(NULL, &bridge->render_state);
	if (!ghostty_bridge_result_ok(result) || bridge->render_state == NULL) {
		ghostty_terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty render state", result);
		return NULL;
	}

	result = ghostty_render_state_row_iterator_new(NULL, &bridge->row_iter);
	if (!ghostty_bridge_result_ok(result) || bridge->row_iter == NULL) {
		ghostty_render_state_free(bridge->render_state);
		ghostty_terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty row iterator", result);
		return NULL;
	}

	result = ghostty_render_state_row_cells_new(NULL, &bridge->row_cells);
	if (!ghostty_bridge_result_ok(result) || bridge->row_cells == NULL) {
		ghostty_render_state_row_iterator_free(bridge->row_iter);
		ghostty_render_state_free(bridge->render_state);
		ghostty_terminal_free(bridge->terminal);
		free(bridge);
		ghostty_bridge_set_result_error("create ghostty row cells", result);
		return NULL;
	}

	result = ghostty_bridge_bind_callbacks(bridge);
	if (result != GHOSTTY_SUCCESS) {
		ghostty_bridge_terminal_free(bridge);
		ghostty_bridge_set_result_error("install ghostty effects", result);
		return NULL;
	}
	const size_t lines = CODELIMA_GHOSTTY_SCROLLBACK_LINES;
	const size_t bytes = CODELIMA_GHOSTTY_SCROLLBACK_BYTES;
	const size_t clipboard_bytes = CODELIMA_GHOSTTY_CLIPBOARD_BYTES;
	const size_t continuation_bytes = CODELIMA_GHOSTTY_EFFECT_BYTES;
	const struct { GhosttyTerminalOption option; const size_t* value; } limits[] = {
		{GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &lines},
		{GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &bytes},
		{GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE_MAX_BYTES, &clipboard_bytes},
		{GHOSTTY_TERMINAL_OPT_CONTINUATION_MAX_BYTES, &continuation_bytes},
	};
	for (size_t i = 0; i < sizeof(limits) / sizeof(limits[0]); i++) {
		result = ghostty_terminal_set(bridge->terminal, limits[i].option, limits[i].value);
		if (result != GHOSTTY_SUCCESS) {
			ghostty_bridge_terminal_free(bridge);
			ghostty_bridge_set_result_error("set ghostty resource limits", result);
			return NULL;
		}
	}
	result = ghostty_bridge_graphics_policy(bridge->terminal);
	if (result == GHOSTTY_SUCCESS)
		result = ghostty_bridge_terminal_resize_pixels(bridge, cols, rows, 8, 16);
	if (result != GHOSTTY_SUCCESS) {
		ghostty_bridge_terminal_free(bridge);
		ghostty_bridge_set_result_error("set ghostty graphics policy", result);
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
	ghostty_selection_gesture_free(bridge->selection_gesture, bridge->terminal);
	ghostty_search_free(bridge->search);
	GhosttyBridgeEffect effect = {0};
	while (ghostty_bridge_terminal_next_effect(term, &effect)) ghostty_bridge_effect_free(&effect);
	free(bridge->response_buf);
	if (bridge->row_cells != NULL) {
		ghostty_render_state_row_cells_free(bridge->row_cells);
	}
	if (bridge->row_iter != NULL) {
		ghostty_render_state_row_iterator_free(bridge->row_iter);
	}
	if (bridge->render_state != NULL) {
		ghostty_render_state_free(bridge->render_state);
	}
	if (bridge->terminal != NULL) {
		ghostty_terminal_free(bridge->terminal);
	}
	free(bridge->restore_allocator);
	ghostty_bridge_frame_free(&bridge->cached_frame);
	free(bridge);
}

void ghostty_bridge_terminal_resize(GhosttyBridgeTerminal term, int cols, int rows) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || cols <= 0 || rows <= 0 || cols > UINT16_MAX || rows > UINT16_MAX) {
		ghostty_bridge_record_error(term, GHOSTTY_INVALID_VALUE);
		return;
	}
	ghostty_bridge_record_error(term, ghostty_bridge_terminal_resize_pixels(term, cols, rows, bridge->cell_width, bridge->cell_height));
}

GhosttyResult ghostty_bridge_terminal_resize_pixels(GhosttyBridgeTerminal term,
	int cols, int rows, uint32_t cell_width, uint32_t cell_height) {
	if (term == NULL || cols <= 0 || rows <= 0 || cols > UINT16_MAX || rows > UINT16_MAX ||
		cell_width == 0 || cell_height == 0 || cell_width > 4096 || cell_height > 4096)
		return GHOSTTY_INVALID_VALUE;
	GhosttyResult result = ghostty_terminal_resize(term->terminal, (uint16_t)cols, (uint16_t)rows, cell_width, cell_height);
	if (result == GHOSTTY_SUCCESS) { term->cell_width = cell_width; term->cell_height = cell_height; }
	return result;
}

GhosttyResult ghostty_bridge_terminal_cell_size(GhosttyBridgeTerminal term,
	uint32_t* cell_width, uint32_t* cell_height) {
	if (term == NULL || cell_width == NULL || cell_height == NULL) return GHOSTTY_INVALID_VALUE;
	*cell_width = term->cell_width;
	*cell_height = term->cell_height;
	return GHOSTTY_SUCCESS;
}

void ghostty_bridge_terminal_write(GhosttyBridgeTerminal term, const uint8_t* data, size_t len) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || data == NULL || len == 0) {
		return;
	}
	ghostty_terminal_vt_write(bridge->terminal, data, len);
	if (bridge->restore_allocator != NULL) {
		if (bridge->restore_allocator->memory.limited) ghostty_bridge_record_error(bridge, GHOSTTY_LIMIT_EXCEEDED);
		else if (bridge->restore_allocator->memory.oom) ghostty_bridge_record_error(bridge, GHOSTTY_OUT_OF_MEMORY);
	}
	bool processing_error = false;
	GhosttyResult result = ghostty_terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_VT_PROCESSING_ERROR, &processing_error);
	if (result == GHOSTTY_SUCCESS && processing_error) ghostty_bridge_record_error(bridge, GHOSTTY_INVALID_VALUE);
	else if (result != GHOSTTY_NO_VALUE) ghostty_bridge_record_error(bridge, result);
}

GhosttyDirty ghostty_bridge_render_state_update(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || bridge->render_state == NULL) {
		return GHOSTTY_DIRTY_NONE;
	}
	GhosttyResult result = ghostty_render_state_update(bridge->render_state, bridge->terminal);
	if (result != GHOSTTY_SUCCESS) {
		ghostty_bridge_record_error(bridge, result);
		return GHOSTTY_DIRTY_NONE;
	}
	GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
	if (!ghostty_bridge_result_ok(ghostty_render_state_get(bridge->render_state, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty))) {
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





void ghostty_bridge_render_state_mark_clean(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->render_state == NULL) {
		return;
	}
	ghostty_bridge_record_error(term, ghostty_render_state_clean(bridge->render_state));
}

static int ghostty_bridge_fill_viewport_cell(struct ghostty_bridge_terminal* bridge, const GhosttyRenderStateColors* colors, GhosttyResolvedCell* out) {
	GhosttyCell raw = 0;
	GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
	GhosttyColorRgb fg = colors->foreground;
	GhosttyColorRgb bg = colors->background;
	uint32_t grapheme_len = 0;
	bool fg_default = true;
	bool bg_default = true;

	if (!ghostty_bridge_result_ok(ghostty_render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw))) {
		return 0;
	}
	(void)ghostty_render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style);
	fg_default = !ghostty_bridge_result_ok(ghostty_render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &fg));
	bg_default = !ghostty_bridge_result_ok(ghostty_render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg));
	(void)ghostty_render_state_row_cells_get(bridge->row_cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &grapheme_len);

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




bool ghostty_bridge_terminal_is_alternate_screen(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL) {
		return false;
	}
	GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
	return ghostty_bridge_result_ok(ghostty_terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen)) &&
		screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE;
}

bool ghostty_bridge_terminal_has_mouse_tracking(GhosttyBridgeTerminal term) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL) {
		return false;
	}
	bool tracking = false;
	return ghostty_bridge_result_ok(ghostty_terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &tracking)) && tracking;
}

bool ghostty_bridge_terminal_get_mode(GhosttyBridgeTerminal term, int mode, bool is_ansi) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || mode < 0) {
		return false;
	}
	GhosttyTerminalModeConfig config = {0};
	config.mode = ghostty_mode_new((uint16_t)mode, is_ansi);
	return ghostty_bridge_result_ok(ghostty_terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_MODE, &config)) && config.value;
}

void ghostty_bridge_terminal_set_color_theme_mode(GhosttyBridgeTerminal term, int mode) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL) {
		return;
	}
	GhosttyColorScheme scheme = GHOSTTY_COLOR_SCHEME_DARK;
	bridge->has_color_scheme = ghostty_bridge_color_scheme_for_mode(mode, &scheme);
	if (bridge->has_color_scheme) {
		bridge->color_scheme = scheme;
		bridge->color_theme_mode = mode;
	}
}

void ghostty_bridge_terminal_report_color_theme_mode(GhosttyBridgeTerminal term) {
	ghostty_bridge_report_color_theme_mode(term);
}

bool ghostty_bridge_terminal_get_scrollbar(GhosttyBridgeTerminal term, GhosttyTerminalScrollbar* out_scrollbar) {
	struct ghostty_bridge_terminal* bridge = term;
	if (bridge == NULL || bridge->terminal == NULL || out_scrollbar == NULL) {
		return false;
	}
	memset(out_scrollbar, 0, sizeof(*out_scrollbar));
	return ghostty_bridge_result_ok(
		ghostty_terminal_get(bridge->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, out_scrollbar)
	);
}

static void ghostty_bridge_terminal_scroll_viewport(struct ghostty_bridge_terminal* bridge, GhosttyTerminalScrollViewportTag tag, intptr_t delta) {
	if (bridge == NULL || bridge->terminal == NULL) {
		return;
	}
	GhosttyTerminalScrollViewport behavior;
	memset(&behavior, 0, sizeof(behavior));
	behavior.tag = tag;
	if (tag == GHOSTTY_SCROLL_VIEWPORT_DELTA) {
		behavior.value.delta = delta;
	}
	ghostty_terminal_scroll_viewport(bridge->terminal, behavior);
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






static int ghostty_bridge_hyperlink(GhosttyBridgeTerminal term, GhosttyPointTag tag, int row, int col, uint8_t* out, size_t capacity) {
	if (term == NULL || out == NULL || row < 0 || col < 0 || col > UINT16_MAX) return -1;
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	GhosttyResult result = ghostty_terminal_grid_ref(term->terminal, ghostty_bridge_make_point(tag, row, col), &ref);
	if (result != GHOSTTY_SUCCESS) return -1;
	size_t written = 0;
	result = ghostty_grid_ref_hyperlink_uri(&ref, out, capacity, &written);
	if (result == GHOSTTY_NO_VALUE) return 0;
	return result == GHOSTTY_SUCCESS && written <= capacity && written <= INT_MAX ? (int)written : -1;
}

int ghostty_bridge_terminal_get_hyperlink_uri(GhosttyBridgeTerminal term, int row, int col, uint8_t* out, size_t capacity) {
	return ghostty_bridge_hyperlink(term, GHOSTTY_POINT_TAG_VIEWPORT, row, col, out, capacity);
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

GhosttyResult ghostty_bridge_focus_encode(GhosttyFocusEvent event, char* out_buffer, size_t out_buffer_size, size_t* out_len) {
	return ghostty_focus_encode(event, out_buffer, out_buffer_size, out_len);
}

GhosttyResult ghostty_bridge_key_encoder_new(GhosttyKeyEncoder* encoder) {
	return ghostty_key_encoder_new(NULL, encoder);
}

void ghostty_bridge_key_encoder_free(GhosttyKeyEncoder encoder) {
	ghostty_key_encoder_free(encoder);
}

void ghostty_bridge_key_encoder_setopt_bool(GhosttyKeyEncoder encoder, GhosttyKeyEncoderOption option, bool value) {
	ghostty_key_encoder_setopt(encoder, option, &value);
}

bool ghostty_bridge_key_encoder_setopt_from_terminal(GhosttyKeyEncoder encoder, GhosttyBridgeTerminal term) {
	if (encoder == NULL || term == NULL) {
		return false;
	}
	ghostty_key_encoder_setopt_from_terminal(encoder, term->terminal);
	return true;
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

	GhosttyKeyEvent event = NULL;
	GhosttyResult result = ghostty_key_event_new(NULL, &event);
	if (result != GHOSTTY_SUCCESS || event == NULL) {
		return result;
	}

	ghostty_key_event_set_action(event, action);
	ghostty_key_event_set_key(event, key);
	ghostty_key_event_set_mods(event, mods);
	ghostty_key_event_set_consumed_mods(event, 0);
	if (utf8 != NULL && utf8_len > 0) {
		ghostty_key_event_set_utf8(event, utf8, utf8_len);
	}
	if (unshifted_codepoint != 0) {
		ghostty_key_event_set_unshifted_codepoint(event, unshifted_codepoint);
	}

	result = ghostty_key_encoder_encode(encoder, event, out_buffer, out_buffer_size, out_len);
	ghostty_key_event_free(event);
	return result;
}

GhosttyResult ghostty_bridge_mouse_encoder_new(GhosttyMouseEncoder* encoder) {
	return ghostty_mouse_encoder_new(NULL, encoder);
}

void ghostty_bridge_mouse_encoder_free(GhosttyMouseEncoder encoder) {
	ghostty_mouse_encoder_free(encoder);
}

void ghostty_bridge_mouse_encoder_reset(GhosttyMouseEncoder encoder) {
	ghostty_mouse_encoder_reset(encoder);
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
	if (encoder == NULL || term == NULL) {
		return GHOSTTY_INVALID_VALUE;
	}

	ghostty_mouse_encoder_setopt_from_terminal(encoder, term->terminal);
	if (size != NULL) {
		ghostty_mouse_encoder_setopt(encoder, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, size);
	}
	ghostty_mouse_encoder_setopt(
		encoder,
		GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED,
		&any_button_pressed
	);
	ghostty_mouse_encoder_setopt(
		encoder,
		GHOSTTY_MOUSE_ENCODER_OPT_TRACK_LAST_CELL,
		&track_last_cell
	);

	GhosttyMouseEvent event = NULL;
	GhosttyResult result = ghostty_mouse_event_new(NULL, &event);
	if (result != GHOSTTY_SUCCESS || event == NULL) {
		return result;
	}

	ghostty_mouse_event_set_action(event, action);
	if (has_button) {
		ghostty_mouse_event_set_button(event, button);
	} else {
		ghostty_mouse_event_clear_button(event);
	}
	ghostty_mouse_event_set_mods(event, mods);
	ghostty_mouse_event_set_position(event, position);

	result = ghostty_mouse_encoder_encode(encoder, event, out_buffer, out_buffer_size, out_len);
	ghostty_mouse_event_free(event);
	return result;
}

/* All output adapters use malloc-owned bounded bytes so their callers never
 * retain a borrow from Ghostty or allocate an unbounded response first. */
struct ghostty_bridge_bytes {
	uint8_t* data;
	size_t len;
	size_t cap;
	size_t limit;
	GhosttyResult error;
};

static bool ghostty_bridge_bytes_write(void* userdata, const uint8_t* data, size_t len) {
	struct ghostty_bridge_bytes* bytes = userdata;
	if (len == 0) return true;
	if (bytes->error != GHOSTTY_SUCCESS) return false;
	if (data == NULL || len > bytes->limit - bytes->len) {
		bytes->error = GHOSTTY_LIMIT_EXCEEDED;
		return false;
	}
	size_t needed = bytes->len + len;
	if (needed > bytes->cap) {
		size_t capacity = bytes->cap == 0 ? 4096 : bytes->cap;
		if (capacity > bytes->limit) capacity = bytes->limit;
		while (capacity < needed) {
			if (capacity > bytes->limit / 2) capacity = bytes->limit;
			else capacity *= 2;
		}
		uint8_t* buffer = realloc(bytes->data, capacity);
		if (buffer == NULL) {
			bytes->error = GHOSTTY_OUT_OF_MEMORY;
			return false;
		}
		bytes->data = buffer;
		bytes->cap = capacity;
	}
	memcpy(bytes->data + bytes->len, data, len);
	bytes->len += len;
	return true;
}

static GhosttyResult ghostty_bridge_format_selection(GhosttyBridgeTerminal term,
	GhosttySelection* selection, bool ansi, bool unwrap, struct ghostty_bridge_bytes* bytes) {
	GhosttyFormatterTerminalOptions options = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalOptions);
	options.emit = ansi ? GHOSTTY_FORMATTER_FORMAT_VT : GHOSTTY_FORMATTER_FORMAT_PLAIN;
	options.trim = true;
	options.unwrap = unwrap;
	options.selection = selection;
	options.extra = GHOSTTY_INIT_SIZED(GhosttyFormatterTerminalExtra);
	options.extra.screen = GHOSTTY_INIT_SIZED(GhosttyFormatterScreenExtra);
	GhosttyFormatter formatter = NULL;
	GhosttyResult result = ghostty_formatter_terminal_new(NULL, &formatter, term->terminal, options);
	if (result != GHOSTTY_SUCCESS) return result;
	GhosttyWriter writer = {.write = ghostty_bridge_bytes_write, .userdata = bytes};
	result = ghostty_formatter_format(formatter, writer);
	ghostty_formatter_free(formatter);
	return bytes->error != GHOSTTY_SUCCESS ? bytes->error : result;
}

static GhosttyResult ghostty_bridge_selection(GhosttyBridgeTerminal term, GhosttyPointTag tag,
	size_t start_row, size_t end_row, uint16_t cols, GhosttySelection* selection) {
	if (start_row > UINT32_MAX || end_row > UINT32_MAX || cols == 0) return GHOSTTY_INVALID_VALUE;
	*selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	selection->start = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	selection->end = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	GhosttyPoint start = {0};
	start.tag = tag;
	start.value.coordinate.y = (uint32_t)start_row;
	GhosttyPoint end = {0};
	end.tag = tag;
	end.value.coordinate.x = cols - 1;
	end.value.coordinate.y = (uint32_t)end_row;
	GhosttyResult result = ghostty_terminal_grid_ref(term->terminal, start, &selection->start);
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_grid_ref(term->terminal, end, &selection->end);
}

GhosttyResult ghostty_bridge_terminal_format(GhosttyBridgeTerminal term, bool recent, bool ansi,
	size_t max_bytes, uint8_t** out, size_t* out_len) {
	if (out != NULL) *out = NULL;
	if (out_len != NULL) *out_len = 0;
	if (term == NULL || out == NULL || out_len == NULL || max_bytes == 0) return GHOSTTY_INVALID_VALUE;
	uint16_t cols = 0, rows = 0;
	size_t history = 0;
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_COLS, &cols);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_ROWS, &rows);
	if (result != GHOSTTY_SUCCESS) return result;
	if (recent) {
		result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBACK_ROWS, &history);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	struct ghostty_bridge_bytes bytes = {.limit = max_bytes};
	GhosttySelection selection;
	if (history != 0) {
		size_t start = history > 2000 ? history - 2000 : 0;
		result = ghostty_bridge_selection(term, GHOSTTY_POINT_TAG_HISTORY, start, history - 1, cols, &selection);
		if (result == GHOSTTY_SUCCESS) result = ghostty_bridge_format_selection(term, &selection, ansi, false, &bytes);
		if (result != GHOSTTY_SUCCESS) goto fail;
		if (bytes.len != 0 && bytes.data[bytes.len - 1] != '\n' &&
			!ghostty_bridge_bytes_write(&bytes, (const uint8_t*)"\n", 1)) {
			result = bytes.error;
			goto fail;
		}
	}
	result = ghostty_bridge_selection(term, GHOSTTY_POINT_TAG_VIEWPORT, 0, rows - 1, cols, &selection);
	if (result == GHOSTTY_SUCCESS) result = ghostty_bridge_format_selection(term, &selection, ansi, false, &bytes);
	if (result != GHOSTTY_SUCCESS) goto fail;
	*out = bytes.data;
	*out_len = bytes.len;
	return GHOSTTY_SUCCESS;
fail:
	free(bytes.data);
	return result;
}

struct ghostty_bridge_paste_data { const uint8_t* data; size_t len; };

static bool ghostty_bridge_paste_read(void* userdata, GhosttyString mime, GhosttyWriter writer) {
	(void)mime;
	const struct ghostty_bridge_paste_data* source = userdata;
	return source->len == 0 || writer.write(writer.userdata, source->data, source->len);
}

GhosttyResult ghostty_bridge_terminal_paste(GhosttyBridgeTerminal term, const uint8_t* data,
	size_t len, bool allow_unsafe) {
	if (term == NULL || (len != 0 && data == NULL)) return GHOSTTY_INVALID_VALUE;
	if (len > CODELIMA_GHOSTTY_EFFECT_BYTES) return GHOSTTY_LIMIT_EXCEEDED;
	struct ghostty_bridge_paste_data source = {.data = data, .len = len};
	GhosttyString mime = {.ptr = (const uint8_t*)"text/plain", .len = 10};
	GhosttyPaste paste = GHOSTTY_INIT_SIZED(GhosttyPaste);
	paste.location = GHOSTTY_CLIPBOARD_LOCATION_STANDARD;
	paste.source = GHOSTTY_PASTE_SOURCE_TEXT;
	paste.mimes = &mime;
	paste.mimes_len = 1;
	paste.reader = (GhosttyMimeReader){.read = ghostty_bridge_paste_read, .userdata = &source};
	paste.allow_unsafe = allow_unsafe;
	GhosttyResult result = ghostty_terminal_paste(term->terminal, &paste, NULL);
	if (result == GHOSTTY_SUCCESS && term->error != GHOSTTY_SUCCESS) result = term->error;
	return result;
}

GhosttyResult ghostty_bridge_terminal_set_default_colors(GhosttyBridgeTerminal term,
	const GhosttyColorRgb* foreground, const GhosttyColorRgb* background,
	const GhosttyColorRgb* cursor, const GhosttyColorRgb* palette) {
	if (term == NULL) return GHOSTTY_INVALID_VALUE;
	const struct { GhosttyTerminalOption option; const void* value; } options[] = {
		{GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, foreground},
		{GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, background},
		{GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, cursor},
		{GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, palette},
	};
	for (size_t i = 0; i < sizeof(options) / sizeof(options[0]); i++) {
		GhosttyResult result = ghostty_terminal_set(term->terminal, options[i].option, options[i].value);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	return GHOSTTY_SUCCESS;
}

bool ghostty_bridge_terminal_cursor_at_prompt(GhosttyBridgeTerminal term) {
	if (term == NULL) return false;
	bool at_prompt = false;
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_AT_PROMPT, &at_prompt);
	ghostty_bridge_record_error(term, result);
	return result == GHOSTTY_SUCCESS && at_prompt;
}

GhosttyResult ghostty_bridge_terminal_compression_activity(GhosttyBridgeTerminal term, uint64_t* activity) {
	if (term == NULL || activity == NULL) return GHOSTTY_INVALID_VALUE;
	return ghostty_terminal_compression_activity(term->terminal, activity);
}

GhosttyResult ghostty_bridge_terminal_compress(GhosttyBridgeTerminal term, GhosttyTerminalCompressionResult* result) {
	if (term == NULL || result == NULL) return GHOSTTY_INVALID_VALUE;
	return ghostty_terminal_compress(term->terminal, GHOSTTY_TERMINAL_COMPRESSION_MODE_INCREMENTAL, result);
}

struct ghostty_bridge_frame_storage {
	atomic_size_t references;
	GhosttyBridgeFrame frame;
};

static void ghostty_bridge_frame_buffers_free(GhosttyBridgeFrame* frame) {
	free(frame->cells);
	free(frame->text);
	free(frame->dirty_rows);
	free(frame->selection_rows);
}

void ghostty_bridge_frame_free(GhosttyBridgeFrame* frame) {
	if (frame == NULL) return;
	struct ghostty_bridge_frame_storage* storage = frame->storage;
	if (storage == NULL) {
		ghostty_bridge_frame_buffers_free(frame);
	} else if (atomic_fetch_sub_explicit(&storage->references, 1, memory_order_acq_rel) == 1) {
		ghostty_bridge_frame_buffers_free(&storage->frame);
		free(storage);
	}
	memset(frame, 0, sizeof(*frame));
}

static GhosttyResult ghostty_bridge_frame_grapheme(GhosttyBridgeTerminal term,
	struct ghostty_bridge_bytes* bytes, GhosttyBridgeFrameCell* cell) {
	uint8_t stack[256];
	GhosttyBuffer buffer = {.ptr = stack, .cap = sizeof(stack)};
	GhosttyResult result = ghostty_render_state_row_cells_get(term->row_cells,
		GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &buffer);
	if (result == GHOSTTY_OUT_OF_SPACE) {
		if (buffer.len > bytes->limit - bytes->len) return GHOSTTY_LIMIT_EXCEEDED;
		buffer.ptr = malloc(buffer.len);
		if (buffer.ptr == NULL) return GHOSTTY_OUT_OF_MEMORY;
		buffer.cap = buffer.len;
		result = ghostty_render_state_row_cells_get(term->row_cells,
			GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_UTF8, &buffer);
	}
	if (result == GHOSTTY_SUCCESS) {
		cell->grapheme_offset = bytes->len;
		cell->grapheme_len = buffer.len;
		if (!ghostty_bridge_bytes_write(bytes, buffer.ptr, buffer.len)) result = bytes->error;
	}
	if (buffer.ptr != stack) free(buffer.ptr);
	return result;
}

static GhosttyResult ghostty_bridge_frame_hyperlink(GhosttyBridgeTerminal term, uint16_t row, uint16_t col,
	struct ghostty_bridge_bytes* bytes, GhosttyBridgeFrameCell* cell) {
	if (cell->cell.hyperlink_id == 0) return GHOSTTY_SUCCESS;
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	GhosttyResult result = ghostty_terminal_grid_ref(term->terminal,
		ghostty_bridge_make_point(GHOSTTY_POINT_TAG_VIEWPORT, row, col), &ref);
	if (result != GHOSTTY_SUCCESS) return result;
	uint8_t stack[512];
	uint8_t* value = stack;
	size_t length = 0;
	result = ghostty_grid_ref_hyperlink_uri(&ref, value, sizeof(stack), &length);
	if (result == GHOSTTY_OUT_OF_SPACE) {
		if (length > bytes->limit - bytes->len) return GHOSTTY_LIMIT_EXCEEDED;
		value = malloc(length);
		if (value == NULL) return GHOSTTY_OUT_OF_MEMORY;
		result = ghostty_grid_ref_hyperlink_uri(&ref, value, length, &length);
	}
	if (result == GHOSTTY_SUCCESS) {
		cell->hyperlink_offset = bytes->len;
		cell->hyperlink_len = length;
		if (!ghostty_bridge_bytes_write(bytes, value, length)) result = bytes->error;
	}
	if (value != stack) free(value);
	return result;
}

GhosttyResult ghostty_bridge_terminal_frame(GhosttyBridgeTerminal term, size_t max_bytes, GhosttyBridgeFrame* frame) {
	if (frame == NULL) return GHOSTTY_INVALID_VALUE;
	memset(frame, 0, sizeof(*frame));
	if (term == NULL || max_bytes == 0) return GHOSTTY_INVALID_VALUE;
	GhosttyResult result = ghostty_render_state_update(term->render_state, term->terminal);
	if (result != GHOSTTY_SUCCESS) return result;
	frame->cursor = GHOSTTY_INIT_SIZED(GhosttyRenderStateCursor);
	frame->colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
	GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FALSE;
	const GhosttyRenderStateData keys[] = {
		GHOSTTY_RENDER_STATE_DATA_COLS, GHOSTTY_RENDER_STATE_DATA_ROWS,
		GHOSTTY_RENDER_STATE_DATA_CURSOR, GHOSTTY_RENDER_STATE_DATA_COLORS,
		GHOSTTY_RENDER_STATE_DATA_DIRTY, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR,
	};
	void* values[] = {&frame->cols, &frame->rows, &frame->cursor, &frame->colors, &dirty, &term->row_iter};
	result = ghostty_render_state_get_multi(term->render_state, sizeof(keys) / sizeof(keys[0]), keys, values, NULL);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_bridge_resolve_render_colors(term, &frame->colors);
	if (result != GHOSTTY_SUCCESS) return result;
	frame->dirty = (int)dirty;
	frame->count = (size_t)frame->cols * frame->rows;
	size_t row_bytes = frame->rows * (1 + sizeof(*frame->selection_rows));
	if (row_bytes > max_bytes || frame->count > (max_bytes - row_bytes) / sizeof(*frame->cells))
		return GHOSTTY_LIMIT_EXCEEDED;
	size_t fixed_bytes = frame->count * sizeof(*frame->cells) + row_bytes;
	frame->cells = calloc(frame->count, sizeof(*frame->cells));
	frame->dirty_rows = calloc(frame->rows, 1);
	frame->selection_rows = calloc(frame->rows, sizeof(*frame->selection_rows));
	if (frame->cells == NULL || frame->dirty_rows == NULL || frame->selection_rows == NULL) {
		ghostty_bridge_frame_free(frame);
		return GHOSTTY_OUT_OF_MEMORY;
	}
	struct ghostty_bridge_bytes bytes = {.limit = max_bytes - fixed_bytes};
	const GhosttyBridgeFrame* cached = &term->cached_frame;
	bool cache_valid = cached->storage != NULL && cached->cols == frame->cols && cached->rows == frame->rows &&
		memcmp(&cached->colors, &frame->colors, sizeof(frame->colors)) == 0;
	size_t row = 0;
	while (ghostty_render_state_row_iterator_next(term->row_iter)) {
		if (row >= frame->rows) { result = GHOSTTY_INVALID_VALUE; goto fail; }
		bool row_dirty = false;
		result = ghostty_render_state_row_get(term->row_iter, GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, &row_dirty);
		if (result != GHOSTTY_SUCCESS) goto fail;
		frame->dirty_rows[row] = !cache_valid || dirty == GHOSTTY_RENDER_STATE_DIRTY_FULL || row_dirty;
		GhosttyRenderStateRowSelection selected = GHOSTTY_INIT_SIZED(GhosttyRenderStateRowSelection);
		result = ghostty_render_state_row_get(term->row_iter, GHOSTTY_RENDER_STATE_ROW_DATA_SELECTION, &selected);
		if (result == GHOSTTY_SUCCESS) {
			frame->selection_rows[row] = (GhosttyBridgeSelectionRow){
				.active = true, .start_x = selected.start_x, .end_x = selected.end_x};
		} else if (result != GHOSTTY_NO_VALUE) goto fail;
		if (cache_valid && !frame->dirty_rows[row]) {
			/* Cached rows are immutable and owned, never borrowed from Ghostty.
			 * Copy their contiguous text span and rebase offsets without touching
			 * the native row-cell iterator, grapheme tables, or hyperlink lookup. */
			size_t index = row * frame->cols;
			size_t old_start = cached->cells[index].grapheme_offset;
			size_t old_end = row + 1 < frame->rows ? cached->cells[index + frame->cols].grapheme_offset : cached->text_len;
			size_t new_start = bytes.len;
			if (old_end < old_start || old_end > cached->text_len) { result = GHOSTTY_INVALID_VALUE; goto fail; }
			if (!ghostty_bridge_bytes_write(&bytes, cached->text == NULL ? NULL : cached->text + old_start, old_end - old_start)) {
				result = bytes.error; goto fail;
			}
			memcpy(&frame->cells[index], &cached->cells[index], frame->cols * sizeof(*frame->cells));
			for (size_t col = 0; col < frame->cols; col++) {
				GhosttyBridgeFrameCell* cell = &frame->cells[index + col];
				cell->grapheme_offset = new_start + cell->grapheme_offset - old_start;
				if (cell->hyperlink_len != 0) cell->hyperlink_offset = new_start + cell->hyperlink_offset - old_start;
			}
			frame->rows_reused++;
			row++;
			continue;
		}
		frame->rows_decoded++;
		result = ghostty_render_state_row_get(term->row_iter, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &term->row_cells);
		if (result != GHOSTTY_SUCCESS) goto fail;
		for (uint16_t col = 0; col < frame->cols; col++) {
			GhosttyBridgeFrameCell* cell = &frame->cells[row * frame->cols + col];
			if (!ghostty_render_state_row_cells_next(term->row_cells)) { result = GHOSTTY_INVALID_VALUE; goto fail; }
			if (!ghostty_bridge_fill_viewport_cell(term, &frame->colors, &cell->cell)) { result = GHOSTTY_INVALID_VALUE; goto fail; }
			result = ghostty_bridge_frame_grapheme(term, &bytes, cell);
			if (result != GHOSTTY_SUCCESS) goto fail;
			result = ghostty_bridge_frame_hyperlink(term, (uint16_t)row, col, &bytes, cell);
			if (result != GHOSTTY_SUCCESS) goto fail;
		}
		row++;
	}
	if (row != frame->rows) { result = GHOSTTY_INVALID_VALUE; goto fail; }
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &frame->captures_mouse);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_CURSOR_AT_PROMPT, &frame->cursor_at_prompt);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &frame->scrollbar);
	if (result != GHOSTTY_SUCCESS) goto fail;
	frame->text = bytes.data;
	frame->text_len = bytes.len;
	struct ghostty_bridge_frame_storage* storage = malloc(sizeof(*storage));
	if (storage == NULL) { result = GHOSTTY_OUT_OF_MEMORY; goto fail_owned; }
	atomic_init(&storage->references, 2); /* Returned frame plus private cache. */
	frame->storage = storage;
	storage->frame = *frame;
	ghostty_bridge_frame_free(&term->cached_frame);
	term->cached_frame = *frame;
	return GHOSTTY_SUCCESS;
fail_owned:
	ghostty_bridge_frame_free(frame);
	return result;
fail:
	free(bytes.data);
	ghostty_bridge_frame_free(frame);
	return result;
}

GhosttyResult ghostty_bridge_terminal_checkpoint(GhosttyBridgeTerminal term, size_t max_bytes,
	uint8_t** out, size_t* out_len) {
	if (out != NULL) *out = NULL;
	if (out_len != NULL) *out_len = 0;
	if (term == NULL || out == NULL || out_len == NULL || max_bytes == 0) return GHOSTTY_INVALID_VALUE;
	bool graphics_present = false;
	GhosttyResult eligibility = ghostty_terminal_get(term->terminal,
		GHOSTTY_TERMINAL_DATA_KITTY_GRAPHICS_PRESENT, &graphics_present);
	if (eligibility != GHOSTTY_SUCCESS) return eligibility;
	if (graphics_present) return GHOSTTY_BRIDGE_UNSUPPORTED;
	const size_t hard_limit = 16u * 1024u * 1024u;
	struct ghostty_bridge_bytes bytes = {.limit = max_bytes < hard_limit ? max_bytes : hard_limit};
	GhosttyWriter writer = {.write = ghostty_bridge_bytes_write, .userdata = &bytes};
	GhosttyResult result = ghostty_snapshot_encode(term->terminal, writer);
	if (bytes.error != GHOSTTY_SUCCESS) result = bytes.error;
	if (result != GHOSTTY_SUCCESS) { free(bytes.data); return result; }
	*out = bytes.data;
	*out_len = bytes.len;
	return GHOSTTY_SUCCESS;
}

static GhosttyResult ghostty_bridge_restore_with_limit(GhosttyBridgeTerminal term,
	const uint8_t* data, size_t len, size_t allocation_limit) {
	if (term == NULL || data == NULL || len == 0) return GHOSTTY_INVALID_VALUE;
	if (len > 16u * 1024u * 1024u) return GHOSTTY_LIMIT_EXCEEDED;
	struct ghostty_bridge_owned_allocator* allocator = calloc(1, sizeof(*allocator));
	if (allocator == NULL) return GHOSTTY_OUT_OF_MEMORY;
	allocator->memory.limit = allocation_limit;
	long page_size = sysconf(_SC_PAGESIZE);
	if (page_size <= 0 || ((size_t)page_size & ((size_t)page_size - 1)) != 0) {
		free(allocator); return GHOSTTY_INVALID_VALUE;
	}
	allocator->memory.page_size = (size_t)page_size;
	allocator->allocator = (GhosttyAllocator){.ctx = &allocator->memory, .vtable = &ghostty_bridge_memory_vtable};
	GhosttySnapshotDecoder decoder = NULL;
	GhosttyTerminal restored = NULL;
	GhosttyRenderState render_state = NULL;
	GhosttyRenderStateRowIterator row_iter = NULL;
	GhosttyRenderStateRowCells row_cells = NULL;
	GhosttyResult result = ghostty_snapshot_decoder_new_buf(&allocator->allocator, &decoder, data, len);
	if (result != GHOSTTY_SUCCESS) goto fail;
	const size_t continuation_limit = CODELIMA_GHOSTTY_EFFECT_BYTES;
	const bool retain = true;
	result = ghostty_snapshot_decoder_set(decoder, GHOSTTY_SNAPSHOT_DECODER_OPT_MAX_CONTINUATION_BYTES, &continuation_limit);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_snapshot_decoder_set(decoder, GHOSTTY_SNAPSHOT_DECODER_OPT_RETAIN_CONTINUATION, &retain);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_snapshot_decoder_set(decoder, GHOSTTY_SNAPSHOT_DECODER_OPT_USE_ALLOCATOR_FOR_PAGES, &retain);
	if (result != GHOSTTY_SUCCESS) goto fail;
	bool pages_accounted = false;
	result = ghostty_snapshot_decoder_get(decoder, GHOSTTY_SNAPSHOT_DECODER_DATA_USE_ALLOCATOR_FOR_PAGES, &pages_accounted);
	if (result != GHOSTTY_SUCCESS) goto fail;
	if (!pages_accounted) { result = GHOSTTY_INVALID_VALUE; goto fail; }
	result = ghostty_snapshot_decoder_decode(decoder, &restored);
	if (result != GHOSTTY_SUCCESS) goto fail;
	size_t consumed = 0;
	result = ghostty_snapshot_decoder_get(decoder, GHOSTTY_SNAPSHOT_DECODER_DATA_SOURCE_OFFSET, &consumed);
	if (result != GHOSTTY_SUCCESS) goto fail;
	if (consumed != len) { result = GHOSTTY_INVALID_VALUE; goto fail; }
	/* Decode has validated FINISH before any current terminal state is changed. */
	result = ghostty_render_state_new(&allocator->allocator, &render_state);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_render_state_row_iterator_new(&allocator->allocator, &row_iter);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_render_state_row_cells_new(&allocator->allocator, &row_cells);
	if (result != GHOSTTY_SUCCESS) goto fail;
	struct ghostty_bridge_terminal candidate = *term;
	candidate.terminal = restored;
	result = ghostty_bridge_bind_callbacks(&candidate);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_set(restored, GHOSTTY_TERMINAL_OPT_USERDATA, term);
	if (result != GHOSTTY_SUCCESS) goto fail;
	const size_t clipboard_limit = CODELIMA_GHOSTTY_CLIPBOARD_BYTES;
	result = ghostty_terminal_set(restored, GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE_MAX_BYTES, &clipboard_limit);
	if (result != GHOSTTY_SUCCESS) goto fail;
	const size_t history_bytes = CODELIMA_GHOSTTY_SCROLLBACK_BYTES;
	const size_t history_rows = CODELIMA_GHOSTTY_SCROLLBACK_LINES;
	result = ghostty_terminal_set(restored, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &history_bytes);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_terminal_set(restored, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &history_rows);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_bridge_graphics_policy(restored);
	if (result != GHOSTTY_SUCCESS) goto fail;
	if (allocator->memory.limited) { result = GHOSTTY_LIMIT_EXCEEDED; goto fail; }
	if (allocator->memory.oom) { result = GHOSTTY_OUT_OF_MEMORY; goto fail; }
	GhosttyBridgeEffect effect = {0};
	while (ghostty_bridge_terminal_next_effect(term, &effect)) ghostty_bridge_effect_free(&effect);
	ghostty_selection_gesture_free(term->selection_gesture, term->terminal);
	term->selection_gesture = NULL;
	ghostty_search_free(term->search);
	term->search = NULL;
	term->search_selected = false;
	term->search_memory.limited = false;
	term->search_memory.oom = false;
	ghostty_render_state_row_cells_free(term->row_cells);
	ghostty_render_state_row_iterator_free(term->row_iter);
	ghostty_render_state_free(term->render_state);
	ghostty_terminal_free(term->terminal);
	free(term->restore_allocator);
	ghostty_bridge_frame_free(&term->cached_frame);
	term->restore_allocator = allocator;
	term->terminal = restored;
	term->render_state = render_state;
	term->row_iter = row_iter;
	term->row_cells = row_cells;
	term->response_len = 0;
	term->error = GHOSTTY_SUCCESS;
	ghostty_snapshot_decoder_free(decoder);
	ghostty_bridge_title_cb(restored, term);
	ghostty_bridge_pwd_cb(restored, term);
	return GHOSTTY_SUCCESS;
fail:
	ghostty_render_state_row_cells_free(row_cells);
	ghostty_render_state_row_iterator_free(row_iter);
	ghostty_render_state_free(render_state);
	ghostty_snapshot_decoder_free(decoder);
	ghostty_terminal_free(restored);
	if (allocator->memory.limited) result = GHOSTTY_LIMIT_EXCEEDED;
	else if (allocator->memory.oom) result = GHOSTTY_OUT_OF_MEMORY;
	free(allocator);
	return result;
}

GhosttyResult ghostty_bridge_terminal_restore(GhosttyBridgeTerminal term, const uint8_t* data, size_t len) {
	return ghostty_bridge_restore_with_limit(term, data, len, CODELIMA_GHOSTTY_RESTORE_BYTES);
}

GhosttyResult ghostty_bridge_terminal_restore_memory(GhosttyBridgeTerminal term,
	size_t* used, size_t* peak, size_t* limit) {
	if (term == NULL || used == NULL || peak == NULL || limit == NULL) return GHOSTTY_INVALID_VALUE;
	*used = 0; *peak = 0; *limit = 0;
	if (term->restore_allocator == NULL) return GHOSTTY_NO_VALUE;
	*used = term->restore_allocator->memory.used;
	*peak = term->restore_allocator->memory.peak;
	*limit = term->restore_allocator->memory.limit;
	return GHOSTTY_SUCCESS;
}

GhosttyResult ghostty_bridge_terminal_selection_event(GhosttyBridgeTerminal term,
	GhosttyBridgeSelectionAction action, int col, int row,
	GhosttySelectionGestureBehavior behavior, bool rectangle) {
	if (term == NULL) return GHOSTTY_INVALID_VALUE;
	if (col < 0 || row < 0 || col > UINT16_MAX || row > UINT16_MAX) return GHOSTTY_INVALID_VALUE;
	if (action == GHOSTTY_BRIDGE_SELECTION_CANCEL) {
		ghostty_selection_gesture_free(term->selection_gesture, term->terminal);
		term->selection_gesture = NULL;
		term->search_selected = false;
		return ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL);
	}
	GhosttySelectionGestureEventType type;
	switch (action) {
	case GHOSTTY_BRIDGE_SELECTION_PRESS: type = GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_PRESS; break;
	case GHOSTTY_BRIDGE_SELECTION_DRAG: type = GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_DRAG; break;
	case GHOSTTY_BRIDGE_SELECTION_RELEASE: type = GHOSTTY_SELECTION_GESTURE_EVENT_TYPE_RELEASE; break;
	default: return GHOSTTY_INVALID_VALUE;
	}
	if (behavior < GHOSTTY_SELECTION_GESTURE_BEHAVIOR_CELL || behavior > GHOSTTY_SELECTION_GESTURE_BEHAVIOR_OUTPUT)
		return GHOSTTY_INVALID_VALUE;
	uint16_t cols = 0, rows = 0;
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_COLS, &cols);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_ROWS, &rows);
	if (result != GHOSTTY_SUCCESS) return result;
	if (cols == 0 || rows == 0) return GHOSTTY_INVALID_VALUE;
	if (action == GHOSTTY_BRIDGE_SELECTION_PRESS && (col < 0 || row < 0 || col >= cols || row >= rows))
		return GHOSTTY_INVALID_VALUE;
	if (col >= cols) col = cols - 1;
	if (row >= rows) row = rows - 1;
	if (term->selection_gesture == NULL) {
		if (action != GHOSTTY_BRIDGE_SELECTION_PRESS) return GHOSTTY_NO_VALUE;
		result = ghostty_selection_gesture_new(NULL, &term->selection_gesture);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	GhosttyGridRef ref = GHOSTTY_INIT_SIZED(GhosttyGridRef);
	result = ghostty_terminal_grid_ref(term->terminal,
		ghostty_bridge_make_point(GHOSTTY_POINT_TAG_VIEWPORT, row, col), &ref);
	if (result != GHOSTTY_SUCCESS) return result;
	GhosttySelectionGestureEvent event = NULL;
	result = ghostty_selection_gesture_event_new(NULL, &event, type);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_REF, &ref);
	if (result != GHOSTTY_SUCCESS) goto done;
	if (action != GHOSTTY_BRIDGE_SELECTION_RELEASE) {
		/* A terminal UI has cell, not pixel coordinates. Unit cells preserve
		 * Ghostty's drag boundary geometry without inventing selection rules. */
		GhosttySurfacePosition position = {.x = col + 0.5, .y = row + 0.5};
		result = ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_POSITION, &position);
		if (result != GHOSTTY_SUCCESS) goto done;
	}
	if (action == GHOSTTY_BRIDGE_SELECTION_PRESS) {
		GhosttySelectionGestureBehaviors behaviors = {
			.single_click = behavior, .double_click = behavior, .triple_click = behavior};
		result = ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_BEHAVIORS, &behaviors);
		if (result != GHOSTTY_SUCCESS) goto done;
		term->search_selected = false;
	} else if (action == GHOSTTY_BRIDGE_SELECTION_DRAG) {
		GhosttySelectionGestureGeometry geometry = {.columns = cols, .cell_width = 1, .screen_height = rows};
		result = ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_GEOMETRY, &geometry);
		if (result != GHOSTTY_SUCCESS) goto done;
		result = ghostty_selection_gesture_event_set(event, GHOSTTY_SELECTION_GESTURE_EVENT_OPT_RECTANGLE, &rectangle);
		if (result != GHOSTTY_SUCCESS) goto done;
	}
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	result = ghostty_selection_gesture_event(term->selection_gesture, term->terminal, event, &selection);
	if (result == GHOSTTY_SUCCESS) {
		result = ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &selection);
	} else if (result == GHOSTTY_NO_VALUE) {
		/* Release preserves the installed selection. A plain press clears the
		 * previous selection until Ghostty reports a drag range. */
		result = action == GHOSTTY_BRIDGE_SELECTION_PRESS ?
			ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL) : GHOSTTY_SUCCESS;
	}
done:
	ghostty_selection_gesture_event_free(event);
	return result;
}

GhosttyResult ghostty_bridge_terminal_selection_format(GhosttyBridgeTerminal term,
	size_t max_bytes, uint8_t** out, size_t* out_len) {
	if (out != NULL) *out = NULL;
	if (out_len != NULL) *out_len = 0;
	if (term == NULL || out == NULL || out_len == NULL || max_bytes == 0) return GHOSTTY_INVALID_VALUE;
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_SELECTION, &selection);
	if (result != GHOSTTY_SUCCESS) return result;
	struct ghostty_bridge_bytes bytes = {
		.limit = max_bytes < CODELIMA_GHOSTTY_CLIPBOARD_BYTES ? max_bytes : CODELIMA_GHOSTTY_CLIPBOARD_BYTES};
	result = ghostty_bridge_format_selection(term, &selection, false, true, &bytes);
	if (result != GHOSTTY_SUCCESS) { free(bytes.data); return result; }
	*out = bytes.data;
	*out_len = bytes.len;
	return GHOSTTY_SUCCESS;
}

/* Search retains copied page data and match indexes, so bounding only the
 * needle or returned match count would not bound its memory. Use the public
 * allocator API to cap all search-owned allocation at 32 MiB per terminal. */
static void* ghostty_bridge_memory_alloc(void* context, size_t len, uint8_t alignment, uintptr_t ret_addr) {
	(void)ret_addr;
	struct ghostty_bridge_memory* memory = context;
	/* The pinned Zig C adapter passes std.mem.Alignment's log2 enum, despite
	 * allocator.h describing byte alignment. Honor the actual pinned ABI. */
	if (alignment >= sizeof(size_t) * CHAR_BIT) { memory->limited = true; return NULL; }
	size_t align_bytes = (size_t)1 << alignment;
	/* Native minimum-page alignment can be smaller than the running kernel's
	 * page size (e.g. a 64KiB Linux kernel). Round and account entire mappings. */
	bool mapped = memory->page_size != 0 && align_bytes >= 4096 && align_bytes <= memory->page_size;
	size_t allocation_alignment = mapped ? memory->page_size : align_bytes;
	if (len > SIZE_MAX - (allocation_alignment - 1)) { memory->limited = true; return NULL; }
	size_t allocated = (len + allocation_alignment - 1) & ~(allocation_alignment - 1);
	if (allocated > memory->limit - memory->used) {
		memory->limited = true;
		return NULL;
	}
	/* Native page pools decommit/recommit mappings. Give those requests real
	 * anonymous mappings, never malloc arenas potentially shared with metadata. */
	void* value;
	if (mapped) {
		value = mmap(NULL, allocated, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
		if (value == MAP_FAILED) value = NULL;
	} else {
		value = align_bytes <= _Alignof(max_align_t) ? malloc(allocated) : aligned_alloc(align_bytes, allocated);
	}
	if (value != NULL) {
		/* Native UntouchedPool requires its backing allocator to zero pages. */
		if (!mapped) memset(value, 0, allocated);
		memory->used += allocated;
		if (memory->used > memory->peak) memory->peak = memory->used;
	}
	else memory->oom = true;
	return value;
}

static bool ghostty_bridge_memory_resize(void* context, void* value, size_t old_len,
	uint8_t alignment, size_t new_len, uintptr_t ret_addr) {
	(void)context; (void)value; (void)old_len; (void)alignment; (void)new_len; (void)ret_addr;
	return false;
}

static void* ghostty_bridge_memory_remap(void* context, void* value, size_t old_len,
	uint8_t alignment, size_t new_len, uintptr_t ret_addr) {
	(void)context; (void)value; (void)old_len; (void)alignment; (void)new_len; (void)ret_addr;
	return NULL;
}

static void ghostty_bridge_memory_release(void* context, void* value, size_t len,
	uint8_t alignment, uintptr_t ret_addr) {
	(void)ret_addr;
	struct ghostty_bridge_memory* memory = context;
	size_t align_bytes = (size_t)1 << alignment;
	bool mapped = memory->page_size != 0 && align_bytes >= 4096 && align_bytes <= memory->page_size;
	size_t allocation_alignment = mapped ? memory->page_size : align_bytes;
	size_t allocated = (len + allocation_alignment - 1) & ~(allocation_alignment - 1);
	memory->used -= allocated;
	if (mapped) {
		(void)munmap(value, allocated);
	} else free(value);
}

static const GhosttyAllocatorVtable ghostty_bridge_memory_vtable = {
	.alloc = ghostty_bridge_memory_alloc, .resize = ghostty_bridge_memory_resize,
	.remap = ghostty_bridge_memory_remap, .free = ghostty_bridge_memory_release,
};

static GhosttyResult ghostty_bridge_search_result(GhosttyBridgeTerminal term, GhosttyResult result) {
	if (!term->search_memory.limited && !term->search_memory.oom) return result;
	GhosttyResult failure = term->search_memory.limited ? GHOSTTY_LIMIT_EXCEEDED : GHOSTTY_OUT_OF_MEMORY;
	/* Upstream feed can log and tolerate allocation failure internally. The
	 * allocator latch still turns budget exhaustion into an explicit failure
	 * and cancellation, never a misleading permanently-incomplete search. */
	ghostty_search_free(term->search);
	term->search = NULL;
	term->search_memory.limited = false;
	term->search_memory.oom = false;
	if (term->search_selected) ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL);
	term->search_selected = false;
	return failure;
}

static GhosttyResult ghostty_bridge_search_status(GhosttyBridgeTerminal term, GhosttyBridgeSearchStatus* status) {
	memset(status, 0, sizeof(*status));
	if (term->search == NULL) { status->caught_up = true; return GHOSTTY_SUCCESS; }
	GhosttySearchStatus native_status;
	GhosttyResult result = ghostty_search_get(term->search, GHOSTTY_SEARCH_DATA_STATUS, &native_status);
	if (result != GHOSTTY_SUCCESS) return result;
	status->caught_up = native_status == GHOSTTY_SEARCH_STATUS_COMPLETE;
	result = ghostty_search_get(term->search, GHOSTTY_SEARCH_DATA_TOTAL_MATCHES, &status->total_matches);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_search_get(term->search, GHOSTTY_SEARCH_DATA_SELECTED_INDEX, &status->selected_index);
	status->has_selected = result == GHOSTTY_SUCCESS;
	return result == GHOSTTY_NO_VALUE ? GHOSTTY_SUCCESS : result;
}

static GhosttyResult ghostty_bridge_search_selection(GhosttyBridgeTerminal term) {
	GhosttySelection selection = GHOSTTY_INIT_SIZED(GhosttySelection);
	GhosttyResult result = ghostty_search_get(term->search, GHOSTTY_SEARCH_DATA_SELECTED_MATCH, &selection);
	if (result == GHOSTTY_NO_VALUE) {
		term->search_selected = false;
		return ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL);
	}
	if (result != GHOSTTY_SUCCESS) return result;
	return ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &selection);
}

GhosttyResult ghostty_bridge_terminal_search_start(GhosttyBridgeTerminal term, const uint8_t* query, size_t len) {
	if (term == NULL || (query == NULL && len != 0)) return GHOSTTY_INVALID_VALUE;
	if (len > 4096) return GHOSTTY_LIMIT_EXCEEDED;
	if (len == 0) {
		ghostty_search_free(term->search);
		term->search = NULL;
		term->search_memory.limited = false;
		term->search_memory.oom = false;
		GhosttyResult result = term->search_selected ?
			ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL) : GHOSTTY_SUCCESS;
		term->search_selected = false;
		return result;
	}
	if (term->search == NULL) {
		term->search_memory.limit = CODELIMA_GHOSTTY_SEARCH_BYTES;
		term->search_allocator = (GhosttyAllocator){.ctx = &term->search_memory, .vtable = &ghostty_bridge_memory_vtable};
		GhosttyResult result = ghostty_search_new(&term->search_allocator, &term->search, term->terminal);
		if (result != GHOSTTY_SUCCESS) return ghostty_bridge_search_result(term, result);
	}
	GhosttyString needle = {.ptr = query, .len = len};
	GhosttyResult result = ghostty_search_set(term->search, GHOSTTY_SEARCH_OPT_NEEDLE, &needle);
	result = ghostty_bridge_search_result(term, result);
	if (result != GHOSTTY_SUCCESS) return result;
	if (term->search_selected) {
		result = ghostty_terminal_set(term->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, NULL);
		term->search_selected = false;
	}
	return result;
}

GhosttyResult ghostty_bridge_terminal_search_tick(GhosttyBridgeTerminal term,
	unsigned int max_steps, GhosttyBridgeSearchStatus* status) {
	if (term == NULL || status == NULL || max_steps == 0) return GHOSTTY_INVALID_VALUE;
	if (term->search == NULL) return ghostty_bridge_search_status(term, status);
	if (max_steps > 32) max_steps = 32;
	GhosttyResult result = ghostty_search_feed(term->search);
	for (unsigned int step = 1; result == GHOSTTY_SUCCESS && !term->search_memory.limited && !term->search_memory.oom && step < max_steps; step++) {
		GhosttySearchStatus native_status;
		result = ghostty_search_get(term->search, GHOSTTY_SEARCH_DATA_STATUS, &native_status);
		if (result != GHOSTTY_SUCCESS || native_status == GHOSTTY_SEARCH_STATUS_COMPLETE) break;
		result = native_status == GHOSTTY_SEARCH_STATUS_FEED_REQUIRED ?
			ghostty_search_feed(term->search) : ghostty_search_tick(term->search, NULL);
	}
	result = ghostty_bridge_search_result(term, result);
	if (result != GHOSTTY_SUCCESS) return result;
	if (term->search_selected) {
		result = ghostty_bridge_search_selection(term);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	return ghostty_bridge_search_status(term, status);
}

GhosttyResult ghostty_bridge_terminal_search_move(GhosttyBridgeTerminal term,
	bool next, GhosttyBridgeSearchStatus* status) {
	if (term == NULL || status == NULL) return GHOSTTY_INVALID_VALUE;
	if (term->search == NULL) return ghostty_bridge_search_status(term, status);
	GhosttyResult result = ghostty_search_set(term->search,
		next ? GHOSTTY_SEARCH_OPT_SELECT_NEXT : GHOSTTY_SEARCH_OPT_SELECT_PREV, NULL);
	result = ghostty_bridge_search_result(term, result);
	if (result != GHOSTTY_SUCCESS && result != GHOSTTY_NO_VALUE) return result;
	if (result == GHOSTTY_SUCCESS) {
		ghostty_selection_gesture_free(term->selection_gesture, term->terminal);
		term->selection_gesture = NULL;
		term->search_selected = true;
		result = ghostty_bridge_search_selection(term);
		if (result != GHOSTTY_SUCCESS) return result;
	}
	return ghostty_bridge_search_status(term, status);
}

void ghostty_bridge_graphics_frame_free(GhosttyBridgeGraphicsFrame* frame) {
	if (frame == NULL) return;
	free(frame->assets);
	free(frame->placements);
	free(frame->rgba);
	memset(frame, 0, sizeof(*frame));
}

static GhosttyResult ghostty_bridge_graphics_asset(GhosttyKittyGraphicsImage image,
	GhosttyBridgeGraphicsAsset* asset, struct ghostty_bridge_bytes* bytes) {
	GhosttyKittyImageFormat format;
	GhosttyKittyImageCompression compression;
	const uint8_t* pixels = NULL;
	size_t length = 0;
	const GhosttyKittyGraphicsImageData keys[] = {
		GHOSTTY_KITTY_IMAGE_DATA_ID, GHOSTTY_KITTY_IMAGE_DATA_GENERATION,
		GHOSTTY_KITTY_IMAGE_DATA_WIDTH, GHOSTTY_KITTY_IMAGE_DATA_HEIGHT,
		GHOSTTY_KITTY_IMAGE_DATA_FORMAT, GHOSTTY_KITTY_IMAGE_DATA_COMPRESSION,
		GHOSTTY_KITTY_IMAGE_DATA_DATA_LEN, GHOSTTY_KITTY_IMAGE_DATA_DATA_PTR,
	};
	void* values[] = {&asset->image_id, &asset->generation, &asset->width, &asset->height,
		&format, &compression, &length, &pixels};
	GhosttyResult result = ghostty_kitty_graphics_image_get_multi(image,
		sizeof(keys) / sizeof(keys[0]), keys, values, NULL);
	if (result != GHOSTTY_SUCCESS) return result;
	if (pixels == NULL || asset->width == 0 || asset->height == 0 || compression != GHOSTTY_KITTY_IMAGE_COMPRESSION_NONE)
		return GHOSTTY_INVALID_VALUE;
	uint64_t pixel_count = (uint64_t)asset->width * asset->height;
	if (pixel_count > CODELIMA_GHOSTTY_GRAPHICS_PIXELS) return GHOSTTY_LIMIT_EXCEEDED;
	size_t bytes_per_pixel;
	switch (format) {
	case GHOSTTY_KITTY_IMAGE_FORMAT_RGBA: bytes_per_pixel = 4; break;
	case GHOSTTY_KITTY_IMAGE_FORMAT_RGB: bytes_per_pixel = 3; break;
	case GHOSTTY_KITTY_IMAGE_FORMAT_GRAY_ALPHA: bytes_per_pixel = 2; break;
	case GHOSTTY_KITTY_IMAGE_FORMAT_GRAY: bytes_per_pixel = 1; break;
	default: return GHOSTTY_INVALID_VALUE;
	}
	if (length != (size_t)pixel_count * bytes_per_pixel) return GHOSTTY_INVALID_VALUE;
	asset->rgba_offset = bytes->len;
	asset->rgba_len = (size_t)pixel_count * 4;
	if (asset->rgba_len > bytes->limit - bytes->len) return GHOSTTY_LIMIT_EXCEEDED;
	if (format == GHOSTTY_KITTY_IMAGE_FORMAT_RGBA)
		return ghostty_bridge_bytes_write(bytes, pixels, length) ? GHOSTTY_SUCCESS : bytes->error;
	/* Convert already-decoded native pixels, never parse an image protocol. */
	uint8_t rgba[4096];
	for (size_t index = 0; index < pixel_count;) {
		size_t count = (size_t)pixel_count - index;
		if (count > sizeof(rgba) / 4) count = sizeof(rgba) / 4;
		for (size_t j = 0; j < count; j++) {
			const uint8_t* pixel = pixels + (index + j) * bytes_per_pixel;
			uint8_t* output = rgba + j * 4;
			if (format == GHOSTTY_KITTY_IMAGE_FORMAT_RGB) {
				output[0] = pixel[0]; output[1] = pixel[1]; output[2] = pixel[2]; output[3] = 255;
			} else {
				output[0] = output[1] = output[2] = pixel[0];
				output[3] = format == GHOSTTY_KITTY_IMAGE_FORMAT_GRAY_ALPHA ? pixel[1] : 255;
			}
		}
		if (!ghostty_bridge_bytes_write(bytes, rgba, count * 4)) return bytes->error;
		index += count;
	}
	return GHOSTTY_SUCCESS;
}

GhosttyResult ghostty_bridge_terminal_graphics(GhosttyBridgeTerminal term,
	size_t max_bytes, GhosttyBridgeGraphicsFrame* frame) {
	if (frame == NULL) return GHOSTTY_INVALID_VALUE;
	memset(frame, 0, sizeof(*frame));
	if (term == NULL || max_bytes == 0) return GHOSTTY_INVALID_VALUE;
	if (max_bytes > CODELIMA_GHOSTTY_GRAPHICS_BYTES) max_bytes = CODELIMA_GHOSTTY_GRAPHICS_BYTES;
	GhosttyKittyGraphics graphics = NULL;
	GhosttyResult result = ghostty_terminal_get(term->terminal, GHOSTTY_TERMINAL_DATA_KITTY_GRAPHICS, &graphics);
	if (result != GHOSTTY_SUCCESS) return result;
	result = ghostty_kitty_graphics_get(graphics, GHOSTTY_KITTY_GRAPHICS_DATA_GENERATION, &frame->generation);
	if (result != GHOSTTY_SUCCESS || frame->generation == 0) return result;
	const size_t fixed = CODELIMA_GHOSTTY_GRAPHICS_ASSETS * sizeof(*frame->assets) +
		CODELIMA_GHOSTTY_GRAPHICS_PLACEMENTS * sizeof(*frame->placements);
	if (fixed > max_bytes) return GHOSTTY_LIMIT_EXCEEDED;
	frame->assets = calloc(CODELIMA_GHOSTTY_GRAPHICS_ASSETS, sizeof(*frame->assets));
	frame->placements = calloc(CODELIMA_GHOSTTY_GRAPHICS_PLACEMENTS, sizeof(*frame->placements));
	if (frame->assets == NULL || frame->placements == NULL) {
		ghostty_bridge_graphics_frame_free(frame);
		return GHOSTTY_OUT_OF_MEMORY;
	}
	struct ghostty_bridge_bytes bytes = {.limit = max_bytes - fixed};
	GhosttyKittyGraphicsPlacementIterator iterator = NULL;
	result = ghostty_kitty_graphics_placement_iterator_new(NULL, &iterator);
	if (result != GHOSTTY_SUCCESS) goto fail;
	result = ghostty_kitty_graphics_get(graphics, GHOSTTY_KITTY_GRAPHICS_DATA_PLACEMENT_ITERATOR, &iterator);
	if (result != GHOSTTY_SUCCESS) goto fail;
	size_t visited = 0;
	while (ghostty_kitty_graphics_placement_next(iterator)) {
		/* Bound work even when most stored placements have scrolled offscreen. */
		if (++visited > 4096) { result = GHOSTTY_LIMIT_EXCEEDED; goto fail; }
		GhosttyBridgeGraphicsPlacement placement = {0};
		bool virtual_placement = false;
		const GhosttyKittyGraphicsPlacementData keys[] = {
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IMAGE_ID,
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_PLACEMENT_ID,
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_IS_VIRTUAL,
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_Z,
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_X_OFFSET,
			GHOSTTY_KITTY_GRAPHICS_PLACEMENT_DATA_Y_OFFSET,
		};
		void* values[] = {&placement.image_id, &placement.placement_id, &virtual_placement,
			&placement.z, &placement.offset_x, &placement.offset_y};
		result = ghostty_kitty_graphics_placement_get_multi(iterator, sizeof(keys) / sizeof(keys[0]), keys, values, NULL);
		if (result != GHOSTTY_SUCCESS) goto fail;
		if (virtual_placement) continue;
		GhosttyKittyGraphicsImage image = ghostty_kitty_graphics_image(graphics, placement.image_id);
		if (image == NULL) { result = GHOSTTY_INVALID_VALUE; goto fail; }
		placement.geometry = GHOSTTY_INIT_SIZED(GhosttyKittyGraphicsPlacementRenderInfo);
		result = ghostty_kitty_graphics_placement_render_info(iterator, image, term->terminal, &placement.geometry);
		if (result == GHOSTTY_NO_VALUE) continue;
		if (result != GHOSTTY_SUCCESS) goto fail;
		if (!placement.geometry.viewport_visible) continue;
		size_t asset_index = 0;
		while (asset_index < frame->assets_len && frame->assets[asset_index].image_id != placement.image_id) asset_index++;
		if (asset_index == frame->assets_len) {
			if (asset_index >= CODELIMA_GHOSTTY_GRAPHICS_ASSETS) { result = GHOSTTY_LIMIT_EXCEEDED; goto fail; }
			result = ghostty_bridge_graphics_asset(image, &frame->assets[asset_index], &bytes);
			if (result == GHOSTTY_NO_VALUE) continue; /* Metadata with pending pixels. */
			if (result != GHOSTTY_SUCCESS) goto fail;
			frame->assets_len++;
		}
		if (frame->placements_len >= CODELIMA_GHOSTTY_GRAPHICS_PLACEMENTS) { result = GHOSTTY_LIMIT_EXCEEDED; goto fail; }
		placement.asset_index = asset_index;
		frame->placements[frame->placements_len++] = placement;
	}
	ghostty_kitty_graphics_placement_iterator_free(iterator);
	frame->rgba = bytes.data;
	frame->rgba_len = bytes.len;
	return GHOSTTY_SUCCESS;
fail:
	ghostty_kitty_graphics_placement_iterator_free(iterator);
	free(bytes.data);
	ghostty_bridge_graphics_frame_free(frame);
	return result;
}
