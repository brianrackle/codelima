//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"golang.org/x/sys/unix"
)

var ghosttyStderrCaptureMu sync.Mutex

type ghosttyFakePTYWriteStep struct {
	n   int
	err error
}

type ghosttyFakePTYWriteTarget struct {
	mu     sync.Mutex
	steps  []ghosttyFakePTYWriteStep
	output bytes.Buffer
	closed bool
}

func (t *ghosttyFakePTYWriteTarget) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.steps) > 0 {
		step := t.steps[0]
		t.steps = t.steps[1:]
		n := step.n
		if n > len(data) {
			n = len(data)
		}
		if n > 0 {
			_, _ = t.output.Write(data[:n])
		}
		return n, step.err
	}
	if t.closed {
		return 0, os.ErrClosed
	}
	_, _ = t.output.Write(data)
	return len(data), nil
}

func (t *ghosttyFakePTYWriteTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

func (t *ghosttyFakePTYWriteTarget) Fd() uintptr {
	return 0
}

func (t *ghosttyFakePTYWriteTarget) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.String()
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestGhosttyWriteAllToPTYHandlesPartialWrites(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{
		steps: []ghosttyFakePTYWriteStep{
			{n: 1},
			{n: 2},
		},
	}

	if err := ghosttyWriteAllToPTY(target, []byte("abc"), nil); err != nil {
		t.Fatalf("ghosttyWriteAllToPTY() error = %v", err)
	}
	if got := target.String(); got != "abc" {
		t.Fatalf("ghosttyWriteAllToPTY() wrote %q, want %q", got, "abc")
	}
}

func TestGhosttyWriteAllToPTYWaitsForTemporaryBackpressure(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{
		steps: []ghosttyFakePTYWriteStep{
			{n: 0, err: unix.EAGAIN},
			{n: 3},
		},
	}

	waitCalls := 0
	if err := ghosttyWriteAllToPTY(target, []byte("abc"), func(fd int) error {
		waitCalls++
		return nil
	}); err != nil {
		t.Fatalf("ghosttyWriteAllToPTY() error = %v", err)
	}
	if waitCalls != 1 {
		t.Fatalf("waitWritable calls = %d, want 1", waitCalls)
	}
	if got := target.String(); got != "abc" {
		t.Fatalf("ghosttyWriteAllToPTY() wrote %q, want %q", got, "abc")
	}
}

func TestGhosttyPTYWriterFlushesQueuedWrites(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{}
	writer := newGhosttyPTYWriter(target, func(fd int) error { return nil }, nil)
	defer writer.Close()

	if !writer.Enqueue([]byte("ab")) {
		t.Fatal("expected first enqueue to succeed")
	}
	if !writer.Enqueue([]byte("cd")) {
		t.Fatal("expected second enqueue to succeed")
	}

	waitForCondition(t, time.Second, func() bool {
		return target.String() == "abcd"
	}, "queued PTY writes to flush")
}

func TestGhosttyTerminalPreservesDelayedInitialOutput(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	cmd := exec.Command("sh", "-lc", "sleep 0.2; printf prompt; sleep 0.2")
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 24, 4)
	defer vx.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		win := vx.Window()
		win.Clear()
		ghostty.Draw(win)
		return strings.Contains(renderedScreenText(t, vx, 24, 4), "prompt")
	}, "delayed ghostty PTY output to reach the rendered terminal")
}

func TestGhosttyStyleForColorsLeavesDefaultColorsUnset(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x112233, false, false)
	if style.Foreground != vaxis.ColorDefault {
		t.Fatalf("foreground = %v, want default foreground", style.Foreground)
	}
	if style.Background != vaxis.ColorDefault {
		t.Fatalf("background = %v, want default background", style.Background)
	}
}

