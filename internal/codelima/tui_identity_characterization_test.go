package codelima

import (
	"context"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

// This file pins today's OBSERVABLE terminal-identity behavior across the
// TargetKey migration (IMPROVEMENT_PLAN Part E, Track 1 PR1). Every expectation
// here is written with literal strings ("node:<id>", "<target>#<n>") so it is
// an independent oracle: it must stay green, unmodified, both before and after
// the migration. If any node-target assertion in this file has to change to
// pass, semantics changed and the migration is wrong. (The project-target
// cases that used to live here were deleted with the schema-v3 removal of the
// project model; the node-target pins are unchanged.)

func noopPostEvent(vaxis.Event) {}

// withFakeSessionTerminals points a store's terminal factory at in-memory
// fakes so real tuiSessionStore.Open*Tab paths allocate them instead of
// spawning PTY-backed shells. The factory is a per-store field, so this is
// safe under parallel tests.
func withFakeSessionTerminals(store *tuiSessionStore) {
	store.newTerminal = func(string, func(vaxis.Event)) tuiTerminal {
		return newFakeTUITerminal()
	}
}

// TestCharacterizeSessionKeyFormatAndMonotonicCounter pins that each opened tab
// is keyed "<targetKey>#<n>" with a per-target counter that only ever
// increments and never decrements or reuses a number when a tab is closed.
func TestCharacterizeSessionKeyFormatAndMonotonicCounter(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	withFakeSessionTerminals(store)

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}

	k1, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #1 error = %v", err)
	}
	if k1 != "node:n1#1" {
		t.Fatalf("expected first tab key node:n1#1, got %q", k1)
	}

	k2, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #2 error = %v", err)
	}
	if k2 != "node:n1#2" {
		t.Fatalf("expected second tab key node:n1#2, got %q", k2)
	}

	k3, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() #3 error = %v", err)
	}
	if k3 != "node:n1#3" {
		t.Fatalf("expected third tab key node:n1#3, got %q", k3)
	}

	// Closing the middle tab must not decrement or free the counter.
	store.CloseSession(k2)
	k4, err := store.OpenNodeTab(node)
	if err != nil {
		t.Fatalf("OpenNodeTab() after close error = %v", err)
	}
	if k4 != "node:n1#4" {
		t.Fatalf("expected counter to keep climbing to node:n1#4, got %q", k4)
	}
}

// TestCharacterizeTabOrderingAcrossOpenCloseReopen pins that TargetSessionKeys
// reports a target's tabs in open order, that closing removes only the closed
// tab, and that reopening appends the new tab at the end.
func TestCharacterizeTabOrderingAcrossOpenCloseReopen(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	withFakeSessionTerminals(store)

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}

	k1, _ := store.OpenNodeTab(node)
	k2, _ := store.OpenNodeTab(node)
	k3, _ := store.OpenNodeTab(node)

	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k2+","+k3 {
		t.Fatalf("expected open order %q, got %q", k1+","+k2+","+k3, got)
	}

	store.CloseSession(k2)
	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k3 {
		t.Fatalf("expected middle tab removed, got %q", got)
	}

	k4, _ := store.OpenNodeTab(node)
	if got := strings.Join(store.TargetSessionKeys("node:n1"), ","); got != k1+","+k3+","+k4 {
		t.Fatalf("expected reopened tab appended last, got %q", got)
	}
}

// TestCharacterizeTargetKeyStringFormsProduced pins the exact "node:<id>"
// strings produced by the list-entry key builder and by the session store's
// open path (session.target and the returned session key prefix).
func TestCharacterizeTargetKeyStringFormsProduced(t *testing.T) {
	if got := (tuiTreeEntry{node: Node{ID: "n1"}}).key(); got != "node:n1" {
		t.Fatalf("expected node entry key node:n1, got %q", got)
	}
	// The empty/unknown entry renders as the empty-string sentinel.
	if got := (tuiTreeEntry{}).key(); got != "" {
		t.Fatalf("expected empty entry key to be empty, got %q", got)
	}

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	withFakeSessionTerminals(store)

	nodeKey, err := store.OpenNodeTab(Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning})
	if err != nil {
		t.Fatalf("OpenNodeTab() error = %v", err)
	}
	if nodeKey != "node:n1#1" {
		t.Fatalf("expected node session key node:n1#1, got %q", nodeKey)
	}
	if session, ok := store.Session(nodeKey); !ok || session.target != "node:n1" {
		t.Fatalf("expected node session target node:n1, got %+v (ok=%v)", session, ok)
	}
}

