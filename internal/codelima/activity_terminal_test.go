package codelima

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestActivityNativeTerminalTransitionsKeepIdentityAndDoNotReplay(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	e := h.activityEvents
	state := daemon.TerminalState{TerminalID: "term_test", Target: "node:" + newID(), Label: "session"}
	observer := terminalActivityState{nodeSlug: "alpha"}
	metadata := TerminalMetadata{Title: "Fix renderer\x1b", BellCount: 2, NotificationCount: 1, NotificationBody: "done\nnow"}
	observer.observe(e, state, daemon.Snapshot{Metadata: metadata, Stale: true})
	if len(e.pending) != 0 {
		t.Fatal("stale snapshot produced notifications")
	}
	observer.observe(e, state, daemon.Snapshot{Metadata: metadata})
	observer.observe(e, state, daemon.Snapshot{Metadata: metadata})
	if len(e.pending) != 2 || !strings.Contains(e.pending[0].Text, "2 bells") || !strings.Contains(e.pending[1].Text, "donenow") {
		t.Fatal("coalesced notifications lost counts or repeated", e.pending)
	}
	for _, message := range e.pending {
		if message.Action != activityOpenTerminal || message.TerminalID != state.TerminalID || message.NodeID == "" || message.NodeSlug != "alpha" || strings.ContainsAny(message.Text, "\x1b\n") {
			t.Fatal("notification lost safe text or identity", message)
		}
	}
	// A restored frame is the baseline, including an already visible notice.
	adopted := terminalActivityState{metadata: metadata, nodeSlug: "alpha"}
	adopted.observe(e, state, daemon.Snapshot{Metadata: metadata})
	metadata.NotificationCount++ // Identical text is still a new notification.
	adopted.observe(e, state, daemon.Snapshot{Metadata: metadata})
	if len(e.pending) != 3 {
		t.Fatal("adoption replayed history or lost repeated notice", e.pending)
	}
	metadata.BellCount, metadata.NotificationCount = 0, 0
	adopted.observe(e, state, daemon.Snapshot{Metadata: metadata})
	metadata.BellCount = 1
	adopted.observe(e, state, daemon.Snapshot{Metadata: metadata})
	if len(e.pending) != 4 {
		t.Fatal("renderer counter reset replayed old state", e.pending)
	}
	e.Close()
	metadata.BellCount++
	adopted.observe(e, state, daemon.Snapshot{Metadata: metadata})
	if len(e.pending) != 4 {
		t.Fatal("closed producer accepted another notice")
	}
}

type adoptedNoticeTerminal struct {
	*fakeHandoffTerminal
	metadata TerminalMetadata
}

func (t *adoptedNoticeTerminal) Snapshot() SnapshotResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	return SnapshotResult{Snapshot: TerminalSnapshot{Metadata: t.metadata}}
}

func TestActivityNativeAdoptedPublisherRecordsOnlyNewOutput(t *testing.T) {
	s, _ := newHandoffTestService(t)
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	manifest := handoffManifestFor([]string{"term_notice"}, "token")
	manifest.Session.Terminals[0].Target = "node:" + newID()
	var adopted *adoptedNoticeTerminal
	adopt := func(_ string, _ func(vaxis.Event), pty *os.File, _, _, _ int, _ []byte, _ bool, _ []byte) (daemonTerminal, error) {
		adopted = &adoptedNoticeTerminal{fakeHandoffTerminal: adoptFakeHandoffTerminal(pty), metadata: TerminalMetadata{BellCount: 3, NotificationCount: 2, NotificationBody: "already announced"}}
		return adopted, nil
	}
	published := make(chan struct{}, 4)
	h.broadcast = func(kind string, _ any) {
		if kind == daemon.EventTerminalDirty {
			published <- struct{}{}
		}
	}
	if _, err := h.adoptHandoffTerminals(context.Background(), manifest, handoffFDs(t, []string{"term_notice"}), adopt); err != nil {
		t.Fatal(err)
	}
	wait := func() {
		t.Helper()
		select {
		case <-published:
		case <-time.After(time.Second):
			t.Fatal("publisher did not run")
		}
	}
	wait()
	h.activityEvents.mu.Lock()
	count := len(h.activityEvents.pending)
	h.activityEvents.mu.Unlock()
	if count != 0 {
		t.Fatal("adopted publisher replayed old notices", count)
	}
	adopted.mu.Lock()
	adopted.metadata.BellCount++
	adopted.metadata.NotificationCount++
	adopted.mu.Unlock()
	entry, err := h.lookup("term_notice")
	if err != nil {
		t.Fatal(err)
	}
	entry.requestSnapshot()
	wait()
	h.activityEvents.mu.Lock()
	defer h.activityEvents.mu.Unlock()
	if len(h.activityEvents.pending) != 2 {
		t.Fatal("new output was suppressed", h.activityEvents.pending)
	}
}

