package codelima

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

type metadataTestTerminal struct {
	*fakeTUITerminal
	metadata TerminalMetadata
}

func (t *metadataTestTerminal) Metadata() TerminalMetadata { return t.metadata }

func TestTerminalTabSegmentsUseCompactDefaults(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatal(err)
	}
	target := nodeTargetKey("node-root")
	first := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal()}
	second := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal()}
	firstKey := putTestSession(sessions, target, &tuiSession{label: "codelima", shellKind: terminal.NodeShell}, first)
	secondKey := putTestSession(sessions, target, &tuiSession{label: "codelima", shellKind: terminal.NodeShell}, second)
	state.setActiveTab(target, firstKey)
	app := &vaxisTUIApp{state: state, sessions: sessions}

	for _, tt := range []struct {
		name   string
		first  TerminalMetadata
		second TerminalMetadata
		host   bool
		want   string
	}{
		{name: "untitled fallback", want: "[shell] shell"},
		{name: "reported examples", first: TerminalMetadata{Title: "Test this | codelima"}, second: TerminalMetadata{Title: "⠇ Test | codelima", BellCount: 1}, want: "[Test this | codelima] ⠇ Test | codelima 🔔"},
		{name: "title changes", first: TerminalMetadata{Title: "New session"}, second: TerminalMetadata{Title: "Done", ProgressState: 1, Progress: 100, NotificationBody: "finished"}, want: "[New session] Done · 100% · notice"},
		{name: "titles cleared", first: TerminalMetadata{BellCount: 1}, want: "[shell] shell"},
		{name: "inert titles", first: TerminalMetadata{Title: "\x1b\a\r\n\u202e"}, second: TerminalMetadata{Title: "\x1b\a\r\n\u202e title "}, want: "[shell] title"},
		{name: "bounded unicode", first: TerminalMetadata{Title: strings.Repeat("界", 41)}, want: "[" + strings.Repeat("界", 40) + "…] shell"},
		{name: "host retains marker", second: TerminalMetadata{Title: "Host task"}, host: true, want: "[shell] host:Host task"},
		{name: "host fallback", host: true, want: "[shell] host"},
		{name: "shell prompt titles", first: TerminalMetadata{Title: "brian@lima-codelima: /Users/brian/projects/codelima"}, second: TerminalMetadata{Title: "brian: ~/projects/codelima"}, want: "[shell] shell"},
		{name: "host prompt title", first: TerminalMetadata{Title: "bash"}, second: TerminalMetadata{Title: "brian@mac: /Users/brian/projects/codelima"}, host: true, want: "[shell] host"},
		{name: "return to shell", first: TerminalMetadata{Title: "brian: /workspace"}, second: TerminalMetadata{Title: "brian: /workspace", BellCount: 1, ProgressState: 1, Progress: 60}, want: "[shell] shell · 60% 🔔"},
		{name: "duplicate titles", first: TerminalMetadata{Title: "Task"}, second: TerminalMetadata{Title: "Task"}, want: "[Task] Task"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			first.metadata, second.metadata = tt.first, tt.second
			sessions.sessions[secondKey].shellKind = terminal.NodeShell
			if tt.host {
				sessions.sessions[secondKey].shellKind = terminal.NodeHostShell
			}
			if got := segmentsText(app.terminalTabSegments(tuiSelectedStyle(), tuiMutedStyle())); got != tt.want {
				t.Fatalf("tab bar = %q, want %q", got, tt.want)
			}
			if state.activeSessionKey() != firstKey || len(sessions.TargetSessionKeys(target)) != 2 {
				t.Fatal("metadata changed tab identity or selection")
			}
		})
	}
}