func TestGhosttyStyleForColorsPreservesExplicitColors(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x112233, true, true)
	if style.Foreground != vaxis.HexColor(0xAABBCC) {
		t.Fatalf("foreground = %v, want %v", style.Foreground, vaxis.HexColor(0xAABBCC))
	}
	if style.Background != vaxis.HexColor(0x112233) {
		t.Fatalf("background = %v, want %v", style.Background, vaxis.HexColor(0x112233))
	}
}

func TestGhosttyTerminalLeavesDefaultColorsUnset(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	vx := newRenderTestVaxis(t, 80, 24)
	defer vx.Close()

	ghostty.ingestPTY([]byte("X"))
	win := vx.Window()
	win.Clear()
	ghostty.Draw(win)

	style := renderedCellStyle(t, vx, 0, 0)
	if style.Foreground != vaxis.ColorDefault {
		t.Fatalf("foreground = %v, want default foreground", style.Foreground)
	}
	if style.Background != vaxis.ColorDefault {
		t.Fatalf("background = %v, want default background", style.Background)
	}
}

func TestGhosttyTerminalPreservesExplicitBackgroundEqualToDefault(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	vx := newRenderTestVaxis(t, 80, 24)
	defer vx.Close()

	defaultBackground := ghostty.defaultBackgroundRGBLocked()
	ghostty.ingestPTY([]byte(fmt.Sprintf(
		"\x1b[48;2;%d;%d;%dmX\x1b[0m",
		(defaultBackground>>16)&0xFF,
		(defaultBackground>>8)&0xFF,
		defaultBackground&0xFF,
	)))
	win := vx.Window()
	win.Clear()
	ghostty.Draw(win)

	style := renderedCellStyle(t, vx, 0, 0)
	if style.Background != vaxis.HexColor(defaultBackground) {
		t.Fatalf("background = %v, want explicit %v", style.Background, vaxis.HexColor(defaultBackground))
	}
}

func TestGhosttyTerminalRedrawsCleanlyAfterWidthGrowth(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	renderSnapshot := func(width, height int) string {
		vx := newRenderTestVaxis(t, width, height)
		defer vx.Close()

		win := vx.Window()
		win.Clear()
		ghostty.Draw(win)
		return renderedScreenText(t, vx, width, height)
	}

	ghostty.Resize(24, 12)

	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(),
		"TERM="+tuiEmbeddedTermEnv,
		`PS1=brianrackle@lima-codelima-codex-codelima-codex-node-test-019d2fff:/Users/brianrackle/Projects/codelima\$ `,
	)
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(strings.ReplaceAll(renderSnapshot(24, 12), "\n", ""), "brianrackle@lima-codelima")
	}, "bash prompt to appear")

	for _, width := range []int{28, 32, 40, 48, 56, 64, 72, 80} {
		ghostty.Resize(width, 12)
		time.Sleep(50 * time.Millisecond)
	}
	wide := renderSnapshot(80, 12)

	got := strings.Join(nonEmptyRenderedLines(wide), "\n")
	want := strings.Join([]string{
		"brianrackle@lima-codelima-codex-codelima-codex-node-test-019d2fff:/Users/brianra",
		"ckle/Projects/codelima$",
	}, "\n")
	if got != want {
		t.Fatalf("rendered terminal after width growth = %q, want %q", got, want)
	}
}

