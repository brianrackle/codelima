package codelima

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

func TestTUIFleetSessionActivityUsesPositiveIntegrationEvidence(t *testing.T) {
	for _, tt := range []struct {
		metadata TerminalMetadata
		want     string
	}{
		{TerminalMetadata{}, "unknown"}, {TerminalMetadata{Title: "codex busy compiling"}, "unknown"},
		{TerminalMetadata{CursorAtPrompt: true}, "at prompt"},
		{TerminalMetadata{ProgressState: 1, Progress: 40, CursorAtPrompt: true}, "working"},
		{TerminalMetadata{ProgressState: 3}, "working"}, {TerminalMetadata{ProgressState: 2}, "error"}, {TerminalMetadata{ProgressState: 4}, "paused"},
	} {
		if got := fleetSessionActivity(tt.metadata); got != tt.want {
			t.Fatalf("%+v: got %q want %q", tt.metadata, got, tt.want)
		}
	}
}

func TestTUIFleetPromptIdleUsesRetainedTimeAndRequiresAllMarkedSessionsIdle(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	node := a.state.nodes[0]
	since := time.Now().Add(-4 * time.Minute).UnixMilli()
	term := &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{CursorAtPrompt: true, PromptIdleSinceUnixMilli: since}}
	putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: terminal.NodeShell}, term)
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.draw()
	if screen := renderedScreenText(t, a.vx, 150, 38); !strings.Contains(screen, "idle 4m") {
		t.Fatalf("retained idle missing:\n%s", screen)
	}
	if got := a.fleetSessionSummary(node); got != " · idle 4m" {
		t.Fatal(got)
	}
	for _, bad := range []string{"future", "busy", "stale", "disconnected", "expired response", "failed request"} {
		term.metadata = TerminalMetadata{CursorAtPrompt: true, PromptIdleSinceUnixMilli: since}
		a.daemonDisconnected = false
		a.fleetEvidence = tuiFleetEvidenceState{}
		switch bad {
		case "future":
			term.metadata.PromptIdleSinceUnixMilli = time.Now().Add(time.Hour).UnixMilli()
		case "busy":
			term.metadata.ProgressState = 3
		case "stale":
			term.metadata.ActivityStale = true
		case "disconnected":
			a.daemonDisconnected = true
		case "expired response":
			a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now().Add(-6 * time.Second)}
		case "failed request":
			a.fleetEvidence.err = errors.New("request timed out")
		}
		if got := a.fleetSessionSummary(node); strings.Contains(got, "idle") {
			t.Fatalf("%s claimed idle: %s", bad, got)
		}
	}
	a.daemonDisconnected = false
	a.fleetEvidence = tuiFleetEvidenceState{}
	term.metadata = TerminalMetadata{CursorAtPrompt: true, PromptIdleSinceUnixMilli: since}
	putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: terminal.NodeShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{ProgressState: 4}})
	if got := a.fleetSessionSummary(node); strings.Contains(got, "idle") {
		t.Fatal("mixed target claimed wholly idle", got)
	}
}

func TestTUIFleetMarkedSessionImpactIncludesHiddenGuestAndHostKinds(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	first, second := a.state.nodes[0], a.state.nodes[1]
	putTestSession(a.sessions, nodeTargetKey(first.ID), &tuiSession{shellKind: terminal.NodeShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{ProgressState: 3, Title: "codex · compile"}})
	putTestSession(a.sessions, nodeTargetKey(second.ID), &tuiSession{shellKind: terminal.NodeShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{CursorAtPrompt: true}})
	putTestSession(a.sessions, nodeTargetKey(second.ID), &tuiSession{shellKind: terminal.NodeHostShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal()})
	a.refreshFleet()
	a.fleet.marks[first.ID], a.fleet.marks[second.ID] = true, true
	a.fleet.setFilter(first.Slug)
	impact := a.markedImpact()
	if got := a.fleetSessionSummary(first); !strings.Contains(got, "1 working (codex · compile") {
		t.Fatalf("marked task detail lost: %s", got)
	}
	if impact.sessions != 3 || impact.guestSessions != 2 || impact.hostSessions != 1 || impact.working != 1 || impact.activityUnknown != 1 || impact.hidden != 1 {
		t.Fatalf("hidden session impact lost: %+v", impact)
	}
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	for _, want := range []string{"2 guest sessions end on stop", "1 working", "0 activity unknown", "1 host sessions"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("missing %q:\n%s", want, screen)
		}
	}
	clear(a.fleet.marks)
	a.draw()
	screen = renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(screen, "working") || !strings.Contains(screen, "codex · compile") {
		t.Fatalf("preview omitted observed task state:\n%s", screen)
	}
}
