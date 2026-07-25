//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
)

const rendererWorkerHelperEnv = "CODELIMA_TEST_RENDERER_WORKER_HELPER"

func TestRendererWorkerProcess(t *testing.T) {
	if os.Getenv(rendererWorkerHelperEnv) != "1" {
		return
	}
	if err := RunRendererWorker(context.Background()); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type rendererIsolationDaemonHandler struct {
	closeOnce sync.Once
	closeFn   func()
}

func (h *rendererIsolationDaemonHandler) Handle(
	context.Context,
	daemon.ClientContext,
	string,
	json.RawMessage,
) (any, error) {
	return map[string]bool{"ok": true}, nil
}

func (h *rendererIsolationDaemonHandler) Snapshot(context.Context) (any, error) {
	return map[string]any{"healthy": true}, nil
}

func (*rendererIsolationDaemonHandler) TerminalCount() int { return 2 }

func (h *rendererIsolationDaemonHandler) Close() error {
	h.closeOnce.Do(h.closeFn)
	return nil
}

func TestHungCgoRendererIsTerminalLocalAndPreservesShell(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: 500 * time.Millisecond,
		QueueFrames:    16,
	}
	hungOptions := helper
	hungOptions.Env = append(
		append([]string(nil), helper.Env...),
		"CODELIMA_TEST_GHOSTTY_HANG_OPERATION=output",
	)

	hung := newIsolatedDaemonTerminalWithOptions("hung", func(vaxis.Event) {}, hungOptions)
	healthy := newIsolatedDaemonTerminalWithOptions("healthy", func(vaxis.Event) {}, helper)
	if err := hung.Start(exec.Command("/bin/sh", "-c", "printf hung-output; sleep 30")); err != nil {
		t.Fatalf("start hung terminal: %v", err)
	}
	if err := healthy.Start(exec.Command("/bin/sh", "-c", "printf healthy-output; sleep 30")); err != nil {
		hung.Close()
		t.Fatalf("start healthy terminal: %v", err)
	}

	t.Cleanup(func() {
		hung.Close()
		healthy.Close()
	})
	hung.mu.Lock()
	shellPID := hung.childPID
	hung.mu.Unlock()
	if shellPID <= 0 {
		t.Fatal("hung terminal has no shell PID")
	}

	waitForCondition(t, 3*time.Second, func() bool {
		return strings.Contains(healthy.ReadVisible(ReadText).Text, "healthy-output")
	}, "healthy terminal snapshot while another renderer hangs")
	waitForCondition(t, 3*time.Second, func() bool {
		return hung.RendererStatus().Generation >= 2
	}, "hung renderer replacement")
	if err := syscall.Kill(shellPID, 0); err != nil {
		t.Fatalf("shell PID %d did not survive renderer replacement: %v", shellPID, err)
	}
	hung.mu.Lock()
	preservedPID := hung.childPID
	hung.mu.Unlock()
	if preservedPID != shellPID {
		t.Fatalf("shell PID changed across renderer replacement: %d -> %d", shellPID, preservedPID)
	}

	home := testutil.TempDir(t, "renderer-isolation-daemon-")
	serverCtx, cancelServer := context.WithCancel(context.Background())
	handler := &rendererIsolationDaemonHandler{closeFn: func() {}}
	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: handler})
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(serverCtx) }()
	t.Cleanup(func() {
		cancelServer()
		if err := <-serverDone; err != nil {
			t.Errorf("daemon server: %v", err)
		}
	})
	waitForCondition(t, time.Second, func() bool {
		_, pingErr := daemonclient.Ping(context.Background(), home, Version)
		return pingErr == nil
	}, "daemon startup")

	start := time.Now()
	status, err := daemonclient.Ping(context.Background(), home, Version)
	if err != nil {
		t.Fatalf("daemon ping during renderer hang: %v", err)
	}
	if status.TerminalCount != 2 {
		t.Fatalf("daemon terminal count = %d, want 2", status.TerminalCount)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("daemon ping during renderer hang took %s", elapsed)
	}
}

