//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

// publishStatusLocked runs on every inbound renderer frame. It used to take a
// deep copy of the whole retained journal to read two numbers off it, which cost
// roughly one journal-sized memcpy per PTY frame while holding the mutex the
// read pump needs to append. The published contract is unchanged; the copy is
// not allowed back.
func TestRendererStatusPublicationDoesNotCopyTheJournal(t *testing.T) {
	t.Parallel()

	journal := newRendererJournal(1 << 20)
	payload := bytes.Repeat([]byte("x"), 4096)
	for range 200 {
		journal.AppendOutput(payload)
	}
	stats := journal.Stats()
	if stats.Events != 200 {
		t.Fatalf("journal retained %d events, want 200", stats.Events)
	}

	supervisor := &rendererSupervisor{
		terminalID: "status-cost",
		journal:    journal,
		options:    defaultRendererProcessOptions(),
		policy:     defaultRendererRestartPolicy(),
		stateWake:  make(chan struct{}),
	}

	allocs := testing.AllocsPerRun(50, func() {
		supervisor.publishStatus(rendererStateReady, 4242)
	})
	// One status struct, plus slack for the atomic store. A journal copy would
	// be at least one allocation per retained event.
	if allocs > 8 {
		t.Fatalf("publishStatus allocated %v objects per call; the journal is being copied again", allocs)
	}

	status := supervisor.Status()
	if status.JournalBytes != stats.Bytes {
		t.Fatalf("published journal bytes = %d, want %d", status.JournalBytes, stats.Bytes)
	}
	if status.PartialRecovery != stats.Partial {
		t.Fatalf("published partial recovery = %v, want %v", status.PartialRecovery, stats.Partial)
	}
	if status.State != rendererStateReady || status.PID != 4242 {
		t.Fatalf("published status = %#v", status)
	}

	// The trimming path is where partial recovery becomes true; the cheap stats
	// accessor must keep reporting it.
	for range 400 {
		journal.AppendOutput(payload)
	}
	supervisor.publishStatus(rendererStateReady, 1)
	if !supervisor.Status().PartialRecovery {
		t.Fatal("published status lost the partial-recovery flag after journal trimming")
	}
}

func newLazyVariantTestTerminal(t *testing.T, id string) *isolatedDaemonTerminal {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := rendererProcessOptions{
		Executable:     executable,
		Args:           []string{"-test.run=^TestRendererWorkerProcess$"},
		Env:            []string{rendererWorkerHelperEnv + "=1"},
		CommandTimeout: 2 * time.Second,
		QueueFrames:    64,
	}
	terminal := newIsolatedDaemonTerminalWithOptions(id, func(vaxis.Event) {}, options)
	t.Cleanup(terminal.Close)
	return terminal
}

