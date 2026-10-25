package codelima

import (
	"slices"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func TestTUIOverviewDaemonOrderKeepsIdentityAndMembership(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	target := nodeTargetKey(a.fleet.selectedID)
	keys := a.sessions.TargetSessionKeys(target)
	a.overview.session = keys[1]
	a.state.setActiveTab(target, keys[1])
	record := func(key string) daemon.TerminalState {
		s, _ := a.sessions.Session(key)
		return daemon.TerminalState{Target: s.target, TerminalID: string(s.terminalID)}
	}
	states := []daemon.TerminalState{record(keys[1]), record(keys[0]), record(keys[2])}
	// An external close and a local creation overtake the order response.
	a.sessions.CloseSession(keys[2])
	newKey := putTestSession(a.sessions, target, &tuiSession{shellKind: terminal.NodeHostShell}, newFakeTUITerminal())
	states = append(states, states[0], daemon.TerminalState{Target: nodeTargetKey(a.state.nodes[1].ID), TerminalID: states[1].TerminalID})
	a.applySessionOrder(tuiSessionOrderEvent{states: states})
	if got := a.sessions.TargetSessionKeys(target); !slices.Equal(got, []string{keys[1], keys[0], newKey}) {
		t.Fatal("order changed membership or duplicated a tab", got)
	}
	a.draw()
	if a.overview.session != keys[1] || a.state.activeTab(target) != keys[1] {
		t.Fatal("reorder changed selected/active session")
	}
	if _, ok := a.sessions.Session(keys[2]); ok {
		t.Fatal("order resurrected closed session")
	}
	// A more recent mutation makes an older inventory reply ineligible.
	a.sessionOrder.serial = 2
	a.applySessionOrder(tuiSessionOrderEvent{serial: 1, states: []daemon.TerminalState{record(keys[0]), record(keys[1])}})
	if a.sessions.TargetSessionKeys(target)[0] != keys[1] {
		t.Fatal("stale response replaced the latest order")
	}
}

func TestTUIOverviewTabChangeUsesCompletionDeliveryAndCoalescesReads(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	post, completion := a.sessions.postEvent, a.sessions.postEventBlocking
	defer func() { a.sessions.postEvent, a.sessions.postEventBlocking = post, completion }()
	var received vaxis.Event
	a.sessions.postEvent = func(vaxis.Event) { t.Fatal("tab mutation used droppable delivery") }
	a.sessions.postEventBlocking = func(event vaxis.Event) { received = event }
	a.sessions.handleDaemonEvent(daemon.Event{Event: daemon.EventTargetTabsChanged})
	if _, ok := received.(tuiSessionOrderChangedEvent); !ok {
		t.Fatalf("tab change still only redraws: %T", received)
	}
	a.sessionOrder.loading = true
	a.requestSessionOrder()
	a.requestSessionOrder()
	if !a.sessionOrder.loading || a.sessionOrder.serial != 2 {
		t.Fatal("event burst lost its follow-up generation")
	}
}