func nonEmptyRenderedLines(text string) []string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func TestGhosttyKeyEncoderMatchesExistingCommonSequences(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	cases := []struct {
		name                  string
		key                   vaxis.Key
		applicationKeypad     bool
		cursorKeysApplication bool
		want                  string
	}{
		{
			name: "cursor-normal",
			key:  vaxis.Key{Keycode: vaxis.KeyUp},
			want: "\x1b[A",
		},
		{
			name:                  "cursor-application",
			key:                   vaxis.Key{Keycode: vaxis.KeyUp},
			cursorKeysApplication: true,
			want:                  "\x1bOA",
		},
		{
			name: "ctrl-c",
			key: vaxis.Key{
				Keycode:        'c',
				BaseLayoutCode: 'c',
				Modifiers:      vaxis.ModCtrl,
			},
			want: "\x03",
		},
		{
			name: "alt-x",
			key: vaxis.Key{
				Keycode:        'x',
				BaseLayoutCode: 'x',
				Modifiers:      vaxis.ModAlt,
			},
			want: "\x1bx",
		},
		{
			name: "shifted-punctuation",
			key: vaxis.Key{
				Text:           ":",
				Keycode:        ';',
				ShiftedCode:    ':',
				BaseLayoutCode: ';',
				Modifiers:      vaxis.ModShift,
			},
			want: ":",
		},
		{
			name: "paste-text",
			key: vaxis.Key{
				Text:      "hello",
				EventType: vaxis.EventPaste,
			},
			want: "hello",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := encodeTUITerminalKeyWithGhostty(tc.key, encoder, tc.applicationKeypad, tc.cursorKeysApplication)
			if got != tc.want {
				t.Fatalf("encodeTUITerminalKeyWithGhostty() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyKeyEncoderSuppressesReleaseEvents(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{
		Keycode:        'a',
		BaseLayoutCode: 'a',
		EventType:      vaxis.EventRelease,
	}, encoder, false, false)
	if got != "" {
		t.Fatalf("release key encoded as %q, want empty sequence", got)
	}
}

func TestGhosttyKeyEncoderFallsBackForUnsupportedFunctionKeys(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{Keycode: vaxis.KeyF26}, encoder, false, false)
	if got != "\x1B[1;5Q" {
		t.Fatalf("unsupported Ghostty key should fall back to legacy encoding, got %q", got)
	}
}

func TestGhosttyMouseEncoderMatchesLegacyCommonSequences(t *testing.T) {
	cases := []struct {
		name             string
		setup            string
		mouse            vaxis.Mouse
		mouseButtonsDown int
		want             string
	}{
		{
			name:  "sgr-press",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventPress,
			},
			want: "\x1b[<0;5;3M",
		},
		{
			name:  "sgr-release",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventRelease,
			},
			want: "\x1b[<0;5;3m",
		},
		{
			name:  "drag-motion",
			setup: "\x1b[?1002h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventMotion,
			},
			mouseButtonsDown: 1,
			want:             "\x1b[<32;5;3M",
		},
		{
			name:  "any-motion-without-button",
			setup: "\x1b[?1003h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseNoButton,
				EventType: vaxis.EventMotion,
			},
			want: "\x1b[<35;5;3M",
		},
		{
			name:  "wheel-up",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseWheelUp,
				EventType: vaxis.EventPress,
			},
			want: "\x1b[<64;5;3M",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
			if err != nil {
				t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
			}
			defer terminal.Close()

			ghostty, ok := terminal.(*ghosttyTUITerminal)
			if !ok {
				t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
			}
			if ghostty.mouseEncoder == nil {
				t.Skip("ghostty mouse encoder unavailable in this test environment")
			}

			ghostty.ingestPTY([]byte(tc.setup))
			got, handled := ghostty.mouseEncoder.Encode(tc.mouse, ghostty.term, ghostty.cols, ghostty.rows, tc.mouseButtonsDown)
			if !handled {
				t.Fatalf("expected ghostty mouse encoder to handle %#v", tc.mouse)
			}
			if got != tc.want {
				t.Fatalf("ghostty mouse encoding = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyMouseEncoderFallsBackWithoutEncoder(t *testing.T) {
	t.Parallel()

	mouse := vaxis.Mouse{
		Col:       4,
		Row:       2,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}
	got := encodeTUITerminalMouseWithGhostty(mouse, nil, nil, 80, 24, 0, true, true, true)
	if got != "\x1b[<0;5;3M" {
		t.Fatalf("fallback mouse encoding = %q, want %q", got, "\x1b[<0;5;3M")
	}
}

func TestGhosttyTerminalWheelScrollUsesGhosttyViewportState(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	var output strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&output, "line %02d\n", i)
	}
	ghostty.ingestPTY([]byte(output.String()))

	ghostty.mu.Lock()
	defer ghostty.mu.Unlock()

	initial, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state")
	}
	if initial.total <= initial.length {
		t.Fatalf("expected scrollback, got total=%d length=%d", initial.total, initial.length)
	}
	if !ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to start at bottom")
	}
	if !ghostty.handleWheelLocked(vaxis.MouseWheelUp) {
		t.Fatal("expected wheel-up to scroll Ghostty viewport")
	}

	scrolled, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state after scrolling")
	}
	if scrolled.offset >= initial.offset {
		t.Fatalf("expected viewport offset to move upward, got initial=%d scrolled=%d", initial.offset, scrolled.offset)
	}
	if ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to be away from bottom after scrolling")
	}

	ghostty.scrollViewportBottomLocked()
	reset, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state after resetting viewport")
	}
	if !ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to return to bottom")
	}
	if reset.offset+reset.length < reset.total {
		t.Fatalf("expected bottom-aligned scrollbar state, got offset=%d length=%d total=%d", reset.offset, reset.length, reset.total)
	}
}