// Read variants other than the plain visible text used to be re-rendered on
// every publish -- up to 20 times a second, including two full scrollback walks
// -- whether or not anything ever called terminal.read. They are now fetched on
// demand and memoized for the life of the published screen.
func TestIsolatedTerminalRendersReadVariantsOnlyWhenRead(t *testing.T) {
	terminal := newLazyVariantTestTerminal(t, "lazy-variants")
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "printf '\\033[31mlazy-variant-marker\\033[0m\\r\\n'; sleep 30")); err != nil {
		t.Fatalf("start terminal: %v", err)
	}

	// Drive a stream of publications with nobody reading.
	waitForCondition(t, 10*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "lazy-variant-marker")
	}, "first published screen")
	for index := range 40 {
		terminal.SendInput([]byte("\r"))
		if index%8 == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(300 * time.Millisecond)

	if got := terminal.readFetches.Load(); got != 0 {
		t.Fatalf("renderer rendered %d read variants with no terminal.read issued, want 0", got)
	}
	epoch := terminal.cache.Load().epoch
	if epoch < 2 {
		t.Fatalf("only %d screens were published; the test never exercised the publish path", epoch)
	}

	// A real read renders exactly one variant and reuses it for the rest of the
	// published screen.
	recent := terminal.ReadRecent(ReadText)
	if recent.Err != nil {
		t.Fatalf("ReadRecent(text) error = %v", recent.Err)
	}
	if !strings.Contains(recent.Text, "lazy-variant-marker") {
		t.Fatalf("ReadRecent(text) = %q, want the shell output", recent.Text)
	}
	if got := terminal.readFetches.Load(); got != 1 {
		t.Fatalf("first read rendered %d variants, want 1", got)
	}
	if again := terminal.ReadRecent(ReadText); again.Text != recent.Text {
		t.Fatalf("memoized ReadRecent(text) = %q, want %q", again.Text, recent.Text)
	}
	if got := terminal.readFetches.Load(); got != 1 {
		t.Fatalf("repeated read of one screen rendered %d variants, want 1", got)
	}

	// A different variant of the same screen is its own rendering, and the ANSI
	// form must actually be the ANSI form.
	ansi := terminal.ReadRecent(ReadANSI)
	if ansi.Err != nil {
		t.Fatalf("ReadRecent(ansi) error = %v", ansi.Err)
	}
	if !strings.Contains(ansi.Text, "lazy-variant-marker") || ansi.Text == recent.Text {
		t.Fatalf("ReadRecent(ansi) = %q, want a distinct ANSI rendering of the shell output", ansi.Text)
	}
	if got := terminal.readFetches.Load(); got != 2 {
		t.Fatalf("second variant rendered %d variants in total, want 2", got)
	}

	// Plain visible text rides along with the snapshot and never costs a fetch.
	if visible := terminal.ReadVisible(ReadText); visible.Text != terminal.cache.Load().state.VisibleText.Text {
		t.Fatalf("ReadVisible(text) = %q, want the published screen text", visible.Text)
	}
	if got := terminal.readFetches.Load(); got != 2 {
		t.Fatalf("visible plain text cost a renderer round-trip, fetches = %d", got)
	}

	// A new published screen invalidates the memo.
	before := terminal.cache.Load().epoch
	terminal.SendInput([]byte("printf 'second-marker\\r\\n'\r"))
	waitForCondition(t, 10*time.Second, func() bool {
		cache := terminal.cache.Load()
		return cache != nil && cache.epoch > before &&
			strings.Contains(cache.state.VisibleText.Text, "second-marker")
	}, "second published screen")
	next := terminal.ReadRecent(ReadText)
	if !strings.Contains(next.Text, "second-marker") {
		t.Fatalf("ReadRecent(text) after republication = %q, want the new output", next.Text)
	}
	if got := terminal.readFetches.Load(); got != 3 {
		t.Fatalf("republication rendered %d variants in total, want 3", got)
	}
}

// A renderer that cannot answer must not turn terminal.read into a failure: the
// daemon served the last pushed variant before, and it serves the last rendered
// one now.
func TestIsolatedTerminalServesTheLastReadVariantWhileTheRendererIsGone(t *testing.T) {
	terminal := newLazyVariantTestTerminal(t, "lazy-variant-fallback")
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "printf 'fallback-marker\\r\\n'; sleep 30")); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "fallback-marker")
	}, "published screen")

	recent := terminal.ReadRecent(ReadText)
	if !strings.Contains(recent.Text, "fallback-marker") {
		t.Fatalf("ReadRecent(text) = %q, want the shell output", recent.Text)
	}

	terminal.mu.Lock()
	renderer := terminal.renderer
	terminal.renderer = nil
	terminal.mu.Unlock()
	t.Cleanup(func() {
		terminal.mu.Lock()
		terminal.renderer = renderer
		terminal.mu.Unlock()
	})

	stale := terminal.ReadRecent(ReadText)
	if stale.Err != nil {
		t.Fatalf("ReadRecent(text) with no renderer error = %v, want the last known text", stale.Err)
	}
	if stale.Text != recent.Text {
		t.Fatalf("ReadRecent(text) with no renderer = %q, want the last known %q", stale.Text, recent.Text)
	}
	// A variant that was never rendered reports empty rather than an error, which
	// is the daemon's existing signal to fall back to the visible screen.
	unseen := terminal.ReadRecent(ReadANSI)
	if unseen.Err != nil || unseen.Text != "" {
		t.Fatalf("never-rendered variant with no renderer = %#v, want an empty result", unseen)
	}
}
