/* Standalone native contract tests. Including the adapter also permits precise
 * allocator accounting checks without adding test-only production ABI. */
#include "../ghostty_bridge_compat.c"

/* PNG codec behavior is tested in Go; native graphics fixtures use raw RGBA. */
bool codelimaGhosttyDecodePNG(GhosttyAllocator* allocator, uint8_t* data, size_t len, GhosttySysImage* out) {
	(void)allocator; (void)data; (void)len; (void)out;
	return false;
}

#define CHECK(value) do { if (!(value)) { \
	fprintf(stderr, "%s:%d: check failed: %s\n", __FILE__, __LINE__, #value); \
	exit(1); \
} } while (0)

static void feed(GhosttyBridgeTerminal term, const char* value) {
	ghostty_bridge_terminal_write(term, (const uint8_t*)value, strlen(value));
	CHECK(ghostty_bridge_terminal_error(term) == GHOSTTY_SUCCESS);
}

static void check_text(GhosttyBridgeTerminal term, const char* expected) {
	uint8_t* value = NULL;
	size_t len = 0;
	CHECK(ghostty_bridge_terminal_format(term, false, false, 4096, &value, &len) == GHOSTTY_SUCCESS);
	CHECK(len >= strlen(expected));
	CHECK(memcmp(value, expected, strlen(expected)) == 0);
	ghostty_bridge_free(value);
}

static void test_frame_and_format(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(20, 4);
	CHECK(term != NULL);
	feed(term, "hello \x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\");
	check_text(term, "hello link");
	uint8_t* value = (void*)1;
	size_t len = 99;
	CHECK(ghostty_bridge_terminal_format(term, false, false, 3, &value, &len) == GHOSTTY_LIMIT_EXCEEDED);
	CHECK(value == NULL && len == 0);
	GhosttyBridgeFrame frame;
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	CHECK(frame.cols == 20 && frame.rows == 4 && frame.count == 80);
	CHECK(frame.cells[0].cell.codepoint == 'h');
	CHECK(frame.cells[6].hyperlink_len == strlen("https://example.com"));
	CHECK(memcmp(frame.text + frame.cells[6].hyperlink_offset, "https://example.com", frame.cells[6].hyperlink_len) == 0);
	feed(term, "\rCHANGED");
	CHECK(frame.cells[0].cell.codepoint == 'h');
	CHECK(memcmp(frame.text + frame.cells[6].hyperlink_offset, "https://example.com", frame.cells[6].hyperlink_len) == 0);
	ghostty_bridge_frame_free(&frame);
	CHECK(frame.cells == NULL && frame.text == NULL && frame.selection_rows == NULL);
	CHECK(ghostty_bridge_terminal_frame(term, 1, &frame) == GHOSTTY_LIMIT_EXCEEDED);
	ghostty_bridge_terminal_free(term);
}

static void test_frame_dirty_row_reuse(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(20, 4);
	CHECK(term != NULL);
	feed(term, "first\r\n\x1b]8;;https://example.com\x1b\\linked\x1b]8;;\x1b\\\r\nthird");
	GhosttyBridgeFrame first, unchanged, partial, colors;
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &first) == GHOSTTY_SUCCESS);
	CHECK(first.rows_decoded == 4 && first.rows_reused == 0);
	ghostty_bridge_render_state_mark_clean(term);
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &unchanged) == GHOSTTY_SUCCESS);
	CHECK(unchanged.rows_decoded == 0 && unchanged.rows_reused == 4);
	CHECK(unchanged.text_len == first.text_len && memcmp(unchanged.text, first.text, first.text_len) == 0);
	ghostty_bridge_render_state_mark_clean(term);
	feed(term, "\x1b[1;1Hchanged");
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &partial) == GHOSTTY_SUCCESS);
	CHECK(partial.rows_decoded > 0 && partial.rows_decoded < 4 && partial.rows_reused > 0);
	CHECK(partial.cells[0].cell.codepoint == 'c' && first.cells[0].cell.codepoint == 'f');
	CHECK(partial.cells[20].hyperlink_len == strlen("https://example.com"));
	CHECK(memcmp(partial.text + partial.cells[20].hyperlink_offset, "https://example.com", partial.cells[20].hyperlink_len) == 0);
	ghostty_bridge_render_state_mark_clean(term);
	const GhosttyColorRgb foreground = {.r = 0x12, .g = 0x34, .b = 0x56};
	CHECK(ghostty_bridge_terminal_set_default_colors(term, &foreground, NULL, NULL, NULL) == GHOSTTY_SUCCESS);
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &colors) == GHOSTTY_SUCCESS);
	CHECK(colors.rows_decoded == 4 && colors.rows_reused == 0);
	CHECK(colors.cells[20].cell.fg_r == 0x12 && colors.cells[20].cell.fg_g == 0x34 && colors.cells[20].cell.fg_b == 0x56);
	printf("Dirty-row accounting: clean=%zu decoded/%zu reused; partial=%zu decoded/%zu reused\n",
		unchanged.rows_decoded, unchanged.rows_reused, partial.rows_decoded, partial.rows_reused);
	ghostty_bridge_render_state_mark_clean(term);
	feed(term, "\x1b]10;#abcdef\a");
	GhosttyBridgeFrame osc;
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &osc) == GHOSTTY_SUCCESS);
	CHECK(osc.rows_decoded == 4 && osc.cells[20].cell.fg_r == 0xab && osc.cells[20].cell.fg_g == 0xcd);
	ghostty_bridge_frame_free(&osc);
	ghostty_bridge_terminal_free(term);
	/* Even terminal destruction cannot invalidate a retained frame. */
	CHECK(first.cells[0].cell.codepoint == 'f' && partial.cells[0].cell.codepoint == 'c');
	ghostty_bridge_frame_free(&first);
	ghostty_bridge_frame_free(&unchanged);
	ghostty_bridge_frame_free(&partial);
	ghostty_bridge_frame_free(&colors);
}

