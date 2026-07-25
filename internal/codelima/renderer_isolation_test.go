//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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
		CommandTimeout: 150 * time.Millisecond,
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
