//go:build integration

package tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima"
	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func TestDaemonRestartEndsShellAndRestoresSavedTab(t *testing.T) {
	h := newHarness(t)
	node := h.createNode()
	h.run(true, "daemon", "restart") // An absent daemon can also be started.
	var before daemon.Status
	if err := json.Unmarshal(h.json("daemon", "status"), &before); err != nil {
		t.Fatal(err)
	}
	var terminal daemon.TerminalState
	if err := json.Unmarshal(h.json("terminal", "open", "node:"+node, "--kind", "node-host-shell"), &terminal); err != nil {
		t.Fatal(err)
	}
	shellPID := h.shellPID(terminal.TerminalID)
	h.run(true, "daemon", "restart")
	var after daemon.Status
	if err := json.Unmarshal(h.json("daemon", "status"), &after); err != nil {
		t.Fatal(err)
	}
	if before.PID == after.PID || after.TerminalCount != 1 {
		t.Fatalf("restart did not replace process/restore tab: %+v / %+v", before, after)
	}
	if pid := h.shellPID(terminal.TerminalID); pid == shellPID {
		t.Fatal("restart kept the old live shell")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && syscall.Kill(shellPID, 0) == nil {
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(shellPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("old shell survived restart: %v", err)
	}
	h.run(true, "terminal", "send", terminal.TerminalID, "--text", "printf 'COUNT=181\\n'\r")
	h.waitForCounter(terminal.TerminalID, "COUNT=181")
	// A new daemon reads the configured restoration policy. Forget must not
	// silently respawn the saved tab.
	if err := os.WriteFile(filepath.Join(h.home, "_config", "settings.yaml"), []byte("daemon:\n  restore: forget\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.run(true, "daemon", "restart")
	if err := json.Unmarshal(h.json("daemon", "status"), &after); err != nil {
		t.Fatal(err)
	}
	if after.TerminalCount != 0 {
		t.Fatal("restart ignored daemon.restore=forget")
	}
}

func TestDaemonDiagnosticCLIObservesWithoutTakingSeatOrStartingDaemon(t *testing.T) {
	h := newHarness(t)
	var offline struct{ Directory string }
	if err := json.Unmarshal(h.json("daemon", "diagnose", "--sample-seconds", "0"), &offline); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nodes", "configurations", "_daemon/daemon.pid"} {
		if _, err := os.Stat(filepath.Join(h.home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("offline capture initialized %s: %v", name, err)
		}
	}
	settings := filepath.Join(h.home, "_config", "settings.yaml")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("daemon: [broken")
	if err := os.WriteFile(settings, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	h.run(true, "daemon", "diagnose", "--sample-seconds", "0")
	if data, err := os.ReadFile(settings); err != nil || string(data) != string(corrupt) {
		t.Fatal("capture changed broken settings", err)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatal(err)
	}
	node := h.createNode()
	h.run(true, "daemon", "start")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner, err := daemonclient.Dial(ctx, daemonclient.Options{Home: h.home, Version: codelima.Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	var terminal daemon.TerminalState
	if err := json.Unmarshal(h.json("terminal", "open", "node:"+node, "--kind", "node-host-shell"), &terminal); err != nil {
		t.Fatal(err)
	}
	shellPID := h.shellPID(terminal.TerminalID)
	var before daemon.Status
	if err := owner.Call(ctx, "daemon.status", nil, &before); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Directory, Summary string
		Files              int
	}
	if err := json.Unmarshal(h.json("daemon", "diagnose", "--sample-seconds", "0"), &result); err != nil {
		t.Fatal(err)
	}
	if result.Files < 10 || !strings.Contains(result.Summary, "cached terminal read both responded") {
		t.Fatalf("incomplete capture: %+v", result)
	}
	for _, name := range []string{"probed-terminal-id.txt", "probed-renderer-pid.txt", "daemon-snapshot.json", "terminal-read.json", "summary.md", "probes.json"} {
		info, err := os.Stat(filepath.Join(result.Directory, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var after daemon.Status
	if err := owner.Call(ctx, "daemon.status", nil, &after); err != nil {
		t.Fatal(err)
	}
	if after.PID != before.PID || after.InputOwner != before.InputOwner || h.shellPID(terminal.TerminalID) != shellPID {
		t.Fatal("diagnostics changed the daemon, seat or shell")
	}
	h.run(true, "terminal", "close", terminal.TerminalID)
}
