//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/terminalio"
	"github.com/creack/pty"
	"go.rockorager.dev/vaxis"
	"golang.org/x/sys/unix"
)

func TestRawAttachOutputCancellationWhenPhysicalTerminalStopsReading(t *testing.T) {
	_, slave := rawAttachTestPTY(t)
	fd, err := terminalio.Descriptor(slave)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- writeRawAttachOutput(ctx, fd, []byte(strings.Repeat("x", 2<<20))) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked physical output: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("physical output ignored cancellation")
	}
}

func TestRawAttachClientUsesPhysicalTerminalAndDrainsExitOutput(t *testing.T) {
	client, terminal := newRawAttachHarness(t, "stty -echo; printf raw-ready; while IFS= read -r line; do [ \"$line\" = exit ] && break; printf 'reply:%s\\n' \"$line\"; done; printf final-output")
	master, slave := rawAttachTestPTY(t)
	var captured lockedRawOutput
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(&captured, master); close(readDone) }()
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close(); <-readDone })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attached := make(chan error, 1)
	go func() { _, err := attachDaemonTerminalFile(ctx, client, "raw", slave); attached <- err }()
	waitForCondition(t, 5*time.Second, func() bool { return captured.contains("raw-ready") }, "attached physical screen")
	_, _ = master.Write([]byte("hello\n"))
	waitForCondition(t, 5*time.Second, func() bool { return captured.contains("reply:hello") }, "physical input reaching daemon PTY")
	_, _ = master.Write([]byte{0x1d})
	select {
	case err := <-attached:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+] did not detach")
	}
	terminal.SendInput([]byte("still-alive\n"))
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(terminal.ReadVisible(ReadText).Text, "reply:still-alive") }, "detached session surviving")
	go func() { _, err := attachDaemonTerminalFile(ctx, client, "raw", slave); attached <- err }()
	waitForCondition(t, 5*time.Second, func() bool { return captured.contains("reply:still-alive") }, "reattached current screen")
	_, _ = master.Write([]byte("exit\n"))
	select {
	case err := <-attached:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exited session did not return")
	}
	waitForCondition(t, time.Second, func() bool { return captured.contains("final-output") }, "last output after terminal removal")
}

func TestRawAttachHostAnswersTerminalQueriesWithoutNativeDuplicate(t *testing.T) {
	client, _ := newRawAttachHarness(t, "stty raw -echo; printf query-ready; dd bs=1 count=1 >/dev/null 2>&1; printf '\033[6n'; dd bs=1 count=6 2>/dev/null | od -An -tx1; printf query-finished")
	master, slave := rawAttachTestPTY(t)
	var captured lockedRawOutput
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(&captured, master); close(readDone) }()
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close(); <-readDone })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attached := make(chan error, 1)
	go func() { _, err := attachDaemonTerminalFile(ctx, client, "raw", slave); attached <- err }()
	waitForCondition(t, 5*time.Second, func() bool { return captured.contains("query-ready") }, "query fixture readiness")
	_, _ = master.Write([]byte("x"))
	waitForCondition(t, 5*time.Second, func() bool { return captured.contains("\x1b[6n") }, "raw terminal query")
	if captured.contains("query-finished") {
		t.Fatal("native renderer answered a query owned by the host")
	}
	_, _ = master.Write([]byte("\x1b[9;9R"))
	select {
	case err := <-attached:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host response did not reach the process")
	}
	waitForCondition(t, time.Second, func() bool { return captured.contains("1b 5b 39 3b 39 52") }, "physical terminal's query response")
}

type lockedRawOutput struct {
	mu    sync.Mutex
	value strings.Builder
}

func (o *lockedRawOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.value.Write(data)
}
func (o *lockedRawOutput) contains(text string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Contains(o.value.String(), text)
}

func rawAttachTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	if err := pty.Setsize(slave, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	fd, err := terminalio.Descriptor(slave)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetNonblock(fd, true); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func newRawAttachHarness(t *testing.T, command string) (*daemonclient.Client, *isolatedDaemonTerminal) {
	t.Helper()
	root := newDaemonTestRoot(t, "raw-")
	service := NewService(DefaultConfig(root), newFakeSandbox(), strings.NewReader(""), io.Discard, io.Discard)
	if err := service.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	host := newDaemonHost(service)
	terminal := newIsolatedDaemonTerminalWithOptions("raw", nil, rendererGhosttyWorkerOptions(t))
	// Use the normal event path so exit removes the public terminal before
	// the attachment drains its final buffered bytes.
	terminal.postEvent = func(event vaxis.Event) { host.handleTerminalEvent("raw", event) }
	host.terminals["raw"] = &daemonTerminalEntry{term: terminal, state: daemon.TerminalState{TerminalID: "raw", Cols: 80, Rows: 24}}
	server := daemon.NewServer(daemon.Config{Home: root, Version: Version, Handler: host})
	host.broadcast = server.Broadcast
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitForDaemonPing(t, root)
	if err := terminal.Start(exec.Command("/bin/sh", "-c", command)); err != nil {
		t.Fatal(err)
	}
	client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: root, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); terminal.Close() })
	return client, terminal
}