func TestGhosttyTerminalDoesNotWriteOSCWarningsToStderr(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b]112\x07"))
		ghostty.ingestPTY([]byte("\x1b]11;?\x07"))
	})
	if strings.Contains(stderrOutput, "warning(osc):") {
		t.Fatalf("expected Ghostty OSC processing to stay off stderr, got %q", stderrOutput)
	}
}

func TestGhosttyTerminalAnswersModifyOtherKeysQueryWithoutWarnings(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	ghostty.ingestPTY([]byte("\x1b[>4;2m"))
	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?4m"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[>4;2m"; got != want {
		t.Fatalf("modifyOtherKeys query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersColorSchemeQueryFromStoredTheme(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	ghostty.mu.Lock()
	ghostty.setColorThemeModeLocked(vaxis.LightMode)
	ghostty.mu.Unlock()

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?996n"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?997;2n"; got != want {
		t.Fatalf("color-scheme query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalReportsColorThemeUpdateWhenModeEnabled(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe for terminal update guard: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	ghostty.mu.Lock()
	ghostty.pty = writer
	ghostty.mu.Unlock()

	ghostty.ingestPTY([]byte("\x1b[?2031h"))
	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.Update(vaxis.ColorThemeUpdate{Mode: vaxis.DarkMode})
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?997;1n"; got != want {
		t.Fatalf("color-theme update report = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersPrimaryDeviceAttributesQuery(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[c"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?62;18;22c"; got != want {
		t.Fatalf("primary device attributes response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersXtwinopsSizeQuery(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[18t"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[8;24;80t"; got != want {
		t.Fatalf("XTWINOPS size query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersXtversionQuery(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[>q"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1bP>|codelima\x1b\\"; got != want {
		t.Fatalf("XTVERSION response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalIgnoresVimTitleStackQueriesWithoutWarnings(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[22;2t\x1b[22;1t\x1b[23;2t\x1b[23;1t"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}
}

func TestGhosttyTerminalSuppressesUnknownParserWarningsFromStderr(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?5m"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected Ghostty parser warnings to stay contained, got %q", stderrOutput)
	}
}

func TestGhosttyTerminalRoundTripsSttyRawPrompt(t *testing.T) {
	script := newSttyRawPromptScript(t)
	runGhosttySttyRawPrompt(t, exec.Command("bash", script.scriptPath), script.readyPath, script.resultPath)
}

func TestNestedPTYScriptCommandUsesPlatformSpecificScriptArgs(t *testing.T) {
	t.Parallel()

	scriptPath := "/tmp/path with spaces/stty-raw-prompt.sh"
	tests := []struct {
		name string
		goos string
		want []string
	}{
		{
			name: "darwin",
			goos: "darwin",
			want: []string{"script", "-q", "/dev/null", "bash", scriptPath},
		},
		{
			name: "linux",
			goos: "linux",
			want: []string{"script", "-q", "-c", "bash " + shellQuote(scriptPath), "/dev/null"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newNestedPTYScriptCommandForOS(tt.goos, scriptPath)
			if !reflect.DeepEqual(cmd.Args, tt.want) {
				t.Fatalf("newNestedPTYScriptCommandForOS(%q) args = %v, want %v", tt.goos, cmd.Args, tt.want)
			}
		})
	}
}

func TestGhosttyTerminalRoundTripsSttyRawPromptThroughNestedPTY(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script utility unavailable: %v", err)
	}

	script := newSttyRawPromptScript(t)
	runGhosttySttyRawPrompt(t, newNestedPTYScriptCommand(script.scriptPath), script.readyPath, script.resultPath)
}

func captureGhosttyProcessStderr(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	stderrFD := int(os.Stderr.Fd())
	savedFD, err := unix.Dup(stderrFD)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("dup stderr error = %v", err)
	}

	if err := unix.Dup2(int(writer.Fd()), stderrFD); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = unix.Close(savedFD)
		t.Fatalf("redirect stderr error = %v", err)
	}
	_ = writer.Close()

	outputCh := make(chan string, 1)
	go func() {
		var buffer bytes.Buffer
		_, _ = io.Copy(&buffer, reader)
		_ = reader.Close()
		outputCh <- buffer.String()
	}()

	fn()

	if err := unix.Dup2(savedFD, stderrFD); err != nil {
		_ = unix.Close(savedFD)
		t.Fatalf("restore stderr error = %v", err)
	}
	_ = unix.Close(savedFD)

	return <-outputCh
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", path)
}

type sttyRawPromptScript struct {
	scriptPath string
	readyPath  string
	resultPath string
}

func newSttyRawPromptScript(t *testing.T) sttyRawPromptScript {
	t.Helper()

	tempDir := t.TempDir()
	readyPath := filepath.Join(tempDir, "ready")
	resultPath := filepath.Join(tempDir, "result")
	errorPath := filepath.Join(tempDir, "restore.err")
	scriptPath := filepath.Join(tempDir, "stty-raw-prompt.sh")
	body := fmt.Sprintf(`
save_state="$(/bin/stty -g)"
printf ready > %q
/bin/stty raw -echo
IFS='' read -r -n 1 -d '' c
if /bin/stty "${save_state}" 2>%q; then
  printf 'ok:%%s' "$c" > %q
else
  printf 'fail:%%s\nstate=%%s\n' "$(/bin/cat %q)" "${save_state}" > %q
fi
`, readyPath, errorPath, resultPath, errorPath, resultPath)
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		t.Fatalf("WriteFile(stty raw prompt script) error = %v", err)
	}
	return sttyRawPromptScript{
		scriptPath: scriptPath,
		readyPath:  readyPath,
		resultPath: resultPath,
	}
}

func newNestedPTYScriptCommand(scriptPath string) *exec.Cmd {
	return newNestedPTYScriptCommandForOS(runtime.GOOS, scriptPath)
}

func newNestedPTYScriptCommandForOS(goos string, scriptPath string) *exec.Cmd {
	if goos == "linux" {
		return exec.Command("script", "-q", "-c", "bash "+shellQuote(scriptPath), "/dev/null")
	}
	return exec.Command("script", "-q", "/dev/null", "bash", scriptPath)
}

func runGhosttySttyRawPrompt(t *testing.T, cmd *exec.Cmd, readyPath string, resultPath string) {
	t.Helper()

	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForFile(t, readyPath, 5*time.Second)
	ghostty.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	waitForFile(t, resultPath, 5*time.Second)

	output, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("ReadFile(result) error = %v", err)
	}

	if got := strings.TrimSpace(string(output)); !strings.HasPrefix(got, "ok:") {
		t.Fatalf("expected stty restore to succeed, got %q", got)
	}
}