func TestActivityNativeAdoptionPublishesUnobservedHandoffTail(t *testing.T) {
	s, _ := newHandoffTestService(t)
	old, next := newDaemonHost(s), newDaemonHost(s)
	t.Cleanup(func() { _ = next.Close() })
	manifest := handoffManifestFor([]string{"term_tail"}, "token")
	manifest.Session.Terminals[0].Target = "node:" + newID()
	entry := &daemonTerminalEntry{state: manifest.Session.Terminals[0]}
	entry.activity.observe(old.activityEvents, entry.state, daemon.Snapshot{Metadata: TerminalMetadata{BellCount: 1, NotificationCount: 1, NotificationBody: "same text"}})
	data, err := old.activityEvents.prepareHandoff([]*daemonTerminalEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Activity = data
	if err := next.activityEvents.restoreHandoff(manifest); err != nil {
		t.Fatal(err)
	}
	published := make(chan struct{}, 1)
	next.broadcast = func(kind string, _ any) {
		if kind == daemon.EventTerminalDirty {
			select {
			case published <- struct{}{}:
			default:
			}
		}
	}
	adopt := func(_ string, _ func(vaxis.Event), pty *os.File, _, _, _ int, _ []byte, _ bool, _ []byte) (daemonTerminal, error) {
		return &adoptedNoticeTerminal{fakeHandoffTerminal: adoptFakeHandoffTerminal(pty), metadata: TerminalMetadata{BellCount: 2, NotificationCount: 2, NotificationBody: "same text"}}, nil
	}
	if _, err := next.adoptHandoffTerminals(context.Background(), manifest, handoffFDs(t, []string{"term_tail"}), adopt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("adopted snapshot was not observed")
	}
	next.activityEvents.mu.Lock()
	defer next.activityEvents.mu.Unlock()
	if len(next.activityEvents.pending) != 4 {
		t.Fatal("restored baseline suppressed unobserved output", next.activityEvents.pending)
	}
	for i := range 2 {
		if next.activityEvents.pending[i] != old.activityEvents.pending[i] || next.activityEvents.pending[i+2].ID == old.activityEvents.pending[i].ID {
			t.Fatal("adopted observer changed admitted IDs or reused them for new output")
		}
	}
}

func TestActivityNativePublisherRunsWhileGeometryChanges(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	term := newRestartRecordingDaemonTerminal("notice")
	term.metadata = TerminalMetadata{BellCount: 1}
	state := daemon.TerminalState{TerminalID: "term_geometry", Target: "node:" + newID()}
	entry := h.newTerminalEntry(state, term)
	t.Cleanup(entry.stopSnapshotPublisher)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 1000 {
			h.mu.Lock()
			entry.state.Cols, entry.state.Rows = i+1, i+2
			h.mu.Unlock()
			entry.requestSnapshot()
			runtime.Gosched()
		}
	}()
	<-done
	waitForCondition(t, time.Second, func() bool { return entry.snapshotSequence.Load() >= 2 }, "snapshots during geometry updates")
	h.activityEvents.mu.Lock()
	defer h.activityEvents.mu.Unlock()
	if len(h.activityEvents.pending) != 1 {
		t.Fatal("resize repeated a notification", h.activityEvents.pending)
	}
}

func TestTUIActivityDaemonNoticesDoNotEnterFrontendOutbox(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	term := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{NotificationBody: "ready"}}
	putTestSession(a.sessions, nodeTargetKey("node-root"), &tuiSession{label: "shell"}, term)
	a.service = &Service{daemonClient: &daemonclient.Client{}}
	a.recordTerminalNotices()
	if len(a.messages.Entries()) != 0 {
		t.Fatal("daemon notice was appended again by TUI")
	}
	a.service = nil
	a.recordTerminalNotices()
	if len(a.messages.Entries()) != 1 {
		t.Fatal("local fallback lost its notice")
	}
}

type baselineTestTerminal struct {
	*fakeHandoffTerminal
	reads int
}

func (t *baselineTestTerminal) Snapshot() SnapshotResult {
	t.reads++
	return SnapshotResult{Snapshot: TerminalSnapshot{Stale: t.reads < 2, Metadata: TerminalMetadata{BellCount: 4, NotificationCount: 2, NotificationBody: "old notice"}}}
}

func TestActivityNativeAdoptionWaitsForFreshBaseline(t *testing.T) {
	term := &baselineTestTerminal{fakeHandoffTerminal: newFakeHandoffTerminal()}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := terminalActivityBaseline(ctx, term)
	if err != nil || term.reads < 2 || got.BellCount != 4 || got.NotificationCount != 2 {
		t.Fatal(got, term.reads, err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := terminalActivityBaseline(canceled, term); err == nil {
		t.Fatal("canceled adoption still acquired baseline")
	}
}

func TestActivityJournalRetainsTypedTargetsAndLegacyMessages(t *testing.T) {
	s, _ := newTestService(t)
	j, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	legacy := activityMessageRequest{ID: newID(), Source: "tui", Text: "legacy"}
	terminal := activityMessageRequest{ID: newID(), Source: "terminal", Text: "bell", NodeID: newID(), Action: activityOpenTerminal, TerminalID: "term_test"}
	update := activityMessageRequest{ID: newID(), Source: "daemon", Text: "updated", Action: activityOpenDaemon, Success: true}
	for _, request := range []activityMessageRequest{legacy, terminal, update} {
		if _, err := j.Append(request); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := newActivityJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	records, _ := reopened.Snapshot()
	page, err := buildActivityPage(nil, records, "", 50)
	if err != nil || len(page.History) != 3 || !page.History[0].Success || page.History[1].TerminalID != "term_test" || page.History[1].Action != activityOpenTerminal || page.History[2].Action != "" {
		t.Fatal(page, err)
	}
	for _, bad := range []activityMessageRequest{
		{ID: newID(), Source: "tui", Text: "bad", Action: "execute"},
		{ID: newID(), Source: "terminal", Text: "bad", Action: activityOpenTerminal, TerminalID: "term_test"},
		{ID: newID(), Source: "terminal", Text: "bad", NodeID: newID(), Action: activityOpenTerminal, TerminalID: "../shell"},
		{ID: newID(), Source: "host", Text: "bad", Action: activityOpenHost, TerminalID: "term_test"},
	} {
		if _, err := j.Append(bad); err == nil {
			t.Fatal("invalid navigation accepted", bad)
		}
	}
}