func TestRendererPTYResponsesAreDeduplicatedAcrossReplay(t *testing.T) {
	t.Parallel()

	terminal := newIsolatedDaemonTerminalWithOptions("dedupe", nil, defaultRendererProcessOptions())
	terminal.applyRendererPTYWrite(1, 42, 1, []byte("first"))
	terminal.applyRendererPTYWrite(2, 42, 1, []byte("duplicate replay"))
	terminal.applyRendererPTYWrite(2, 42, 2, []byte("second response"))
	terminal.applyRendererPTYWrite(1, rendererInputEventID(7), 1, []byte("generation one input response"))
	terminal.applyRendererPTYWrite(2, rendererInputEventID(7), 1, []byte("generation two input response"))

	terminal.responseMu.Lock()
	defer terminal.responseMu.Unlock()
	if got := len(terminal.responses); got != 4 {
		t.Fatalf("dedupe response keys = %d, want 4", got)
	}
}

func TestRendererWorkerCoalescesRapidOutputPublications(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: time.Second,
		QueueFrames:    128,
	}

	var (
		snapshotCount atomic.Int32
		latestMu      sync.Mutex
		latest        rendererPublishedState
	)
	link, err := startRendererLink(helper, 1, func(frame rendererWorkerFrame) {
		if frame.Type != rendererFrameSnapshot {
			return
		}
		var state rendererPublishedState
		if json.Unmarshal(frame.Result, &state) != nil {
			return
		}
		latestMu.Lock()
		latest = state
		latestMu.Unlock()
		snapshotCount.Add(1)
	})
	if err != nil {
		t.Fatalf("start renderer worker: %v", err)
	}
	t.Cleanup(func() { link.Fail(errors.New("renderer coalescing test complete")) })

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := link.Call(ctx, "init", rendererInitParams{
		TerminalID: "coalesced-output",
		Cols:       160,
		Rows:       24,
	}); err != nil {
		t.Fatalf("initialize renderer worker: %v", err)
	}
	baseline := snapshotCount.Load()

	const outputCount = 64
	for index := 1; index <= outputCount; index++ {
		data := []byte("x")
		if index == outputCount {
			data = []byte("publication-complete\r\n")
		}
		if _, err := link.TryCall("output", rendererOutputParams{
			EventID: uint64(index),
			Data:    data,
		}); err != nil {
			t.Fatalf("queue renderer output %d: %v", index, err)
		}
	}
	if err := link.Call(ctx, "snapshot", nil); err != nil {
		t.Fatalf("publish final renderer snapshot: %v", err)
	}
	latestMu.Lock()
	visibleText := latest.VisibleText.Text
	latestMu.Unlock()
	if !strings.Contains(visibleText, "publication-complete") {
		t.Fatalf("final renderer snapshot = %q, want final output", visibleText)
	}

	if publications := snapshotCount.Load() - baseline; publications >= outputCount {
		t.Fatalf("rapid output produced %d snapshots for %d mutations, want coalescing", publications, outputCount)
	}
}