func TestTerminalTabLabelRecognizesDefaultShellTitles(t *testing.T) {
	for _, tt := range []struct {
		title string
		want  string
	}{
		{"", "shell"},
		{"bash", "shell"},
		{"-zsh", "shell"},
		{"fish", "shell"},
		{"brian: /Users/brian/projects/codelima", "shell"},
		{"brian@lima-codelima:/workspace", "shell"},
		{"brian.rackle_guest@mac.local: ~/My Projects/codelima", "shell"},
		{"brian: ~", "shell"},
		{"brian: /", "shell"},
		{"\u202ebrian: /workspace\a", "shell"},
		{strings.Repeat("user", 20) + "@host: /workspace", "shell"},
		{"Fix login bug", "Fix login bug"},
		{"Review: parser", "Review: parser"},
		{"Fix login bug: /workspace", "Fix login bug: /workspace"},
		{"https://example.com/task", "https://example.com/task"},
		{"brian: /workspace | Review", "brian: /workspace | Review"},
		{"brian: /" + strings.Repeat("x", 40) + " | Review", "brian: /" + strings.Repeat("x", 32) + "…"},
		{"brian: /workspace · Review", "brian: /workspace · Review"},
		{"brian@@host: /workspace", "brian@@host: /workspace"},
		{"@host: /workspace", "@host: /workspace"},
		{"brian@host@other: /workspace", "brian@host@other: /workspace"},
		{"brian: project", "brian: project"},
		{"⠋ Working | codelima", "⠋ Working | codelima"},
		{"bash - running tests", "bash - running tests"},
	} {
		t.Run(tt.title, func(t *testing.T) {
			for _, kind := range []terminal.TerminalKind{terminal.NodeShell, terminal.NodeHostShell} {
				want := tt.want
				if kind == terminal.NodeHostShell {
					if want == "shell" {
						want = "host"
					} else {
						want = "host:" + want
					}
				}
				session := &tuiSession{key: "node:private-id#17", label: "long-repeated-node", shellKind: kind}
				if got := terminalTabLabel(session, TerminalMetadata{Title: tt.title}); got != want {
					t.Fatalf("%s title %q: got %q, want %q", kind, tt.title, got, want)
				}
			}
		})
	}
}

func TestTerminalBellClearsOnVisitAndReturnsForNewBell(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(ctx, service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatal(err)
	}
	target := nodeTargetKey("node-root")
	firstKey := putTestSession(sessions, target, &tuiSession{label: "one", shellKind: terminal.NodeShell}, newFakeTUITerminal())
	second := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{Title: "Task", BellCount: 2}}
	secondKey := putTestSession(sessions, target, &tuiSession{label: "two", shellKind: terminal.NodeShell}, second)
	state.setActiveTab(target, firstKey)
	app := &vaxisTUIApp{state: state, sessions: sessions}
	check := func(want string) {
		t.Helper()
		if got := segmentsText(app.terminalTabSegments(tuiSelectedStyle(), tuiMutedStyle())); got != want {
			t.Fatalf("tab bar = %q, want %q", got, want)
		}
	}
	check("[shell] Task 🔔")
	state.setActiveTab(target, secondKey)
	state.treePaneMode = tuiTreePaneModeInfo
	check("shell [Task 🔔]") // An info pane is not a visit to its terminal.
	state.treePaneMode = tuiTreePaneModeTerminal
	sessions.setWindowFocused(false)
	check("shell [Task 🔔]")
	sessions.setWindowFocused(true)
	app.overlay = newTUIDialog("test", "close", nil, nil, nil)
	check("shell [Task 🔔]")
	app.overlay = nil
	check("shell [Task]")
	state.setActiveTab(target, firstKey)
	check("[shell] Task") // The same cumulative count stays acknowledged.
	second.metadata.BellCount++
	check("[shell] Task 🔔")
	state.setActiveTab(target, secondKey)
	check("shell [Task]")
	second.metadata.BellCount++
	check("shell [Task]") // Bells while viewing this tab are already seen.
	state.setActiveTab(target, firstKey)
	check("[shell] Task")
	second.metadata.BellCount = 0 // A restarted renderer may reset its counter.
	check("[shell] Task")
	second.metadata.BellCount = 1
	check("[shell] Task 🔔")
}