// TestCharacterizeTargetKeyConsumption pins how the string target keys are
// parsed back into nodes: entryForKey resolves the "node:" prefix (including
// via the by-ID fallback when the entry is not currently visible), rejects
// unknown/empty keys, and activeNode resolves from the focused terminal target
// string.
func TestCharacterizeTargetKeyConsumption(t *testing.T) {
	t.Parallel()

	state, err := newTUIState(testTUINodes(t), nil)
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	if entry, ok := state.entryForKey("node:node-root"); !ok || entry.node.ID != "node-root" {
		t.Fatalf("expected node:node-root to resolve to node-root, got %+v (ok=%v)", entry, ok)
	}
	if _, ok := state.entryForKey("bogus"); ok {
		t.Fatalf("expected malformed key to resolve to nothing")
	}
	if _, ok := state.entryForKey(""); ok {
		t.Fatalf("expected empty key to resolve to nothing")
	}

	// Drop node-root from the visible entries; the by-ID fallback in
	// entryForKey must still parse "node:node-root". (Before schema v3 this
	// was exercised by collapsing the node's project branch.)
	state.entries = state.entries[1:]
	if state.findEntryByKey("node:node-root") >= 0 {
		t.Fatalf("expected node-root to be hidden after truncating the entries")
	}
	if entry, ok := state.entryForKey("node:node-root"); !ok || entry.node.ID != "node-root" {
		t.Fatalf("expected hidden node:node-root to resolve via by-ID fallback, got %+v (ok=%v)", entry, ok)
	}

	// activeNode resolves from the focused terminal target string when the
	// selection itself does not resolve.
	state.selection = -1
	state.focus = tuiFocusTerminal
	state.terminalTarget = "node:node-child"
	if node, ok := state.activeNode(); !ok || node.ID != "node-child" {
		t.Fatalf("expected terminalTarget node:node-child to resolve activeNode, got %+v (ok=%v)", node, ok)
	}
}

// TestCharacterizeActiveTabFallbackToFirstKey pins targetActiveSessionKey's
// fallback: it returns the recorded active tab when that tab is still open,
// otherwise it falls back to the target's first open tab.
func TestCharacterizeActiveTabFallbackToFirstKey(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	withFakeSessionTerminals(store)
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(store))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}

	node := Node{ID: "n1", Slug: "n1", Status: NodeStatusRunning}
	k1, _ := store.OpenNodeTab(node)
	k2, _ := store.OpenNodeTab(node)

	// Recorded active tab is honored while it is open.
	state.setActiveTab("node:n1", k2)
	if got := state.targetActiveSessionKey("node:n1"); got != k2 {
		t.Fatalf("expected recorded active tab %q, got %q", k2, got)
	}

	// When the recorded active tab is gone, fall back to the first open tab.
	store.CloseSession(k2)
	if got := state.targetActiveSessionKey("node:n1"); got != k1 {
		t.Fatalf("expected fallback to first open tab %q, got %q", k1, got)
	}

	// With no open tabs, there is no active tab.
	store.CloseSession(k1)
	if got := state.targetActiveSessionKey("node:n1"); got != "" {
		t.Fatalf("expected empty active tab when none are open, got %q", got)
	}
}

// TestCharacterizeSessionErrorLifecycleKeyedByTarget pins that an open failure
// records an error keyed by TARGET (not session key), that the error is
// retrievable via the target, that closing the target's tabs clears it, and
// that a subsequent successful open also clears it.
func TestCharacterizeSessionErrorLifecycleKeyedByTarget(t *testing.T) {
	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	withFakeSessionTerminals(store)

	// An empty directory path fails before any tab is created.
	if _, err := store.OpenNodeHostTab(Node{ID: "n1", Slug: "n1", DirectoryPath: ""}); err == nil {
		t.Fatalf("expected OpenNodeHostTab with empty directory to fail")
	}
	if store.SessionError("node:n1") == nil {
		t.Fatalf("expected an error recorded under the target key node:n1")
	}
	// The error is keyed by target, so it is not addressable as a session key.
	if store.SessionError("node:n1#1") != nil {
		t.Fatalf("expected no error recorded under a session key")
	}

	// Closing the target's tabs clears the recorded error.
	store.CloseTargetSessions("node:n1")
	if store.SessionError("node:n1") != nil {
		t.Fatalf("expected CloseTargetSessions to clear the recorded error")
	}

	// Re-arm the error, then a successful open must clear it.
	if _, err := store.OpenNodeHostTab(Node{ID: "n1", Slug: "n1", DirectoryPath: ""}); err == nil {
		t.Fatalf("expected second failing OpenNodeHostTab to fail")
	}
	if store.SessionError("node:n1") == nil {
		t.Fatalf("expected error re-armed under node:n1")
	}
	if _, err := store.OpenNodeHostTab(Node{ID: "n1", Slug: "n1", DirectoryPath: t.TempDir()}); err != nil {
		t.Fatalf("expected successful OpenNodeHostTab, got %v", err)
	}
	if store.SessionError("node:n1") != nil {
		t.Fatalf("expected a successful open to clear the recorded error")
	}
}

// TestCharacterizeSessionErrorClearedOnReloadWhenTargetRemoved pins that a
// reload prunes a target-keyed session error when that target no longer exists
// in the reloaded node list.
func TestCharacterizeSessionErrorClearedOnReloadWhenTargetRemoved(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	service, _ := newTestService(t)
	store := newTUISessionStore(ctx, service, noopPostEvent)
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(store))
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{ctx: ctx, service: service, state: state, sessions: store}

	// Record an error for a target that exists in the current node list.
	if _, err := store.OpenNodeHostTab(Node{ID: "node-root", Slug: "root-node", DirectoryPath: ""}); err == nil {
		t.Fatalf("expected OpenNodeHostTab with empty directory to fail")
	}
	if store.SessionError("node:node-root") == nil {
		t.Fatalf("expected error recorded for node:node-root")
	}

	// Reloading to a node list without that node prunes the stale error.
	if err := app.applyReloadedNodes([]Node{}, ""); err != nil {
		t.Fatalf("applyReloadedNodes() error = %v", err)
	}
	if store.SessionError("node:node-root") != nil {
		t.Fatalf("expected reload to prune the error for the removed target")
	}
}