func TestRendererWorkerKeyEncodingDoesNotPublishUnchangedSnapshot(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: time.Second,
		QueueFrames:    16,
	}

	var snapshotCount, ptyWriteCount atomic.Int32
	link, err := startRendererLink(helper, 1, func(frame rendererWorkerFrame) {
		switch frame.Type {
		case rendererFrameSnapshot:
			snapshotCount.Add(1)
		case rendererFramePTYWrite:
			ptyWriteCount.Add(1)
		}
	})
	if err != nil {
		t.Fatalf("start renderer worker: %v", err)
	}
	t.Cleanup(func() { link.Fail(errors.New("renderer key publication test complete")) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := link.Call(ctx, "init", rendererInitParams{
		TerminalID: "key-without-screen-change",
		Cols:       80,
		Rows:       24,
	}); err != nil {
		t.Fatalf("initialize renderer worker: %v", err)
	}
	time.Sleep(3 * rendererPublishMinInterval)
	baseline := snapshotCount.Load()

	if err := link.Call(ctx, "update", rendererUpdateParams{Event: rendererInputEvent{
		Type:      "key",
		Keycode:   'x',
		Text:      "x",
		EventType: uint8(vaxis.EventPress),
	}}); err != nil {
		t.Fatalf("encode renderer key: %v", err)
	}
	waitForCondition(t, time.Second, func() bool {
		return ptyWriteCount.Load() == 1
	}, "renderer key PTY write")
	time.Sleep(3 * rendererPublishMinInterval)

	if got := snapshotCount.Load(); got != baseline {
		t.Fatalf("key encoding snapshots = %d -> %d, want unchanged until PTY output", baseline, got)
	}
}

func TestRendererWorkerFireAndForgetInputsKeepDistinctEventIDs(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: time.Second,
		QueueFrames:    16,
	}

	var (
		eventIDsMu sync.Mutex
		eventIDs   []uint64
	)
	link, err := startRendererLink(helper, 1, func(frame rendererWorkerFrame) {
		if frame.Type != rendererFramePTYWrite {
			return
		}
		eventIDsMu.Lock()
		eventIDs = append(eventIDs, frame.EventID)
		eventIDsMu.Unlock()
	})
	if err != nil {
		t.Fatalf("start renderer worker: %v", err)
	}
	t.Cleanup(func() { link.Fail(errors.New("renderer notification ID test complete")) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := link.Call(ctx, "init", rendererInitParams{
		TerminalID: "notification-event-ids",
		Cols:       80,
		Rows:       24,
	}); err != nil {
		t.Fatalf("initialize renderer worker: %v", err)
	}
	stop := make(chan struct{})
	for _, text := range []string{"x", "y"} {
		if err := link.Notify(stop, "update", rendererUpdateParams{Event: rendererInputEvent{
			Type:      "key",
			Keycode:   rune(text[0]),
			Text:      text,
			EventType: uint8(vaxis.EventPress),
		}}); err != nil {
			t.Fatalf("notify renderer input %q: %v", text, err)
		}
	}
	waitForCondition(t, time.Second, func() bool {
		eventIDsMu.Lock()
		defer eventIDsMu.Unlock()
		return len(eventIDs) == 2
	}, "two fire-and-forget renderer input writes")

	eventIDsMu.Lock()
	got := slices.Clone(eventIDs)
	eventIDsMu.Unlock()
	if got[0] == got[1] || got[0]&rendererInputEventBit == 0 || got[1]&rendererInputEventBit == 0 {
		t.Fatalf("renderer notification event IDs = %#v, want distinct input-scoped IDs", got)
	}
	if pending := link.Stats().PendingRequests; pending != 0 {
		t.Fatalf("fire-and-forget renderer inputs left %d pending responses", pending)
	}
}

func TestSustainedFullscreenOutputDoesNotRestartRendererOrEmitQueueErrors(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: defaultRendererCommandTimeout,
		QueueFrames:    defaultRendererQueueFrames,
	}

	var terminalErrors atomic.Int32
	terminal := newIsolatedDaemonTerminalWithOptions("sustained-output", func(event vaxis.Event) {
		if _, ok := event.(tuiTerminalErrorEvent); ok {
			terminalErrors.Add(1)
		}
	}, options)
	command := exec.Command(
		"/usr/bin/perl",
		"-e",
		`$frame = "\e[H\e[2J\e[32m" . ("X" x 1920); print $frame for 1..10000; print "\e[0m\e[2J\e[HCMATRIX_BURST_DONE\n"; sleep 30`,
	)
	if err := terminal.Start(command); err != nil {
		t.Fatalf("start sustained-output terminal: %v", err)
	}
	t.Cleanup(terminal.Close)

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(terminal.ReadVisible(ReadText).Text, "CMATRIX_BURST_DONE") {
		if time.Now().After(deadline) {
			t.Fatalf(
				"final sustained-output renderer snapshot timed out: status=%#v terminal_errors=%d",
				terminal.RendererStatus(),
				terminalErrors.Load(),
			)
		}
		time.Sleep(10 * time.Millisecond)
	}

	status := terminal.RendererStatus()
	if status.RestartCount != 0 || status.State != "ready" {
		t.Fatalf("sustained output renderer status = %#v, want ready without restarts", status)
	}
	if got := terminalErrors.Load(); got != 0 {
		t.Fatalf("sustained output emitted %d terminal errors, want none", got)
	}
}

func TestRendererRestartMarksCachedScreenStale(t *testing.T) {
	t.Parallel()

	terminal := newIsolatedDaemonTerminalWithOptions("stale", nil, defaultRendererProcessOptions())
	terminal.cache.Store(&isolatedTerminalCache{
		state: rendererPublishedState{Snapshot: TerminalSnapshot{Cols: 1, Rows: 1}},
	})
	terminal.markRendererStale()
	if snapshot := terminal.Snapshot().Snapshot; !snapshot.Stale {
		t.Fatal("renderer restart did not mark the cached screen stale")
	}
}
