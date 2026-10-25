package codelima

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestTerminalPromptIdleRequiresPositiveFreshEvidence(t *testing.T) {
	now := time.Now().UnixMilli()
	clock := terminalPromptIdle{}
	prompt := TerminalMetadata{CursorAtPrompt: true}
	if got := clock.observe(prompt, false, 0, now); got != now {
		t.Fatal("first prompt did not start idle clock", got)
	}
	if got := clock.observe(prompt, false, 0, now+60000); got != now {
		t.Fatal("observation reset the clock", got)
	}
	if got := clock.observe(prompt, false, now+10000, now+60000); got != now+10000 {
		t.Fatal("input did not reset the clock", got)
	}
	if got := clock.observe(prompt, true, 0, now+70000); got != 0 {
		t.Fatal("stale frame claimed current idle", got)
	}
	if got := clock.observe(prompt, false, 0, now+80000); got != now+10000 {
		t.Fatal("renderer recovery lost retained idle", got)
	}
	for _, state := range []TerminalMetadata{{Title: "idle shell"}, {CursorAtPrompt: true, ProgressState: 1}, {CursorAtPrompt: true, ProgressState: 2}, {CursorAtPrompt: true, ProgressState: 4}} {
		if got := clock.observe(state, false, 0, now+90000); got != 0 {
			t.Fatal("non-idle state retained idle clock", state, got)
		}
	}
	if got := clock.observe(prompt, false, 0, now+100000); got != now+100000 {
		t.Fatal("return to prompt reused earlier interval", got)
	}
	if got := clock.observe(prompt, false, now+200000, now); got != now {
		t.Fatal("clock rollback produced future idle timestamp", got)
	}
}

type promptIdleTestTerminal struct {
	*restartRecordingDaemonTerminal
	input atomic.Int64
}

func (t *promptIdleTestTerminal) LastInputUnixMilli() int64 { return t.input.Load() }

func TestTerminalPromptIdlePublicationSurvivesFrontendAndHandoff(t *testing.T) {
	s, _ := newTestService(t)
	h := newDaemonHost(s)
	t.Cleanup(func() { _ = h.Close() })
	published := make(chan daemon.TerminalDirtyEvent, 8)
	h.broadcast = func(kind string, data any) {
		if kind == daemon.EventTerminalDirty {
			published <- data.(daemon.TerminalDirtyEvent)
		}
	}
	term := &promptIdleTestTerminal{restartRecordingDaemonTerminal: newRestartRecordingDaemonTerminal("prompt")}
	term.metadata = TerminalMetadata{CursorAtPrompt: true}
	since := time.Now().Add(-4 * time.Minute).UnixMilli()
	state := daemon.TerminalState{TerminalID: "term_idle", Target: "node:" + newID()}
	entry := h.newTerminalEntry(state, term, terminalActivityState{metadata: TerminalMetadata{PromptIdleSinceUnixMilli: since}})
	t.Cleanup(entry.stopSnapshotPublisher)
	next := func() daemon.TerminalDirtyEvent {
		t.Helper()
		select {
		case event := <-published:
			return event
		case <-time.After(3 * time.Second):
			t.Fatal("no prompt publication")
			return daemon.TerminalDirtyEvent{}
		}
	}
	if got := next().Metadata.PromptIdleSinceUnixMilli; got != since {
		t.Fatal("restored prompt interval reset", got)
	}
	entry.requestSnapshot() // A second frontend reads the same retained interval.
	if got := next().Metadata.PromptIdleSinceUnixMilli; got != since {
		t.Fatal("new observer reset interval", got)
	}
	data, err := h.activityEvents.prepareHandoff([]*daemonTerminalEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	other := newDaemonHost(s)
	t.Cleanup(func() { _ = other.Close() })
	if err := other.activityEvents.restoreHandoff(daemon.HandoffManifest{Version: daemon.HandoffVersion, Activity: data, Session: daemon.Session{Terminals: []daemon.TerminalState{state}}}); err != nil {
		t.Fatal(err)
	}
	if got := other.activityEvents.adoptedBaseline(state.TerminalID, TerminalMetadata{}).PromptIdleSinceUnixMilli; got != since {
		t.Fatal("handoff lost prompt interval", got)
	}
	h.activityEvents.resumeAfterHandoff()
	input := time.Now().UnixMilli()
	term.input.Store(input)
	entry.requestSnapshot()
	if got := next().Metadata.PromptIdleSinceUnixMilli; got != input {
		t.Fatal("accepted input did not reset published idle", got)
	}
}