static void test_effects_and_paste(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(20, 4);
	CHECK(term != NULL);
	feed(term, "\x1b]52;c;aGVsbG8=\a");
	GhosttyBridgeEffect effect;
	CHECK(ghostty_bridge_terminal_next_effect(term, &effect));
	CHECK(effect.kind == GHOSTTY_BRIDGE_EFFECT_CLIPBOARD);
	CHECK(effect.len == 5 && memcmp(effect.data, "hello", 5) == 0);
	ghostty_bridge_effect_free(&effect);
	CHECK(!ghostty_bridge_terminal_next_effect(term, &effect));
	feed(term, "\x1b]52;c;\a");
	CHECK(ghostty_bridge_terminal_next_effect(term, &effect));
	CHECK(effect.kind == GHOSTTY_BRIDGE_EFFECT_CLIPBOARD && effect.len == 0);
	ghostty_bridge_effect_free(&effect);
	feed(term, "\x1b]5522;type=write:id=new\x1b\\"
		"\x1b]5522;type=wdata:mime=dGV4dC9wbGFpbg==;bmV3\x1b\\"
		"\x1b]5522;type=wdata\x1b\\");
	CHECK(!ghostty_bridge_terminal_next_effect(term, &effect));
	uint8_t response[1024];
	int response_len = ghostty_bridge_terminal_read_response(term, response, sizeof(response));
	const char* denied = "\x1b]5522;type=write:status=ENOSYS:id=new\x1b\\";
	CHECK(response_len == (int)strlen(denied) && memcmp(response, denied, response_len) == 0);
	CHECK(ghostty_bridge_terminal_paste(term, (const uint8_t*)"a\nb", 3, false) == GHOSTTY_REJECTED);
	CHECK(!ghostty_bridge_terminal_has_response(term));
	CHECK(ghostty_bridge_terminal_paste(term, (const uint8_t*)"a\nb", 3, true) == GHOSTTY_SUCCESS);
	response_len = ghostty_bridge_terminal_read_response(term, response, sizeof(response));
	CHECK(response_len == 3 && memcmp(response, "a\rb", 3) == 0);
	feed(term, "\x1b[?4m");
	response_len = ghostty_bridge_terminal_read_response(term, response, sizeof(response));
	CHECK(response_len == 7 && memcmp(response, "\x1b[>4;0m", 7) == 0);
	ghostty_bridge_terminal_free(term);
}

