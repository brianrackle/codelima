package codelima

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

type delayedCloseDaemonTerminal struct {
	*resizeCountingDaemonTerminal
	started chan<- struct{}
	release <-chan struct{}
}

func (t *delayedCloseDaemonTerminal) Close() {
	t.started <- struct{}{}
	<-t.release
}

func TestDaemonHostClosesTerminalRuntimesConcurrently(t *testing.T) {
	t.Parallel()

	home, err := os.MkdirTemp("../../tmp", "daemon-close-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	newTerminal := func() daemonTerminal {
		return &delayedCloseDaemonTerminal{
			resizeCountingDaemonTerminal: &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()},
			started:                      started,
			release:                      release,
		}
	}
	host := &daemonHost{
		session: filepath.Join(home, "session.json"),
		terminals: map[string]*daemonTerminalEntry{
			"one": {term: newTerminal()},
			"two": {term: newTerminal()},
		},
	}
	done := make(chan error, 1)
	go func() { done <- host.Close() }()

	for range 2 {
		select {
		case <-started:
		case <-time.After(250 * time.Millisecond):
			close(release)
			<-done
			t.Fatal("terminal shutdowns were serialized")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("daemonHost.Close() error = %v", err)
	}
}