func TestHiddenTerminalBellMetadataArrivesWithoutSnapshotPull(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	events := make(chan vaxis.Event, 1)
	sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatal(err)
	}
	target := nodeTargetKey("node-root")
	active := putTestSession(sessions, target, &tuiSession{label: "one", shellKind: terminal.NodeShell}, newFakeTUITerminal())
	hidden := &daemonTUITerminal{snapshotWake: make(chan struct{}, 1)}
	hiddenKey := putTestSession(sessions, target, &tuiSession{label: "two", shellKind: terminal.NodeShell}, hidden)
	state.setActiveTab(target, active)
	app := &vaxisTUIApp{state: state, sessions: sessions}
	sessions.handleDaemonEvent(daemon.Event{Event: daemon.EventTerminalDirty, Data: map[string]any{
		"terminal_id":       sessions.sessions[hiddenKey].terminalID,
		"snapshot_sequence": 7,
		"metadata":          TerminalMetadata{Title: "Background task", BellCount: 1},
	}})
	if _, err := app.handleEvent(<-events); err != nil {
		t.Fatal(err)
	}
	if got := segmentsText(app.terminalTabSegments(tuiSelectedStyle(), tuiMutedStyle())); got != "[shell] Background task 🔔" {
		t.Fatalf("hidden tab bar = %q", got)
	}
	if len(hidden.snapshotWake) != 0 {
		t.Fatal("hidden metadata update requested a full screen snapshot")
	}
}

func TestTerminalTabMetadataUpdatesRegardlessOfFocus(t *testing.T) {
	for _, mode := range []string{"active", "background", "unfocused window"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			service, _ := newTestService(t)
			events := make(chan vaxis.Event, 1)
			sessions := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
			state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
			if err != nil {
				t.Fatal(err)
			}
			target := nodeTargetKey("node-root")
			other := putTestSession(sessions, target, &tuiSession{label: "other", shellKind: terminal.NodeShell}, newFakeTUITerminal())
			view := &daemonTUITerminal{snapshotWake: make(chan struct{}, 1)}
			key := putTestSession(sessions, target, &tuiSession{label: "task", shellKind: terminal.NodeShell}, view)
			state.setActiveTab(target, key)
			if mode == "background" {
				state.setActiveTab(target, other)
			}
			sessions.setWindowFocused(mode != "unfocused window")
			app := &vaxisTUIApp{state: state, sessions: sessions}
			for index, update := range []struct {
				metadata TerminalMetadata
				label    string
			}{
				{TerminalMetadata{Title: "⠋ Working", ProgressState: 1, Progress: 10}, "⠋ Working · 10%"},
				{TerminalMetadata{Title: "⠧ Working", ProgressState: 1, Progress: 10}, "⠧ Working · 10%"},
				{TerminalMetadata{Title: "⠧ Renamed", ProgressState: 1, Progress: 10}, "⠧ Renamed · 10%"},
				{TerminalMetadata{Title: "⠧ Renamed", ProgressState: 1, Progress: 60}, "⠧ Renamed · 60%"},
				{TerminalMetadata{Title: "Done", BellCount: 1}, "Done"},
			} {
				before := app.drawPasses
				sessions.handleDaemonEvent(daemon.Event{Event: daemon.EventTerminalDirty, Data: daemon.TerminalDirtyEvent{
					TerminalID:       string(sessions.sessions[key].terminalID),
					SnapshotSequence: uint64(index + 1),
					Metadata:         update.metadata,
				}})
				if _, err := app.handleEvent(<-events); err != nil {
					t.Fatal(err)
				}
				if view.Metadata() != update.metadata || app.drawPasses != before+1 {
					t.Fatalf("update %d did not immediately update metadata and redraw in %s", index, mode)
				}
				want := update.label
				if update.metadata.BellCount > 0 && mode != "active" {
					want += " 🔔"
				}
				if mode == "background" {
					want = "[shell] " + want
				} else {
					want = "shell [" + want + "]"
				}
				for range 2 {
					if got := segmentsText(app.terminalTabSegments(tuiSelectedStyle(), tuiMutedStyle())); got != want {
						t.Fatalf("update %d tab bar = %q, want %q", index, got, want)
					}
				}
				if mode == "background" && len(view.snapshotWake) != 0 {
					t.Fatal("background status update requested a full terminal grid")
				}
			}
		})
	}
}