static void test_snapshot(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(20, 4);
	CHECK(term != NULL);
	feed(term, "hello ");
	const uint8_t unfinished[] = {0xe2, 0x82};
	ghostty_bridge_terminal_write(term, unfinished, sizeof(unfinished));
	uint8_t* snapshot = NULL;
	size_t len = 0;
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &snapshot, &len) == GHOSTTY_SUCCESS);
	CHECK(snapshot != NULL && len > 0);
	feed(term, "\xAC world");
	check_text(term, "hello \xE2\x82\xAC world");
	CHECK(ghostty_bridge_terminal_restore(term, snapshot, len - 1) != GHOSTTY_SUCCESS);
	check_text(term, "hello \xE2\x82\xAC world");
	uint8_t* trailing = malloc(len + 1);
	CHECK(trailing != NULL);
	memcpy(trailing, snapshot, len);
	trailing[len] = 0;
	CHECK(ghostty_bridge_terminal_restore(term, trailing, len + 1) != GHOSTTY_SUCCESS);
	free(trailing);
	CHECK(ghostty_bridge_terminal_restore(term, snapshot, len) == GHOSTTY_SUCCESS);
	uint8_t* second = NULL;
	size_t second_len = 0;
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &second, &second_len) == GHOSTTY_SUCCESS);
	CHECK(ghostty_bridge_terminal_restore(term, second, second_len) == GHOSTTY_SUCCESS);
	feed(term, "\xAC resumed");
	check_text(term, "hello \xE2\x82\xAC resumed");
	ghostty_bridge_free(second);
	ghostty_bridge_free(snapshot);
	ghostty_bridge_terminal_free(term);
}

