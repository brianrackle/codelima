#define _POSIX_C_SOURCE 200809L
#include "../ghostty_bridge_compat.c"
#include <time.h>
#include <unistd.h>

bool codelimaGhosttyDecodePNG(GhosttyAllocator* allocator, uint8_t* data, size_t len, GhosttySysImage* out) {
	(void)allocator; (void)data; (void)len; (void)out;
	return false;
}

static uint64_t nanos(void) {
	struct timespec now;
	if (clock_gettime(CLOCK_MONOTONIC, &now) != 0) exit(2);
	return (uint64_t)now.tv_sec * 1000000000u + now.tv_nsec;
}

static uint64_t resident_bytes(void) {
#if defined(__linux__)
	FILE* status = fopen("/proc/self/statm", "r");
	unsigned long total = 0, resident = 0;
	if (status == NULL) return 0;
	int fields = fscanf(status, "%lu %lu", &total, &resident);
	fclose(status);
	return fields == 2 ? (uint64_t)resident * (uint64_t)sysconf(_SC_PAGESIZE) : 0;
#else
	return 0; /* Never substitute peak RSS for current residency. */
#endif
}

static int compare_u64(const void* a, const void* b) {
	uint64_t left = *(const uint64_t*)a, right = *(const uint64_t*)b;
	return (left > right) - (left < right);
}

int main(int argc, char** argv) {
	bool compress = argc == 2 && strcmp(argv[1], "on") == 0;
	GhosttyBridgeTerminal terminal = ghostty_bridge_terminal_new(200, 32);
	if (terminal == NULL) return 2;
	uint8_t line[202];
	memset(line, 'a', 200);
	line[200] = '\r'; line[201] = '\n';
	for (unsigned int row = 0; row < 10000; row++) ghostty_bridge_terminal_write(terminal, line, sizeof(line));
	if (ghostty_bridge_terminal_error(terminal) != GHOSTTY_SUCCESS) return 2;
	uint64_t before = resident_bytes(), steps = 0, max_step = 0, started = nanos();
	if (compress) {
		GhosttyTerminalCompressionResult progress;
		do {
			uint64_t step_start = nanos();
			if (ghostty_bridge_terminal_compress(terminal, &progress) != GHOSTTY_SUCCESS) return 2;
			uint64_t elapsed = nanos() - step_start;
			if (elapsed > max_step) max_step = elapsed;
			if (++steps > 100000) return 2;
		} while (progress == GHOSTTY_TERMINAL_COMPRESSION_RESULT_PENDING);
	}
	uint64_t compress_ns = nanos() - started, after = resident_bytes();
	uint64_t input[1000];
	for (size_t i = 0; i < sizeof(input) / sizeof(input[0]); i++) {
		uint64_t start = nanos();
		ghostty_bridge_terminal_write(terminal, (const uint8_t*)"\rinteractive input", 18);
		input[i] = nanos() - start;
	}
	qsort(input, 1000, sizeof(input[0]), compare_u64);
	started = nanos();
	ghostty_bridge_terminal_scroll_viewport_top(terminal);
	GhosttyBridgeFrame frame;
	if (ghostty_bridge_terminal_frame(terminal, 4 * 1024 * 1024, &frame) != GHOSTTY_SUCCESS) return 2;
	ghostty_bridge_frame_free(&frame);
	uint64_t history_ns = nanos() - started;
	printf("{\"compression\":%s,\"rss_before\":%llu,\"rss_after\":%llu,\"steps\":%llu,\"compression_ns\":%llu,\"max_step_ns\":%llu,\"input_p50_ns\":%llu,\"input_p95_ns\":%llu,\"input_max_ns\":%llu,\"history_frame_ns\":%llu}\n",
		compress ? "true" : "false", (unsigned long long)before, (unsigned long long)after,
		(unsigned long long)steps, (unsigned long long)compress_ns, (unsigned long long)max_step,
		(unsigned long long)input[500], (unsigned long long)input[950], (unsigned long long)input[999],
		(unsigned long long)history_ns);
	ghostty_bridge_terminal_free(terminal);
	return 0;
}