func TestTerminalMetadataIsBoundedInertText(t *testing.T) {
	got := terminalMetadataText("\x1b]2;evil\a\r\n\u202ehello", 12)
	if strings.ContainsAny(got, "\x1b\a\r\n\u202e") || len([]rune(got)) > 13 {
		t.Fatalf("unsafe label: %q", got)
	}
	badge := terminalMetadataBadge(TerminalMetadata{Title: "shell", BellCount: 2, ProgressState: 1, Progress: 999, NotificationBody: "notice"})
	if badge != "100% · notice" {
		t.Fatalf("badge=%q", badge)
	}
}

func TestDaemonTerminalMetadataRejectsStaleUpdates(t *testing.T) {
	view := &daemonTUITerminal{}
	view.installSnapshot(daemon.Snapshot{SnapshotSequence: 5, Metadata: TerminalMetadata{Title: "Initial"}})
	want := TerminalMetadata{Title: "Alert", BellCount: 2}
	if !view.updateMetadata(7, want) {
		t.Fatal("new metadata did not request a redraw")
	}
	if view.updateMetadata(6, TerminalMetadata{Title: "Stale"}) || view.Metadata() != want {
		t.Fatal("out-of-order metadata replaced the current alert")
	}
	// The screen worker can finish a request issued before the pushed event.
	view.mu.Lock()
	view.snapshot = daemon.Snapshot{SnapshotSequence: 6, Metadata: TerminalMetadata{Title: "Delayed screen"}}
	view.mu.Unlock()
	if view.Metadata() != want {
		t.Fatal("a delayed screen rolled back pushed metadata")
	}
	if view.updateMetadata(8, want) {
		t.Fatal("unchanged metadata requested another redraw")
	}
	if !view.updateMetadata(9, TerminalMetadata{}) || view.Metadata() != (TerminalMetadata{}) {
		t.Fatal("cleared metadata did not replace the alert")
	}
	view.installSnapshot(daemon.Snapshot{SnapshotSequence: 1, Metadata: TerminalMetadata{Title: "Reconnected"}})
	if view.Metadata().Title != "Reconnected" || !view.updateMetadata(2, want) {
		t.Fatal("authoritative synchronization did not reset the sequence epoch")
	}
	view.closed = true
	if view.updateMetadata(3, TerminalMetadata{}) || view.Metadata() != want {
		t.Fatal("a closed terminal accepted metadata")
	}
}

type metadataSnapshotCaller struct {
	requests chan chan daemon.Snapshot
}

func (c metadataSnapshotCaller) Call(ctx context.Context, _ string, _ any, result any) error {
	reply := make(chan daemon.Snapshot, 1)
	c.requests <- reply
	select {
	case snapshot := <-reply:
		*result.(*daemon.Snapshot) = snapshot
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestDaemonTerminalMetadataSurvivesInflightSnapshotAtReconnect(t *testing.T) {
	caller := metadataSnapshotCaller{requests: make(chan chan daemon.Snapshot, 2)}
	view := newDaemonTUITerminal(caller, "term-1", nil)
	t.Cleanup(view.Detach)
	nextRequest := func() chan daemon.Snapshot {
		t.Helper()
		select {
		case reply := <-caller.requests:
			return reply
		case <-time.After(time.Second):
			t.Fatal("snapshot worker did not make its request")
			return nil
		}
	}
	view.requestSnapshot()
	oldReply := nextRequest()
	want := TerminalMetadata{Title: "Reconnected"}
	view.installSnapshot(daemon.Snapshot{SnapshotSequence: 1, Metadata: want})
	view.markSnapshotDirty()
	view.requestSnapshot()
	oldReply <- daemon.Snapshot{SnapshotSequence: 100, Metadata: TerminalMetadata{Title: "Old daemon", BellCount: 8}}
	// Starting the next request proves that the first response was processed.
	newReply := nextRequest()
	defer func() { newReply <- daemon.Snapshot{SnapshotSequence: 2, Metadata: want} }()
	if got := view.Metadata(); got != want {
		t.Fatalf("old daemon snapshot replaced synchronized metadata: %#v", got)
	}
}