static void test_selection_search(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(30, 5);
	CHECK(term != NULL);
	feed(term, "alpha ALPHA omega\r\nalpha");
	CHECK(ghostty_bridge_terminal_selection_event(term, GHOSTTY_BRIDGE_SELECTION_PRESS,
		1, 0, GHOSTTY_SELECTION_GESTURE_BEHAVIOR_WORD, false) == GHOSTTY_SUCCESS);
	uint8_t* text = NULL;
	size_t len = 0;
	CHECK(ghostty_bridge_terminal_selection_format(term, 65536, &text, &len) == GHOSTTY_SUCCESS);
	CHECK(len == 5 && memcmp(text, "alpha", 5) == 0);
	ghostty_bridge_free(text);
	GhosttyBridgeFrame frame;
	CHECK(ghostty_bridge_terminal_frame(term, 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	CHECK(frame.selection_rows[0].active && frame.selection_rows[0].start_x == 0 && frame.selection_rows[0].end_x == 4);
	ghostty_bridge_frame_free(&frame);
	CHECK(ghostty_bridge_terminal_selection_event(term, GHOSTTY_BRIDGE_SELECTION_CANCEL,
		0, 0, GHOSTTY_SELECTION_GESTURE_BEHAVIOR_CELL, false) == GHOSTTY_SUCCESS);
	CHECK(term->selection_gesture == NULL);
	CHECK(ghostty_bridge_terminal_selection_format(term, 65536, &text, &len) == GHOSTTY_NO_VALUE);
	CHECK(ghostty_bridge_terminal_search_start(term, (const uint8_t*)"AlPhA", 5) == GHOSTTY_SUCCESS);
	GhosttyBridgeSearchStatus status;
	for (int step = 0; step < 100; step++) {
		CHECK(ghostty_bridge_terminal_search_tick(term, 4, &status) == GHOSTTY_SUCCESS);
		if (status.caught_up) break;
	}
	CHECK(status.caught_up && status.total_matches == 3);
	CHECK(ghostty_bridge_terminal_search_move(term, true, &status) == GHOSTTY_SUCCESS);
	CHECK(status.has_selected && status.selected_index < status.total_matches);
	CHECK(ghostty_bridge_terminal_selection_format(term, 65536, &text, &len) == GHOSTTY_SUCCESS);
	CHECK(len == 5);
	ghostty_bridge_free(text);
	feed(term, " ALPHA");
	CHECK(ghostty_bridge_terminal_search_tick(term, 32, &status) == GHOSTTY_SUCCESS);
	CHECK(status.total_matches == 4);
	CHECK(ghostty_bridge_terminal_search_start(term, NULL, 0) == GHOSTTY_SUCCESS);
	CHECK(term->search == NULL && term->search_memory.used == 0);
	CHECK(ghostty_bridge_terminal_search_tick(term, 1, &status) == GHOSTTY_SUCCESS);
	CHECK(status.caught_up && status.total_matches == 0);
	ghostty_bridge_terminal_free(term);
}

static void test_snapshot_allocation_budget(void) {
	GhosttyBridgeTerminal source = ghostty_bridge_terminal_new(200, 20);
	GhosttyBridgeTerminal target = ghostty_bridge_terminal_new(20, 4);
	CHECK(source != NULL && target != NULL);
	feed(target, "preserved on failure");
	const size_t small_budget = 4u * 1024u * 1024u;
	uint8_t* normal = NULL;
	size_t normal_len = 0;
	CHECK(ghostty_bridge_terminal_checkpoint(target, 1024 * 1024, &normal, &normal_len) == GHOSTTY_SUCCESS);
	CHECK(ghostty_bridge_restore_with_limit(target, normal, normal_len, small_budget) == GHOSTTY_SUCCESS);
	ghostty_bridge_free(normal);
	uint8_t line[202];
	memset(line, 'a', 200);
	line[200] = '\r'; line[201] = '\n';
	for (size_t row = 0; row < 5000; row++) ghostty_bridge_terminal_write(source, line, sizeof(line));
	/* Deliberately permissive encoded policy must never override the host's
	 * fixed restoration policy after a successful decode. */
	const size_t huge_bytes = 512u * 1024u * 1024u, huge_rows = 100000;
	CHECK(ghostty_terminal_set(source->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, &huge_bytes) == GHOSTTY_SUCCESS);
	CHECK(ghostty_terminal_set(source->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &huge_rows) == GHOSTTY_SUCCESS);
	uint8_t* snapshot = NULL;
	size_t len = 0;
	CHECK(ghostty_bridge_terminal_checkpoint(source, 16 * 1024 * 1024, &snapshot, &len) == GHOSTTY_SUCCESS);
	/* This low-budget instance exercises exactly the production allocator
	 * against a valid highly-compressible history, without a 128MiB test heap.
	 * The same budget accepts the normal terminal above, and encoded bytes fit. */
	CHECK(len < small_budget);
	GhosttyResult limited_result = ghostty_bridge_restore_with_limit(target, snapshot, len, small_budget);
	CHECK(limited_result == GHOSTTY_LIMIT_EXCEEDED);
	check_text(target, "preserved on failure");
	uint8_t* corrupt = malloc(len);
	CHECK(corrupt != NULL);
	memcpy(corrupt, snapshot, len);
	corrupt[len / 2] ^= 0x80;
	CHECK(ghostty_bridge_terminal_restore(target, corrupt, len) != GHOSTTY_SUCCESS);
	check_text(target, "preserved on failure");
	free(corrupt);
	CHECK(ghostty_bridge_terminal_restore(target, snapshot, len) == GHOSTTY_SUCCESS);
	CHECK(target->restore_allocator != NULL && target->restore_allocator->memory.limit == 128u * 1024u * 1024u);
	CHECK(target->restore_allocator->memory.used <= target->restore_allocator->memory.limit);
	/* Native page allocations (not merely the tiny decoder heap) are counted. */
	CHECK(target->restore_allocator->memory.used > 512 * 1024);
	CHECK(target->restore_allocator->memory.peak <= CODELIMA_GHOSTTY_RESTORE_BYTES);
	size_t used = 0, peak = 0, limit = 0;
	CHECK(ghostty_bridge_terminal_restore_memory(target, &used, &peak, &limit) == GHOSTTY_SUCCESS);
	CHECK(used > small_budget && used <= peak && peak <= limit && limit == CODELIMA_GHOSTTY_RESTORE_BYTES);
	printf("Checkpoint allocation accounting: encoded=%zu live=%zu peak=%zu limit=%zu bytes\n", len, used, peak, limit);
	size_t bytes = 0, rows = 0;
	CHECK(ghostty_terminal_get(target->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBACK_MAX_BYTES, &bytes) == GHOSTTY_SUCCESS);
	CHECK(ghostty_terminal_get(target->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBACK_MAX_LINES, &rows) == GHOSTTY_SUCCESS);
	CHECK(bytes == CODELIMA_GHOSTTY_SCROLLBACK_BYTES && rows == CODELIMA_GHOSTTY_SCROLLBACK_LINES);
	GhosttyBridgeFrame frame;
	CHECK(ghostty_bridge_terminal_frame(target, 4 * 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	ghostty_bridge_frame_free(&frame);
	/* Replacing an already-restored terminal validates allocator lifetime. */
	CHECK(ghostty_bridge_terminal_restore(target, snapshot, len) == GHOSTTY_SUCCESS);
	ghostty_bridge_terminal_free(source);
	ghostty_bridge_terminal_free(target);
	ghostty_bridge_free(snapshot);
}

static void test_search_allocator_and_logs(void) {
	struct ghostty_bridge_memory memory = {.limit = CODELIMA_GHOSTTY_SEARCH_BYTES};
	void* bytes = ghostty_bridge_memory_alloc(&memory, CODELIMA_GHOSTTY_SEARCH_BYTES, 12, 0);
	CHECK(bytes != NULL && ((uintptr_t)bytes & 4095) == 0);
	CHECK(ghostty_bridge_memory_alloc(&memory, 1, 0, 0) == NULL && memory.limited);
	ghostty_bridge_memory_release(&memory, bytes, CODELIMA_GHOSTTY_SEARCH_BYTES, 12, 0);
	CHECK(memory.used == 0);
	/* Account the whole OS mapping when runtime pages exceed Zig's minimum
	 * alignment; this exercises 64KiB rounding even on a 4KiB test kernel. */
	struct ghostty_bridge_memory large_pages = {.limit = 65536, .page_size = 65536};
	bytes = ghostty_bridge_memory_alloc(&large_pages, 4097, 12, 0);
	CHECK(bytes != NULL && large_pages.used == 65536 && large_pages.peak == 65536);
	CHECK(((const uint8_t*)bytes)[0] == 0 && ((const uint8_t*)bytes)[4096] == 0);
	CHECK(ghostty_bridge_memory_alloc(&large_pages, 1, 0, 0) == NULL && large_pages.limited);
	ghostty_bridge_memory_release(&large_pages, bytes, 4097, 12, 0);
	CHECK(large_pages.used == 0);
	uint8_t logs[CODELIMA_GHOSTTY_LOG_BYTES];
	while (ghostty_bridge_read_log(logs, sizeof(logs)) != 0) {}
	ghostty_bridge_test_log((const uint8_t*)"native diagnostic", 17);
	int len = ghostty_bridge_read_log(logs, sizeof(logs));
	const char* expected = "[test] native diagnostic\n";
	CHECK(len == (int)strlen(expected) && memcmp(logs, expected, strlen(expected)) == 0);
}

static void test_graphics(void) {
	GhosttyBridgeTerminal term = ghostty_bridge_terminal_new(30, 5);
	CHECK(term != NULL);
	CHECK(ghostty_bridge_terminal_resize_pixels(term, 30, 5, 8, 16) == GHOSTTY_SUCCESS);
	CHECK(ghostty_bridge_terminal_resize_pixels(term, 30, 5, 0, 16) == GHOSTTY_INVALID_VALUE);
	feed(term, "\x1b_Ga=T,f=32,s=1,v=1,i=7,p=9,c=2,r=1,z=-1,X=1,Y=2,q=2;/wAA/w==\x1b\\");
	GhosttyBridgeGraphicsFrame frame;
	CHECK(ghostty_bridge_terminal_graphics(term, 16 * 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	CHECK(frame.assets_len == 1 && frame.placements_len == 1 && frame.rgba_len == 4);
	CHECK(frame.assets[0].image_id == 7 && frame.assets[0].width == 1 && frame.assets[0].height == 1);
	CHECK(frame.rgba[0] == 255 && frame.rgba[1] == 0 && frame.rgba[2] == 0 && frame.rgba[3] == 255);
	CHECK(frame.placements[0].placement_id == 9 && frame.placements[0].z == -1);
	CHECK(frame.placements[0].offset_x == 1 && frame.placements[0].offset_y == 2);
	CHECK(frame.placements[0].geometry.grid_cols == 2 && frame.placements[0].geometry.grid_rows == 1);
	CHECK(frame.placements[0].geometry.viewport_visible);
	uint8_t* checkpoint = NULL;
	size_t checkpoint_len = 0;
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_BRIDGE_UNSUPPORTED);
	CHECK(checkpoint == NULL && checkpoint_len == 0);
	feed(term, "\x1b[?1049h");
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_BRIDGE_UNSUPPORTED);
	feed(term, "\x1b[?1049l");
	uint64_t generation = frame.assets[0].generation;
	feed(term, "\x1b_Ga=T,f=32,s=1,v=1,i=7,p=9,c=2,r=1,q=2;AP8A/w==\x1b\\");
	CHECK(frame.rgba[0] == 255 && frame.rgba[1] == 0); /* Owns old pixels. */
	ghostty_bridge_graphics_frame_free(&frame);
	CHECK(ghostty_bridge_terminal_graphics(term, 16 * 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	CHECK(frame.assets_len == 1 && frame.assets[0].generation != generation);
	CHECK(frame.rgba[0] == 0 && frame.rgba[1] == 255);
	ghostty_bridge_graphics_frame_free(&frame);
	const char* animation[] = {
		"\x1b_Ga=f,f=32,s=1,v=1,i=7;/wAA/w==\x1b\\",
		"\x1b_Ga=a,i=7,s=3;\x1b\\",
		"\x1b_Ga=c,i=7,r=1;\x1b\\",
	};
	uint8_t response[1024];
	for (size_t i = 0; i < sizeof(animation) / sizeof(animation[0]); i++) {
		feed(term, animation[i]);
		int len = ghostty_bridge_terminal_read_response(term, response, sizeof(response) - 1);
		CHECK(len > 0);
		response[len] = 0;
		CHECK(strstr((const char*)response, "ENOTSUP: animation disabled") != NULL);
	}
	feed(term, "\x1b_Ga=T,f=32,s=1,v=1,i=8,t=f;L2V0Yy9wYXNzd2Q=\x1b\\");
	int len = ghostty_bridge_terminal_read_response(term, response, sizeof(response) - 1);
	CHECK(len > 0);
	response[len] = 0;
	CHECK(strstr((const char*)response, "unsupported medium") != NULL);
	feed(term, "\x1b_Ga=d,d=I,i=7,q=2;\x1b\\");
	CHECK(ghostty_bridge_terminal_graphics(term, 16 * 1024 * 1024, &frame) == GHOSTTY_SUCCESS);
	CHECK(frame.assets_len == 0 && frame.placements_len == 0);
	ghostty_bridge_graphics_frame_free(&frame);
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_SUCCESS);
	ghostty_bridge_free(checkpoint);
	feed(term, "\x1b_Ga=t,f=32,s=1,v=1,i=8,q=2;/wAA/w==\x1b\\");
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_BRIDGE_UNSUPPORTED);
	feed(term, "\x1b_Ga=d,d=I,i=8,q=2;\x1b\\");
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_SUCCESS);
	ghostty_bridge_free(checkpoint);
	feed(term, "\x1b_Ga=t,f=32,s=1,v=1,i=9,m=1,q=2;/wAA\x1b\\");
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_BRIDGE_UNSUPPORTED);
	feed(term, "\x1b_Gm=0,q=2;/w==\x1b\\\x1b_Ga=d,d=I,i=9,q=2;\x1b\\");
	CHECK(ghostty_bridge_terminal_checkpoint(term, 16 * 1024 * 1024, &checkpoint, &checkpoint_len) == GHOSTTY_SUCCESS);
	ghostty_bridge_free(checkpoint);
	feed(term, "\x1b_Ga=t,f=24,s=2049,v=2049,i=10;AAAA\x1b\\");
	len = ghostty_bridge_terminal_read_response(term, response, sizeof(response) - 1);
	CHECK(len > 0);
	response[len] = 0;
	CHECK(strstr((const char*)response, "EINVAL") != NULL);
	GhosttySysImage image;
	CHECK(ghostty_bridge_png_alloc(NULL, 2049, 2049, &image) == GHOSTTY_LIMIT_EXCEEDED);
	CHECK(image.data == NULL);
	ghostty_bridge_terminal_free(term);
}

int main(void) {
	CHECK(ghostty_bridge_init() == GHOSTTY_SUCCESS);
	CHECK(strlen(ghostty_bridge_build_identity()) == 64);
	test_frame_and_format();
	test_frame_dirty_row_reuse();
	test_effects_and_paste();
	test_snapshot();
	test_snapshot_allocation_budget();
	test_selection_search();
	test_search_allocator_and_logs();
	test_graphics();
	puts("Ghostty native bridge contract tests passed");
	return 0;
}
